package nics

import "strings"

// Kind is a presentation hint only; it never affects discovery or scheduling.
type Kind string

const (
	Unknown  Kind = "network"
	WiFi     Kind = "wifi"
	Ethernet Kind = "ethernet"
	Phone    Kind = "phone"
)

func phoneDescription(description string) bool {
	s := strings.ToLower(description)
	for _, word := range []string{"iphone", "apple mobile device", "ipheth", "rndis", "remote ndis", "usb tether", "android", "wwan"} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}
