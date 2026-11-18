package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/operators"
	"github.com/abolfazl/w-ui/internal/scope"
)

// Every rule of how customers spend an operator's users, one test each, and
// after each the ledger summing to every figure.

type seatPanel struct {
	db      *gorm.DB
	clients *Clients
	admins  *operators.Admins
}

func newSeatPanel(t *testing.T) seatPanel {
	t.Helper()
	db, clients, admins := resellerDB(t)
	return seatPanel{db, clients, admins}
}

// operator makes an admin or a reseller with users counted mode (the owner
// making them) or, given parent, a reseller under that admin.
func (p seatPanel) operator(t *testing.T, name string, role model.AdminRole, mode string, users int, parent *model.Admin) *model.Admin {
	t.Helper()
	on := true
	ctx := context.Background()
	if parent != nil {
		ctx = operators.WithCaller(WithScope(ctx, ScopeFor(parent)), parent)
	}
	a, err := p.admins.Create(ctx, operators.AdminInput{Username: name, Password: "correct-horse", Role: role,
		Enabled: &on, ClientLimit: &users, UserMode: mode, InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return a
}

func (p seatPanel) acc(t *testing.T, a *model.Admin) *model.CapacityAccount {
	t.Helper()
	acc, err := capacity.Account(p.db, a.ID, model.CapacityUsers)
	if err != nil || acc == nil {
		t.Fatalf("%s's account: %v", a.Username, err)
	}
	return acc
}

func (p seatPanel) balanced(t *testing.T) {
	t.Helper()
	bad, err := capacity.Balanced(p.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("accounts whose figures are not their ledger: %v", bad)
	}
}

func (p seatPanel) make(t *testing.T, ctx context.Context, name string, users int) *model.Client {
	t.Helper()
	c, err := p.clients.Create(ctx, CreateInput{Name: name, InterfaceIDs: []uint{1}, DeviceLimit: users,
		ExpiresAt: ptrTime(time.Now().UTC().Add(48 * time.Hour)), QuotaBytes: 1 << 30})
	if err != nil {
		t.Fatalf("make %s: %v", name, err)
	}
	return c
}

func ptrTime(t time.Time) *time.Time { return &t }

func refusedForUsers(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrNoUsers) {
		t.Fatalf("%s: %v, want a refusal for want of users", what, err)
	}
}

func later(p seatPanel, t *testing.T, ctx context.Context, c *model.Client) error {
	t.Helper()
	at := time.Now().UTC().Add(240 * time.Hour)
	_, err := p.clients.Update(ctx, c.ID, UpdateInput{ExpiresAt: OptionalTime{Set: true, Value: &at}})
	return err
}

// endBy is the panel noticing a customer's time is up, as its sweep does.
func endBy(p seatPanel, t *testing.T, c *model.Client) {
	t.Helper()
	err := SettleChange(context.Background(), p.db, []uint{c.ID}, ByPanel, func(tx *gorm.DB) ([]uint, error) {
		return nil, tx.Model(&model.Client{}).Where("id = ?", c.ID).Update("status", model.StatusExpired).Error
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ── Open: seats held at once ──

// A customer holds as many seats as its users; the ceiling is the number of
// seats; a customer for two users is two.
func TestOpenACustomerHoldsItsUsers(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := asReseller(r, 1)
	p.make(t, ctx, "a", 2)
	p.make(t, ctx, "b", 3)
	_, err := p.clients.Create(ctx, CreateInput{Name: "c", InterfaceIDs: []uint{1}, DeviceLimit: 1})
	refusedForUsers(t, "a sixth seat of five", err)
	if a := p.acc(t, r); a.Used != 5 {
		t.Fatalf("used %d", a.Used)
	}
	p.balanced(t)
}

// Extending a customer being served costs nothing; extending one that ended
// takes its seats again, and is refused if there are none.
func TestOpenExtendingCostsNothingUnlessItRevives(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 3, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 2)
	if err := later(p, t, ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 2 {
		t.Fatalf("extending a served customer moved seats: %d", got)
	}
	endBy(p, t, a)
	if got := p.acc(t, r).Used; got != 0 {
		t.Fatalf("an ended customer still holds %d", got)
	}
	p.make(t, ctx, "b", 2)
	refusedForUsers(t, "reviving with one seat free", later(p, t, ctx, a))
	var still model.Client
	p.db.First(&still, a.ID)
	if still.Status != model.StatusExpired {
		t.Fatalf("a refused revival changed the customer: %s", still.Status)
	}
	p.balanced(t)
}

// Growing takes the difference, shrinking gives it back, deleting gives all
// back -- one by one and in bulk.
func TestOpenGrowShrinkAndDelete(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 10, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 2)
	b := p.make(t, ctx, "b", 2)
	four, one := 4, 1
	if _, err := p.clients.Update(ctx, a.ID, UpdateInput{DeviceLimit: &four}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 6 {
		t.Fatalf("after growing: %d", got)
	}
	if _, err := p.clients.Update(ctx, a.ID, UpdateInput{DeviceLimit: &one}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 3 {
		t.Fatalf("after shrinking: %d", got)
	}
	if err := p.clients.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.clients.Bulk(ctx, BulkDelete, []uint{b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 0 {
		t.Fatalf("after deleting: %d", got)
	}
	p.balanced(t)
}

// Switching an ended customer back on in bulk is a revival like any other:
// it cannot be had for nothing when there is no seat.
func TestOpenBulkEnableOfAnEndedCustomerIsHeldToo(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 2, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 2)
	endBy(p, t, a)
	p.make(t, ctx, "b", 2)
	_, err := p.clients.Bulk(ctx, BulkEnable, []uint{a.ID})
	refusedForUsers(t, "enabling an ended customer with no seat", err)
	p.balanced(t)
}

// ── Consumable: units spent ──

// Making a customer spends its users; extending it -- a later end, more
// traffic, a reset, days added in bulk -- spends them again; growing spends
// the difference.
func TestConsumableEverySaleAndExtensionSpends(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 100, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 2) // 2
	steps := []struct {
		what  string
		do    func() error
		spent int64
	}{
		{"a later end", func() error { return later(p, t, ctx, a) }, 4},
		{"more traffic", func() error {
			q := uint64(2 << 30)
			_, err := p.clients.Update(ctx, a.ID, UpdateInput{QuotaBytes: &q})
			return err
		}, 6},
		{"a usage reset", func() error {
			p.db.Model(&model.Client{}).Where("id = ?", a.ID).Update("used_bytes", 100)
			_, err := p.clients.ResetTraffic(ctx, a.ID)
			return err
		}, 8},
		{"days added in bulk", func() error {
			d := 3.0
			_, err := p.clients.Adjust(ctx, AdjustInput{IDs: []uint{a.ID}, AddDays: &d})
			return err
		}, 10},
		{"grown by one", func() error {
			three := 3
			_, err := p.clients.Update(ctx, a.ID, UpdateInput{DeviceLimit: &three})
			return err
		}, 11},
		{"a rename alone", func() error {
			n := "renamed"
			_, err := p.clients.Update(ctx, a.ID, UpdateInput{Name: &n})
			return err
		}, 11},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.what, err)
		}
		if got := p.acc(t, r).Used; got != s.spent {
			t.Fatalf("after %s: spent %d, want %d", s.what, got, s.spent)
		}
	}
	p.balanced(t)
}

// A spent unit never comes back: not when the customer ends, shrinks, is
// deleted alone or in bulk, nor when the same customer is made again.
func TestConsumableNothingEverComesBack(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 100, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 3)
	b := p.make(t, ctx, "b", 2)
	c := p.make(t, ctx, "c", 1)
	spent := func() int64 { return p.acc(t, r).Used }
	if spent() != 6 {
		t.Fatal(spent())
	}
	endBy(p, t, c)
	one := 1
	p.clients.Update(ctx, a.ID, UpdateInput{DeviceLimit: &one})
	p.clients.Delete(ctx, a.ID)
	p.clients.Bulk(ctx, BulkDelete, []uint{b.ID, c.ID})
	if spent() != 6 {
		t.Fatalf("units came back: %d spent of 6", spent())
	}
	p.make(t, ctx, "a", 3)
	if spent() != 9 {
		t.Fatalf("making it again was free: %d", spent())
	}
	p.balanced(t)
}

// At zero nothing is made or extended, and what is refused changes nothing.
func TestConsumableAtZeroNothingMoves(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 2, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 2)
	_, err := p.clients.Create(ctx, CreateInput{Name: "b", InterfaceIDs: []uint{1}, DeviceLimit: 1})
	refusedForUsers(t, "making at zero", err)
	refusedForUsers(t, "extending at zero", later(p, t, ctx, a))
	var n int64
	p.db.Model(&model.Client{}).Count(&n)
	if n != 1 {
		t.Fatalf("%d customers", n)
	}
	p.balanced(t)
}

// Several at once are all or none: a batch past the users left makes none.
func TestABatchPastTheUsersLeftMakesNone(t *testing.T) {
	for _, mode := range []string{model.CapacityOpen, model.CapacityConsumable} {
		p := newSeatPanel(t)
		r := p.operator(t, "reza", model.RoleReseller, mode, 5, nil)
		ctx := asReseller(r, 1)
		_, err := p.clients.CreateBatch(ctx, BatchInput{CreateInput: CreateInput{InterfaceIDs: []uint{1}, DeviceLimit: 2}, Count: 3, Prefix: "u"})
		refusedForUsers(t, mode+": three of two users from five", err)
		var n int64
		p.db.Model(&model.Client{}).Count(&n)
		if n != 0 || p.acc(t, r).Used != 0 {
			t.Fatalf("%s: half a batch kept: %d customers, %d used", mode, n, p.acc(t, r).Used)
		}
		p.balanced(t)
	}
}

// What the owner does to an operator's customers spends nothing, and on an
// open account moves the seats to match even past the ceiling.
func TestTheOwnersHandSpendsNothing(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 10, nil)
	a := p.make(t, asReseller(r, 1), "a", 2)
	if err := later(p, t, context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 2 {
		t.Fatalf("the owner's extension spent the reseller's units: %d", got)
	}
	p.balanced(t)
}

// ── The tree ──

// An admin hands their resellers no more than they have free; a reseller's
// use is out of what they were given; what was handed is the admin's no
// longer to use.
func TestAnAdminSellsWhatTheyHaveFree(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adm", model.RoleAdmin, model.CapacityOpen, 10, nil)
	p.make(t, asReseller(ad, 1), "own", 3)
	on, eight := true, 8
	ctx := operators.WithCaller(WithScope(context.Background(), ScopeFor(ad)), ad)
	_, err := p.admins.Create(ctx, operators.AdminInput{Username: "toomuch", Password: "correct-horse", Role: model.RoleReseller,
		Enabled: &on, ClientLimit: &eight, InterfaceIDs: []uint{1}})
	if err == nil {
		t.Fatal("an admin with 7 free gave a reseller 8")
	}
	r := p.operator(t, "rsa", model.RoleReseller, "", 7, ad)
	if p.acc(t, r).Mode != model.CapacityOpen {
		t.Fatal("a reseller is not counted as their admin is")
	}
	_, err = p.clients.Create(asReseller(ad, 1), CreateInput{Name: "x", InterfaceIDs: []uint{1}, DeviceLimit: 1})
	refusedForUsers(t, "the admin using what they handed on", err)
	p.make(t, asReseller(r, 1), "r1", 7)
	if a := p.acc(t, ad); a.Used != 3 || a.Handed != 7 || capacity.Free(a) != 0 {
		t.Fatalf("admin %+v", a)
	}
	p.balanced(t)
}

// A reseller removed: counted as seats, what they were given goes back to
// their admin; counted as spent units, nothing does.
func TestRemovingAResellerGivesBackSeatsNotUnits(t *testing.T) {
	for mode, back := range map[string]int64{model.CapacityOpen: 0, model.CapacityConsumable: 6} {
		p := newSeatPanel(t)
		ad := p.operator(t, "adm", model.RoleAdmin, mode, 10, nil)
		r := p.operator(t, "rsa", model.RoleReseller, "", 6, ad)
		p.make(t, asReseller(r, 1), "c", 2)
		if err := p.admins.Delete(context.Background(), r.ID, operators.DeleteWithClients, p.clients); err != nil {
			t.Fatal(err)
		}
		if got := p.acc(t, ad).Handed; got != back {
			t.Fatalf("%s: the admin's handed figure is %d, want %d", mode, got, back)
		}
		p.balanced(t)
	}
}

// Switching how an operator is counted leaves their customers alone and
// charges nothing for them: to consumable, spent starts at nothing; to open,
// the seats held are counted once.
func TestSwitchingModeChargesNothing(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 10, nil)
	ctx := asReseller(r, 1)
	p.make(t, ctx, "a", 3)
	ended := p.make(t, ctx, "b", 2)
	endBy(p, t, ended)
	ten := 10
	if _, err := p.admins.Update(context.Background(), r.ID, operators.AdminInput{ClientLimit: &ten, UserMode: model.CapacityConsumable}); err != nil {
		t.Fatal(err)
	}
	if a := p.acc(t, r); a.Mode != model.CapacityConsumable || a.Used != 0 {
		t.Fatalf("to consumable: %+v", a)
	}
	if _, err := p.admins.Update(context.Background(), r.ID, operators.AdminInput{ClientLimit: &ten, UserMode: model.CapacityOpen}); err != nil {
		t.Fatal(err)
	}
	if a := p.acc(t, r); a.Mode != model.CapacityOpen || a.Used != 3 {
		t.Fatalf("to open: %+v (one served customer of 3 users)", a)
	}
	p.balanced(t)
}

