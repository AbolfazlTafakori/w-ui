package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/scope"
)

// What a reseller must never be able to reach.
//
// The separation is one condition applied by the database itself, which is
// what makes it hold for code nobody has written yet -- so these tests go
// at it through the service layer exactly as a request would, rather than
// asserting that a particular function remembered to filter.

// resellerDB is a panel with two tunnels and the scoping rule registered,
// which is how the real one is opened.
func resellerDB(t *testing.T) (*gorm.DB, *Clients, *Admins) {
	t.Helper()
	db := testDB(t)
	if err := scope.Register(db); err != nil {
		t.Fatalf("register scoping: %v", err)
	}
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	pools := ipam.NewPools()
	for i, name := range []string{"wg0", "wg1"} {
		subnet := fmt.Sprintf("10.9.%d.0/24", i)
		iface := &model.Interface{
			Name: name, Protocol: model.ProtocolWireGuard, ListenPort: 51820 + i,
			Subnet: subnet, EndpointHost: "vpn.example.com", NodeID: 1,
		}
		if err := db.Create(iface).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := pools.Add(iface.ID, subnet); err != nil {
			t.Fatal(err)
		}
	}
	return db, NewClients(db, pools, quietLog()), NewAdmins(db, quietLog())
}

// asReseller is the context a request from that operator is served under.
func asReseller(admin *model.Admin, ifaces ...uint) context.Context {
	sc := ScopeFor(admin)
	sc.ClientLimit = admin.ClientLimit
	if ifaces != nil {
		sc.Interfaces = map[uint]bool{}
		for _, id := range ifaces {
			sc.Interfaces[id] = true
		}
	}
	return WithScope(context.Background(), sc)
}

