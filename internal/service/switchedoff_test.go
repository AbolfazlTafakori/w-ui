package service

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// Created switched off stays switched off.
//
// The schema defaults every "enabled" column to true, and an insert treats a
// false as nothing given and takes the default: a reseller, a server or a
// node created switched off came up switched on -- a reseller the owner meant
// to prepare before selling could sell at once, and a server meant to be
// configured first came up carrying traffic.
func TestWhatIsCreatedSwitchedOffStaysOff(t *testing.T) {
	ctx := t.Context()
	off, on := false, true

	t.Run("a reseller", func(t *testing.T) {
		db, _, admins := resellerDB(t)
		for _, want := range []bool{false, true} {
			sw, limit := want, 0
			name := map[bool]string{false: "created-off", true: "created-on"}[want]
			a, err := admins.Create(ctx, AdminInput{Username: name, Password: "correct-horse",
				Role: model.RoleReseller, Enabled: &sw, ClientLimit: &limit, InterfaceIDs: []uint{1}})
			if err != nil {
				t.Fatal(err)
			}
			var stored model.Admin
			db.First(&stored, a.ID)
			if stored.Enabled != want || a.Enabled != want {
				t.Errorf("created enabled=%v: stored %v, answered %v", want, stored.Enabled, a.Enabled)
			}
		}
	})

	t.Run("a server", func(t *testing.T) {
		db, _ := seeded(t)
		ifaces := NewInterfaces(db, ipam.NewPools(), quietLog())
		for i, sw := range []*bool{&off, &on, nil} {
			want := sw == nil || *sw
			in := CreateInterfaceInput{Name: []string{"wg-off", "wg-on", "wg-unsaid"}[i], Protocol: "wireguard",
				ListenPort: freeUDPPort(t), Subnet: []string{"10.31.0.0/24", "10.32.0.0/24", "10.33.0.0/24"}[i],
				EndpointHost: "vpn.example.com", Enabled: sw}
			got, err := ifaces.Create(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			var stored model.Interface
			db.First(&stored, got.ID)
			if stored.Enabled != want || got.Enabled != want {
				t.Errorf("%s: stored %v, answered %v, want %v", in.Name, stored.Enabled, got.Enabled, want)
			}
		}
	})

	t.Run("a node", func(t *testing.T) {
		db := testDB(t)
		nodes := NewNodes(db, quietLog())
		for i, sw := range []*bool{&off, &on} {
			want := *sw
			got, err := nodes.Create(ctx, NodeInput{Name: []string{"node-off", "node-on"}[i],
				Address: []string{"https://n1.example.com:2096", "https://n2.example.com:2096"}[i],
				Token:   "wui_x", Enabled: sw})
			if err != nil {
				t.Fatal(err)
			}
			var stored model.Node
			db.First(&stored, got.ID)
			if stored.Enabled != want || got.Enabled != want {
				t.Errorf("%s: stored %v, answered %v, want %v", got.Name, stored.Enabled, got.Enabled, want)
			}
		}
	})
}
