package nics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxPresentationTypesFromSysfs(t *testing.T) {
	for _, tc := range []struct {
		name, driver string
		wireless     bool
		want         Kind
	}{
		{"wifi", "iwlwifi", true, WiFi}, {"ethernet", "r8169", false, Ethernet},
		{"iphone", "ipheth", false, Phone}, {"android", "rndis_host", false, Phone},
		{"virtual", "", false, Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.driver != "" {
				if err := os.MkdirAll(filepath.Join(root, "device"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/drivers/"+tc.driver, filepath.Join(root, "device", "driver")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.wireless {
				if err := os.Mkdir(filepath.Join(root, "wireless"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "type"), []byte("1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := linuxKind(root); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
