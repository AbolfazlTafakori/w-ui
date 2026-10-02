package service

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// A group made on the groups page before anyone is in it is offered by
// the picker alongside the labels customers already carry.
func TestGroupNamesIncludeEmptyGroups(t *testing.T) {
	db := testDB(t)
	c := model.Client{Name: "a", Status: model.StatusActive}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientGroup{ClientID: c.ID, Name: "labelled"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Group{Name: "made-empty"}).Error; err != nil {
		t.Fatal(err)
	}
	names, err := NewClients(db, ipam.NewPools(), quietLog()).ListGroupNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "labelled" || names[1] != "made-empty" {
		t.Fatalf("names: %v", names)
	}
}

// A customer can be in several groups: set as a list, read back as one,
// added to and taken out of one at a time with the others untouched, and
// the groups page counts them in each.
func TestACustomerCanBeInSeveralGroups(t *testing.T) {
	db := testDB(t)
	svc := NewClients(db, ipam.NewPools(), quietLog())
	ctx := context.Background()
	c := model.Client{Name: "ali", Status: model.StatusActive}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := setGroups(context.Background(), db, c.ID, groupsOf([]string{" vip ", "tehran", "VIP", ""}, "")); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 2 || got.Groups[0] != "tehran" || got.Groups[1] != "vip" || got.Group != "tehran" {
		t.Fatalf("groups: %v label %q", got.Groups, got.Group)
	}
	if _, err := svc.AssignGroup(ctx, "reseller-a", []uint{c.ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AssignGroup(ctx, "vip", []uint{c.ID}, true); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, c.ID)
	if len(got.Groups) != 2 || got.Groups[0] != "reseller-a" || got.Groups[1] != "tehran" {
		t.Fatalf("after add and remove: %v", got.Groups)
	}
	page, err := svc.List(ctx, ListFilter{Groups: []string{"tehran"}})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("filter by one of their groups: %v %+v", err, page)
	}
	res, err := svc.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Items[0].Clients != 1 || res.Items[1].Clients != 1 || res.Totals.Ungrouped != 0 {
		t.Fatalf("groups page: %+v", res)
	}
	if _, err := svc.RenameGroup(ctx, "tehran", "reseller-a"); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, c.ID)
	if len(got.Groups) != 1 || got.Groups[0] != "reseller-a" {
		t.Fatalf("renaming onto a group they are in merges: %v", got.Groups)
	}
}

// Extending a group's validity moves each member's own end date: one with
// time left keeps it, one already past is counted from now and is back on.
// One with no end date -- a plan that never ends, or one waiting for its
// first connection -- is left alone, as the customer list's time action
// leaves them.
func TestExtendingAGroupMovesEachMembersOwnDate(t *testing.T) {
	db := testDB(t)
	svc := NewClients(db, ipam.NewPools(), quietLog())
	ctx := context.Background()
	now := time.Now().UTC()
	inAWeek, aDayAgo := now.AddDate(0, 0, 7), now.AddDate(0, 0, -1)
	running := model.Client{Name: "running", Status: model.StatusActive, ExpiresAt: &inAWeek}
	ended := model.Client{Name: "ended", Status: model.StatusExpired, ExpiresAt: &aDayAgo}
	forever := model.Client{Name: "forever", Status: model.StatusActive}
	waiting := model.Client{Name: "waiting", Status: model.StatusActive, StartOnFirstUse: true, DurationDays: 30}
	for _, c := range []*model.Client{&running, &ended, &forever, &waiting} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AssignGroup(ctx, "batch", []uint{c.ID}, false); err != nil {
			t.Fatal(err)
		}
	}
	n, err := svc.ApplyToGroup(ctx, GroupOp{Action: GroupExtend, Group: "batch", Days: 10})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("extended %d, want 2: the two with an end date", n)
	}
	get := func(c model.Client) model.Client {
		var got model.Client
		if err := db.First(&got, c.ID).Error; err != nil {
			t.Fatal(err)
		}
		return got
	}
	if g := get(running); g.ExpiresAt == nil || g.ExpiresAt.Sub(inAWeek) != 10*24*time.Hour {
		t.Errorf("the running plan ends %v, want ten days after %s", g.ExpiresAt, inAWeek)
	}
	if g := get(ended); g.ExpiresAt == nil || g.ExpiresAt.Before(now.AddDate(0, 0, 9)) || g.Status != model.StatusActive {
		t.Errorf("the ended plan: ends %v, status %s; want ten days from now and active", g.ExpiresAt, g.Status)
	}
	if g := get(forever); g.ExpiresAt != nil {
		t.Errorf("a plan that never ends was given an end date: %v", g.ExpiresAt)
	}
	if g := get(waiting); g.ExpiresAt != nil {
		t.Errorf("a plan waiting for its first connection was given an end date: %v", g.ExpiresAt)
	}
}
