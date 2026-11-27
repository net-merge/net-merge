// Package provider discovers ISP names through each adapter, for the tray rows.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"net-merge/internal/nics"
)

type entry struct {
	key    string
	name   string
	cancel context.CancelFunc
}

// Resolver makes one asynchronous lookup per adapter/address, including failures.
// Repeated Refresh calls never poll a service. Disable/re-enable explicitly retries.
type Resolver struct {
	mu      sync.Mutex
	entries map[string]*entry
	lookup  func(context.Context, nics.Nic) (string, error)
	ctx     context.Context
	cancel  context.CancelFunc
}

func New() *Resolver {
	ctx, cancel := context.WithCancel(context.Background())
	return &Resolver{entries: map[string]*entry{}, lookup: lookup, ctx: ctx, cancel: cancel}
}

func (r *Resolver) Close() { r.cancel() }

func (r *Resolver) Refresh(adapters []nics.Nic, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	present := map[string]bool{}
	if enabled && r.ctx.Err() == nil {
		for _, n := range adapters {
			present[n.Name] = true
			key := fmt.Sprintf("%d/%s/%s", n.Index, n.IP, n.IP6)
			if e := r.entries[n.Name]; e != nil {
				if e.key == key {
					continue
				}
				e.cancel()
			}
			ctx, cancel := context.WithTimeout(r.ctx, 25*time.Second)
			e := &entry{key: key, cancel: cancel}
			r.entries[n.Name] = e
			go func() {
				defer cancel()
				name, err := r.lookup(ctx, n)
				r.mu.Lock()
				defer r.mu.Unlock()
				if r.entries[n.Name] == e && ctx.Err() == nil && err == nil {
					e.name = name
				}
			}()
		}
	}
	for name, e := range r.entries {
		if !present[name] {
			e.cancel()
			delete(r.entries, name)
		}
	}
}

func (r *Resolver) Get(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[name]; e != nil {
		return e.name
	}
	return ""
}

func clientFor(n nics.Nic, v6 bool) *http.Client {
	ip, network := n.IP, "tcp4"
	if v6 {
		ip, network = n.IP6, "tcp6"
	}
	d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: ip}, Control: nics.ForceEgress(n.Index, v6), Timeout: 4 * time.Second}
	return &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			// No environment proxy: that would identify the proxy's ISP instead.
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				var lastErr error
				for _, a := range addresses {
					ip := a.IP
					if (ip.To4() == nil) != v6 || !ip.IsGlobalUnicast() || ip.IsPrivate() {
						continue
					}
					conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				if lastErr == nil {
					lastErr = fmt.Errorf("no public address for ISP service")
				}
				return nil, lastErr
			},
			TLSHandshakeTimeout: 4 * time.Second,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" {
				return fmt.Errorf("invalid ISP lookup redirect")
			}
			return nil
		},
	}
}

const primaryURL = "https://ipwho.is/?fields=success,connection.isp"
const fallbackURL = "https://ipinfo.io/json"

func lookup(ctx context.Context, n nics.Nic) (string, error) {
	return lookupWith(ctx, n, clientFor)
}

func lookupWith(ctx context.Context, n nics.Nic, client func(nics.Nic, bool) *http.Client) (string, error) {
	var lastErr error
	for _, v6 := range []bool{false, true} {
		if (!v6 && n.IP == nil) || (v6 && n.IP6 == nil) {
			continue
		}
		c := client(n, v6)
		for _, endpoint := range []string{primaryURL, fallbackURL} {
			name, err := query(ctx, c, endpoint)
			if err == nil {
				return name, nil
			}
			lastErr = err
		}
	}
	return "", lastErr
}

func query(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "net-merge/provider")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ISP lookup: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Success    *bool
		Error      bool
		Org        string
		Connection struct{ ISP string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&body); err != nil {
		return "", err
	}
	if body.Error || (body.Success != nil && !*body.Success) {
		return "", fmt.Errorf("ISP lookup was unsuccessful")
	}
	name := body.Connection.ISP
	if name == "" {
		name = strings.TrimSpace(body.Org)
		if asn, rest, ok := strings.Cut(name, " "); ok && strings.HasPrefix(asn, "AS") && len(asn) > 2 && strings.Trim(asn[2:], "0123456789") == "" {
			name = rest
		}
	}
	name = shortISP(cleanName(name))
	if name == "" {
		return "", fmt.Errorf("ISP lookup returned no name")
	}
	return name, nil
}

func cleanName(s string) string {
	runes := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)))
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return string(runes)
}
