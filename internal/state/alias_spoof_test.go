package state

import "testing"

// Written as code points: these are invisible characters, and must stay visible in the source.
func chars(codes ...rune) string { return string(codes) }

func TestAliasRejectsTextDirectionOverridesButKeepsRealScripts(t *testing.T) {
	for _, bad := range []string{
		"safe" + chars(0x202E) + "evil", // right-to-left override
		"a" + chars(0x202A) + "b",       // left-to-right embedding
		chars(0x2066) + "x" + chars(0x2069),
		"a" + chars(0x2028) + "b", // line separator
		"a" + chars(0x2029) + "b", // paragraph separator
		chars(0xFEFF) + "a",       // byte-order mark
	} {
		if ValidateAlias("Ethernet", bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if ValidateAlias("Eth"+chars(0x202E)+"ernet", "Home") == nil {
		t.Error("accepted an adapter name with an override")
	}
	for _, ok := range []string{
		"خط البيت",
		"Home" + chars(0x200E),          // left-to-right mark
		chars(0x200F) + "البيت",         // right-to-left mark
		"کارگاه" + chars(0x200C) + "ها", // zero-width non-joiner, needed in Persian
		"Jio phone",
	} {
		if err := ValidateAlias("Ethernet", ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
}
