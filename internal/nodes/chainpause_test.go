package nodes

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// A customer whose reseller is paused is pushed to a W-UI node as off, and
// back on when the reseller is: the node is told what this server's own
// tunnels already did. Before, the push read the customer's own status
// alone, and a paused tree went on being served on every node.
func TestThePushStopsAPausedTree(t *testing.T) {
	db := testDB(t)
	seedNode(t, db, model.StatusActive)
	res := model.Admin{Username: "reza", PasswordHash: "x", Role: model.RoleReseller, Enabled: true}
	if err := db.Create(&res).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Client{}).Where("1 = 1").Update("owner_id", res.ID).Error; err != nil {
		t.Fatal(err)
	}
	s := newSyncer(db)
	enabled := func() bool {
		t.Helper()
		states, err := s.desired(context.Background(), 7, false)
		if err != nil {
			t.Fatal(err)
		}
		return states[0].Clients[0].Enabled
	}
	if !enabled() {
		t.Fatal("served before the pause: off")
	}
	db.Model(&res).Update("enabled", false)
	if enabled() {
		t.Error("the reseller is paused and the node is told to serve their customer")
	}
	db.Model(&res).Update("enabled", true)
	if !enabled() {
		t.Error("the reseller is back and the node is told to keep their customer off")
	}
}

// On a 3x-ui node a paused tree's file is switched off there, and a file
// the connections limit holds off is too; each comes back after.
func TestA3xuiFileIsOffWhilePausedOrHeld(t *testing.T) {
	f := newFakeXUI(t, "3.10.2")
	in := f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	c := r.customer("alice", "vless-in")
	r.round()
	acc := r.account(c.ID, "vless-in")
	email := service.XUIEmail(r.tag(), &acc)
	on := func() bool { return f.client(in.ID, email)["enable"] == true }
	if !on() {
		t.Fatal("not on to begin with")
	}

	res := model.Admin{Username: "reza", PasswordHash: "x", Role: model.RoleReseller, Enabled: true}
	if err := r.db.Create(&res).Error; err != nil {
		t.Fatal(err)
	}
	r.db.Model(&model.Client{}).Where("id = ?", c.ID).Update("owner_id", res.ID)
	r.db.Model(&res).Update("enabled", false)
	r.round()
	if on() {
		t.Error("the reseller is paused and the file is on there")
	}
	r.db.Model(&res).Update("enabled", true)
	r.round()
	if !on() {
		t.Error("the reseller is back and the file is off there")
	}

	until := time.Now().Add(time.Minute)
	r.s.Holds = func() map[uint]time.Time { return map[uint]time.Time{acc.ID: until} }
	r.round()
	if on() {
		t.Error("the connections limit holds the file off and it is on there")
	}
	r.s.Holds = func() map[uint]time.Time { return nil }
	r.round()
	if !on() {
		t.Error("the hold is over and the file is still off there")
	}
}
