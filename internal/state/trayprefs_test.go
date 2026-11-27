package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAliasesSurviveReload(t *testing.T) {
	dir := t.TempDir()
	p, err := LoadTrayPreferences(dir)
	if err != nil || p.Aliases == nil {
		t.Fatalf("missing-file defaults: %+v %v", p, err)
	}
	if err := SaveTrayPreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	if err := SetAlias(dir, "Ethernet 5", "خط البيت"); err != nil {
		t.Fatal(err)
	}
	p, err = LoadTrayPreferences(dir)
	if err != nil || p.Aliases["Ethernet 5"] != "خط البيت" {
		t.Fatalf("lost preferences: %+v %v", p, err)
	}
	if err := SetAlias(dir, "Ethernet 5", ""); err != nil {
		t.Fatal(err)
	}
	p, _ = LoadTrayPreferences(dir)
	if len(p.Aliases) != 0 {
		t.Fatalf("removing an alias left it behind: %+v", p)
	}
	if filepath.Dir(TrayPreferencesPath(dir)) != filepath.Dir(LimitsPath(dir)) {
		t.Fatal("aliases must live beside limits.json")
	}
}

func TestBrokenPreferencesAreNotOverwrittenByAlias(t *testing.T) {
	dir := t.TempDir()
	path := TrayPreferencesPath(dir)
	if err := os.WriteFile(path, []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if SetAlias(dir, "Ethernet", "Test") == nil {
		t.Fatal("silently replaced damaged preferences")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "broken" {
		t.Fatal("damaged preferences were overwritten")
	}
}

func TestAliasRequestsAreBoundedValidatedAndConsumed(t *testing.T) {
	dir := t.TempDir()
	want := AliasRequest{Adapter: "Ethernet 5", Alias: "Jio phone"}
	if err := SendAlias(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, ok := TakeAlias(dir); !ok || got != want {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
	if _, ok := TakeAlias(dir); ok {
		t.Fatal("request was applied twice")
	}
	for _, raw := range []string{`{"adapter":"","alias":"X"}`, `{"adapter":"Ethernet","alias":"bad\nrow"}`, strings.Repeat("x", 8<<10)} {
		if err := os.WriteFile(filepath.Join(dir, "ctl", "alias"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := TakeAlias(dir); ok {
			t.Fatal("accepted an invalid request")
		}
	}
	if err := SendAlias(dir, AliasRequest{Adapter: "Ethernet", Alias: strings.Repeat("ع", 81)}); err == nil {
		t.Fatal("accepted an overlong alias")
	}
}
