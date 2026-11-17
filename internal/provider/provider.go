// Package provider resolves the internet provider of each adapter through a socket
// pinned to that adapter. Discovery is asynchronous and never gates connectivity.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"net-merge/internal/nics"
)

type Info struct {
	Name, Domain, IPv4, IPv6 string
	Logo                     image.Image
}

type entry struct {
	key     string
	info    Info
	pending bool
	retry   time.Time
}

// Resolver caches successful lookups for six hours and retries offline adapters
// after two minutes. A DHCP/address change invalidates only the affected adapter.
type Resolver struct {
	mu      sync.Mutex
	entries map[string]*entry
	lookup  func(context.Context, nics.Nic) (Info, error)
	ctx     context.Context
	cancel  context.CancelFunc
}

func New() *Resolver {
	ctx, cancel := context.WithCancel(context.Background())
	return &Resolver{entries: map[string]*entry{}, lookup: lookup, ctx: ctx, cancel: cancel}
}

func (r *Resolver) Close() { r.cancel() }

func (r *Resolver) Refresh(adapters []nics.Nic) {
	r.mu.Lock()
	defer r.mu.Unlock()
	present := map[string]bool{}
	for _, n := range adapters {
		present[n.Name] = true
		key := fmt.Sprintf("%d/%s/%s", n.Index, n.IP, n.IP6)
		e := r.entries[n.Name]
		if e == nil || e.key != key {
			e = &entry{key: key}
			r.entries[n.Name] = e
		}
		if e.pending || time.Now().Before(e.retry) || r.ctx.Err() != nil {
			continue
		}
		e.pending = true
		go func() {
			ctx, cancel := context.WithTimeout(r.ctx, 25*time.Second)
			defer cancel()
			info, err := r.lookup(ctx, n)
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.entries[n.Name] != e {
				return // Ignore a result from an adapter's previous address.
			}
			e.pending = false
			e.retry = time.Now().Add(6 * time.Hour)
			if err != nil {
				e.retry = time.Now().Add(2 * time.Minute)
			}
			if info.Name != "" || info.IPv4 != "" || info.IPv6 != "" {
				e.info = info
			}
		}()
	}
	for name := range r.entries {
		if !present[name] {
			delete(r.entries, name)
		}
	}
}

func (r *Resolver) Get(name string) Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[name]; e != nil {
		return e.info
	}
	return Info{}
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
			// Deliberately do not use environment proxies: they would identify the
			// proxy's provider rather than the adapter's actual internet connection.
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
					lastErr = fmt.Errorf("no public address for provider website")
				}
				return nil, lastErr
			},
			TLSHandshakeTimeout: 4 * time.Second,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" {
				return fmt.Errorf("invalid provider lookup redirect")
			}
			return nil
		},
	}
}

func lookup(ctx context.Context, n nics.Nic) (Info, error) {
	var out Info
	var lastErr error
	var logoClient *http.Client
	for _, v6 := range []bool{false, true} {
		if (!v6 && n.IP == nil) || (v6 && n.IP6 == nil) {
			continue
		}
		client := clientFor(n, v6)
		info, err := query(ctx, client, "https://ipwho.is/?fields=success,ip,connection", v6)
		if err != nil {
			lastErr = err
			continue
		}
		if out.Name == "" {
			out.Name, out.Domain = info.Name, info.Domain
			logoClient = client
		}
		if v6 {
			out.IPv6 = info.IPv6
		} else {
			out.IPv4 = info.IPv4
		}
	}
	if logoClient != nil && validDomain(out.Domain) {
		out.Logo = lookupLogo(ctx, logoClient, out.Domain)
	}
	if out.IPv4 != "" || out.IPv6 != "" {
		return out, nil
	}
	return out, lastErr
}

func query(ctx context.Context, client *http.Client, endpoint string, v6 bool) (Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("User-Agent", "net-merge/provider")
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("provider lookup: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Success    bool
		IP         string
		Connection struct{ ISP, Org, Domain string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&body); err != nil {
		return Info{}, err
	}
	ip := net.ParseIP(body.IP)
	if !body.Success || ip == nil || (ip.To4() == nil) != v6 || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return Info{}, fmt.Errorf("provider lookup returned no public address for this family")
	}
	name := cleanName(body.Connection.ISP)
	if name == "" {
		name = cleanName(body.Connection.Org)
	}
	out := Info{Name: name}
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(body.Connection.Domain), "."))
	if validDomain(domain) {
		out.Domain = domain
	}
	if v6 {
		out.IPv6 = ip.String()
	} else {
		out.IPv4 = ip.String()
	}
	return out, nil
}

func cleanName(s string) string {
	runes := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)))
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return string(runes)
}

func validDomain(s string) bool {
	if len(s) < 4 || len(s) > 253 || net.ParseIP(s) != nil || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
