package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/scope"
)

// What making one customer costs a reseller with a user ceiling, from the
// scope to the stored files. Run before and after the capacity ledger, so
// the ledger is seen to cost nothing a seller would notice:
//
//	go test ./internal/service -run X -bench ResellerCreates -benchtime 400x
func BenchmarkResellerCreatesACustomer(b *testing.B) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(b.TempDir(), "bench.db")+"?_pragma=journal_mode(WAL)"), &gorm.Config{Logger: gormlogger.Discard})
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
	if err := scope.Register(db); err != nil {
		b.Fatal(err)
	}
	iface := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.64.0.0/14", EndpointHost: "vpn.example.com", Enabled: true}
	if err := db.Create(&iface).Error; err != nil {
		b.Fatal(err)
	}
	reseller := model.Admin{Username: "bench", PasswordHash: "x", Role: model.RoleReseller, Enabled: true, ClientLimit: 1_000_000}
	if err := db.Create(&reseller).Error; err != nil {
		b.Fatal(err)
	}
	pools := ipam.NewPools()
	if _, err := pools.Add(iface.ID, iface.Subnet); err != nil {
		b.Fatal(err)
	}
	svc := NewClients(db, pools, quietLog())
	ctx := WithScope(context.Background(), Scope{AdminID: reseller.ID, Owners: []uint{reseller.ID}, Restricted: true,
		Interfaces: map[uint]bool{iface.ID: true}, ClientLimit: reseller.ClientLimit})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Create(ctx, CreateInput{Name: fmt.Sprintf("c%06d", i), InterfaceIDs: []uint{iface.ID}, DeviceLimit: 2}); err != nil {
			b.Fatal(err)
		}
	}
}
