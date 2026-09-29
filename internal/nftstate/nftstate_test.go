package nftstate

import "testing"

func TestHasTable(t *testing.T) {
	out := []byte("table inet filter\ntable inet wui\ntable ip wui_nat\ntable ip6 docker\n")
	for _, c := range []struct {
		family, name string
		want         bool
	}{
		{"inet", "wui", true},
		{"ip", "wui_nat", true},
		{"inet", "wui_policy", false}, // flushed away
		{"ip", "wui", false},          // the right name in the wrong family is not it
		{"inet", "wu", false},         // nor a prefix of it
	} {
		if got := HasTable(out, c.family, c.name); got != c.want {
			t.Errorf("HasTable(%s %s) = %v, want %v", c.family, c.name, got, c.want)
		}
	}
	if HasTable(nil, "inet", "wui") {
		t.Error("an empty ruleset -- everything flushed -- was read as having the table")
	}
}

func TestRuleMarks(t *testing.T) {
	out := []byte(`0:	from all lookup local
20011:	from all fwmark 0xa7000b lookup 47011
20012:	from all fwmark 0xa7000c/0xffffffff lookup 47012
32000:	from all fwmark 0x1 lookup vpn
32766:	from all lookup main
32767:	from all lookup default
`)
	got := RuleMarks(out)
	want := map[uint32]string{0xa7000b: "47011", 0xa7000c: "47012", 0x1: "vpn"}
	if len(got) != len(want) {
		t.Fatalf("RuleMarks = %v, want %v", got, want)
	}
	for m, table := range want {
		if got[m] != table {
			t.Errorf("mark %#x looks up %q, want %q", m, got[m], table)
		}
	}
}
