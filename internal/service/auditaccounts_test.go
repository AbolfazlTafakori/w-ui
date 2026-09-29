package service

import (
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The account warnings are about the owner's account. They used to be raised
// once per account, the same unnamed line each time: an owner with two
// resellers saw "two-factor authentication is off" three times, and a reseller
// who had not signed in yet was reported as "the generated password has never
// been changed", a password the installer never generated.
func TestAccountWarningsAreTheOwnersAndSaidOnce(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	for _, ad := range []model.Admin{
		{Username: "admin", Role: model.RoleOwner, Enabled: true, LastLoginAt: &now},
		{Username: "reza", Role: model.RoleReseller, Enabled: true},
		{Username: "sara", Role: model.RoleReseller, Enabled: true},
		{Username: "ops", Role: model.RoleAdmin, Enabled: true, LastLoginAt: &now},
		{Username: "guarded", Role: model.RoleAdmin, Enabled: true, TOTPSecret: "JBSWY3DPEHPK3PXP"},
	} {
		ad := ad
		if err := db.Create(&ad).Error; err != nil {
			t.Fatal(err)
		}
	}

	got := (&Audit{db: db}).checkAdmins(t.Context())

	count := map[string]int{}
	for _, w := range got {
		count[w.ID]++
	}
	for id, n := range count {
		if n > 1 {
			t.Errorf("%s was raised %d times", id, n)
		}
	}
	if count["no-totp"] != 1 {
		t.Errorf("the owner's missing second factor was raised %d times, want once", count["no-totp"])
	}
	if count["admin-username"] != 1 {
		t.Error("the owner still called admin was not raised")
	}
	if count["never-signed-in"] != 0 {
		t.Error("resellers who have not signed in were reported as an unchanged installer password")
	}
	var admins *Warning
	for i := range got {
		if got[i].ID == "admins-no-totp" {
			admins = &got[i]
		}
	}
	if admins == nil {
		t.Fatal("a panel administrator without a second factor was not named")
	}
	if !strings.Contains(admins.Detail, "ops") || strings.Contains(admins.Detail, "guarded") ||
		strings.Contains(admins.Detail, "reza") {
		t.Errorf("the administrators named are wrong: %q", admins.Detail)
	}
}

// An owner with a second factor, a username of their own and a password they
// have set is told nothing, however many resellers they have.
func TestAGuardedOwnerIsToldNothing(t *testing.T) {
	db := testDB(t)
	now := time.Now()
	for _, ad := range []model.Admin{
		{Username: "abolfazl", Role: model.RoleOwner, Enabled: true, LastLoginAt: &now, TOTPSecret: "JBSWY3DPEHPK3PXP"},
		{Username: "reza", Role: model.RoleReseller, Enabled: true},
	} {
		ad := ad
		if err := db.Create(&ad).Error; err != nil {
			t.Fatal(err)
		}
	}
	if got := (&Audit{db: db}).checkAdmins(t.Context()); len(got) != 0 {
		t.Errorf("a guarded owner was warned: %+v", got)
	}
}
