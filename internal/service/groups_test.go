package service

import (
	"context"
	"testing"

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
