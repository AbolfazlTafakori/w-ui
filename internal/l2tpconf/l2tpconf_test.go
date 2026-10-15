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
		"no key":                   func(s *State) { s.Interface.PSK = "" },
		"space in the key":         func(s *State) { s.Interface.PSK = "Shared Key" },
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
	got := ChapSecrets(existing, ProtocolL2TP, valid())
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
	again := ChapSecrets(got, ProtocolL2TP, valid())
	if again != got {
		t.Fatalf("rewriting is not idempotent:\n%s", again)
	}
	removed := ChapSecrets(got, ProtocolL2TP, nil)
	if removed != existing {
		t.Fatalf("removing left %q, want %q", removed, existing)
	}
}

func TestPPPOptionsTakeTheAddressFromTheSecretsLine(t *testing.T) {
	got := PPPOptions(valid())
	for _, want := range []string{"name wui-l2tp\n", "10.77.0.1:\n", "require-mschap-v2\n", "refuse-pap\n", "ms-dns 1.1.1.1\n", "mtu 1280\n", "noccp\n"} {
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
	for _, want := range []string{"version = 1", "mode = transport", "local_ts = dynamic[17/1701]", `secret = "SharedKey123"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGuardDropsL2TPOutsideIPsec(t *testing.T) {
	if g := Guard(valid()); !strings.Contains(g, "udp dport 1701 meta ipsec missing drop") {
		t.Errorf("guard:\n%s", g)
	}
}

// A tunnel that serves routers without IPsec opens the port to them, and
// accepts the CHAP their PPP has; never a password sent as it is, and no
// MPPE, which pppd could only require of every router.
func TestATunnelForRoutersWithoutIPsec(t *testing.T) {
	s := valid()
	s.Interface.AllowPlain = true
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if g := Guard(s); strings.Contains(g, "drop") || g != GuardRemoval() {
		t.Errorf("the port stays closed to plain L2TP:\n%s", g)
	}
	opts := PPPOptions(s)
	if !strings.Contains(opts, "noccp\n") || strings.Contains(opts, "mppe") {
		t.Errorf("MPPE is negotiated, and would be required of every router:\n%s", opts)
	}
	for _, want := range []string{"require-mschap-v2\n", "require-chap\n", "refuse-pap\n"} {
		if !strings.Contains(opts, want) {
			t.Errorf("missing %q:\n%s", want, opts)
		}
	}
	if strings.Contains(opts, "refuse-chap") {
		t.Errorf("a router that only has CHAP is turned away:\n%s", opts)
	}
	// A tunnel for phones and computers stays MS-CHAPv2 only.
	if o := PPPOptions(valid()); !strings.Contains(o, "refuse-chap\n") || strings.Contains(o, "require-chap") {
		t.Errorf("an IPsec-only tunnel accepts CHAP:\n%s", o)
	}
	// A different tunnel as far as the agent is concerned: xl2tpd restarts.
	if Fingerprint(PPPOptions(valid())) == Fingerprint(opts) {
		t.Error("turning plain L2TP on changes nothing the agent would notice")
	}
}

// Each client that has a built-in L2TP/IPsec offers its own fixed set; the
// server has to accept at least one combination from each, or that device
// cannot connect and nothing on its screen says why.
func TestEveryBuiltInClientFindsAProposal(t *testing.T) {
	got := Swanctl(valid())
	for client, want := range map[string][]string{
		"Windows as installed (3DES, SHA-1, group 2)":       {"3des-sha1-modp1024", "esp_proposals = ", "3des-sha1"},
		"Windows told to prefer AES-256 and group 14":       {"aes256-sha1-modp2048", "aes256-sha1"},
		"iOS and macOS (AES-256, SHA-256, group 14)":        {"aes256-sha256-modp2048", "aes256-sha256"},
		"MikroTik with use-ipsec (AES-128, SHA-1, group 2)": {"aes128-sha1-modp1024", "aes128-sha1"},
		"Android 11 and earlier (group 2)":                  {"aes256-sha1-modp1024"},
		"NetworkManager-l2tp defaults (group 14)":           {"aes128-sha1-modp2048"},
		"clients offering elliptic curves":                  {"aes256-sha256-ecp384", "aes256-sha256-ecp256"},
		"clients offering AES-GCM for the data":             {"aes256gcm16", "aes128gcm16"},
	} {
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q is not offered", client, w)
			}
		}
	}
	// Strongest first: a device that offers both gets the better one.
	if strings.Index(got, "aes256-sha256-modp2048") > strings.Index(got, "3des-sha1-modp1024") {
		t.Error("the proposals are not strongest first")
	}
}

// A key an existing service already handed out is kept as it is, however
// short; the panel says so rather than refuse it.
func TestAShortKeyFromAnExistingServiceIsAccepted(t *testing.T) {
	for _, k := range []string{"1", "123", "vpn"} {
		if err := CheckPSK(k); err != nil {
			t.Errorf("%q: %v", k, err)
		}
	}
	if WeakPSK <= 3 {
		t.Errorf("WeakPSK is %d; a key as short as 123 must be called weak", WeakPSK)
	}
}

func TestKickNames(t *testing.T) {
	for name, want := range map[string]bool{"12": true, "0": false, "012": false, "-1": false, "a": false, "../1": false, "99999999999": false} {
		if _, ok := ParseKickName(name); ok != want {
			t.Errorf("ParseKickName(%q) ok=%v, want %v", name, ok, want)
		}
	}
}

// A modem with a "Tunnel Password" field may ask the server to prove it; the
// server answers with the tunnel's key, and never asks a client to prove one.
func TestTheTunnelSecretIsTheKey(t *testing.T) {
	x := XL2TPD(valid())
	for _, want := range []string{"auth file = /run/wui-l2tp/l2tp-secrets\n", "challenge = no\n"} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %q in:\n%s", want, x)
		}
	}
	if got := TunnelSecrets(valid()); got != "* * SharedKey123\n" {
		t.Errorf("secrets = %q", got)
	}
	// What xl2tpd would cut short or read as a comment is not written.
	for _, k := range []string{"key!123", "key;123"} {
		s := valid()
		s.Interface.PSK = k
		if got := TunnelSecrets(s); strings.Contains(got, k) || !strings.HasPrefix(got, "#") {
			t.Errorf("%q: secrets = %q", k, got)
		}
	}
}

// pppd is never given "lock": Ubuntu 24.04's refuses it, and with it every
// L2TP and PPTP session.
func TestNoSessionIsGivenLock(t *testing.T) {
	s := &State{Interface: Iface{Gateway: "10.77.0.1", MTU: 1400, DNS: []string{"10.77.0.1"}}}
	for name, opts := range map[string]string{"L2TP": PPPOptions(s), "PPTP": PPTPOptions(s)} {
		for _, line := range strings.Split(opts, "\n") {
			if strings.TrimSpace(line) == "lock" {
				t.Fatalf("%s's options lock the line:\n%s", name, opts)
			}
		}
	}
}
