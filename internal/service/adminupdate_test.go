package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// What changing an operator does, rule by rule: the tests the 212-line
// Admins.Update was missing before it is split.

func asOperator(a *model.Admin) context.Context {
	return operators.WithCaller(WithScope(context.Background(), ScopeFor(a)), a)
}

func row(t *testing.T, p seatPanel, id uint) model.Admin {
	t.Helper()
	var a model.Admin
	if err := p.db.First(&a, id).Error; err != nil {
		t.Fatal(err)
	}
	return a
}

func refusedOn(t *testing.T, what string, err error, field string) {
	t.Helper()
	if err == nil || FieldOf(err) != field {
		t.Fatalf("%s: err %v (field %q), want refused on %s", what, err, FieldOf(err), field)
	}
}

func TestAnOperatorsNameIsTheirsAlone(t *testing.T) {
	p := newSeatPanel(t)
	p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	sara := p.operator(t, "sara", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := context.Background()
	_, err := p.admins.Update(ctx, sara.ID, operators.AdminInput{Username: "REZA"})
	refusedOn(t, "a name another operator has, in other letters' case", err, "username")
	if _, err := p.admins.Update(ctx, sara.ID, operators.AdminInput{Username: "sara2"}); err != nil {
		t.Fatal(err)
	}
	if got := row(t, p, sara.ID).Username; got != "sara2" {
		t.Fatalf("username %q, want sara2", got)
	}
}

// A new password, and switching an operator off, end the sessions they
// have open; switching them back on does not need to.
func TestANewPasswordOrSwitchingOffEndsTheSessions(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := context.Background()
	epoch := row(t, p, r.ID).SessionEpoch

	_, err := p.admins.Update(ctx, r.ID, operators.AdminInput{Password: "short"})
	refusedOn(t, "a password under eight characters", err, "password")
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{Password: "a-new-password"}); err != nil {
		t.Fatal(err)
	}
	if got := row(t, p, r.ID).SessionEpoch; got != epoch+1 {
		t.Fatalf("after a new password the sessions are at %d, want %d", got, epoch+1)
	}
	off, on := false, true
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if got := row(t, p, r.ID).SessionEpoch; got != epoch+2 {
		t.Fatalf("after switching off the sessions are at %d, want %d", got, epoch+2)
	}
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if got := row(t, p, r.ID).SessionEpoch; got != epoch+2 {
		t.Fatalf("switching back on moved the sessions to %d", got)
	}
}

// The owner stays the owner, and on; nobody is made owner from here.
func TestTheOwnerStaysTheOwner(t *testing.T) {
	p := newSeatPanel(t)
	owner := model.Admin{Username: "boss", PasswordHash: "x", Role: model.RoleOwner, Enabled: true}
	if err := p.db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := context.Background()
	_, err := p.admins.Update(ctx, owner.ID, operators.AdminInput{Role: model.RoleAdmin})
	refusedOn(t, "the owner's role changed", err, "role")
	off := false
	_, err = p.admins.Update(ctx, owner.ID, operators.AdminInput{Enabled: &off})
	refusedOn(t, "the owner switched off", err, "enabled")
	_, err = p.admins.Update(ctx, r.ID, operators.AdminInput{Role: model.RoleOwner})
	refusedOn(t, "a reseller made owner", err, "role")
	_, err = p.admins.Update(ctx, r.ID, operators.AdminInput{Role: "king"})
	refusedOn(t, "a role the panel has not", err, "role")
}

// A term and an end date are one or the other: setting one clears the
// other, and a term past the longest is refused.
func TestATermAndAnEndAreOneOrTheOther(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := context.Background()
	end := time.Now().UTC().Add(30 * 24 * time.Hour)
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{ExpiresAt: At(end)}); err != nil {
		t.Fatal(err)
	}
	days := 60
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{DurationDays: &days}); err != nil {
		t.Fatal(err)
	}
	if a := row(t, p, r.ID); a.ExpiresAt != nil || a.DurationDays != 60 {
		t.Fatalf("after a term: ends %v, term %d; want no end and 60 days", a.ExpiresAt, a.DurationDays)
	}
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{ExpiresAt: At(end)}); err != nil {
		t.Fatal(err)
	}
	if a := row(t, p, r.ID); a.ExpiresAt == nil || a.DurationDays != 0 {
		t.Fatalf("after an end: ends %v, term %d; want the end and no term", a.ExpiresAt, a.DurationDays)
	}
	long := operators.MaxTermDays + 1
	_, err := p.admins.Update(ctx, r.ID, operators.AdminInput{DurationDays: &long})
	refusedOn(t, "a term past the longest", err, "durationDays")
}

