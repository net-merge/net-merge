package winmenu

import "strings"

// Segoe Fluent Icons code points (Segoe MDL2 Assets has the same ones, for Windows 10). They are drawn
// like text, in the row's ink colour, so they are black in light mode and white in dark mode. They are
// private-use characters that no editor shows, so each is written as its code point.
const (
	iconNetwork  = string(rune(0xE968))
	iconWifi     = string(rune(0xE701))
	iconEthernet = string(rune(0xE839))
	iconPhone    = string(rune(0xE8EA))
	iconUSB      = string(rune(0xE88E))
	iconBT       = string(rune(0xE702))
	iconSpeed    = string(rune(0xEC4A))
	iconPause    = string(rune(0xE769))
	iconPlay     = string(rune(0xE768))
	iconWarning  = string(rune(0xE7BA))
	iconSync     = string(rune(0xE895))
	iconGlobe    = string(rune(0xE774))
	iconLink     = string(rune(0xE71B))
	iconPerson   = string(rune(0xE77B))
	iconInfo     = string(rune(0xE946))
	iconDownload = string(rune(0xE896))
	iconPower    = string(rune(0xE7E8))
	iconGame     = string(rune(0xE7FC)) // a game controller
	iconCool     = string(rune(0xE9CA)) // a thermometer
	iconProvider = string(rune(0xE825)) // a building with columns: the company that provides the connection
)

// leftToRight is U+200E, the invisible mark that the tray puts after an adapter's name so its numbers
// stay on the same side of an Arabic or Hebrew name.
const leftToRight = string(rune(0x200E))

// iconFor picks a row's icon from its title. Titles change while the menu runs, so this is asked each
// time a row is drawn: "Start Merging" and "Pause Merging" swap icons by themselves. A row with no icon,
// like a speed limit in a submenu, returns "" and shows its check mark on the left instead.
func iconFor(title string, hasSub bool) string { return iconForItem(title, hasSub, "") }

// iconForItem is iconFor for an item that may also know its kind (see Item.SetAdapterKind). An adapter
// whose kind is known gets that kind's icon; one whose kind is not is guessed from its name.
func iconForItem(title string, hasSub bool, kind string) string {
	if hasSub { // an adapter, as "Wi-Fi  ↓79 ↑7.0 Mbps  (31 flows via net-merge)"
		switch kind {
		case "wifi":
			return iconWifi
		case "ethernet":
			return iconEthernet
		case "phone":
			return iconPhone
		}
		return adapterIcon(adapterName(title))
	}
	switch {
	case strings.HasPrefix(title, "Merging"):
		return iconSpeed
	case strings.HasPrefix(title, "Paused"), title == "Pause Merging":
		return iconPause
	case strings.HasPrefix(title, "Error"):
		return iconWarning
	case strings.HasPrefix(title, "Turning On"):
		return iconSync
	case title == "net-merge":
		return iconNetwork
	case title == "Start Merging":
		return iconPlay
	case strings.HasPrefix(title, "Use All Adapters"):
		return iconGlobe
	case title == "Show ISP Names":
		return iconProvider
	case title == "Game Mode":
		return iconGame
	case title == "Auto-Pause":
		return iconLink
	case strings.HasPrefix(title, "Cool Down"):
		return iconCool
	case title == "Start at Sign-In":
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

// adapterName is the adapter's own name in an adapter row, which also carries its speed and, when asked
// for, an ISP name: "Home · Example ISP  ↓1 ↑1 Mbps" and the older "Wi-Fi:  ↓1 ↑1 Mbps" both give the part
// before that. What follows the name must not change the icon.
func adapterName(title string) string {
	title = strings.TrimLeft(title, leftToRight)
	end := len(title)
	for _, sep := range []string{":", leftToRight, " · ", "  ↓"} {
		if i := strings.Index(title, sep); i >= 0 && i < end {
			end = i
		}
	}
	return title[:end]
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

// isVersion matches the version row: "v0.3.7", or a build between releases such as "v0.3.7-5-g1a2b3c".
func isVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' || s[1] < '0' || s[1] > '9' {
		return false
	}
	for _, c := range s[1:] {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '.', c == '-', c == '+':
		default:
			return false
		}
	}
	return true
}
