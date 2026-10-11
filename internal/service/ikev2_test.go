package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ikev2conf"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

func createIKEv2(t *testing.T, svc *Interfaces, in CreateInterfaceInput) (*model.Interface, error) {
	t.Helper()
	if in.Name == "" {
		in.Name = "ike0"
	}
	in.Protocol = model.ProtocolIKEv2
	if in.Subnet == "" {
		in.Subnet = "10.79.0.0/24"
	}
	if in.EndpointHost == "" {
		in.EndpointHost = "vpn.example.com"
	}
	return svc.Create(context.Background(), in)
}

func parseCert(t *testing.T, p string) *x509.Certificate {
	t.Helper()
	b, _ := pem.Decode([]byte(p))
	if b == nil {
		t.Fatal("no certificate")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A new tunnel is on IKE's fixed ports, lets devices in all four ways when
// none is chosen, and has its own authority and a server certificate for
// what devices dial.
func TestAnIKEv2TunnelIsMadeWithItsCertificates(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	got, err := createIKEv2(t, svc, CreateInterfaceInput{ListenPort: 4242})
	if err != nil {
		t.Fatal(err)
	}
	p := got.IKEv2.V
	if got.ListenPort != 500 || !p.MSCHAPv2 || !p.PSK || !p.RSA || !p.EAPTLS {
		t.Fatalf("port %d, modes %+v", got.ListenPort, p)
	}
	ca, srv := parseCert(t, p.CACert), parseCert(t, p.ServerCert)
	if err := srv.CheckSignatureFrom(ca); err != nil || srv.Subject.CommonName != "vpn.example.com" ||
		len(p.ServerNames) != 1 || p.ServerNames[0] != "vpn.example.com" || p.CAKey == "" || p.ServerKey == "" {
		t.Fatalf("server certificate %v %+v", err, srv.Subject)
	}

	// One per server, as L2TP.
	_, err = createIKEv2(t, svc, CreateInterfaceInput{Name: "ike1", Subnet: "10.80.0.0/24"})
	check(t, "second", err, wantErr{"protocol", "already has an IKEV2 tunnel"})
}

func TestIKEv2InputIsChecked(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	_, err := createIKEv2(t, svc, CreateInterfaceInput{IKEv2Modes: []string{"psk", "magic"}})
	check(t, "unknown way", err, wantErr{"ikev2Modes", "magic"})
	_, err = createIKEv2(t, svc, CreateInterfaceInput{Subnet: "fd00::/64"})
	check(t, "IPv6", err, wantErr{"subnet", "IPv4"})

	remote := model.Node{Name: "far", Kind: model.KindRemote, Enabled: true, Version: "v3.0.0"}
	if err := db.Create(&remote).Error; err != nil {
		t.Fatal(err)
	}
	_, err = createIKEv2(t, svc, CreateInterfaceInput{NodeID: remote.ID})
	check(t, "node", err, wantErr{"nodeId", "this server only"})

	got, err := createIKEv2(t, svc, CreateInterfaceInput{IKEv2Modes: []string{"psk"}})
	if err != nil {
		t.Fatal(err)
	}
	if p := got.IKEv2.V; !p.PSK || p.MSCHAPv2 || p.RSA || p.EAPTLS {
		t.Fatalf("modes %+v", p)
	}
	ctx := context.Background()
	port := 4500
	_, err = svc.Update(ctx, got.ID, UpdateInterfaceInput{ListenPort: &port})
	check(t, "port", err, wantErr{"listenPort", "500 and 4500"})
	modes := []string{"rsa", "eap-tls"}
	up, err := svc.Update(ctx, got.ID, UpdateInterfaceInput{IKEv2Modes: &modes})
	if err != nil {
		t.Fatal(err)
	}
	if p := up.IKEv2.V; p.PSK || !p.RSA || !p.EAPTLS || p.CAKey == "" {
		t.Fatalf("after update %+v", p)
	}
}

// Every device on a tunnel that signs in by certificate gets one, for its
// username, from its tunnel's authority; renamed, it gets a new one; on a
// tunnel without certificates nothing is made. A new endpoint gives the
// server a new certificate.
func TestIKEv2CertificatesFollowTheDevices(t *testing.T) {
	db, _ := seeded(t)
	pools := ipam.NewPools()
	ifs := NewInterfaces(db, pools, quietLog())
	ike, err := createIKEv2(t, ifs, CreateInterfaceInput{IKEv2Modes: []string{"mschapv2", "rsa"}})
	if err != nil {
		t.Fatal(err)
	}
	clients := NewClients(db, pools, quietLog())
	expires := time.Now().Add(24 * time.Hour)
	ctx := context.Background()
	c, err := clients.Create(ctx, CreateInput{Name: "Roya", InterfaceIDs: []uint{ike.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "roya", OpenVPNPassword: "s3cret-pass"})
	if err != nil {
		t.Fatal(err)
	}
	var local model.Node
	if err := db.Where("kind = ?", model.KindLocal).First(&local).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureIKEv2Certs(ctx, db, local.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	acc := accountsOn(t, db, c.ID, ike.ID)[0]
	ca := parseCert(t, ike.IKEv2.V.CACert)
	cert := parseCert(t, acc.CertPEM)
	if cert.Subject.CommonName != "roya" || cert.CheckSignatureFrom(ca) != nil || acc.CertKeyPEM == "" {
		t.Fatalf("device certificate %+v", cert.Subject)
	}
	// A second pass leaves a current certificate alone.
	if err := EnsureIKEv2Certs(ctx, db, local.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if again := accountsOn(t, db, c.ID, ike.ID)[0]; again.CertPEM != acc.CertPEM {
		t.Fatal("a current certificate was issued again")
	}
	// Renamed: a new one, for the new name.
	if err := db.Model(&model.Account{}).Where("id = ?", acc.ID).Update("username", "roya2").Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureIKEv2Certs(ctx, db, local.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if cn := parseCert(t, accountsOn(t, db, c.ID, ike.ID)[0].CertPEM).Subject.CommonName; cn != "roya2" {
		t.Fatalf("after a rename the certificate is for %q", cn)
	}

	// The endpoint moves: the server's certificate follows it.
	host := "new.example.com"
	if _, err := ifs.Update(ctx, ike.ID, UpdateInterfaceInput{EndpointHost: &host}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureIKEv2Certs(ctx, db, local.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var moved model.Interface
	if err := db.First(&moved, ike.ID).Error; err != nil {
		t.Fatal(err)
	}
	if cn := parseCert(t, moved.IKEv2.V.ServerCert).Subject.CommonName; cn != host || moved.IKEv2.V.CACert != ike.IKEv2.V.CACert {
		t.Fatalf("server certificate for %q after the move", cn)
	}
}

func TestIKEv2PasswordsFollowTheL2TPRules(t *testing.T) {
	db, _ := seeded(t)
	pools := ipam.NewPools()
	ike, err := createIKEv2(t, NewInterfaces(db, pools, quietLog()), CreateInterfaceInput{})
	if err != nil {
		t.Fatal(err)
	}
	clients := NewClients(db, pools, quietLog())
	expires := time.Now().Add(24 * time.Hour)
	ctx := context.Background()
	_, err = clients.Create(ctx, CreateInput{Name: "Mina", InterfaceIDs: []uint{ike.ID}, ExpiresAt: &expires, DeviceLimit: 1,
		OpenVPNUsername: "mina", OpenVPNPassword: `pass"word1`})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a quote in an IKEv2 password: %v", err)
	}
	c, err := clients.Create(ctx, CreateInput{Name: "Sina", InterfaceIDs: []uint{ike.ID}, ExpiresAt: &expires, DeviceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	gen := accountsOn(t, db, c.ID, ike.ID)
	if len(gen) != 1 || l2tpconf.CheckUsername(gen[0].Username) != nil || l2tpconf.CheckSecret(gen[0].Secret) != nil {
		t.Fatalf("generated credentials: %+v", gen)
	}
}

func TestModesNoneIsAll(t *testing.T) {
	p, err := ikev2Modes(nil)
	if err != nil || !p.MSCHAPv2 || !p.PSK || !p.RSA || !p.EAPTLS {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ikev2Modes([]string{ikev2conf.ModeEAPTLS, " PSK "}); err != nil {
		t.Fatal(err)
	}
	if _, err := ikev2Modes([]string{"eap"}); err == nil || !strings.Contains(err.Error(), "eap") {
		t.Fatal(err)
	}
}
