package provider

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"net-merge/internal/nics"
)

func TestQueryValidatesFamilyAndResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		v6, valid  bool
	}{
		{"v4", `{"success":true,"ip":"203.0.113.8","connection":{"isp":"Example ISP","domain":"example.com"}}`, false, true},
		{"v6", `{"success":true,"ip":"2001:db8::8","connection":{"org":"Example IPv6"}}`, true, true},
		{"wrong family", `{"success":true,"ip":"203.0.113.8"}`, true, false},
		{"router", `{"success":true,"ip":"192.168.1.1"}`, false, false},
		{"failed", `{"success":false,"ip":"203.0.113.8"}`, false, false},
		{"invalid", `{"success":true,"ip":"invalid"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			info, err := query(context.Background(), server.Client(), server.URL, tc.v6)
			if (err == nil) != tc.valid {
				t.Fatalf("info=%+v, err=%v", info, err)
			}
		})
	}
}

func TestProviderDomainsCannotBecomeURLsOrLocalAddresses(t *testing.T) {
	for _, domain := range []string{"https://example.com", "localhost", "127.0.0.1", "example.com/path", "example.com:443", "-bad.com", "a..com", "a.com?x=1"} {
		if validDomain(domain) {
			t.Errorf("accepted %q", domain)
		}
	}
	if !validDomain("isp.example.com") {
		t.Fatal("rejected an ordinary ISP domain")
	}
}

func TestResolverCachesByAdapterAddressAndRejectsStaleResults(t *testing.T) {
	r := New()
	defer r.Close()
	first := make(chan struct{})
	second := make(chan struct{})
	var calls atomic.Int32
	r.lookup = func(ctx context.Context, n nics.Nic) (Info, error) {
		calls.Add(1)
		if n.IP.String() == "10.0.0.1" {
			<-first
			return Info{Name: "Old provider"}, nil
		}
		<-second
		return Info{Name: "New provider"}, nil
	}
	r.Refresh([]nics.Nic{{Name: "Ethernet", IP: net.ParseIP("10.0.0.1")}})
	r.Refresh([]nics.Nic{{Name: "Ethernet", IP: net.ParseIP("10.0.0.2")}})
	close(second)
	deadline := time.Now().Add(2 * time.Second)
	for r.Get("Ethernet").Name != "New provider" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.Get("Ethernet").Name != "New provider" {
		t.Fatal("new address was not resolved")
	}
	close(first)
	for range 10 {
		r.Refresh([]nics.Nic{{Name: "Ethernet", IP: net.ParseIP("10.0.0.2")}})
	}
	if calls.Load() > 2 {
		t.Fatal("cached adapter triggered repeated network lookups")
	}
	r.Refresh(nil)
	if r.Get("Ethernet").Name != "" {
		t.Fatal("removed adapter retained metadata")
	}
}
