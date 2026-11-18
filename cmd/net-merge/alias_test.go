package main

import (
	"net"
	"strings"
	"testing"
	"time"

	"net-merge/internal/engine"
	"net-merge/internal/nics"
	"net-merge/internal/state"
)

func TestAliasDisplayPreservesUnderlyingInterface(t *testing.T) {
	aliases := map[string]string{"Ethernet 5": "خط البيت"}
	n := nics.Nic{Name: "Ethernet 5", IP: net.ParseIP("192.168.1.2")}
	for _, got := range []string{describeNic(n, aliases), formatNICs([]engine.SlotStats{{Name: n.Name, Present: true, Weight: 3}}, aliases)} {
		if !strings.Contains(got, "خط البيت (Ethernet 5)") {
			t.Fatalf("terminal label lost the original name: %s", got)
		}
	}
	if n.Name != "Ethernet 5" {
		t.Fatal("display alias renamed the underlying interface")
	}
}

func TestAliasApplyOfflineAndThroughRunningInstance(t *testing.T) {
	dir := t.TempDir()
	if err := aliasApply(dir, "Ethernet", "Home", false, time.Second); err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	defer func() { close(stop); <-done }()
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if req, ok := state.TakeAlias(dir); ok {
					_ = state.SetAlias(dir, req.Adapter, req.Alias)
				}
			}
		}
	}()
	if err := aliasApply(dir, "Ethernet", "البيت", true, time.Second); err != nil {
		t.Fatal(err)
	}
	p, _ := state.LoadTrayPreferences(dir)
	if p.Aliases["Ethernet"] != "البيت" {
		t.Fatal("running instance did not save the alias")
	}
	if err := aliasApply(dir, "Ethernet", "", true, time.Second); err != nil {
		t.Fatal(err)
	}
	p, _ = state.LoadTrayPreferences(dir)
	if len(p.Aliases) != 0 {
		t.Fatal("removing a live alias failed")
	}
}

func TestAliasTimeoutDoesNotReportAnUnsavedChange(t *testing.T) {
	if err := aliasApply(t.TempDir(), "Ethernet", "Home", true, time.Millisecond); err == nil {
		t.Fatal("an old/unresponsive instance was reported as updated")
	}
}
