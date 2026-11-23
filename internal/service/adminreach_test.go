package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// The admin form sends an admin's reach -- every customer, every tunnel --
// on each save. The owner's is kept, both ways; an admin widening a
// reseller's is refused.
func TestAnAdminsReachIsSavedFromTheForm(t *testing.T) {
	p := newSeatPanel(t)
	on, off := true, false
	ad, err := p.admins.Create(context.Background(), operators.AdminInput{Username: "adm", Password: "correct-horse",
		Role: model.RoleAdmin, Enabled: &on, CanViewAllClients: &on, CanViewAllInterfaces: &on})
	if err != nil {
		t.Fatalf("an admin made with their reach widened: %v", err)
	}
	got, _ := p.admins.Get(context.Background(), ad.ID)
	if !got.CanViewAllClients || !got.CanViewAllInterfaces {
		t.Fatalf("made with every customer and tunnel, kept %v and %v", got.CanViewAllClients, got.CanViewAllInterfaces)
	}
	if _, err := p.admins.Update(context.Background(), ad.ID, operators.AdminInput{CanViewAllClients: &off}); err != nil {
		t.Fatal(err)
	}
	got, _ = p.admins.Get(context.Background(), ad.ID)
	if got.CanViewAllClients || !got.CanViewAllInterfaces {
		t.Fatalf("narrowed to their own customers, kept %v and %v", got.CanViewAllClients, got.CanViewAllInterfaces)
	}

	r := p.operator(t, "rsa", model.RoleReseller, "", 5, got)
	asAdmin := operators.WithCaller(WithScope(context.Background(), ScopeFor(got)), got)
	if _, err := p.admins.Update(asAdmin, r.ID, operators.AdminInput{CanViewAllClients: &on}); err == nil {
		t.Fatal("an admin let their reseller see every customer")
	}
}
