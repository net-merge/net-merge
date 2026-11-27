package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, want := range []Settings{{PhoneCooling: true}, {PhoneCooling: false}} {
		if err := SaveSettings(dir, want); err != nil {
			t.Fatal(err)
		}
		got, err := LoadSettings(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("LoadSettings = %+v, want %+v", got, want)
		}
	}
	if _, err := os.Stat(SettingsPath(dir) + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("the temporary file was left behind: %v", err)
	}
}

func TestSettingsDefaultWithoutAFile(t *testing.T) {
	got, err := LoadSettings(t.TempDir())
	if err != nil {
		t.Fatalf("a missing settings file must not be an error: %v", err)
	}
	if got.PhoneCooling {
		t.Fatal("phone cooling must be off unless it was chosen")
	}
}

func TestSettingsLiveNextToLimits(t *testing.T) {
	dir := t.TempDir()
	if filepath.Dir(SettingsPath(dir)) != filepath.Dir(LimitsPath(dir)) {
		t.Fatalf("settings at %s, limits at %s: they belong together", SettingsPath(dir), LimitsPath(dir))
	}
}

func TestLoadSettingsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(SettingsPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(dir); err == nil {
		t.Fatal("a broken settings file must be reported")
	}
}
