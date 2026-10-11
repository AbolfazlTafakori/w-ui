package ikev2conf

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"
)

// tunnel is a valid state with every way of signing in on, and two devices.
func tunnel(t *testing.T) *State {
	t.Helper()
	ca, err := NewAuthority("ike0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := ca.IssueServer([]string{"vpn.example.com", "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	s := &State{Version: SchemaVersion, Interface: Iface{
		ID: 3, Name: "ike0", Subnet: "10.79.0.0/24", Gateway: "10.79.0.1", DNS: []string{"1.1.1.1"},
		Modes:    []string{ModeMSCHAPv2, ModePSK, ModeRSA, ModeEAPTLS},
		ServerID: "vpn.example.com", CACert: ca.Cert, ServerCert: srv.Cert, ServerKey: srv.Key,
	}}
	for i, u := range []string{"s1-ali", "s1-sara"} {
		dev, err := ca.IssueDevice(u)
		if err != nil {
			t.Fatal(err)
		}
		s.Interface.Accounts = append(s.Interface.Accounts, Account{
			ID: uint(10 + i), Username: u, Secret: "Pw" + u + "x9", Cert: dev.Cert, IP: []string{"10.79.0.2", "10.79.0.3"}[i]})
	}
	return s
}

func TestAWholeTunnelValidates(t *testing.T) {
	s := tunnel(t)
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(raw)
	if err != nil || len(back.Interface.Accounts) != 2 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

// Anything that could change a file's meaning, or a device's identity, is
// refused rather than written.
func TestWhatCouldMisleadAFileIsRefused(t *testing.T) {
	other, _ := NewAuthority("other")
	stranger, _ := other.IssueDevice("s1-ali")
	cases := map[string]func(*State){
		"no way in":                  func(s *State) { s.Interface.Modes = nil },
		"an unknown way":             func(s *State) { s.Interface.Modes = []string{"xauth"} },
		"a quote in a secret":        func(s *State) { s.Interface.Accounts[0].Secret = `pa"ss123` },
		"a space in a name":          func(s *State) { s.Interface.Accounts[0].Username = "s1 ali" },
		"a server id newline":        func(s *State) { s.Interface.ServerID = "vpn\nexample" },
		"the server's own id":        func(s *State) { s.Interface.Accounts[0].Username = "vpn.example.com" },
		"two with one name":          func(s *State) { s.Interface.Accounts[1].Username = "s1-ali" },
		"two at one address":         func(s *State) { s.Interface.Accounts[1].IP = "10.79.0.2" },
		"the gateway's address":      func(s *State) { s.Interface.Accounts[0].IP = "10.79.0.1" },
		"outside the subnet":         func(s *State) { s.Interface.Accounts[0].IP = "10.80.0.2" },
		"no certificate":             func(s *State) { s.Interface.Accounts[0].Cert = "" },
		"someone else's certificate": func(s *State) { s.Interface.Accounts[1].Cert = s.Interface.Accounts[0].Cert },
		"a server cert from another authority": func(s *State) {
			x, _ := other.IssueServer([]string{"vpn.example.com"})
			s.Interface.ServerCert = x.Cert
		},
		"no server key": func(s *State) { s.Interface.ServerKey = "" },
	}
	for name, edit := range cases {
		s := tunnel(t)
		edit(s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	// A certificate the right authority did not sign is still a certificate
	// for the name: refused at the door by strongSwan, which checks it
	// against the tunnel's authority and the device's own certificate.
	_ = stranger
	// PSK alone needs no certificates at all.
	s := tunnel(t)
	s.Interface.Modes = []string{ModePSK}
	s.Interface.CACert, s.Interface.ServerCert, s.Interface.ServerKey = "", "", ""
	for i := range s.Interface.Accounts {
		s.Interface.Accounts[i].Cert = ""
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("a PSK-only tunnel: %v", err)
	}
}

// Each device has a connection per way of signing in, matched by its own
// identity and with its own one-address pool; each secret is bound to the
// device's identity.
func TestEachDeviceIsItsOwnConnection(t *testing.T) {
	s := tunnel(t)
	conf := Swanctl(s)
	for _, want := range []string{
		"wui-ikev2-password {", "auth = eap-radius", "pools = radius", "unique = no",
		"wui-ikev2-10-psk {", "wui-ikev2-10-rsa {", "wui-ikev2-10-eap-tls {",
		`id = "s1-ali"`, "auth = eap-tls", `id = "tls-s1-ali"`, "eap_id = %any",
		"pools = wui-ikev2-10", "addrs = 10.79.0.2/32", "dns = 1.1.1.1", "unique = replace",
		"certs = wui-ikev2-10.pem", "cacerts = wui-ikev2-ca.pem", `id = "vpn.example.com"`,
		"modp1024", "fragmentation = yes",
		"ike-wui-ikev2-10 {\n    id = \"s1-ali\"\n    secret = \"Pws1-alix9\"",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Passwords are RADIUS's: none is in strongSwan's file, and no device
	// has a password connection of its own.
	if strings.Contains(conf, "eap-wui-ikev2") || strings.Contains(conf, "-mschapv2 {") {
		t.Fatal("a password is in strongSwan's own file")
	}
	// No secret without an identity: a key with none would stand in for
	// any device.
	for _, block := range strings.Split(conf[strings.Index(conf, "secrets {"):], "  }") {
		if strings.Contains(block, "secret =") && !strings.Contains(block, "id =") {
			t.Fatalf("a secret with no identity: %q", block)
		}
	}
	// Only what is on: PSK alone has no certificates and no EAP.
	s.Interface.Modes = []string{ModePSK}
	conf = Swanctl(s)
	if strings.Contains(conf, "eap") || strings.Contains(conf, "certs") || strings.Contains(conf, "radius") || !strings.Contains(conf, "wui-ikev2-11-psk") {
		t.Fatalf("PSK alone:\n%s", conf)
	}
	for _, f := range Files(s, "x") {
		if f.Dir != "conf.d" {
			t.Fatalf("PSK alone writes %s/%s", f.Dir, f.Name)
		}
	}
}

func TestEveryFileHasItsPlace(t *testing.T) {
	s := tunnel(t)
	got := map[string]bool{}
	for _, f := range Files(s, "RadSecret9") {
		got[f.Dir+"/"+f.Name] = f.Secret
		if f.Dir == SettingsDir && (!strings.Contains(f.Body, "secret = RadSecret9") || !strings.Contains(f.Body, "address = 127.0.0.1")) {
			t.Fatalf("RADIUS settings:\n%s", f.Body)
		}
	}
	for name, secret := range map[string]bool{
		"x509ca/wui-ikev2-ca.pem": false, "x509/wui-ikev2-server.pem": false, "private/wui-ikev2-server.pem": true,
		"x509/wui-ikev2-10.pem": false, "x509/wui-ikev2-11.pem": false, "conf.d/wui-ikev2.conf": true,
		"strongswan.d/wui-ikev2-radius.conf": true,
	} {
		if s, ok := got[name]; !ok || s != secret {
			t.Errorf("%s: present=%v secret=%v", name, ok, s)
		}
	}
}

func TestAConnectionNameGivesItsAccountBack(t *testing.T) {
	for name, want := range map[string]uint{
		ConnName(42, ModeEAPTLS): 42, ConnName(7, ModePSK): 7, "wui-ikev2-x-psk": 0, "wui-l2tp": 0, "wui-ikev2-0-psk": 0,
	} {
		got, ok := ParseConnName(name)
		if got != want || ok != (want != 0) {
			t.Errorf("%q -> %d %v", name, got, ok)
		}
	}
}

// The server's certificate carries what Windows and macOS look for, and
// every name; a device's carries its username.
func TestTheCertificatesCarryWhatClientsCheck(t *testing.T) {
	s := tunnel(t)
	parse := func(p string) *x509.Certificate {
		b, _ := pem.Decode([]byte(p))
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	srv := parse(s.Interface.ServerCert)
	if srv.Subject.CommonName != "vpn.example.com" || len(srv.DNSNames) != 1 || len(srv.IPAddresses) != 1 ||
		srv.IPAddresses[0].String() != "203.0.113.10" {
		t.Fatalf("server names: %v %v %v", srv.Subject.CommonName, srv.DNSNames, srv.IPAddresses)
	}
	ike := false
	for _, u := range srv.UnknownExtKeyUsage {
		if u.Equal(oidIKEIntermediate) {
			ike = true
		}
	}
	if !ike || len(srv.ExtKeyUsage) != 1 || srv.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatal("the server's certificate lacks server authentication or the IKE intermediate use")
	}
	if srv.NotAfter.Sub(srv.NotBefore) > serverLifetime+2*time.Hour {
		t.Fatal("the server's certificate lasts longer than Apple accepts")
	}
	dev := parse(s.Interface.Accounts[0].Cert)
	if dev.Subject.CommonName != "s1-ali" || len(dev.DNSNames) != 2 || dev.DNSNames[0] != "s1-ali" || dev.DNSNames[1] != "tls-s1-ali" ||
		dev.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("device: %v %v %v", dev.Subject.CommonName, dev.DNSNames, dev.ExtKeyUsage)
	}
}

// What the customer installs opens with their password, and only with it.
func TestTheDeviceFileOpensWithItsPassword(t *testing.T) {
	ca, _ := NewAuthority("ike0")
	dev, _ := ca.IssueDevice("s1-ali")
	p12, err := PKCS12(dev.Cert, dev.Key, ca.Cert, "Secret11")
	if err != nil {
		t.Fatal(err)
	}
	_, cert, chain, err := pkcs12.DecodeChain(p12, "Secret11")
	if err != nil || cert.Subject.CommonName != "s1-ali" || len(chain) != 1 {
		t.Fatalf("decode: %v", err)
	}
	if _, _, _, err := pkcs12.DecodeChain(p12, "wrong"); err == nil {
		t.Fatal("opened with the wrong password")
	}
}

func TestTheServerCertificateIsRenewedWhenItMust(t *testing.T) {
	ca, _ := NewAuthority("ike0")
	srv, _ := ca.IssueServer([]string{"vpn.example.com", "203.0.113.10"})
	now := time.Now()
	if NeedsRenewal(srv.Cert, []string{"vpn.example.com", "203.0.113.10"}, now) {
		t.Fatal("a fresh certificate for these names wants renewing")
	}
	for name, names := range map[string][]string{
		"a name added":    {"vpn.example.com", "203.0.113.10", "b.example.com"},
		"a name gone":     {"vpn.example.com"},
		"another first":   {"203.0.113.10", "vpn.example.com"},
		"another address": {"vpn.example.com", "203.0.113.11"},
	} {
		if !NeedsRenewal(srv.Cert, names, now) {
			t.Errorf("%s: not renewed", name)
		}
	}
	if !NeedsRenewal(srv.Cert, []string{"vpn.example.com", "203.0.113.10"}, now.Add(serverLifetime-RenewBefore+time.Hour)) {
		t.Error("not renewed near its end")
	}
	if !NeedsRenewal("", []string{"x"}, now) {
		t.Error("no certificate is not renewed")
	}
}
