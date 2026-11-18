package nodes

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fleet"
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

// What a 3x-ui node says is online reaches the reconciler as that node's
// live sessions, by this panel's own file ids, oldest kept as oldest: the
// connections limit counts a customer's xray connection with their others.
func TestA3xuiNodesLiveFilesAreCounted(t *testing.T) {
	f := newFakeXUI(t, "3.10.2")
	f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, true, false))
	r.round()
	c := r.customer("alice", "vless-in")
	r.round()
	acc := r.account(c.ID, "vless-in")

	var got []fleet.NodeSession
	r.s.Sessions = func(_ uint, ss []fleet.NodeSession) { got = ss }
	f.mu.Lock()
	f.online = []string{service.XUIEmail(r.tag(), &acc), "owner-1"}
	f.mu.Unlock()
	r.s.sessionsRound(r.ctx)
	if len(got) != 1 || got[0].OriginID != acc.ID || got[0].Connections != 1 {
		t.Fatalf("reported %+v, want alice's file live, and nothing of the operator's own", got)
	}
	f.mu.Lock()
	f.online = nil
	f.mu.Unlock()
	r.s.sessionsRound(r.ctx)
	if len(got) != 0 {
		t.Fatalf("offline there, reported %+v", got)
	}
}

// One xray file used from two addresses at once is two connections, counted
// by the panel itself from what 3x-ui says of each address's last sighting
// -- not left to 3x-ui's own IP limit, which needs its fail2ban job. A 3x-ui
// that answers with addresses but no times, or does not say who is online,
// is said on the node, and nothing is assumed from it.
func TestA3xuiFileOnTwoAddressesIsTwoConnections(t *testing.T) {
	for _, old := range []bool{false, true} {
		f := newFakeXUI(t, "3.10.2")
		f.oldOnlines = old
		f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
		r := newRig(t, xuiNode(f, true, false))
		r.round()
		c := r.customer("alice", "vless-in")
		r.round()
		acc := r.account(c.ID, "vless-in")
		email := service.XUIEmail(r.tag(), &acc)
		var got []fleet.NodeSession
		r.s.Sessions = func(_ uint, ss []fleet.NodeSession) { got = ss }
		warning := func() string {
			var n model.Node
			r.db.First(&n, r.node.ID)
			return n.LimitWarning
		}
		ask := func() {
			r.s.mu.Lock()
			r.s.xuiAddrsAt = map[uint]time.Time{} // ask for addresses this time
			r.s.mu.Unlock()
			r.s.sessionsRound(r.ctx)
			r.s.addrsWG.Wait()
			r.s.sessionsRound(r.ctx) // and counted from the round after
		}
		now := time.Now().Unix()
		f.mu.Lock()
		f.online = []string{email}
		f.ips = map[string]any{email: []map[string]any{
			{"ip": "198.51.100.7", "lastSeen": now * 1000},
			{"ip": "203.0.113.9", "lastSeen": (now - 10) * 1000},
			{"ip": "192.0.2.1", "lastSeen": (now - 600) * 1000}, // long gone
		}}
		f.mu.Unlock()
		ask()
		if len(got) != 1 || got[0].Connections != 2 {
			t.Fatalf("old=%v: reported %+v, want alice's file on two addresses", old, got)
		}
		if w := warning(); w != "" {
			t.Errorf("old=%v: a warning while every connection is counted: %q", old, w)
		}
		// Addresses with no times: not trusted, one connection, and said.
		f.mu.Lock()
		f.ips = map[string]any{email: []string{"198.51.100.7 (2026-01-01)", "203.0.113.9 (2026-01-01)"}}
		f.mu.Unlock()
		ask()
		if len(got) != 1 || got[0].Connections != 1 || warning() != warnNoAddrs {
			t.Fatalf("old=%v: undated addresses gave %+v, warning %q", old, got, warning())
		}
		// Not saying who is online at all: said.
		f.mu.Lock()
		f.noOnlines = true
		f.mu.Unlock()
		ask()
		if warning() != warnNoOnlines {
			t.Fatalf("old=%v: no online list, warning %q", old, warning())
		}
	}
}

// A Pasarguard node says who is online and from where; both are counted
// with a customer's other connections, as on a 3x-ui.
func TestAPasarguardNodesLiveFilesAreCounted(t *testing.T) {
	f := newFakePG(t)
	f.addInbound(vlessInbound("VLESS TCP", 32001))
	r := newRig(t, pgNodeFor(f, true))
	r.round()
	tunnel := "VLESS TCP"
	c := r.customer("alice", tunnel)
	r.round()
	acc := r.account(c.ID, tunnel)
	name := service.PGUsername(r.tag(), &acc)
	var got []fleet.NodeSession
	r.s.Sessions = func(_ uint, ss []fleet.NodeSession) { got = ss }
	now := time.Now()
	f.mu.Lock()
	u := f.users[name]
	if u == nil {
		f.mu.Unlock()
		t.Fatalf("no user %s there", name)
	}
	u.OnlineAt, u.IPs = &now, []string{"198.51.100.7", "203.0.113.9"}
	f.mu.Unlock()
	r.s.sessionsRound(r.ctx)
	r.s.addrsWG.Wait()
	r.s.sessionsRound(r.ctx)
	if len(got) != 1 || got[0].OriginID != acc.ID || got[0].Connections != 2 {
		t.Fatalf("reported %+v, want alice's file on two addresses", got)
	}
	f.mu.Lock()
	u.OnlineAt = nil
	f.mu.Unlock()
	r.s.sessionsRound(r.ctx)
	if len(got) != 0 {
		t.Fatalf("offline there, reported %+v", got)
	}
}
