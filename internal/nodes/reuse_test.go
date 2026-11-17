package nodes

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/service"
)

// After an older backup is restored, the account numbers used since are
// handed out again. A node that was out of reach when the panel came back
// still has the clients made under those numbers -- another customer's. The
// device that now has the number must get none of it: not its credential,
// and not the traffic it moved.

func TestA3xuiClientLeftUnderAReusedNumberGivesTheNewDeviceNothing(t *testing.T) {
	f := newFakeXUI(t, "3.10.2")
	in := f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, true, false))
	r.round()

	c := r.customer("new", "vless-in")
	acc := r.account(c.ID, "vless-in")
	email := service.XUIEmail(r.tag(), &acc)
	// The customer who had the number before the restore, still there.
	f.addOwnClient(in.ID, map[string]any{"id": "0b1c2d3e-0000-4000-8000-0000000000aa", "email": email, "enable": true, "subId": email})
	f.count(in.ID, email, 3_000_000, 9_000_000)

	r.round()
	r.round()
	if got := f.client(in.ID, email); got == nil || got["id"] != acc.Secret {
		t.Fatalf("the client there is %v, want the new device's id %s", got, acc.Secret)
	}
	if got := r.chargedTo(c.ID); got != 0 {
		t.Fatalf("the new customer was charged %d bytes the old one moved", got)
	}
}

func TestAPasarguardUserLeftUnderAReusedNumberGivesTheNewDeviceNothing(t *testing.T) {
	f := newFakePG(t)
	f.addInbound(vlessInbound("VLESS TCP", 32001))
	r := newRig(t, pgNodeFor(f, true))
	r.round()

	c := r.customer("new", "VLESS TCP")
	acc := r.account(c.ID, "VLESS TCP")
	name := service.PGUsername(r.tag(), &acc)
	g := f.addGroup(pgGroupName("VLESS TCP"), "VLESS TCP")
	f.addUser(fakePGUser{Username: name, Status: "active", Groups: []int{g}, Password: "the-old-customers-credential", Used: 12_000_000})

	r.round()
	r.round()
	u := f.get(name)
	if u == nil {
		t.Fatal("no user there")
	}
	if u.Password == "the-old-customers-credential" {
		t.Fatal("the new device was given the old customer's credential: whoever still has the old links is let in as the new customer")
	}
	if got := r.chargedTo(c.ID); got != 0 {
		t.Fatalf("the new customer was charged %d bytes the old one moved", got)
	}
}

// 3x-ui 2 will not take the last client off an inbound: the one left under
// the number is made the new device's where it is -- its credential
// replaced -- and the traffic it had counted is not charged.
func TestA3xui2LastClientLeftUnderAReusedNumberIsMadeTheNewDevices(t *testing.T) {
	f := newFakeXUI(t, "2.9.4")
	in := f.addInbound("vless-in", "vless", 31001, map[string]any{"decryption": "none"}, nil)
	r := newRig(t, xuiNode(f, false, true))
	r.round()

	c := r.customer("new", "vless-in")
	acc := r.account(c.ID, "vless-in")
	email := service.XUIEmail(r.tag(), &acc)
	f.addOwnClient(in.ID, map[string]any{"id": "0b1c2d3e-0000-4000-8000-0000000000aa", "email": email, "enable": true, "subId": email})
	f.count(in.ID, email, 3_000_000, 9_000_000)

	r.round()
	r.round()
	if got := f.client(in.ID, email); got == nil || got["id"] != acc.Secret {
		t.Fatalf("the client there is %v, want the new device's id %s", got, acc.Secret)
	}
	if got := r.chargedTo(c.ID); got != 0 {
		t.Fatalf("the new customer was charged %d bytes the old one moved", got)
	}
	f.count(in.ID, email, 1000, 4000)
	r.round()
	r.round()
	if got := r.chargedTo(c.ID); got != 5000 {
		t.Fatalf("charged %d for what it moved itself, want 5000", got)
	}
}
