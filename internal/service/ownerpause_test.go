package service

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
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
	if _, err := admins.Update(t.Context(), reza.ID, AdminInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	check(model.PauseSwitchedOff, string(model.StatusDisabled))

	// Nothing was written to the customer: switched back on, it is as it was.
	on := true
	if _, err := admins.Update(t.Context(), reza.ID, AdminInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	check("", string(model.StatusActive))
}
