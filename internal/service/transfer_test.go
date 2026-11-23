package service

import (
	"context"
	"errors"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// Handing customers between operators, one rule a test, each ending with
// the ledger summing to every figure.

func (p seatPanel) owned(t *testing.T, id uint) model.Client {
	t.Helper()
	var c model.Client
	if err := p.db.First(&c, id).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func asAdminOf(ad *model.Admin, resellers ...*model.Admin) context.Context {
	sc := ScopeFor(ad)
	for _, r := range resellers {
		sc.Owners = append(sc.Owners, r.ID)
	}
	return operators.WithCaller(WithScope(context.Background(), sc), ad)
}

func (p seatPanel) usedBytes(t *testing.T, a *model.Admin) uint64 {
	t.Helper()
	var got model.Admin
	p.db.First(&got, a.ID)
	return got.UsedBytes
}

// Given by the owner, a customer takes the receiver's seats; past their
// ceiling it is refused and stays the owner's. Taken back, the seats are
// free again.
func TestATransferTakesTheReceiversSeats(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 3, nil)
	a := p.make(t, context.Background(), "a", 2)
	b := p.make(t, context.Background(), "b", 2)

	res, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 || res.Units != 2 || p.acc(t, r).Used != 2 {
		t.Fatalf("given one for 2 users: %+v, seats %d", res, p.acc(t, r).Used)
	}
	if c := p.owned(t, a.ID); c.OwnerID != r.ID || c.AdminID != 0 {
		t.Fatalf("handed over, the customer is owner %d admin %d", c.OwnerID, c.AdminID)
	}
	_, err = p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{b.ID}, To: r.ID})
	refusedForUsers(t, "a second for 2 users with 1 free", err)
	if c := p.owned(t, b.ID); c.OwnerID != 0 {
		t.Fatal("a refused transfer moved the customer")
	}
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: 0}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 0 {
		t.Fatalf("taken back, the reseller holds %d seats", got)
	}
	p.balanced(t)
}

// Consumable: receiving spends, giving up gives nothing back; the ledger
// says which was which.
func TestATransferOnConsumableSpendsAndNeverRefunds(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 10, nil)
	a := p.make(t, context.Background(), "a", 3)
	for _, to := range []uint{r.ID, 0} {
		if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: to}); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.acc(t, r).Used; got != 3 {
		t.Fatalf("given and taken back: spent %d, want 3", got)
	}
	var reasons []string
	p.db.Model(&model.CapacityEntry{}).Where("account_id = ? AND field = 'used'", p.acc(t, r).ID).Pluck("reason", &reasons)
	if len(reasons) != 1 || reasons[0] != model.CapacityTransferIn {
		t.Fatalf("the ledger says %v", reasons)
	}
	p.balanced(t)
}

