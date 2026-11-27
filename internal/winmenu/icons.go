package winmenu

import "strings"

// Segoe Fluent Icons code points (Segoe MDL2 Assets has the same ones, for Windows 10). They are drawn
// like text, in the row's ink colour, so they are black in light mode and white in dark mode.
const (
	iconNetwork  = ""
	iconWifi     = ""
	iconEthernet = ""
	iconPhone    = ""
	iconUSB      = ""
	iconBT       = ""
	iconSpeed    = ""
	iconPause    = ""
	iconPlay     = ""
	iconWarning  = ""
	iconSync     = ""
	iconGlobe    = ""
	iconLink     = ""
	iconPerson   = ""
	iconInfo     = ""
	iconDownload = ""
	iconPower    = ""
)

// iconFor picks a row's icon from its title. Titles change while the menu runs, so this is asked each
// time a row is drawn: "Start merging" and "Pause merging" swap icons by themselves. A row with no icon,
// like a speed limit in a submenu, returns "" and shows its check mark on the left instead.
func iconFor(title string, hasSub bool) string {
	if hasSub { // an adapter, as "Wi-Fi:  ↓79 ↑7.0 Mbps  (31 flows via net-merge)"
		name, _, _ := strings.Cut(title, ":")
		return adapterIcon(name)
	}
	switch {
	case strings.HasPrefix(title, "Merging"):
		return iconSpeed
	case strings.HasPrefix(title, "Paused"), title == "Pause merging":
		return iconPause
	case strings.HasPrefix(title, "Error"):
		return iconWarning
	case strings.HasPrefix(title, "Turning on"):
		return iconSync
	case title == "net-merge":
		return iconNetwork
	case title == "Start merging":
		return iconPlay
	case strings.HasPrefix(title, "Use all adapters"):
		return iconGlobe
	case strings.HasPrefix(title, "Pause when"):
		return iconLink
	case title == "Start at sign-in":
		return iconPerson
	case strings.HasPrefix(title, "Update to"):
		return iconDownload
	case title == "dev", isVersion(title):
		return iconInfo
	case title == "Quit":
		return iconPower
	}
	return ""
}

// adapterIcon picks an adapter's icon from its Windows name.
func adapterIcon(name string) string {
	n := strings.ToLower(name)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(n, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("wi-fi", "wifi", "wlan", "wireless"):
		return iconWifi
	case has("iphone", "android", "phone", "cellular", "mobile", "tether", "rndis"):
		return iconPhone
	case has("bluetooth"):
		return iconBT
	case has("usb"):
		return iconUSB
	case has("ethernet", "local area connection", "lan"):
		return iconEthernet
	}
	return iconNetwork
}

// isVersion matches the version row, as "v0.3.7".
func isVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if (c < '0' || c > '9') && c != '.' {
			return false
		}
	}
	return true
}