func newReseller(t *testing.T, admins *Admins, name string, limit int) *model.Admin {
	t.Helper()
	enabled := true
	a, err := admins.Create(context.Background(), AdminInput{
		Username: name, Password: "correct-horse", Role: model.RoleReseller,
		Enabled: &enabled, ClientLimit: &limit, InterfaceIDs: []uint{1},
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return a
}

func TestResellerSeesOnlyTheirOwnCustomers(t *testing.T) {
	db, clients, admins := resellerDB(t)
	_ = db
	owner := context.Background()

	reza := newReseller(t, admins, "reza", 0)
	sara := newReseller(t, admins, "sara", 0)

	mk := func(ctx context.Context, name string) *model.Client {
		t.Helper()
		c, err := clients.Create(ctx, CreateInput{Name: name, InterfaceIDs: []uint{1}})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return c
	}

	rezas := mk(asReseller(reza, 1), "reza-1")
	saras := mk(asReseller(sara, 1), "sara-1")
	mine := mk(owner, "owner-1")

	// The list.
	page, err := clients.List(asReseller(reza, 1), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ID != rezas.ID {
		t.Fatalf("reseller sees %d customers, want only their own", page.Total)
	}

	// Reading one of somebody else's, by id.
	for _, id := range []uint{saras.ID, mine.ID} {
		if _, err := clients.Get(asReseller(reza, 1), id); !errors.Is(err, ErrNotFound) {
			t.Errorf("reading customer %d gave %v, want not-found", id, err)
		}
	}

	// Changing one.
	name := "renamed"
	if _, err := clients.Update(asReseller(reza, 1), saras.ID, UpdateInput{Name: &name}); err == nil {
		t.Error("a reseller renamed another operator's customer")
	}

	// Deleting one.
	if err := clients.Delete(asReseller(reza, 1), saras.ID); err == nil {
		t.Error("a reseller deleted another operator's customer")
	}

	// In bulk, which is the path that takes a list of ids and is the
	// easiest one to forget.
	if n, err := clients.Bulk(asReseller(reza, 1), BulkDelete, []uint{saras.ID, mine.ID}); err == nil && n > 0 {
		t.Errorf("a bulk delete reached %d of somebody else's customers", n)
	}
	var left int64
	if err := db.Model(&model.Client{}).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 3 {
		t.Fatalf("%d customers left, want all 3 still there", left)
	}

	// The owner sees all three.
	all, err := clients.List(owner, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 3 {
		t.Fatalf("the owner sees %d customers, want 3", all.Total)
	}
}

func TestResellerCannotSellAServerTheyWereNotGiven(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	ctx := asReseller(reza, 1)

	_, err := clients.Create(ctx, CreateInput{Name: "x", InterfaceIDs: []uint{2}})
	if err == nil || !strings.Contains(err.Error(), "not one of yours") {
		t.Fatalf("creating on a server they were not given gave %v", err)
	}

	// And cannot get there afterwards by adding it to a customer they own.
	c, err := clients.Create(ctx, CreateInput{Name: "y", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clients.Update(ctx, c.ID, UpdateInput{InterfaceIDs: []uint{1, 2}}); err == nil {
		t.Fatal("a reseller added a server they were not given")
	}
}

func TestResellerCeilingOnCustomerCount(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 2)
	ctx := asReseller(reza, 1)

	for i := range 2 {
		if _, err := clients.Create(ctx, CreateInput{Name: string(rune('a' + i)), InterfaceIDs: []uint{1}}); err != nil {
			t.Fatalf("customer %d: %v", i, err)
		}
	}
	_, err := clients.Create(ctx, CreateInput{Name: "one too many", InterfaceIDs: []uint{1}})
	if err == nil || !strings.Contains(err.Error(), "you may have 2 customers") {
		t.Fatalf("past the ceiling gave %v, want a refusal naming it", err)
	}
}

func TestSuspensionIsComputedAndLeavesCustomersAlone(t *testing.T) {
	db, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	ctx := asReseller(reza, 1)

	live, err := clients.Create(ctx, CreateInput{Name: "live", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	stopped, err := clients.Create(ctx, CreateInput{Name: "stopped", InterfaceIDs: []uint{1}, Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}

	// The owner switches the reseller off.
	no := false
	if _, err := admins.Update(context.Background(), reza.ID, AdminInput{Enabled: &no}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	suspended, err := SuspendedOwners(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if !suspended[reza.ID] {
		t.Fatal("a switched-off reseller is not suspended")
	}

	// Neither customer's own row was touched, which is what makes turning
	// the reseller back on restore what they had.
	var after []model.Client
	if err := db.Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after[0].ID != live.ID || after[0].Status != model.StatusActive {
		t.Errorf("the live customer is now %q, want it untouched", after[0].Status)
	}
	if after[1].ID != stopped.ID || after[1].Status != model.StatusDisabled {
		t.Errorf("the stopped customer is now %q, want it untouched", after[1].Status)
	}

	// Switched back on, and the one that was stopped by hand stays stopped.
	yes := true
	if _, err := admins.Update(context.Background(), reza.ID, AdminInput{Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	suspended, err = SuspendedOwners(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if suspended[reza.ID] {
		t.Fatal("a reseller switched back on is still suspended")
	}
}

func TestSuspensionFollowsTheCeilings(t *testing.T) {
	db, _, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	now := time.Now().UTC()

	quota := uint64(100)
	if _, err := admins.Update(context.Background(), reza.ID, AdminInput{QuotaBytes: &quota}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Admin{}).Where("id = ?", reza.ID).
		Update("used_bytes", 100).Error; err != nil {
		t.Fatal(err)
	}
	if s, _ := SuspendedOwners(context.Background(), db, now); !s[reza.ID] {
		t.Error("a reseller who has spent their allowance is not suspended")
	}

	// Topped up.
	if err := admins.ResetUsage(context.Background(), reza.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := SuspendedOwners(context.Background(), db, now); s[reza.ID] {
		t.Error("a reseller topped up again is still suspended")
	}

	// Out of time.
	past := now.Add(-time.Hour)
	if _, err := admins.Update(context.Background(), reza.ID, AdminInput{ExpiresAt: At(past)}); err != nil {
		t.Fatal(err)
	}
	if s, _ := SuspendedOwners(context.Background(), db, now); !s[reza.ID] {
		t.Error("a reseller past their term is not suspended")
	}
}

func TestOwnerGroupIsHiddenFromTheResellerAndKeptOnTheirCustomers(t *testing.T) {
	db, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	ctx := asReseller(reza, 1)

	c, err := clients.Create(ctx, CreateInput{
		Name: "someone", InterfaceIDs: []uint{1}, Groups: []string{"trial"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The reseller sees their own label and not the owner's.
	seen, err := clients.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen.Groups) != 1 || seen.Groups[0] != "trial" {
		t.Fatalf("the reseller sees groups %v, want only their own", seen.Groups)
	}
	names, err := clients.ListGroupNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n == reza.GroupName {
			t.Fatalf("the owner's label %q is offered to the reseller", n)
		}
	}

	// The owner sees both, which is how they tell whose customer this is.
	var rows []model.ClientGroup
	if err := db.Where("client_id = ?", c.ID).Order("name").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("customer carries %d labels, want their own and the owner's", len(rows))
	}

	// And cannot be taken off: clearing every group leaves the owner's.
	if _, err := clients.AssignGroup(ctx, "", []uint{c.ID}, false); err != nil {
		t.Fatal(err)
	}
	var left int64
	if err := db.Model(&model.ClientGroup{}).Where("client_id = ?", c.ID).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("%d labels left after the reseller cleared them, want the owner's one", left)
	}

	// Nor renamed, deleted or acted on by name.
	if _, err := clients.RenameGroup(ctx, reza.GroupName, "mine"); err == nil {
		t.Error("a reseller renamed the owner's label")
	}
	if _, err := clients.DeleteGroup(ctx, reza.GroupName); err == nil {
		t.Error("a reseller deleted the owner's label")
	}
	if _, err := clients.ApplyToGroup(ctx, GroupOp{Action: GroupClear, Group: reza.GroupName}); err == nil {
		t.Error("a reseller cleared the owner's label")
	}
}

func TestTwoResellersMayUseTheSameGroupName(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	sara := newReseller(t, admins, "sara", 0)

	if _, err := clients.CreateGroup(asReseller(reza, 1), "trial", ""); err != nil {
		t.Fatalf("reza's group: %v", err)
	}
	if _, err := clients.CreateGroup(asReseller(sara, 1), "trial", ""); err != nil {
		t.Fatalf("sara's group: %v", err)
	}

	// And each sees one of them.
	for _, a := range []*model.Admin{reza, sara} {
		res, err := clients.Groups(asReseller(a, 1))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) != 1 || res.Items[0].Name != "trial" {
			t.Fatalf("%s sees %d groups, want their own one", a.Username, len(res.Items))
		}
	}
}

func TestTheOwnerCannotBeRemovedOrSwitchedOff(t *testing.T) {
	db, clients, admins := resellerDB(t)
	owner := model.Admin{Username: "admin", PasswordHash: "x", Role: model.RoleOwner, Enabled: true}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}

	no := false
	if _, err := admins.Update(context.Background(), owner.ID, AdminInput{Enabled: &no}); err == nil {
		t.Error("the owner was switched off")
	}
	if err := admins.Delete(context.Background(), owner.ID, DeleteKeepClients, clients); err == nil {
		t.Error("the owner was deleted")
	}
	if _, err := admins.Create(context.Background(), AdminInput{
		Username: "second", Password: "correct-horse", Role: model.RoleOwner,
	}); err == nil {
		t.Error("a second owner was created")
	}
}

func TestDeletingAResellerNeedsAnAnswerAboutTheirCustomers(t *testing.T) {
	db, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	ctx := asReseller(reza, 1)
	if _, err := clients.Create(ctx, CreateInput{Name: "someone", InterfaceIDs: []uint{1}}); err != nil {
		t.Fatal(err)
	}

	err := admins.Delete(context.Background(), reza.ID, "", clients)
	if err == nil || !strings.Contains(err.Error(), "say whether to keep them") {
		t.Fatalf("deleting with customers gave %v, want a refusal asking which", err)
	}

	if err := admins.Delete(context.Background(), reza.ID, DeleteKeepClients, clients); err != nil {
		t.Fatal(err)
	}
	var kept model.Client
	if err := db.First(&kept).Error; err != nil {
		t.Fatalf("the customer was not kept: %v", err)
	}
	if kept.OwnerID != 0 {
		t.Errorf("the customer is still owned by %d, want the panel's owner", kept.OwnerID)
	}
}
