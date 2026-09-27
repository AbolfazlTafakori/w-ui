package service

import (
	"errors"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// One reseller cannot delete another's device by its number.
//
// Devices are not narrowed to their operator the way customers are, and the
// endpoint takes a bare id. Before this, a reseller counting up from one could
// delete every device on the panel -- another reseller's, the owner's -- and
// each customer would find out when their app stopped connecting.
func TestAResellerCannotRemoveAnotherOperatorsDevice(t *testing.T) {
	db, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	sara := newReseller(t, admins, "sara", 0)

	theirs, err := clients.Create(asReseller(sara, 1), CreateInput{Name: "saras", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	owners, err := clients.Create(t.Context(), CreateInput{Name: "owners", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}

	for _, victim := range []*model.Client{theirs, owners} {
		var acc model.Account
		if err := db.Where("client_id = ?", victim.ID).First(&acc).Error; err != nil {
			t.Fatal(err)
		}

		err := clients.RemoveDevice(asReseller(reza, 1), acc.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("removing %s's device gave %v, want not found", victim.Name, err)
		}
		var still int64
		db.Model(&model.Account{}).Where("id = ?", acc.ID).Count(&still)
		if still != 1 {
			t.Fatalf("a reseller deleted %s's device", victim.Name)
		}

		// Reading the configuration is refused the same way: it carries the
		// customer's private key.
		if _, err := clients.Profiles(asReseller(reza, 1), acc.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("reading %s's configuration gave %v, want not found", victim.Name, err)
		}
	}

	// Their own device, they can remove.
	mine, err := clients.Create(asReseller(reza, 1), CreateInput{Name: "rezas", InterfaceIDs: []uint{1}, DeviceLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	var acc model.Account
	if err := db.Where("client_id = ?", mine.ID).First(&acc).Error; err != nil {
		t.Fatal(err)
	}
	if err := clients.RemoveDevice(asReseller(reza, 1), acc.ID); err != nil {
		t.Fatalf("a reseller could not remove their own customer's device: %v", err)
	}
}

// When the owner takes a server back, the reseller can still look after the
// customers already on it -- rename them, extend them, take them off it --
// and cannot put anyone new there.
func TestAServerTakenBackStillLetsTheResellerLookAfterItsCustomers(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)

	// Sold on both servers while they had both.
	both := asReseller(reza, 1, 2)
	c, err := clients.Create(both, CreateInput{Name: "on-both", InterfaceIDs: []uint{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := clients.Create(both, CreateInput{Name: "on-one", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}

	// Server 2 taken back.
	now := asReseller(reza, 1)

	if _, err := clients.Update(now, c.ID, UpdateInput{Name: strptr("renamed"), InterfaceIDs: []uint{1, 2}}); err != nil {
		t.Fatalf("renaming a customer on a server taken back was refused: %v", err)
	}
	if _, err := clients.Update(now, c.ID, UpdateInput{InterfaceIDs: []uint{1}}); err != nil {
		t.Fatalf("taking a customer off a server taken back was refused: %v", err)
	}
	if _, err := clients.Update(now, other.ID, UpdateInput{InterfaceIDs: []uint{1, 2}}); err == nil {
		t.Fatal("a reseller put a customer on a server they no longer have")
	}
	if _, err := clients.AttachServers(now, []uint{other.ID}, []uint{2}); err == nil {
		t.Fatal("a reseller attached a server they no longer have, in bulk")
	}

	// Detaching in bulk from a server taken back is allowed.
	back, err := clients.Create(both, CreateInput{Name: "detach-me", InterfaceIDs: []uint{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := clients.DetachServers(now, []uint{back.ID}, []uint{2})
	if err != nil || res.Changed != 1 {
		t.Fatalf("taking customers off a server taken back, in bulk, gave %+v, %v", res, err)
	}
}

// The group actions write the plan directly, so they are refused to a barred
// reseller on their own account: extending a whole group by a year is the
// same storing-up as extending one customer.
func TestGroupActionsAreRefusedToABarredReseller(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	if _, err := clients.Create(asReseller(reza, 1), CreateInput{
		Name: "in-a-group", InterfaceIDs: []uint{1}, Groups: []string{"vip"}}); err != nil {
		t.Fatal(err)
	}

	sc := ScopeFor(reza)
	sc.Interfaces = map[uint]bool{1: true}
	sc.Barred = "your account's term has ended"
	ctx := WithScope(t.Context(), sc)

	for name, op := range map[string]GroupOp{
		"extend": {Group: "vip", Action: GroupExtend, Days: 365},
		"quota":  {Group: "vip", Action: GroupSetQuota, QuotaBytes: uint64(500) << 30},
		"reset":  {Group: "vip", Action: GroupReset},
	} {
		if _, err := clients.ApplyToGroup(ctx, op); err == nil {
			t.Errorf("a barred reseller could %s a whole group", name)
		}
	}
	if _, err := clients.AttachServers(ctx, []uint{1}, []uint{1}); err == nil {
		t.Error("a barred reseller could move customers between servers")
	}
}

// A term on hold is days, with no date until the reseller first signs in; a
// date given later replaces it.
func TestAResellersTermCanWaitForTheirFirstSignIn(t *testing.T) {
	_, _, admins := resellerDB(t)
	thirty, limit := 30, 5
	a, err := admins.Create(t.Context(), AdminInput{
		Username: "on-hold", Password: "correct-horse", Role: model.RoleReseller,
		ClientLimit: &limit, InterfaceIDs: []uint{1}, DurationDays: &thirty,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ExpiresAt != nil || a.DurationDays != 30 {
		t.Fatalf("on hold for 30 days came back as expires=%v days=%d", a.ExpiresAt, a.DurationDays)
	}
	if a.Expired(time.Now().UTC()) || a.Suspended(time.Now().UTC()) {
		t.Fatal("a reseller whose term has not started is treated as ended")
	}

	// A date replaces the hold.
	when := time.Now().UTC().AddDate(0, 2, 0)
	got, err := admins.Update(t.Context(), a.ID, AdminInput{ExpiresAt: OptionalTime{Set: true, Value: &when}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt == nil || got.DurationDays != 0 {
		t.Fatalf("a date given did not replace the hold: expires=%v days=%d", got.ExpiresAt, got.DurationDays)
	}

	// And the hold replaces a date.
	got, err = admins.Update(t.Context(), a.ID, AdminInput{DurationDays: &thirty})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiresAt != nil || got.DurationDays != 30 {
		t.Fatalf("a hold given did not replace the date: expires=%v days=%d", got.ExpiresAt, got.DurationDays)
	}

	tooLong := maxTermDays + 1
	if _, err := admins.Update(t.Context(), a.ID, AdminInput{DurationDays: &tooLong}); err == nil {
		t.Fatal("a term past the limit was accepted")
	}
}

// The owner chooses what a reseller's customers are filed under, renaming it
// moves them, and removing the reseller takes nobody else's customers out of
// a label the owner shares with their own.
func TestTheOwnerChoosesAndRenamesAResellersLabel(t *testing.T) {
	db, clients, admins := resellerDB(t)
	limit := 0
	label := "Tehran agents"
	a, err := admins.Create(t.Context(), AdminInput{
		Username: "reza", Password: "correct-horse", Role: model.RoleReseller,
		ClientLimit: &limit, InterfaceIDs: []uint{1}, GroupName: &label,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.GroupName != label {
		t.Fatalf("the label chosen came back as %q", a.GroupName)
	}

	c, err := clients.Create(asReseller(a, 1), CreateInput{Name: "theirs", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	inGroup := func(clientID uint, name string) bool {
		var n int64
		db.Model(&model.ClientGroup{}).Where("client_id = ? AND name = ?", clientID, name).Count(&n)
		return n == 1
	}
	if !inGroup(c.ID, label) {
		t.Fatal("a reseller's customer was not filed under the label chosen")
	}

	// Renamed onto a group the owner already has customers of their own in,
	// typed in a different case.
	own, err := clients.Create(t.Context(), CreateInput{Name: "owners", InterfaceIDs: []uint{1}, Groups: []string{"VIP"}})
	if err != nil {
		t.Fatal(err)
	}
	vip := "vip"
	got, err := admins.Update(t.Context(), a.ID, AdminInput{GroupName: &vip})
	if err != nil {
		t.Fatal(err)
	}
	if got.GroupName != "VIP" {
		t.Fatalf("the label took the spelling %q rather than the group's own", got.GroupName)
	}
	if !inGroup(c.ID, "VIP") || inGroup(c.ID, label) {
		t.Fatal("renaming the label did not move the reseller's customer")
	}
	var old int64
	db.Model(&model.Group{}).Where("owner_id = 0 AND name = ?", label).Count(&old)
	if old != 0 {
		t.Fatal("the label left behind was not removed")
	}

	// Two resellers under one label cannot be told apart.
	sara := newReseller(t, admins, "sara", 0)
	if _, err := admins.Update(t.Context(), sara.ID, AdminInput{GroupName: &vip}); err == nil {
		t.Fatal("a second reseller was filed under the same label")
	}

	// Removing the reseller, keeping their customers: theirs leave the
	// label, the owner's own stay in it, and so does the group.
	if err := admins.Delete(t.Context(), a.ID, DeleteKeepClients, clients); err != nil {
		t.Fatal(err)
	}
	if inGroup(c.ID, "VIP") {
		t.Error("the departed reseller's customer is still under their label")
	}
	if !inGroup(own.ID, "VIP") {
		t.Fatal("removing a reseller took the owner's own customer out of a shared group")
	}
	var kept int64
	db.Model(&model.Group{}).Where("owner_id = 0 AND name = ?", "VIP").Count(&kept)
	if kept != 1 {
		t.Fatal("removing a reseller deleted a group the owner still uses")
	}
}

// The owner's label is not read out to the reseller through the one-label
// field every customer row still carries.
func TestTheOwnersLabelIsNotOnTheResellersRows(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	ctx := asReseller(reza, 1)

	quiet, err := clients.Create(ctx, CreateInput{Name: "no-groups", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	grouped, err := clients.Create(ctx, CreateInput{Name: "grouped", InterfaceIDs: []uint{1}, Groups: []string{"zeta"}})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []uint{quiet.ID, grouped.ID} {
		c, err := clients.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Group == reza.GroupName {
			t.Errorf("%s carries the owner's label %q in its group field", c.Name, c.Group)
		}
	}
	page, err := clients.List(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range page.Items {
		if c.Group == reza.GroupName {
			t.Errorf("the list shows %s under the owner's label", c.Name)
		}
		if c.Name == "grouped" && c.Group != "zeta" {
			t.Errorf("a reseller's own group reads %q, want zeta", c.Group)
		}
	}

	// The owner still sees it.
	c, err := clients.Get(t.Context(), quiet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Group != reza.GroupName {
		t.Errorf("the owner reads %q, want their label %q", c.Group, reza.GroupName)
	}
}
