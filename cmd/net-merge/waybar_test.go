package main

import (
	"strings"
	"testing"

	"net-merge/internal/engine"
	"net-merge/internal/state"
)

func TestWaybarJSON(t *testing.T) {
	if got := waybarJSON(nil, false); got != `{"text":"off","tooltip":"net-merge is off","class":"off"}` {
		t.Fatal(got)
	}
	got := waybarJSON(&state.Status{NICs: []engine.SlotStats{
		{Name: "wlo1", Present: true, LinkIn: 5e6, LinkOut: 5e5},
		{Name: "a<b", Present: true, LinkIn: 5e6},
		{Name: "gone", LinkIn: 9e9},
	}}, true)
	for _, want := range []string{`"text":"↓80 ↑4.0"`, `wlo1  ↓40 ↑4.0`, `lt;b`, `"class":"on"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "gone") || strings.Contains(got, "\n") || strings.Contains(got, "u003c") {
		t.Fatalf("absent adapter, a second line or unescaped markup: %s", got)
	}
}
