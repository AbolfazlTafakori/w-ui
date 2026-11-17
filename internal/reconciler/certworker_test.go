package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ikev2conf"
)

// A pass of the reconciler makes no certificate. Hundreds of IKEv2 devices
// waiting for theirs -- a batch of customers just sold -- used to hold the
// pass while a key was made for each, a tenth of a second or more apiece:
// no peer added on any tunnel and no plan ended on the whole panel until the
// last was done. The pass now takes as long with them as without them, and
// the certificates are left to the worker beside it.
func TestAPassMakesNoCertificates(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	ca, err := ikev2conf.NewAuthority("ike0")
	if err != nil {
		t.Fatal(err)
	}
	ike := model.Interface{Name: "ike0", Protocol: model.ProtocolIKEv2, Subnet: "10.79.0.0/16", ListenPort: 500,
		EndpointHost: "vpn.example.com", Enabled: true, NodeID: 1,
		IKEv2: model.JSON(model.IKEv2Params{MSCHAPv2: true, RSA: true, CACert: ca.Cert, CAKey: ca.Key})}
	if err := db.Create(&ike).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		c := model.Client{Name: fmt.Sprintf("c%d", i), Status: model.StatusActive}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: ike.ID, DeviceName: "d", IP: fmt.Sprintf("10.79.%d.%d", i/250, i%250+2),
			Username: fmt.Sprintf("u%d", i), Secret: "s"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := newReconciler(t, db)
	start := time.Now()
	r.Tick(context.Background())
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a pass with 300 devices waiting for certificates took %s", took)
	}
	var issued int64
	db.Model(&model.Account{}).Where("cert_pem <> ''").Count(&issued)
	if issued != 0 {
		t.Fatalf("the pass made %d certificates itself", issued)
	}
	// But it sets the worker going: a device just sold waits for its key,
	// not for the worker's next tick.
	select {
	case <-r.certWake:
	default:
		t.Fatal("a pass that found devices waiting for certificates did not wake the worker")
	}
}

// A pass with every certificate made leaves the worker to its tick.
func TestAPassWithCertificatesWakesNothing(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	ca, err := ikev2conf.NewAuthority("ike0")
	if err != nil {
		t.Fatal(err)
	}
	ike := model.Interface{Name: "ike0", Protocol: model.ProtocolIKEv2, Subnet: "10.79.0.0/16", ListenPort: 500,
		EndpointHost: "vpn.example.com", Enabled: true, NodeID: 1,
		IKEv2: model.JSON(model.IKEv2Params{MSCHAPv2: true, RSA: true, CACert: ca.Cert, CAKey: ca.Key})}
	if err := db.Create(&ike).Error; err != nil {
		t.Fatal(err)
	}
	dev, err := ca.IssueDevice("u0")
	if err != nil {
		t.Fatal(err)
	}
	c := model.Client{Name: "c0", Status: model.StatusActive}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: ike.ID, DeviceName: "d", IP: "10.79.0.2",
		Username: "u0", Secret: "s", CertPEM: dev.Cert, CertKeyPEM: dev.Key}).Error; err != nil {
		t.Fatal(err)
	}
	r := newReconciler(t, db)
	r.Tick(context.Background())
	select {
	case <-r.certWake:
		t.Fatal("a pass with every certificate made woke the worker")
	default:
	}
}
