package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type TrayPreferences struct {
	LookupISP bool              `json:"lookupISP"`
	Aliases   map[string]string `json:"aliases,omitempty"`
}

// TrayPreferencesPath keeps UI preferences beside limits.json, including the
// persistent Application Support location used on macOS.
func TrayPreferencesPath(dir string) string {
	return filepath.Join(limitsDir(dir), "tray-preferences.json")
}

// ErrDamagedPreferences is LoadTrayPreferences' error for a file that can't be decoded. The
// preferences returned with it are the defaults.
var ErrDamagedPreferences = errors.New("damaged tray preferences")

func defaultTrayPreferences() TrayPreferences {
	return TrayPreferences{LookupISP: true, Aliases: map[string]string{}} // ISP names are on until unticked
}

func LoadTrayPreferences(dir string) (TrayPreferences, error) {
	p := defaultTrayPreferences()
	f, err := os.Open(TrayPreferencesPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer f.Close()
	if err := json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&p); err != nil {
		return defaultTrayPreferences(), fmt.Errorf("%w %s: %v", ErrDamagedPreferences, TrayPreferencesPath(dir), err)
	}
	if p.Aliases == nil {
		p.Aliases = map[string]string{}
	}
	for name, alias := range p.Aliases {
		if ValidateAlias(name, alias) != nil || alias == "" {
			delete(p.Aliases, name)
		}
	}
	return p, nil
}

func SaveTrayPreferences(dir string, p TrayPreferences) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if len(b) >= 64<<10 {
		return fmt.Errorf("too many saved adapter aliases")
	}
	if err := os.MkdirAll(limitsDir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(limitsDir(dir), "tray-preferences-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil { // status/list may be run without elevation
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmp.Name(), TrayPreferencesPath(dir))
}

func ValidateAlias(name, alias string) error {
	if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > 256 {
		return fmt.Errorf("adapter name must contain 1-256 characters")
	}
	if alias != strings.TrimSpace(alias) || utf8.RuneCountInString(alias) > 80 {
		return fmt.Errorf("alias must contain at most 80 characters, without surrounding spaces")
	}
	for _, r := range name + alias {
		if unicode.IsControl(r) {
			return fmt.Errorf("adapter names and aliases cannot contain control characters")
		}
		if reordersText(r) {
			return fmt.Errorf("adapter names and aliases cannot contain text-direction overrides")
		}
	}
	return nil
}

// reordersText reports the characters that change how the text around them is laid out or shown: the
// bidirectional embeddings, overrides and isolates, the line and paragraph separators and the byte-order
// mark. An alias with one could make a menu row read as something else. The left-to-right and
// right-to-left marks and the joiners that Persian and Indic scripts need are fine, and stay allowed.
func reordersText(r rune) bool {
	return r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 || r == 0x2028 || r == 0x2029 || r == 0xFEFF
}

// AdapterLabel preserves the system name in terminal output so it can still be
// used with -include, -weight, limit and alias. Scheduling always uses that name.
func AdapterLabel(name string, aliases map[string]string) string {
	if a := aliases[name]; a != "" && a != name {
		return a + " (" + name + ")"
	}
	return name
}

// UpdateTrayPreferences applies change to the saved preferences and saves them. A file that can't
// be decoded is first moved to tray-preferences.json.broken, so nothing in it is lost, and the change
// is applied to the defaults: otherwise one damaged file would leave every tick box and alias stuck,
// each click only showing an error. A file that can't be read at all is left alone.
func UpdateTrayPreferences(dir string, change func(*TrayPreferences)) (TrayPreferences, error) {
	p, err := LoadTrayPreferences(dir)
	if errors.Is(err, ErrDamagedPreferences) {
		path := TrayPreferencesPath(dir)
		err = replaceFile(path, path+".broken")
	}
	if err != nil {
		return p, err
	}
	change(&p)
	return p, SaveTrayPreferences(dir, p)
}

func SetAlias(dir, name, alias string) error {
	if err := ValidateAlias(name, alias); err != nil {
		return err
	}
	_, err := UpdateTrayPreferences(dir, func(p *TrayPreferences) {
		if alias == "" {
			delete(p.Aliases, name)
		} else {
			p.Aliases[name] = alias
		}
	})
	return err
}
