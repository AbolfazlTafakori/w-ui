package upgradecheck

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSub(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "v2.6.0", "sub", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Each kind of configuration a link serves reads as what a client needs.
func TestProfilesReadAsTheirApp(t *testing.T) {
	for name, want := range map[string]struct {
		kind  string
		count int
		keys  []string
	}{
		"wg-limited.conf":    {"wireguard", 2, []string{"interface.privatekey", "peer.endpoint", "peer.presharedkey"}},
		"awg-first-use.conf": {"wireguard", 1, []string{"interface.jc", "interface.h4"}},
		"ovpn-user.conf":     {"openvpn", 1, []string{"remote", "<ca>", "auth-user-pass"}},
		"two-tunnels.conf":   {"wireguard", 6, []string{"interface.address"}},
	} {
		ps, err := ParseProfiles(readSub(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ps) != want.count {
			t.Errorf("%s: %d configurations, want %d", name, len(ps), want.count)
		}
		for _, p := range ps {
			if p.Kind != want.kind {
				t.Errorf("%s: read as %s", name, p.Kind)
			}
			if err := p.Validate(); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			for _, k := range want.keys {
				if _, ok := p.Keys[k]; !ok {
					t.Errorf("%s: no %s", name, k)
				}
			}
		}
	}
}

// A configuration written differently but connecting the same way is the
// same; one that would connect differently is not.
func TestSameProfilesTellsWhatMatters(t *testing.T) {
	orig := string(readSub(t, "awg-first-use.conf"))
	parse := func(s string) []Profile {
		ps, err := ParseProfiles([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return ps
	}
	base := parse(orig)

	same := map[string]string{
		"a comment added": "# written by a newer panel\n" + orig,
		"spacing changed": strings.ReplaceAll(orig, " = ", "="),
		"a line added":    strings.Replace(orig, "[Peer]", "[Peer]\nPersistentKeepalive = 15", 1),
		"base64 encoded":  base64.StdEncoding.EncodeToString([]byte(orig)),
		"DNS changed":     strings.Replace(orig, "DNS = ", "DNS = 9.9.9.9, ", 1),
	}
	for name, s := range same {
		if err := SameProfiles(base, parse(s)); err != nil {
			t.Errorf("%s: read as connecting differently: %v", name, err)
		}
	}

	line := func(prefix string) string {
		for _, l := range strings.Split(orig, "\n") {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		t.Fatalf("no %s line", prefix)
		return ""
	}
	different := map[string]string{
		"another key":       strings.Replace(orig, line("PrivateKey"), "PrivateKey = AAAA", 1),
		"another port":      strings.Replace(orig, line("Endpoint"), line("Endpoint")+"9", 1),
		"another Jc":        strings.Replace(orig, line("Jc"), "Jc = 99", 1),
		"the preshared key": strings.Replace(orig, line("PresharedKey"), "", 1),
		"a second device":   orig + "\n\n" + orig,
	}
	for name, s := range different {
		if err := SameProfiles(base, parse(s)); err == nil {
			t.Errorf("%s: read as connecting the same way", name)
		}
	}
}
