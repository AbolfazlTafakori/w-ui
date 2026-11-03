package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// A customer carries the admin at the top of its ownership chain in admin_id:
// a reseller's customer the admin above that reseller, an admin's own
// customer the admin themselves, and the owner's own customer nobody.
func TestCustomerCarriesTheAdminAtTheTopOfItsChain(t *testing.T) {
	_, clients, admins := resellerDB(t)

	enabled := true
	ali, err := admins.Create(context.Background(), operators.AdminInput{
		Username: "ali", Password: "correct-horse", Role: model.RoleAdmin,
		Enabled: &enabled, InterfaceIDs: []uint{1},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A reseller under ali: their customer's admin_id is ali's.
	reza := newReseller(t, admins, "reza", 0)
	reza.ParentAdminID = ali.ID
	c, err := clients.Create(asReseller(reza, 1), CreateInput{Name: "reza's", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	if c.OwnerID != reza.ID || c.AdminID != ali.ID {
		t.Errorf("reseller's customer: owner %d admin %d, want %d and %d", c.OwnerID, c.AdminID, reza.ID, ali.ID)
	}

	// A customer ali creates directly: ali is both owner and admin.
	d, err := clients.Create(asReseller(ali, 1), CreateInput{Name: "ali's", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	if d.OwnerID != ali.ID || d.AdminID != ali.ID {
		t.Errorf("admin's own customer: owner %d admin %d, want %d for both", d.OwnerID, d.AdminID, ali.ID)
	}

	// The owner's own customer has neither.
	e, err := clients.Create(context.Background(), CreateInput{Name: "owner's", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}
	if e.OwnerID != 0 || e.AdminID != 0 {
		t.Errorf("owner's own customer: owner %d admin %d, want 0 for both", e.OwnerID, e.AdminID)
	}
}
