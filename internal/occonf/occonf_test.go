package occonf

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func tunnel(t *testing.T) *State {
	t.Helper()
	cert, key, err := NewServerCert([]string{"vpn.example.com", "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	return &State{Version: SchemaVersion, Interface: Iface{
		ID: 4, Name: "oc0", Port: 443, Subnet: "10.81.0.0/24", Gateway: "10.81.0.1",
		DNS: []string{"1.1.1.1"}, MTU: DefaultMTU, DTLS: true,
		Camouflage: true, CamouflageSecret: "Hidden42Door",
		Cert: cert, Key: key,
		Accounts: []Account{
			{ID: 10, Username: "s1-ali", Secret: "Pws1-alix9", IP: "10.81.0.2"},
			{ID: 11, Username: "s1-sara", Secret: "Pws1-sarax9", IP: "10.81.0.3"},
		},
	}}
}

func TestAWholeTunnelValidates(t *testing.T) {
	s := tunnel(t)
	raw, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(raw)
	if err != nil || len(back.Interface.Accounts) != 2 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

// Anything that could change a file's meaning is refused, not written.
func TestWhatCouldMisleadAFileIsRefused(t *testing.T) {
	otherCert, otherKey, _ := NewServerCert([]string{"other.example"})
	cases := map[string]func(*State){
		"version":            func(s *State) { s.Version = 9 },
		"no id":              func(s *State) { s.Interface.ID = 0 },
		"name with a space":  func(s *State) { s.Interface.Name = "oc 0" },
		"port":               func(s *State) { s.Interface.Port = 70000 },
		"ipv6 subnet":        func(s *State) { s.Interface.Subnet = "fd00::/64" },
		"gateway outside":    func(s *State) { s.Interface.Gateway = "10.9.0.1" },
		"resolver name":      func(s *State) { s.Interface.DNS = []string{"dns.example"} },
		"mtu":                func(s *State) { s.Interface.MTU = 1500 },
		"camouflage quote":   func(s *State) { s.Interface.CamouflageSecret = `abc"defgh` },
		"camouflage short":   func(s *State) { s.Interface.CamouflageSecret = "abc" },
		"key of another":     func(s *State) { s.Interface.Key = otherKey },
		"cert with a key in": func(s *State) { s.Interface.Cert += otherKey },
		"not a cert":         func(s *State) { s.Interface.Cert = "hello" },
		"username space":     func(s *State) { s.Interface.Accounts[0].Username = "a b" },
		"password newline":   func(s *State) { s.Interface.Accounts[0].Secret = "Pass\nword1" },
		"same username":      func(s *State) { s.Interface.Accounts[1].Username = "s1-ali" },
		"same address":       func(s *State) { s.Interface.Accounts[1].IP = "10.81.0.2" },
		"gateway address":    func(s *State) { s.Interface.Accounts[0].IP = "10.81.0.1" },
		"outside address":    func(s *State) { s.Interface.Accounts[0].IP = "10.82.0.2" },
	}
	_ = otherCert
	for name, edit := range cases {
		s := tunnel(t)
		edit(s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	// Camouflage off: no secret needed.
	s := tunnel(t)
	s.Interface.Camouflage, s.Interface.CamouflageSecret = false, ""
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

// ocserv asks the agent's RADIUS for every sign-in and takes each device's
// address from it; no password is written into ocserv's files; there is no
// limit of ocserv's own on sessions, which the agent decides.
func TestTheServerAsksTheAgentForEverySignIn(t *testing.T) {
	s := tunnel(t)
	conf := Ocserv(s)
	for _, want := range []string{
		`auth = "radius[config=/etc/wui-ocserv/radiusclient.conf,groupconfig=true]"`,
		"tcp-port = 443", "udp-port = 443", "max-same-clients = 0", "ipv4-network = 10.81.0.0",
		"ipv4-netmask = 255.255.255.0", "dns = 1.1.1.1", "mtu = 1400", "camouflage = true",
		`camouflage_secret = "Hidden42Door"`, "device = vpns", "occtl-socket-file = /run/wui-occtl.socket",
		"isolate-workers = true", "compression = false",
	} {
		if !strings.Contains(conf, want+"\n") {
			t.Errorf("ocserv.conf lacks %q", want)
		}
	}
	for _, a := range s.Interface.Accounts {
		if strings.Contains(conf, a.Secret) {
			t.Fatal("a password is in ocserv's configuration")
		}
	}
	radcli := Radcli("RadSecret9")
	if !strings.Contains(radcli, "authserver 127.0.0.1:18121:RadSecret9\n") {
		t.Fatalf("radcli:\n%s", radcli)
	}
	// DTLS off: TCP only.
	s.Interface.DTLS = false
	s.Interface.Camouflage = false
	conf = Ocserv(s)
	if strings.Contains(conf, "udp-port") || strings.Contains(conf, "camouflage") {
		t.Fatalf("DTLS and camouflage off:\n%s", conf)
	}
	secret := map[string]bool{}
	for _, f := range Files(tunnel(t), "x") {
		secret[f.Name] = f.Secret
	}
	if !secret[KeyFile] || !secret[RadcliFile] || secret[ConfFile] || secret[CertFile] {
		t.Fatalf("which files are root's alone: %v", secret)
	}
}

func TestTheAddressADeviceDials(t *testing.T) {
	for _, c := range []struct {
		host   string
		port   int
		cam    bool
		secret string
		want   string
	}{
		{"vpn.example.com", 443, false, "", "vpn.example.com"},
		{"vpn.example.com", 8443, false, "", "vpn.example.com:8443"},
		{"vpn.example.com", 443, true, "Hidden42Door", "vpn.example.com/?Hidden42Door"},
		{"2001:db8::1", 4443, true, "abcdefgh", "[2001:db8::1]:4443/?abcdefgh"},
	} {
		if got := URL(c.host, c.port, c.cam, c.secret); got != c.want {
			t.Errorf("URL(%s, %d) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestTheTunnelsOwnCertificate(t *testing.T) {
	cert, key, err := NewServerCert([]string{"vpn.example.com", "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkCertPair(cert, key); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if NeedsRenewal(cert, "vpn.example.com", now) || NeedsRenewal(cert, "203.0.113.10", now) {
		t.Fatal("a fresh certificate needs renewing")
	}
	if !NeedsRenewal(cert, "other.example", now) || !NeedsRenewal(cert, "vpn.example.com", now.Add(800*24*time.Hour)) {
		t.Fatal("a certificate for another name, or near its end, is kept")
	}
	if !Covers(cert, "vpn.example.com", now) || Covers(cert, "other.example", now) {
		t.Fatal("Covers")
	}
	if p := Pin(cert); !strings.HasPrefix(p, "pin-sha256:") || len(p) != len("pin-sha256:")+44 {
		t.Fatalf("pin %q", p)
	}
	if !SelfSigned(cert) {
		t.Fatal("the tunnel's own certificate is not self-signed")
	}
}