// The owner lowering a ceiling below what is held cuts nobody off; nothing
// new is let in until enough has ended.
func TestALowerCeilingCutsNobodyOff(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 10, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 4)
	two := 2
	if _, err := p.admins.Update(context.Background(), r.ID, operators.AdminInput{ClientLimit: &two}); err != nil {
		t.Fatal(err)
	}
	var still model.Client
	p.db.First(&still, a.ID)
	if still.Status != model.StatusActive {
		t.Fatalf("a customer was cut off: %s", still.Status)
	}
	_, err := p.clients.Create(ctx, CreateInput{Name: "b", InterfaceIDs: []uint{1}, DeviceLimit: 1})
	refusedForUsers(t, "making while over", err)
	p.balanced(t)
}

// A plan the panel renews brings a customer back only if a seat is free; if
// not, the period starts but they stay out, and nothing is spent.
func TestARenewalWithNoSeatFreeStaysOut(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 2, nil)
	ctx := asReseller(r, 1)
	a := p.make(t, ctx, "a", 2)
	err := SettleChange(context.Background(), p.db, []uint{a.ID}, ByPanel, func(tx *gorm.DB) ([]uint, error) {
		return nil, tx.Model(&model.Client{}).Where("id = ?", a.ID).Update("status", model.StatusExhausted).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	p.make(t, ctx, "b", 2)
	err = SettleChange(context.Background(), p.db, []uint{a.ID}, ByPanelIfFree, func(tx *gorm.DB) ([]uint, error) {
		return nil, tx.Model(&model.Client{}).Where("id = ?", a.ID).Update("status", model.StatusActive).Error
	})
	refusedForUsers(t, "the panel reviving with no seat free", err)
	p.balanced(t)
}

// Numbers no plan could mean are refused at the door: a negative or a
// fractional ceiling, more than a million users, a plan for no users or any
// number at once under a ceiling.
func TestNumbersNoPlanCouldMeanAreRefused(t *testing.T) {
	p := newSeatPanel(t)
	on := true
	for _, n := range []int{-1, capacity.MaxUnits + 1} {
		n := n
		_, err := p.admins.Create(context.Background(), operators.AdminInput{Username: fmt.Sprintf("bad%d", n+5), Password: "correct-horse",
			Role: model.RoleReseller, Enabled: &on, ClientLimit: &n, InterfaceIDs: []uint{1}})
		if err == nil {
			t.Errorf("a ceiling of %d was accepted", n)
		}
	}
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := asReseller(r, 1)
	for _, users := range []int{0, -3, 51} {
		if _, err := p.clients.Create(ctx, CreateInput{Name: fmt.Sprintf("u%d", users+5), InterfaceIDs: []uint{1}, DeviceLimit: users}); err == nil {
			t.Errorf("a customer for %d users was made", users)
		}
	}
	p.balanced(t)
}

// Requests at once, as a panel takes them -- on a database file set as the
// panel sets it -- cannot pass a ceiling: forty customers made at once
// against ten users let ten in, in either mode, and no address is lost.
func TestCustomersMadeAtOnceCannotPassTheCeiling(t *testing.T) {
	for _, mode := range []string{model.CapacityOpen, model.CapacityConsumable} {
		t.Run(mode, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "w.db")+
				"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(15000)&_txlock=immediate"),
				&gorm.Config{Logger: gormlogger.Discard})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, _ := db.DB()
			sqlDB.SetMaxOpenConns(8)
			t.Cleanup(func() { sqlDB.Close() })
			if err := db.AutoMigrate(model.AllModels()...); err != nil {
				t.Fatal(err)
			}
			if err := scope.Register(db); err != nil {
				t.Fatal(err)
			}
			db.Create(&model.Node{Name: "local", Kind: model.KindLocal})
			iface := model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820, Subnet: "10.9.0.0/24", EndpointHost: "x", NodeID: 1}
			db.Create(&iface)
			pools := ipam.NewPools()
			pools.Add(iface.ID, iface.Subnet)
			clients := NewClients(db, pools, quietLog())
			admins := operators.NewAdmins(db, quietLog())
			on, ten := true, 10
			r, err := admins.Create(context.Background(), operators.AdminInput{Username: "reza", Password: "correct-horse",
				Role: model.RoleReseller, Enabled: &on, ClientLimit: &ten, UserMode: mode, InterfaceIDs: []uint{iface.ID}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := asReseller(r, iface.ID)
			var won atomic.Int64
			var wg sync.WaitGroup
			for i := 0; i < 40; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := clients.Create(ctx, CreateInput{Name: fmt.Sprintf("c%02d", i), InterfaceIDs: []uint{iface.ID}, DeviceLimit: 1})
					switch {
					case err == nil:
						won.Add(1)
					case !errors.Is(err, ErrNoUsers):
						t.Errorf("customer %d failed otherwise: %v", i, err)
					}
				}(i)
			}
			wg.Wait()
			var made, accounts int64
			db.Model(&model.Client{}).Count(&made)
			db.Model(&model.Account{}).Count(&accounts)
			acc, _ := capacity.Account(db, r.ID, model.CapacityUsers)
			if won.Load() != 10 || made != 10 || accounts != 10 || acc.Used != 10 {
				t.Fatalf("let in %d, stored %d customers and %d files, used %d -- against ten users", won.Load(), made, accounts, acc.Used)
			}
			if alloc, err := pools.Get(iface.ID); err != nil || alloc.InUse() != 10 {
				t.Fatalf("addresses held: %v %v, want the ten customers' alone", alloc.InUse(), err)
			}
			if bad, _ := capacity.Balanced(db); len(bad) > 0 {
				t.Fatalf("unbalanced: %v", bad)
			}
		})
	}
}

