package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const settingsVersion = 1

// Settings are the tray choices that have to outlive a restart. Start at sign-in launches a bare
// `net-merge tray`, so a choice that lived only in a flag would be lost there.
type Settings struct {
	// PhoneCooling holds a phone hotspot to a lower speed after long heavy use (engine/phone.go).
	PhoneCooling bool `json:"phone_cooling"`
}

type settingsFile struct {
	Version int `json:"version"`
	Settings
}

// SettingsPath is where the settings live: next to limits.json, so on macOS they survive a reboot
// too (see LimitsPath).
func SettingsPath(dir string) string {
	return filepath.Join(limitsDir(dir), "settings.json")
}

// LoadSettings reads the saved settings. A missing file means the defaults, not an error.
func LoadSettings(dir string) (Settings, error) {
	b, err := os.ReadFile(SettingsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var f settingsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", SettingsPath(dir), err)
	}
	return f.Settings, nil
}

// SaveSettings writes the settings atomically, so a crash mid-write cannot leave a broken file.
func SaveSettings(dir string, s Settings) error {
	if err := os.MkdirAll(limitsDir(dir), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(settingsFile{Version: settingsVersion, Settings: s}, "", "  ")
	if err != nil {
		return err
	}
	tmp := SettingsPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return replaceFile(tmp, SettingsPath(dir))
}
