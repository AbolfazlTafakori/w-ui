package service

import "testing"

// The reasons an engine gives are Go errors: several package prefixes and then
// one sentence meant for a person. Only the last part belongs in the panel.
func TestEngineReasonsReadAsSentencesNotAsGoErrors(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"enforce: enforcement backend unavailable: nftables is Linux-only and this panel is running on windows",
			"Nftables is Linux-only and this panel is running on windows.",
		},
		{
			"shaper: traffic shaping is unavailable: tc is Linux-only and this panel is running on windows",
			"Tc is Linux-only and this panel is running on windows.",
		},
		{
			"routing: policy routing unavailable: packet marking and policy routing are Linux-only, and this panel is running on windows",
			"Packet marking and policy routing are Linux-only, and this panel is running on windows.",
		},
		{"", ""},
	}
	for _, c := range cases {
		if got := plainReason(c.in); got != c.want {
			t.Errorf("plainReason(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestARealSentenceKeepsItsColons(t *testing.T) {
	// A message that happens to contain a colon — a URL, a quoted value — must
	// survive intact, or the stripper eats the thing the operator needs to read.
	in := "Get \"http://10.0.0.1:2096/api/system\": connection refused"
	if got := plainReason(in); !contains(got, "10.0.0.1:2096") {
		t.Fatalf("the address was stripped out of %q -> %q", in, got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
