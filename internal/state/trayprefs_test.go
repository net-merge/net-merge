package state

import "testing"

func TestTrayPreferencesRoundTripAndMissingDefaults(t *testing.T) {
	dir := t.TempDir()
	p := LoadTrayPreferences(dir)
	if p.ShowPublicIPs || p.Aliases == nil {
		t.Fatal("public addresses should start hidden with usable aliases map")
	}
	p.ShowPublicIPs = true
	p.Aliases["Ethernet"] = "الجزيرة"
	if err := SaveTrayPreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	got := LoadTrayPreferences(dir)
	if !got.ShowPublicIPs || got.Aliases["Ethernet"] != "الجزيرة" {
		t.Fatalf("preferences lost: %+v", got)
	}
}
