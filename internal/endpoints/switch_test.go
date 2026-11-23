package endpoints

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// A host made switched off -- alone, or as a group of addresses -- stays
// off: the schema defaults enabled to true and a create left a false out.
func TestAHostMadeSwitchedOffStaysOff(t *testing.T) {
	db := testDB(t)
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.Create(&model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820,
		Subnet: "10.66.0.0/24", EndpointHost: "x", NodeID: 1, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	h := NewHosts(db, quietLog())
	ctx := context.Background()
	off := false
	one, err := h.Create(ctx, HostInput{InterfaceID: 1, Name: "single", Address: "203.0.113.5", Port: 443, Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	var got model.Host
	db.First(&got, one.ID)
	if got.Enabled {
		t.Fatal("a host made switched off is stored on")
	}
	g, err := h.CreateGroup(ctx, HostGroup{Remark: "group", Hosts: []string{"203.0.113.6", "203.0.113.7"}, InterfaceIDs: []uint{1}, Port: 443, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	var on int64
	db.Model(&model.Host{}).Where("group_id = ? AND enabled = ?", g.GroupID, true).Count(&on)
	if on != 0 {
		t.Fatalf("%d of a group's addresses made switched off are stored on", on)
	}
}
