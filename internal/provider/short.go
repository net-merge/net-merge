package provider

import "strings"

// Brands that are not simply the first words of the registered name.
// ponytail: a few big Indian providers; add a line when another reads badly.
var brands = []struct{ prefix, brand string }{
	{"bharti airtel", "Airtel"},
	{"reliance jio", "Jio"},
	{"bharat sanchar", "BSNL"},
	{"atria convergence", "ACT"},
}

// The first of these words, and everything after it, is the company's legal or trade tail.
var tailWords = map[string]bool{
	"ag": true, "bv": true, "broadband": true, "cable": true, "co": true, "communication": true,
	"communications": true, "company": true, "corp": true, "corporation": true, "gmbh": true, "inc": true,
	"infocomm": true, "internet": true, "ip": true, "limited": true, "llc": true, "ltd": true, "network": true,
	"networks": true, "plc": true, "private": true, "pty": true, "pvt": true, "sa": true, "services": true,
	"technologies": true,
}

// shortISP turns a registered company name into the name people use: "Railtel Corporation of India
// Ltd" is "Railtel". A name that starts with a tail word is left as it is.
func shortISP(name string) string {
	lower := strings.ToLower(name)
	for _, b := range brands {
		if strings.HasPrefix(lower, b.prefix) {
			return b.brand
		}
	}
	words := strings.Fields(name)
	for i, w := range words {
		if i > 0 && tailWords[strings.ToLower(strings.NewReplacer(".", "", ",", "").Replace(w))] {
			return strings.TrimRight(strings.Join(words[:i], " "), ",")
		}
	}
	return name
}
