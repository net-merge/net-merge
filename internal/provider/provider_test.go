package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"net-merge/internal/nics"
)

func TestSmallJSONNamesAndUntrustedResponses(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"success":true,"connection":{"isp":"Example ISP"}}`, "Example ISP"},
		{`{"org":"AS12345 Example ISP"}`, "Example ISP"},
		{`{"org":"  AS12345 Example ISP  "}`, "Example ISP"},
		{`{"org":"ASword Broadband"}`, "ASword Broadband"},
		{`{"connection":{"isp":"A\nB\tC\u202e"}}`, "ABC"},
		{`{"success":false,"connection":{"isp":"Ignore me"}}`, ""},
		{`{"error":true,"org":"Ignore me"}`, ""},
		{`{"org":""}`, ""},
		{`{"org":"` + strings.Repeat("x", 33<<10) + `"}`, ""},
		{`<html>not JSON</html>`, ""},
	} {
		t.Run(tc.body[:min(len(tc.body), 45)], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer s.Close()
			got, err := query(context.Background(), s.Client(), s.URL)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("name=%q, err=%v; want %q", got, err, tc.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLookupFallsBackWithoutFetchingImages(t *testing.T) {
	var urls []string
	c := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		urls = append(urls, req.URL.String())
		if req.URL.String() == primaryURL {
			return nil, errors.New("primary offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"org":"AS123 Example ISP"}`))}, nil
	})}
	got, err := lookupWith(context.Background(), nics.Nic{IP: net.ParseIP("192.168.1.2")}, func(_ nics.Nic, v6 bool) *http.Client {
		if v6 {
			t.Fatal("IPv4 adapter tried an IPv6 socket")
		}
		return c
	})
	if err != nil || got != "Example ISP" || len(urls) != 2 || urls[0] != primaryURL || urls[1] != fallbackURL {
		t.Fatalf("got=%q err=%v requests=%v", got, err, urls)
	}
}

func TestPinnedClientHasNoProxyAndRejectsHTTPRedirects(t *testing.T) {
	c := clientFor(nics.Nic{Index: 7, IP: net.ParseIP("192.168.1.2")}, false)
	tr := c.Transport.(*http.Transport)
	if tr.Proxy != nil || tr.DialContext == nil || c.Timeout == 0 {
		t.Fatal("lookup must use the pinned dialer with no proxy and a deadline")
	}
	r, _ := http.NewRequest("GET", "http://example.com", nil)
	if c.CheckRedirect(r, nil) == nil {
		t.Fatal("accepted a downgrade to unencrypted HTTP")
	}
}

func TestResolverOptInNoPollingAndStaleCancellation(t *testing.T) {
	r := New()
	defer r.Close()
	oldStarted, oldStopped := make(chan struct{}), make(chan struct{})
	newDone := make(chan struct{})
	var calls atomic.Int32
	r.lookup = func(ctx context.Context, n nics.Nic) (string, error) {
		calls.Add(1)
		if n.IP.Equal(net.ParseIP("10.0.0.1")) {
			close(oldStarted)
			<-ctx.Done()
			close(oldStopped)
			return "Stale provider", nil // even a non-cooperative result must be ignored
		}
		defer close(newDone)
		return "Current ISP", nil
	}
	old := []nics.Nic{{Name: "Ethernet", IP: net.ParseIP("10.0.0.1")}}
	for range 5 {
		r.Refresh(old, false)
	}
	if calls.Load() != 0 {
		t.Fatal("disabled discovery made a request")
	}
	r.Refresh(old, true)
	await(t, oldStarted)
	current := []nics.Nic{{Name: "Ethernet", IP: net.ParseIP("10.0.0.2")}}
	r.Refresh(current, true)
	await(t, oldStopped)
	await(t, newDone)
	eventually(t, func() bool { return r.Get("Ethernet") == "Current ISP" })
	for range 10 {
		r.Refresh(current, true)
	}
	if calls.Load() != 2 {
		t.Fatalf("same address was polled: %d calls", calls.Load())
	}
	r.Refresh(current, false)
	if r.Get("Ethernet") != "" {
		t.Fatal("turning off retained a visible ISP name")
	}
}

func TestResolverCachesFailuresUntilAnotherAdapterEvent(t *testing.T) {
	r := New()
	defer r.Close()
	var calls atomic.Int32
	r.lookup = func(context.Context, nics.Nic) (string, error) {
		calls.Add(1)
		return "", errors.New("offline")
	}
	adapters := []nics.Nic{{Name: "Ethernet", IP: net.ParseIP("10.0.0.2")}}
	r.Refresh(adapters, true)
	eventually(t, func() bool { return calls.Load() == 1 })
	for range 10 {
		r.Refresh(adapters, true)
	}
	if calls.Load() != 1 {
		t.Fatal("failed lookup was polled")
	}
	r.Refresh(nil, true)
	r.Refresh(adapters, true)
	eventually(t, func() bool { return calls.Load() == 2 })
}

func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup did not finish")
	}
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("lookup result was not published")
		}
		time.Sleep(time.Millisecond)
	}
}
