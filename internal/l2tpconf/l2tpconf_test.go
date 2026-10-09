package l2tpconf

import (
	"errors"
	"strings"
	"testing"
)

func valid() *State {
	return &State{
		Version: SchemaVersion,
		Interface: Iface{
			ID: 4, Name: "l2tp0", Subnet: "10.77.0.0/24", Gateway: "10.77.0.1",
			DNS: []string{"1.1.1.1"}, MTU: DefaultMTU, PSK: "SharedKey123",
			Accounts: []Account{
				{ID: 2, Username: "s9-phone", Secret: "Secret99", IP: "10.77.0.3"},
				{ID: 1, Username: "s9-laptop", Secret: "Secret11", IP: "10.77.0.2"},
			},
		},
	}
}

func TestAValidStateRoundTrips(t *testing.T) {
	raw, err := Encode(valid())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Interface.Accounts[0].ID != 1 {
		t.Errorf("accounts are written in id order, got %+v", s.Interface.Accounts)
	}
	again, _ := Encode(s)
	if string(again) != string(raw) {
		t.Error("encoding is not stable: an unchanged tunnel would look changed")
	}
}

// Everything the panel writes reaches files root hands to pppd, xl2tpd and
// strongSwan. A value that could change what such a file says is refused,
// whoever sent it.
func TestValidateRefusesWhatCouldReachAFileInTheWrongShape(t *testing.T) {
	cases := map[string]func(s *State){
		"newline in a username":    func(s *State) { s.Interface.Accounts[0].Username = "a\nplugin evil.so" },
		"space in a username":      func(s *State) { s.Interface.Accounts[0].Username = "a b" },
		"quote in a password":      func(s *State) { s.Interface.Accounts[0].Secret = `abc"def` },
		"backslash in a password":  func(s *State) { s.Interface.Accounts[0].Secret = `abc\def` },
		"hash in a password":       func(s *State) { s.Interface.Accounts[0].Secret = "abc#defg" },
		"space in a password":      func(s *State) { s.Interface.Accounts[0].Secret = "abc defg" },
		"short password":           func(s *State) { s.Interface.Accounts[0].Secret = "abc" },
		"non-ASCII password":       func(s *State) { s.Interface.Accounts[0].Secret = "پسورد123" },
		"quote in the key":         func(s *State) { s.Interface.PSK = `Shared"Key` },
		"newline in the key":       func(s *State) { s.Interface.PSK = "Shared\nKey123" },
		"short key":                func(s *State) { s.Interface.PSK = "short" },
		"slash in the name":        func(s *State) { s.Interface.Name = "../etc" },
		"long name":                func(s *State) { s.Interface.Name = "a-very-long-tunnel-name" },
		"address outside":          func(s *State) { s.Interface.Accounts[0].IP = "10.78.0.3" },
		"address is the gateway":   func(s *State) { s.Interface.Accounts[0].IP = "10.77.0.1" },
		"address is the network":   func(s *State) { s.Interface.Accounts[0].IP = "10.77.0.0" },
		"address is the broadcast": func(s *State) { s.Interface.Accounts[0].IP = "10.77.0.255" },
		"two devices one address":  func(s *State) { s.Interface.Accounts[0].IP = "10.77.0.2" },
		"two devices one name":     func(s *State) { s.Interface.Accounts[0].Username = "s9-laptop" },
		"repeated id":              func(s *State) { s.Interface.Accounts[0].ID = 1 },
		"gateway outside":          func(s *State) { s.Interface.Gateway = "10.1.0.1" },
		"v6 subnet":                func(s *State) { s.Interface.Subnet = "fd00::/64" },
		"unmasked subnet":          func(s *State) { s.Interface.Subnet = "10.77.0.5/24" },
		"resolver not an address":  func(s *State) { s.Interface.DNS = []string{"1.1.1.1\nplugin x"} },
		"mtu too large":            func(s *State) { s.Interface.MTU = 9000 },
		"unknown version":          func(s *State) { s.Version = 2 },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid()
			spoil(s)
			if err := s.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

// The panel's lines in /etc/ppp/chap-secrets are fenced; everything else in
// the file is somebody else's and survives every write, and removing the
// tunnel removes only the fence.
func TestChapSecretsKeepsWhatIsNotOurs(t *testing.T) {
	existing := "# other links\nalice * \"pw\" *\n"
	got := ChapSecrets(existing, valid())
	if !strings.HasPrefix(got, existing) {
		t.Fatalf("the other lines were not kept:\n%s", got)
	}
	for _, want := range []string{
		`s9-laptop wui-l2tp "Secret11" 10.77.0.2`,
		`s9-phone wui-l2tp "Secret99" 10.77.0.3`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Written again over itself: one block, not two.
	again := ChapSecrets(got, valid())
	if again != got {
		t.Fatalf("rewriting is not idempotent:\n%s", again)
	}
	removed := ChapSecrets(got, nil)
	if removed != existing {
		t.Fatalf("removing left %q, want %q", removed, existing)
	}
}

func TestPPPOptionsTakeTheAddressFromTheSecretsLine(t *testing.T) {
	got := PPPOptions(valid())
	for _, want := range []string{"name wui-l2tp\n", "10.77.0.1:\n", "require-mschap-v2\n", "refuse-pap\n", "ms-dns 1.1.1.1\n", "mtu 1400\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if x := XL2TPD(valid()); !strings.Contains(x, "assign ip = no\n") || !strings.Contains(x, "pppoptfile = /run/wui-l2tp/options\n") {
		t.Errorf("xl2tpd must leave addresses to pppd:\n%s", x)
	}
}

func TestSwanctlCarriesTheKeyAndOnlyL2TP(t *testing.T) {
	got := Swanctl(valid())
	for _, want := range []string{"version = 1", "mode = transport", "local_ts = dynamic[udp/1701]", `secret = "SharedKey123"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGuardDropsL2TPOutsideIPsec(t *testing.T) {
	if !strings.Contains(Guard(), "udp dport 1701 meta ipsec missing drop") {
		t.Errorf("guard:\n%s", Guard())
	}
}

func TestKickNames(t *testing.T) {
	for name, want := range map[string]bool{"12": true, "0": false, "012": false, "-1": false, "a": false, "../1": false, "99999999999": false} {
		if _, ok := ParseKickName(name); ok != want {
			t.Errorf("ParseKickName(%q) ok=%v, want %v", name, ok, want)
		}
	}
}