// An admin moves customers between themselves and their own resellers, and
// nowhere else; a reseller moves nothing.
func TestWhoMayHandCustomersOn(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adm", model.RoleAdmin, model.CapacityOpen, 20, nil)
	r := p.operator(t, "rsa", model.RoleReseller, "", 5, ad)
	other := p.operator(t, "adm2", model.RoleAdmin, model.CapacityOpen, 20, nil)
	r2 := p.operator(t, "rsb", model.RoleReseller, "", 5, other)
	ctx := asAdminOf(ad, r)

	mine := p.make(t, ctx, "mine", 1)
	ownerss := p.make(t, context.Background(), "owners", 1)

	if _, err := p.clients.Transfer(ctx, TransferInput{IDs: []uint{mine.ID}, To: r.ID}); err != nil {
		t.Fatalf("an admin to their own reseller: %v", err)
	}
	if c := p.owned(t, mine.ID); c.OwnerID != r.ID || c.AdminID != ad.ID {
		t.Fatalf("under their reseller: owner %d admin %d", c.OwnerID, c.AdminID)
	}
	if _, err := p.clients.Transfer(ctx, TransferInput{IDs: []uint{mine.ID}, To: ad.ID}); err != nil {
		t.Fatalf("an admin taking a reseller's customer back: %v", err)
	}
	for what, in := range map[string]TransferInput{
		"to another admin's reseller": {IDs: []uint{mine.ID}, To: r2.ID},
		"to another admin":            {IDs: []uint{mine.ID}, To: other.ID},
		"to the owner":                {IDs: []uint{mine.ID}, To: 0},
		"the owner's customer":        {IDs: []uint{ownerss.ID}, To: r.ID},
	} {
		if _, err := p.clients.Transfer(ctx, in); err == nil {
			t.Fatalf("an admin moved %s", what)
		}
	}
	if _, err := p.clients.Transfer(asReseller(r, 1), TransferInput{IDs: []uint{mine.ID}, To: r.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a reseller moving a customer: %v", err)
	}
	p.balanced(t)
}

// A customer handed on ends up on exactly the receiver's servers: kept
// where they overlap, added where the receiver has one the customer had
// not, taken off the rest. Back with the owner, nothing moves; a receiver
// with no servers is handed nobody; a preview moves no server.
func TestATransferPutsTheCustomerOnTheReceiversServers(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 10, nil) // server 1
	servers := func(id uint) []uint {
		var got []uint
		p.db.Model(&model.Account{}).Where("client_id = ?", id).Distinct("interface_id").Order("interface_id").Pluck("interface_id", &got)
		return got
	}
	same := func(a, b []uint) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	both, err := p.clients.Create(context.Background(), CreateInput{Name: "both", InterfaceIDs: []uint{1, 2}, DeviceLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	only2, err := p.clients.Create(context.Background(), CreateInput{Name: "only2", InterfaceIDs: []uint{2}, DeviceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}

	res, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{both.ID, only2.ID}, To: r.ID, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ServersAdded != 1 || res.ServersRemoved != 2 || !same(servers(both.ID), []uint{1, 2}) || !same(servers(only2.ID), []uint{2}) {
		t.Fatalf("the preview: %+v, and it moved servers: %v %v", res, servers(both.ID), servers(only2.ID))
	}
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{both.ID, only2.ID}, To: r.ID}); err != nil {
		t.Fatal(err)
	}
	if !same(servers(both.ID), []uint{1}) || !same(servers(only2.ID), []uint{1}) {
		t.Fatalf("with the reseller: %v and %v, want [1] each", servers(both.ID), servers(only2.ID))
	}
	var files int64
	p.db.Model(&model.Account{}).Where("client_id = ?", both.ID).Count(&files)
	if files != 2 {
		t.Fatalf("a customer for 2 users holds %d files, want 2", files)
	}

	// The reseller given server 2 as well: a customer on 1 gets 2 added.
	p.db.Create(&model.AdminInterface{AdminID: r.ID, InterfaceID: 2})
	mine := p.make(t, context.Background(), "mine", 1) // server 1
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{mine.ID}, To: r.ID}); err != nil {
		t.Fatal(err)
	}
	if !same(servers(mine.ID), []uint{1, 2}) {
		t.Fatalf("handed to a reseller on 1 and 2: %v", servers(mine.ID))
	}
	// Back with the owner: kept as they are.
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{mine.ID}, To: 0}); err != nil {
		t.Fatal(err)
	}
	if !same(servers(mine.ID), []uint{1, 2}) {
		t.Fatalf("back with the owner: %v", servers(mine.ID))
	}

	empty := p.operator(t, "nobody", model.RoleReseller, model.CapacityOpen, 10, nil)
	p.db.Where("admin_id = ?", empty.ID).Delete(&model.AdminInterface{})
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{mine.ID}, To: empty.ID}); err == nil {
		t.Fatal("handed a customer to a reseller with no servers")
	}
	p.balanced(t)
}

// The receiver's rules of sale hold: a plan they refuse stays where it was.
func TestATransferIsHeldToTheReceiversRules(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 10, nil)
	p.rules(t, r, 30, 0, false)
	unlimited, err := p.clients.Create(context.Background(), CreateInput{Name: "forever", InterfaceIDs: []uint{1}, DeviceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{unlimited.ID}, To: r.ID})
	refusedBySale(t, "a plan with no end to a reseller held to 30 days", err)
	if p.owned(t, unlimited.ID).OwnerID != 0 {
		t.Fatal("a refused transfer moved the customer")
	}
	p.balanced(t)
}

// Past traffic is the receiver's only when asked, and an admin already
// counting a customer is not charged for it twice.
func TestATransferCarriesPastTrafficWhenAsked(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adm", model.RoleAdmin, model.CapacityOpen, 20, nil)
	r := p.operator(t, "rsa", model.RoleReseller, "", 5, ad)
	a := p.make(t, context.Background(), "a", 1)
	b := p.make(t, context.Background(), "b", 1)
	p.db.Model(&model.Client{}).Where("id IN ?", []uint{a.ID, b.ID}).Update("used_bytes", 5*gib)

	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: r.ID}); err != nil {
		t.Fatal(err)
	}
	if p.usedBytes(t, r) != 0 || p.usedBytes(t, ad) != 0 {
		t.Fatal("past traffic was counted without being asked")
	}
	res, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{b.ID}, To: r.ID, CarryUsage: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.CarriedBytes != 5*gib || p.usedBytes(t, r) != 5*gib || p.usedBytes(t, ad) != 5*gib {
		t.Fatalf("carried %d: reseller %d, admin %d", res.CarriedBytes, p.usedBytes(t, r), p.usedBytes(t, ad))
	}
	// Back to the admin, carrying: the admin counted it all along.
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{b.ID}, To: ad.ID, CarryUsage: true}); err != nil {
		t.Fatal(err)
	}
	if got := p.usedBytes(t, ad); got != 5*gib {
		t.Fatalf("the admin was charged twice: %d", got)
	}
	p.balanced(t)
}

