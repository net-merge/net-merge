package main

import (
	"testing"

	"net-merge/internal/state"
)

func TestSetISPNamesKeepsAliases(t *testing.T) {
	dir := t.TempDir()
	if err := state.SetAlias(dir, "Ethernet 5", "Home"); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{false, true} {
		if err := setISPNames(dir, on); err != nil {
			t.Fatal(err)
		}
		p, err := state.LoadTrayPreferences(dir)
		if err != nil || p.LookupISP != on || p.Aliases["Ethernet 5"] != "Home" {
			t.Fatalf("on=%v saved %+v, %v", on, p, err)
		}
	}
}