// ── What an extension would take, shown before it is confirmed ──

// The preview takes what the extension takes -- counted by running it and
// rolling it back -- and leaves the account and the customer as they were.
func TestAnExtensionsPreviewIsWhatItTakes(t *testing.T) {
	p := newSeatPanel(t)
	units := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 5, nil)
	seats := p.operator(t, "sara", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctxU, ctxS := asReseller(units, 1), asReseller(seats, 1)
	a := p.make(t, ctxU, "a", 2)
	b := p.make(t, ctxS, "b", 2)
	in := func(id uint, dry bool) ExtendInput {
		return ExtendInput{IDs: []uint{id}, Kind: ExtendTime, Days: 3, DryRun: dry}
	}
	check := func(what string, ctx context.Context, op *model.Admin, id uint, want int64) {
		t.Helper()
		was := p.acc(t, op).Used
		var before model.Client
		p.db.First(&before, id)
		got, err := p.clients.Extend(ctx, in(id, true))
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got.Units != want {
			t.Errorf("%s: the preview says %d units, want %d", what, got.Units, want)
		}
		var after model.Client
		p.db.First(&after, id)
		if p.acc(t, op).Used != was || !after.ExpiresAt.Equal(*before.ExpiresAt) || after.Status != before.Status {
			t.Fatalf("%s: the preview changed something", what)
		}
		if _, err := p.clients.Extend(ctx, in(id, false)); err != nil {
			t.Fatalf("%s, for real: %v", what, err)
		}
		if took := p.acc(t, op).Used - was; took != want {
			t.Errorf("%s: the preview said %d, the extension took %d", what, want, took)
		}
	}
	// Consumable: every extension spends the customer's users.
	check("units, a live customer", ctxU, units, a.ID, 2)
	// Open: free while the customer is on; ended, it takes the seats back.
	check("seats, a live customer", ctxS, seats, b.ID, 0)
	endBy(p, t, b)
	if err := p.db.Model(&model.Client{}).Where("id = ?", b.ID).Update("expires_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	check("seats, an ended customer", ctxS, seats, b.ID, 2)
	p.balanced(t)

	// One unit of five left, two needed: refused in the preview, in the
	// same words as the extension itself.
	_, err := p.clients.Extend(ctxU, in(a.ID, true))
	refusedForUsers(t, "the preview of one past the units left", err)
	_, err = p.clients.Extend(ctxU, in(a.ID, false))
	refusedForUsers(t, "the extension past the units left", err)
}

// An admin the owner lets see every customer makes customers that are
// theirs, paid from their users, and is refused past what is left. Before,
// they made the owner's customers, for nothing.
func TestAnAdminWhoSeesEveryonePaysForWhatTheySell(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adm", model.RoleAdmin, model.CapacityConsumable, 3, nil)
	if err := p.db.Model(&model.Admin{}).Where("id = ?", ad.ID).Update("can_view_all_clients", true).Error; err != nil {
		t.Fatal(err)
	}
	ad.CanViewAllClients = true
	sc := ScopeFor(ad)
	if sc.Restricted || !sc.Capped {
		t.Fatalf("the scope: %+v", sc)
	}
	ctx := WithScope(context.Background(), sc)
	c := p.make(t, ctx, "x", 2)
	if c.OwnerID != ad.ID || c.AdminID != ad.ID {
		t.Errorf("stamped owner %d admin %d, want %d", c.OwnerID, c.AdminID, ad.ID)
	}
	if used := p.acc(t, ad).Used; used != 2 {
		t.Errorf("spent %d, want 2", used)
	}
	_, err := p.clients.Create(ctx, CreateInput{Name: "y", InterfaceIDs: []uint{1}, DeviceLimit: 2,
		ExpiresAt: ptrTime(time.Now().UTC().Add(48 * time.Hour))})
	refusedForUsers(t, "past the units left", err)
	p.balanced(t)
}
