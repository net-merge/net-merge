package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type TrayPreferences struct {
	ShowPublicIPs bool              `json:"showPublicIPs"`
	Aliases       map[string]string `json:"aliases,omitempty"`
}

func LoadTrayPreferences(dir string) TrayPreferences {
	p := TrayPreferences{Aliases: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(dir, "tray-preferences.json"))
	if err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if p.Aliases == nil {
		p.Aliases = map[string]string{}
	}
	return p
}

func SaveTrayPreferences(dir string, p TrayPreferences) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "tray-preferences.json")
	tmp, err := os.CreateTemp(dir, "tray-preferences-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmp.Name(), path)
}
