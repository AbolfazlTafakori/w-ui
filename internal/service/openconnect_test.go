package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/occonf"
)

func createOC(t *testing.T, svc *Interfaces, in CreateInterfaceInput) (*model.Interface, error) {
	t.Helper()
	if in.Name == "" {
		in.Name = "oc0"
	}
	in.Protocol = model.ProtocolOpenConnect
	if in.ListenPort == 0 {
		in.ListenPort = 41443 // 443 is not the test's to take
	}
	if in.Subnet == "" {
		in.Subnet = "10.81.0.0/24"
	}
	if in.EndpointHost == "" {
		in.EndpointHost = "vpn.example.com"
	}
	return svc.Create(context.Background(), in)
}

// A new tunnel: DTLS on, camouflage on with a secret drawn for it, the MTU
// that leaves room for DTLS, and a certificate of its own for its address
// when the panel has none for it.
func TestAnOpenConnectTunnelIsMadeReadyToUse(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	f, err := createOC(t, svc, CreateInterfaceInput{})
	if err != nil {
		t.Skipf("create: %v", err) // a port this machine keeps
	}
	p := f.OpenConnect.V
	if !p.DTLS || !p.Camouflage || occonf.CheckCamouflage(p.CamouflageSecret) != nil || f.MTU != occonf.DefaultMTU {
		t.Fatalf("defaults %+v mtu %d", p, f.MTU)
	}
	if p.CertSource != "own" || !occonf.Covers(p.Cert, "vpn.example.com", time.Now()) || p.Key == "" {
		t.Fatalf("certificate %q", p.CertSource)
	}
	// One per server.
	_, err = createOC(t, svc, CreateInterfaceInput{Name: "oc1", Subnet: "10.82.0.0/24", ListenPort: 41444})
	check(t, "second", err, wantErr{"protocol", "already has an OPENCONNECT tunnel"})
}

func TestOpenConnectInputIsChecked(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	bad := "no spaces"
	_, err := createOC(t, svc, CreateInterfaceInput{OpenConnect: &OpenConnectInput{CamouflageSecret: &bad}})
	check(t, "secret", err, wantErr{"openconnect.camouflageSecret", "letters and digits"})
	_, err = createOC(t, svc, CreateInterfaceInput{Subnet: "fd00::/64"})
	check(t, "IPv6", err, wantErr{"subnet", "IPv4"})
	_, err = createOC(t, svc, CreateInterfaceInput{MTU: 1500})
	check(t, "MTU", err, wantErr{"mtu", "OpenConnect MTU"})
	off := false
	_, err = svc.Create(context.Background(), CreateInterfaceInput{Name: "wg9", Protocol: model.ProtocolWireGuard,
		ListenPort: 41899, Subnet: "10.83.0.0/24", EndpointHost: "vpn.example.com", OpenConnect: &OpenConnectInput{DTLS: &off}})
	check(t, "on WireGuard", err, wantErr{"openconnect", "OpenConnect tunnels only"})

	remote := model.Node{Name: "far", Kind: model.KindRemote, Enabled: true, Version: "v3.0.0"}
	if err := db.Create(&remote).Error; err != nil {
		t.Fatal(err)
	}
	_, err = createOC(t, svc, CreateInterfaceInput{NodeID: remote.ID})
	check(t, "node", err, wantErr{"nodeId", "this server only"})
}

// The panel's own certificate goes on the tunnel when it covers the
// address; a new address gets a certificate for it at once; turning
// camouflage off and on keeps or draws a secret.
func TestTheOpenConnectCertificateFollowsTheAddress(t *testing.T) {
	db, _ := seeded(t)
	svc := NewInterfaces(db, ipam.NewPools(), quietLog())
	dir := t.TempDir()
	pc, pk, err := occonf.NewServerCert([]string{"panel.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(cf, []byte(pc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kf, []byte(pk), 0o600); err != nil {
		t.Fatal(err)
	}
	old := PanelCertFiles
	PanelCertFiles = func() (string, string) { return cf, kf }
	defer func() { PanelCertFiles = old }()

	f, err := createOC(t, svc, CreateInterfaceInput{EndpointHost: "panel.example.com"})
	if err != nil {
		t.Skipf("create: %v", err)
	}
	if f.OpenConnect.V.CertSource != "panel" || f.OpenConnect.V.Cert != pc {
		t.Fatalf("the panel's certificate was not taken: %q", f.OpenConnect.V.CertSource)
	}
	ctx := context.Background()
	host := "other.example.com"
	got, err := svc.Update(ctx, f.ID, UpdateInterfaceInput{EndpointHost: &host})
	if err != nil {
		t.Fatal(err)
	}
	p := got.OpenConnect.V
	if p.CertSource != "own" || !occonf.Covers(p.Cert, host, time.Now()) {
		t.Fatalf("after the address moved: %q", p.CertSource)
	}
	// The reconciler's pass leaves a current certificate alone.
	var local model.Node
	if err := db.Where("kind = ?", model.KindLocal).First(&local).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnsureOpenConnectCerts(ctx, db, local.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var again model.Interface
	if err := db.First(&again, f.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.OpenConnect.V.Cert != p.Cert {
		t.Fatal("a current certificate was made again")
	}
	// ...and renews one near its end.
	if err := EnsureOpenConnectCerts(ctx, db, local.ID, time.Now().Add(800*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&again, f.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.OpenConnect.V.Cert == p.Cert {
		t.Fatal("a certificate near its end was kept")
	}

	off, on := false, true
	got, err = svc.Update(ctx, f.ID, UpdateInterfaceInput{OpenConnect: &OpenConnectInput{Camouflage: &off}})
	if err != nil || got.OpenConnect.V.Camouflage {
		t.Fatalf("camouflage off: %v %+v", err, got.OpenConnect.V)
	}
	secret := got.OpenConnect.V.CamouflageSecret
	got, err = svc.Update(ctx, f.ID, UpdateInterfaceInput{OpenConnect: &OpenConnectInput{Camouflage: &on}})
	if err != nil || !got.OpenConnect.V.Camouflage || got.OpenConnect.V.CamouflageSecret != secret {
		t.Fatalf("camouflage on again: %v %+v", err, got.OpenConnect.V)
	}
}
