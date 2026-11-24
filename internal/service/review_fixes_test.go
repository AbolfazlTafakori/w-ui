package service

import (
	"context"
	"errors"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// The holes a review of the latest work found, each held shut.

// A file removed and added back hands that user's place to someone new, as
// a new link does, and is paid for on consumable users; a file for one
// more user than the plan holds is too. A server added gives nobody new
// credentials, and the owner's hand spends nothing.
func TestAFileRemovedAndAddedBackIsNewCredentials(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 20, nil)
	p.db.Create(&model.AdminInterface{AdminID: r.ID, InterfaceID: 2})
	ctx := asReseller(r, 1, 2)
	c := p.make(t, ctx, "c", 2) // 2
	full, _ := p.clients.Get(ctx, c.ID)
	var user2 uint
	for _, a := range full.Accounts {
		if a.DeviceName != full.Accounts[0].DeviceName {
			user2 = a.ID
		}
	}
	if user2 == 0 {
		t.Fatal("no second user's file")
	}
	if err := p.clients.RemoveDevice(ctx, user2); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 2 {
		t.Fatalf("a file removed spent %d", got-2)
	}
	if _, err := p.clients.AddDevice(ctx, c.ID, "user-2"); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 3 {
		t.Fatalf("the file added back spent %d, want 1", got-2)
	}
	if _, err := p.clients.AddDevice(ctx, c.ID, "extra"); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 4 {
		t.Fatalf("a file for a user the plan does not hold spent %d, want 1", got-3)
	}
	if _, err := p.clients.Update(ctx, c.ID, UpdateInput{InterfaceIDs: []uint{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 4 {
		t.Fatalf("a server added spent %d", got-4)
	}
	if _, err := p.clients.AddDevice(context.Background(), c.ID, "by-owner"); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 4 {
		t.Fatalf("the owner's file spent %d", got-4)
	}
	p.balanced(t)
}

// Growing a plan in the same change as a new link does not let the users
// it had go free: they pay for the link, the user added for the growth.
func TestAGrowAndANewLinkTogetherPayForBoth(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 40, nil)
	ctx := asReseller(r, 1)
	c := p.make(t, ctx, "c", 10) // 10
	subs := NewSubscriptions(p.db, nil, nil, quietLog())
	if _, err := subs.EnsureToken(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	eleven := 11
	if _, err := p.clients.Update(ctx, c.ID, UpdateInput{DeviceLimit: &eleven, SubID: "a-brand-new-link-id"}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 21 {
		t.Fatalf("grown by one with a new link: spent %d, want 11 (1 for the user added, 10 for the link)", got-10)
	}
	p.balanced(t)
}

// A 3x-ui or Pasarguard inbound is refused before anything is moved, the
// preview too: it is removed on that panel, or with its node.
func TestAnInboundOfAnotherPanelIsNotEmptied(t *testing.T) {
	p := newSeatPanel(t)
	in := model.Interface{Name: "xui-in", Protocol: model.ProtocolXray, ListenPort: 443, Subnet: "10.80.0.0/24", NodeID: 1}
	if err := p.db.Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	for _, plan := range []TunnelRemoval{{DryRun: true}, {Alone: "delete"}} {
		if _, err := RemoveTunnel(context.Background(), p.clients, p.ifaces(t), in.ID, plan); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", plan, err)
		}
	}
	var left int64
	p.db.Model(&model.Interface{}).Where("id = ?", in.ID).Count(&left)
	if left != 1 {
		t.Fatal("the inbound was removed")
	}
}

// A customer with a user on another panel's inbound still has every file
// this panel writes in one answer; that user is left out.
func TestProfilesInOneAnswerLeaveOutAnInboundsUser(t *testing.T) {
	p := newSeatPanel(t)
	c := p.make(t, context.Background(), "mixed", 1)
	in := model.Interface{Name: "xui-in", Protocol: model.ProtocolXray, ListenPort: 443, Subnet: "10.80.0.0/24", NodeID: 1}
	if err := p.db.Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.db.Create(&model.Account{ClientID: c.ID, InterfaceID: in.ID, DeviceName: "x", IP: "10.80.0.2"}).Error; err != nil {
		t.Fatal(err)
	}
	all, err := p.clients.ClientProfiles(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("one inbound user failed the whole answer: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("%d devices answered, want the one with a file", len(all))
	}
}

// A refusal of an admin's reach names the switch that was asked for.
func TestAReachRefusalNamesTheSwitch(t *testing.T) {
	p := newSeatPanel(t)
	on := true
	r := p.operator(t, "rsa", model.RoleReseller, "", 5, nil)
	_, err := p.admins.Update(context.Background(), r.ID, operators.AdminInput{CanViewAllInterfaces: &on})
	if got := FieldOf(err); got != "canViewAllInterfaces" {
		t.Fatalf("the refusal names %q (%v)", got, err)
	}
}

// Made and handed on in one change, the customer answered with is the
// receiver's, as they are after the move.
func TestAHandedOnCustomerIsAnsweredAsTheReceivers(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	c, err := p.clients.Create(context.Background(), CreateInput{Name: "made", InterfaceIDs: []uint{1}, DeviceLimit: 1,
		HandTo: &HandTo{To: r.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if c.OwnerID != r.ID {
		t.Fatalf("answered with owner %d, want the reseller %d", c.OwnerID, r.ID)
	}
	n := "renamed"
	u, err := p.clients.Update(context.Background(), c.ID, UpdateInput{Name: &n, HandTo: &HandTo{To: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if u.OwnerID != 0 || u.Name != "renamed" {
		t.Fatalf("answered with owner %d, name %q", u.OwnerID, u.Name)
	}
}

// A customer leaves the groups of the operator they came from even where
// the receiver has a group of the same name.
func TestAHandedOnCustomerLeavesTheGiversSameNamedGroup(t *testing.T) {
	p := newSeatPanel(t)
	a := p.operator(t, "ada", model.RoleAdmin, model.CapacityOpen, 10, nil)
	b := p.operator(t, "bea", model.RoleAdmin, model.CapacityOpen, 10, nil)
	for _, owner := range []uint{a.ID, b.ID} {
		if err := p.db.Create(&model.Group{OwnerID: owner, Name: "vip"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	c := p.make(t, asAdminOf(b), "theirs", 1)
	p.db.Create(&model.ClientGroup{ClientID: c.ID, Name: "vip"})
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{c.ID}, To: a.ID}); err != nil {
		t.Fatal(err)
	}
	var n int64
	p.db.Model(&model.ClientGroup{}).Where("client_id = ? AND name = ?", c.ID, "vip").Count(&n)
	if n != 0 {
		t.Fatal("the customer stayed in the giver's vip, now read as the receiver's")
	}
}

// Past traffic carried between resellers of two admins counts to the new
// reseller and their admin, and to nobody it already counted to.
func TestPastTrafficBetweenTwoAdminsTrees(t *testing.T) {
	p := newSeatPanel(t)
	a1 := p.operator(t, "ad1", model.RoleAdmin, model.CapacityOpen, 20, nil)
	a2 := p.operator(t, "ad2", model.RoleAdmin, model.CapacityOpen, 20, nil)
	r1 := p.operator(t, "res-one", model.RoleReseller, "", 5, a1)
	r2 := p.operator(t, "res-two", model.RoleReseller, "", 5, a2)
	p.db.Create(&model.AdminInterface{AdminID: r2.ID, InterfaceID: 1})
	c := p.make(t, asReseller(r1, 1), "c", 1)
	p.db.Model(&model.Client{}).Where("id = ?", c.ID).Update("used_bytes", 3*gib)
	before := map[string]uint64{"a1": p.usedBytes(t, a1), "r1": p.usedBytes(t, r1)}
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{c.ID}, To: r2.ID, CarryUsage: true}); err != nil {
		t.Fatal(err)
	}
	if p.usedBytes(t, r2) != 3*gib || p.usedBytes(t, a2) != 3*gib {
		t.Fatalf("receiver %d and their admin %d, want 3 GiB each", p.usedBytes(t, r2), p.usedBytes(t, a2))
	}
	if p.usedBytes(t, a1) != before["a1"] || p.usedBytes(t, r1) != before["r1"] {
		t.Fatal("the giving side's figures moved")
	}
}

// An admin let see every customer still moves only their own tree's, and
// to an operator that does not exist nothing moves.
func TestAnAdminSeeingEveryoneMovesOnlyTheirOwn(t *testing.T) {
	p := newSeatPanel(t)
	on := true
	ad, err := p.admins.Create(context.Background(), operators.AdminInput{Username: "wide", Password: "correct-horse",
		Role: model.RoleAdmin, Enabled: &on, CanViewAllClients: &on})
	if err != nil {
		t.Fatal(err)
	}
	ad, _ = p.admins.Get(context.Background(), ad.ID)
	owners := p.make(t, context.Background(), "owners", 1)
	ctx := operators.WithCaller(WithScope(context.Background(), ScopeFor(ad)), ad)
	if _, err := p.clients.Transfer(ctx, TransferInput{IDs: []uint{owners.ID}, To: ad.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an admin seeing everyone took the owner's customer: %v", err)
	}
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{owners.ID}, To: 999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("handed to nobody: %v", err)
	}
	if p.owned(t, owners.ID).OwnerID != 0 {
		t.Fatal("a refused move moved the customer")
	}
}
