package reconciler

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// chainTree is one admin over 20 resellers, each with 250 customers of one
// file: 5,000 customers in all, on one tunnel.
func chainTree(b *testing.B) (*gorm.DB, uint) {
	b.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(b.TempDir(), "chain.db")+"?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		b.Fatal(err)
	}
	must := func(err error) {
		if err != nil {
			b.Fatal(err)
		}
	}
	must(db.Create(&model.Node{Name: "local", Kind: model.KindLocal, Enabled: true}).Error)
	iface := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.64.0.0/14", EndpointHost: "x", Enabled: true, NodeID: 1}
	must(db.Create(&iface).Error)
	admin := model.Admin{Username: "ad", PasswordHash: "x", Role: model.RoleAdmin, Enabled: true}
	must(db.Create(&admin).Error)
	n := 0
	for r := 0; r < 20; r++ {
		res := model.Admin{Username: fmt.Sprintf("r%02d", r), PasswordHash: "x", Role: model.RoleReseller, Enabled: true, ParentAdminID: admin.ID}
		must(db.Create(&res).Error)
		clients := make([]model.Client, 250)
		for i := range clients {
			clients[i] = model.Client{Name: fmt.Sprintf("c%05d", n+i), Status: model.StatusActive, OwnerID: res.ID, AdminID: admin.ID, DeviceLimit: 1}
		}
		must(db.CreateInBatches(&clients, 250).Error)
		accs := make([]model.Account, 250)
		for i := range clients {
			n++
			accs[i] = model.Account{ClientID: clients[i].ID, InterfaceID: iface.ID, NodeID: 1, DeviceName: "d", Enabled: true,
				IP: fmt.Sprintf("10.%d.%d.%d", 64+n/65536, (n/256)%256, n%256+0), PublicKey: fmt.Sprintf("k%05d", n)}
		}
		must(db.CreateInBatches(&accs, 250).Error)
	}
	return db, admin.ID
}

// How long one pass takes to read what every tunnel should carry, with the
// tree serving and with its admin's term ended -- which takes all 5,000
// customers off without a row of theirs written.
func BenchmarkAPassOverATreeOf5000(b *testing.B) {
	for _, paused := range []bool{false, true} {
		b.Run(fmt.Sprintf("adminPaused=%v", paused), func(b *testing.B) {
			db, adminID := chainTree(b)
			if paused {
				past := time.Now().UTC().Add(-time.Hour)
				if err := db.Model(&model.Admin{}).Where("id = ?", adminID).Update("expires_at", past).Error; err != nil {
					b.Fatal(err)
				}
			}
			r := New(Options{DB: db, Enforcer: newFakeEnforcer(), Pool: backend.NewPool(quietLog()), Interval: time.Second, Log: quietLog()})
			ctx := context.Background()
			// One pass first, as the panel's first after start: it gives each
			// new device its transport secret, once. What is timed is every
			// pass after that.
			if _, err := r.readDesired(ctx); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d, err := r.readDesired(ctx)
				if err != nil {
					b.Fatal(err)
				}
				carried := 0
				for _, accs := range d.perIface {
					carried += len(accs)
				}
				if (paused && carried != 0) || (!paused && carried != 5000) {
					b.Fatalf("paused=%v carries %d", paused, carried)
				}
			}
		})
	}
}

// The first pass after 5,000 devices arrive without a transport secret --
// after an import, or the upgrade that brought secrets -- gives every one
// of them its secret, once.
func BenchmarkTheFirstPassOver5000NewDevices(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		db, _ := chainTree(b)
		r := New(Options{DB: db, Enforcer: newFakeEnforcer(), Pool: backend.NewPool(quietLog()), Interval: time.Second, Log: quietLog()})
		b.StartTimer()
		if _, err := r.readDesired(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