// An admin, over their own resellers, may change what the owner lets them
// and nothing more: not a role, not tokens, not another admin's reseller.
func TestAnAdminChangesTheirOwnResellersWithinBounds(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adam", model.RoleAdmin, model.CapacityOpen, 10, nil)
	other := p.operator(t, "other", model.RoleAdmin, model.CapacityOpen, 10, nil)
	mine := p.operator(t, "mine", model.RoleReseller, model.CapacityOpen, 2, ad)
	theirs := p.operator(t, "theirs", model.RoleReseller, model.CapacityOpen, 2, other)
	ctx := asOperator(ad)

	_, err := p.admins.Update(ctx, mine.ID, operators.AdminInput{Role: model.RoleAdmin})
	refusedOn(t, "an admin promoting their reseller", err, "role")
	tokens := 5
	_, err = p.admins.Update(ctx, mine.ID, operators.AdminInput{TokenLimit: &tokens})
	refusedOn(t, "an admin giving tokens", err, "tokenLimit")
	if _, err := p.admins.Update(ctx, theirs.ID, operators.AdminInput{Note: noteOf("mine now")}); err == nil {
		t.Fatal("an admin changed another admin's reseller")
	}
	if got := row(t, p, theirs.ID).Note; got == "mine now" {
		t.Fatal("another admin's reseller was written to")
	}
	three := 3
	if _, err := p.admins.Update(ctx, mine.ID, operators.AdminInput{ClientLimit: &three}); err != nil {
		t.Fatalf("an admin raising their own reseller within what they have: %v", err)
	}
	if acc := p.acc(t, mine); acc.Granted != 3 {
		t.Fatalf("the reseller has %d users, want 3", acc.Granted)
	}
	p.balanced(t)
}

// An operator's pages change only when a list is sent: none leaves them,
// an empty one takes every page away.
func TestAnOperatorsPagesChangeOnlyWhenSent(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adam", model.RoleAdmin, model.CapacityOpen, 10, nil)
	ctx := context.Background()
	pages := func() int64 {
		var n int64
		p.db.Model(&model.AdminMenuPermission{}).Where("admin_id = ?", ad.ID).Count(&n)
		return n
	}
	if _, err := p.admins.Update(ctx, ad.ID, operators.AdminInput{MenuIDs: []string{model.MenuClients, model.MenuGroups}}); err != nil {
		t.Fatal(err)
	}
	if pages() != 2 {
		t.Fatalf("pages %d, want 2", pages())
	}
	if _, err := p.admins.Update(ctx, ad.ID, operators.AdminInput{Note: noteOf("x")}); err != nil {
		t.Fatal(err)
	}
	if pages() != 2 {
		t.Fatalf("a change with no list took the pages to %d", pages())
	}
	if _, err := p.admins.Update(ctx, ad.ID, operators.AdminInput{MenuIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if pages() != 0 {
		t.Fatalf("an empty list left %d pages", pages())
	}
}

// A reseller's customers are always filed under something.
func TestAResellersLabelCannotBeEmptied(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	empty := "  "
	_, err := p.admins.Update(context.Background(), r.ID, operators.AdminInput{GroupName: &empty})
	refusedOn(t, "an empty label", err, "groupName")
}

func noteOf(s string) *string { return &s }

// A change that does not send a note leaves it; one that sends "" clears
// it. Before, the note was a plain string, and a script moving a reseller's
// users -- sending only that -- wiped the note the owner had written.
func TestANoteIsLeftUnlessOneIsSent(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := context.Background()
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{Note: noteOf("pays on the 5th")}); err != nil {
		t.Fatal(err)
	}
	var in operators.AdminInput
	if err := json.Unmarshal([]byte(`{"clientLimit": 7}`), &in); err != nil {
		t.Fatal(err)
	}
	if _, err := p.admins.Update(ctx, r.ID, in); err != nil {
		t.Fatal(err)
	}
	if got := row(t, p, r.ID).Note; got != "pays on the 5th" {
		t.Fatalf("a change without a note left it as %q", got)
	}
	var clear operators.AdminInput
	if err := json.Unmarshal([]byte(`{"note": ""}`), &clear); err != nil {
		t.Fatal(err)
	}
	if _, err := p.admins.Update(ctx, r.ID, clear); err != nil {
		t.Fatal(err)
	}
	if got := row(t, p, r.ID).Note; got != "" {
		t.Fatalf("a note sent empty left %q", got)
	}
}