// Handed on, a customer leaves the groups of the operator they came from
// and is filed in the receiver's hidden one; back with the owner, the
// hidden one goes and the owner's own groups the customer kept remain.
func TestATransferRefilesTheCustomer(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 10, nil)
	a := p.make(t, context.Background(), "a", 1)
	p.db.Create(&model.Group{OwnerID: 0, Name: "vip"})
	p.db.Create(&model.ClientGroup{ClientID: a.ID, Name: "vip"})
	groups := func() []string {
		var g []string
		p.db.Model(&model.ClientGroup{}).Where("client_id = ?", a.ID).Order("name").Pluck("name", &g)
		return g
	}
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: r.ID}); err != nil {
		t.Fatal(err)
	}
	if g := groups(); len(g) != 1 || g[0] != r.GroupName {
		t.Fatalf("with the reseller, in %v, want only %q", g, r.GroupName)
	}
	p.db.Create(&model.ClientGroup{ClientID: a.ID, Name: "vip"})
	if _, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: 0}); err != nil {
		t.Fatal(err)
	}
	if g := groups(); len(g) != 1 || g[0] != "vip" {
		t.Fatalf("back with the owner, in %v, want [vip]", g)
	}
}

// A preview says what a move would take, and moves nothing.
func TestATransferPreviewMovesNothing(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 10, nil)
	a := p.make(t, context.Background(), "a", 2)
	res, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: r.ID, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 || res.Units != 2 {
		t.Fatalf("the preview says %+v", res)
	}
	if p.owned(t, a.ID).OwnerID != 0 || p.acc(t, r).Used != 0 {
		t.Fatal("a preview moved something")
	}
	p.balanced(t)
}

// Made or changed and handed on in one change: refused, neither happens.
func TestASaveThatHandsOnIsAllOrNothing(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 1, nil)
	c, err := p.clients.Create(context.Background(), CreateInput{Name: "made", InterfaceIDs: []uint{1}, DeviceLimit: 1,
		HandTo: &HandTo{To: r.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if p.owned(t, c.ID).OwnerID != r.ID || p.acc(t, r).Used != 1 {
		t.Fatal("made and handed on, the customer is not the reseller's")
	}
	var before int64
	p.db.Model(&model.Client{}).Count(&before)
	_, err = p.clients.Create(context.Background(), CreateInput{Name: "too-many", InterfaceIDs: []uint{1}, DeviceLimit: 1,
		HandTo: &HandTo{To: r.ID}})
	refusedForUsers(t, "made for a reseller with no seat free", err)
	var after int64
	p.db.Model(&model.Client{}).Count(&after)
	if after != before {
		t.Fatal("refused to be handed on, the customer was made anyway")
	}
	mine := p.make(t, context.Background(), "mine", 1)
	n := "renamed"
	_, err = p.clients.Update(context.Background(), mine.ID, UpdateInput{Name: &n, HandTo: &HandTo{To: r.ID}})
	refusedForUsers(t, "changed and handed to a full reseller", err)
	if got := p.owned(t, mine.ID); got.Name != "mine" || got.OwnerID != 0 {
		t.Fatalf("a refused change was kept: %q owner %d", got.Name, got.OwnerID)
	}
	p.balanced(t)
}

// A move refused after the customer's servers were matched, or only
// previewed, leaves every address pool as it was.
func TestARefusedTransferGivesItsAddressesBack(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 1, nil)
	p.db.Create(&model.AdminInterface{AdminID: r.ID, InterfaceID: 2})
	a := p.make(t, context.Background(), "a", 2) // server 1, 2 users
	pool1, _ := p.clients.pools.Get(1)
	pool2, _ := p.clients.pools.Get(2)
	in1, in2 := pool1.InUse(), pool2.InUse()

	_, err := p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: r.ID, DryRun: true})
	refusedForUsers(t, "previewing 2 users to a reseller with 1 seat", err)
	_, err = p.clients.Transfer(context.Background(), TransferInput{IDs: []uint{a.ID}, To: r.ID})
	refusedForUsers(t, "2 users to a reseller with 1 seat", err)
	if pool1.InUse() != in1 || pool2.InUse() != in2 {
		t.Fatalf("pools moved from %d/%d to %d/%d", in1, in2, pool1.InUse(), pool2.InUse())
	}
	p.balanced(t)
}
