package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// A paused reseller's customers say so, to the owner and to the reseller,
// and their own page says they are off -- without saying why.
func TestAPausedResellersCustomersSaySo(t *testing.T) {
	db, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	c, err := clients.Create(asReseller(reza, 1), CreateInput{Name: "theirs", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	subs := NewSubscriptions(db, nil, nil, quietLog())

	check := func(want, page string) {
		t.Helper()
		got, err := clients.Get(t.Context(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.OwnerPaused != want {
			t.Errorf("the owner reads %q, want %q", got.OwnerPaused, want)
		}
		list, err := clients.List(asReseller(reza, 1), ListFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 || list.Items[0].OwnerPaused != want {
			t.Errorf("the reseller's list reads %+v, want %q", list.Items, want)
		}
		if shown := subs.statusShown(t.Context(), got); shown != page {
			t.Errorf("the customer's page says %q, want %q", shown, page)
		}
	}

	check("", string(model.StatusActive))

	off := false
	if _, err := admins.Update(t.Context(), reza.ID, operators.AdminInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	check(model.PauseSwitchedOff, string(model.StatusDisabled))

	// Nothing was written to the customer: switched back on, it is as it was.
	on := true
	if _, err := admins.Update(t.Context(), reza.ID, operators.AdminInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	check("", string(model.StatusActive))
}

// Switching off the admin above a reseller takes that reseller's customers
// off too, though the reseller themselves is in good standing: the cascade
// runs down the chain without a row on the reseller or the customer being
// touched.
func TestSwitchingOffAnAdminStopsItsResellersCustomers(t *testing.T) {
	db, clients, admins := resellerDB(t)

	enabled := true
	ali, err := admins.Create(context.Background(), operators.AdminInput{
		Username: "ali", Password: "correct-horse", Role: model.RoleAdmin, Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	reza := newReseller(t, admins, "reza", 0)
	// The reseller sits under the admin, and so does the customer's chain:
	// what phase-5 stamping will set on create, set here directly.
	if err := db.Model(&model.Admin{}).Where("id = ?", reza.ID).Update("parent_admin_id", ali.ID).Error; err != nil {
		t.Fatal(err)
	}
	c, err := clients.Create(asReseller(reza, 1), CreateInput{Name: "theirs", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Client{}).Where("id = ?", c.ID).Update("admin_id", ali.ID).Error; err != nil {
		t.Fatal(err)
	}
	subs := NewSubscriptions(db, nil, nil, quietLog())

	check := func(want, page string) {
		t.Helper()
		got, err := clients.Get(t.Context(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.OwnerPaused != want {
			t.Errorf("the owner reads %q, want %q", got.OwnerPaused, want)
		}
		if shown := subs.statusShown(t.Context(), got); shown != page {
			t.Errorf("the customer's page says %q, want %q", shown, page)
		}
	}

	check("", string(model.StatusActive))

	// The admin is switched off; the reseller is left enabled.
	off := false
	if _, err := admins.Update(t.Context(), ali.ID, operators.AdminInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	check(model.PauseSwitchedOff, string(model.StatusDisabled))

	// And back: nothing on the customer or the reseller was ever written.
	on := true
	if _, err := admins.Update(t.Context(), ali.ID, operators.AdminInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	check("", string(model.StatusActive))
}
