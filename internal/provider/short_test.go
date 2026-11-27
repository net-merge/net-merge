package provider

import "testing"

func TestShortISP(t *testing.T) {
	for full, want := range map[string]string{
		"Railtel Corporation of India Ltd":       "Railtel",
		"RailTel Corporation of India Limited":   "RailTel",
		"Bharti Airtel Ltd.":                     "Airtel",
		"Reliance Jio Infocomm Limited":          "Jio",
		"Bharat Sanchar Nigam Ltd":               "BSNL",
		"Atria Convergence Technologies Pvt Ltd": "ACT",
		"Hathway IP Over Cable Internet":         "Hathway",
		"Excitel Broadband Private Limited":      "Excitel",
		"Comcast Cable Communications, LLC":      "Comcast",
		"AT&T Services, Inc.":                    "AT&T",
		"Google, LLC":                            "Google",
		"Deutsche Telekom AG":                    "Deutsche Telekom",
		"Vodafone Idea Ltd":                      "Vodafone Idea",
		"Cable One":                              "Cable One",
		"Example ISP":                            "Example ISP",
		"":                                       "",
	} {
		if got := shortISP(full); got != want {
			t.Errorf("shortISP(%q) = %q, want %q", full, got, want)
		}
	}
}
