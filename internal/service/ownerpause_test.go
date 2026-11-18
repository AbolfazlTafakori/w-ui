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
	check(model.PauseSwitchedOff, StatusPaused)
	if got, _ := clients.Get(t.Context(), c.ID); got.PausedBy != string(model.RoleReseller) {
		t.Errorf("paused by %q, want the reseller", got.PausedBy)
	}

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
	check(model.PauseSwitchedOff, StatusPaused)
	if got, _ := clients.Get(t.Context(), c.ID); got.PausedBy != string(model.RoleAdmin) {
		t.Errorf("paused by %q, want the admin", got.PausedBy)
	}

	// And back: nothing on the customer or the reseller was ever written.
	on := true
	if _, err := admins.Update(t.Context(), ali.ID, operators.AdminInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	check("", string(model.StatusActive))
}

// A customer paused from above is given nothing to connect with: the link
// answers with no configuration, a single device's file is not found, and
// the page lists none. Lifted, all of it comes back.
func TestAPausedCustomersLinkCarriesNoConfiguration(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	reseller := model.Admin{Username: "reza", Role: model.RoleReseller, PasswordHash: "x"}
	if err := db.Create(&reseller).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(c).Update("owner_id", reseller.ID).Error; err != nil {
		t.Fatal(err)
	}
	subs := newSubs(db, &fakeDriver{body: "conf"})
	tok, err := subs.EnsureToken(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var device model.Account
	if err := db.Where("client_id = ?", c.ID).First(&device).Error; err != nil {
		t.Fatal(err)
	}
	served := func() (int, error, int) {
		t.Helper()
		b, err := subs.Serve(t.Context(), tok, "conf")
		if err != nil {
			t.Fatal(err)
		}
		_, derr := subs.DeviceConfig(t.Context(), tok, device.ID, 0)
		page, err := subs.PageFor(t.Context(), tok, "https://x/sub/"+tok)
		if err != nil {
			t.Fatal(err)
		}
		return len(b.Body), derr, len(page.Devices)
	}
	setEnabled := func(on bool) {
		t.Helper()
		if err := db.Model(&reseller).Update("enabled", on).Error; err != nil {
			t.Fatal(err)
		}
	}
	if body, derr, n := served(); body == 0 || derr != nil || n == 0 {
		t.Fatalf("before the pause: body %d, device %v, page %d", body, derr, n)
	}
	setEnabled(false)
	if body, derr, n := served(); body != 0 || derr == nil || n != 0 {
		t.Errorf("paused: body %d, device %v, page %d", body, derr, n)
	}
	setEnabled(true)
	if body, derr, n := served(); body == 0 || derr != nil || n == 0 {
		t.Errorf("back on: body %d, device %v, page %d", body, derr, n)
	}
}
