package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// The rules of a sale and the price of new credentials, one rule a test,
// each through the same paths an operator's request takes.


// rules sets an operator's rules of sale, as the owner's form does.
func (p seatPanel) rules(t *testing.T, a *model.Admin, days int, bytes uint64, rekeyFree bool) {
	t.Helper()
	if _, err := p.admins.Update(context.Background(), a.ID, operators.AdminInput{
		SaleMaxDays: &days, SaleMaxBytes: &bytes, RekeyFree: &rekeyFree}); err != nil {
		t.Fatalf("rules for %s: %v", a.Username, err)
	}
}

func refusedBySale(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrNoUsers) {
		t.Fatalf("%s: %v, want a refusal by the rules of sale", what, err)
	}
}

func sell(p seatPanel, ctx context.Context, name string, days int, quota uint64) (*model.Client, error) {
	in := CreateInput{Name: name, InterfaceIDs: []uint{1}, DeviceLimit: 1, QuotaBytes: quota}
	if days > 0 {
		in.ExpiresAt = ptrTime(time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour))
	}
	return p.clients.Create(ctx, in)
}

// A sale is held to the longest term and the most traffic, and to having an
// end and a limit at all; within them it goes through, and a renewal made
// early adds the rule's worth to the end the customer already had.
func TestASaleIsHeldToTheOperatorsRules(t *testing.T) {
	for _, mode := range []string{model.CapacityOpen, model.CapacityConsumable} {
		p := newSeatPanel(t)
		r := p.operator(t, "reza", model.RoleReseller, mode, 100, nil)
		ctx := asReseller(r, 1)
		// Sold before the rules: a plan with no end, left alone by them.
		old, err := p.clients.Create(ctx, CreateInput{Name: "old", InterfaceIDs: []uint{1}, DeviceLimit: 1})
		if err != nil {
			t.Fatal(err)
		}
		p.rules(t, r, 30, 10*gib, false)
		spent := p.acc(t, r).Used

		_, err = sell(p, ctx, "no-end", 0, 5*gib)
		refusedBySale(t, mode+": no end", err)
		_, err = sell(p, ctx, "too-long", 40, 5*gib)
		refusedBySale(t, mode+": 40 days", err)
		_, err = sell(p, ctx, "no-limit", 30, 0)
		refusedBySale(t, mode+": no traffic limit", err)
		_, err = sell(p, ctx, "too-much", 30, 11*gib)
		refusedBySale(t, mode+": 11 GB", err)
		if got := p.acc(t, r).Used; got != spent {
			t.Fatalf("%s: refused sales moved the users from %d to %d", mode, spent, got)
		}

		c, err := sell(p, ctx, "ok", 30, 10*gib)
		if err != nil {
			t.Fatalf("%s: a sale within the rules: %v", mode, err)
		}
		// Renewed early by the rule's worth: from the end they had.
		end := c.ExpiresAt.Add(30 * 24 * time.Hour)
		if _, err := p.clients.Update(ctx, c.ID, UpdateInput{ExpiresAt: OptionalTime{Set: true, Value: &end}}); err != nil {
			t.Fatalf("%s: an early renewal of 30 days: %v", mode, err)
		}
		far := end.Add(45 * 24 * time.Hour)
		_, err = p.clients.Update(ctx, c.ID, UpdateInput{ExpiresAt: OptionalTime{Set: true, Value: &far}})
		refusedBySale(t, mode+": 45 days more", err)
		more := 21 * gib
		_, err = p.clients.Update(ctx, c.ID, UpdateInput{QuotaBytes: &more})
		refusedBySale(t, mode+": 10 GB more than the rule", err)
		d := 40.0
		_, err = p.clients.Adjust(ctx, AdjustInput{IDs: []uint{c.ID}, AddDays: &d})
		refusedBySale(t, mode+": 40 days added in bulk", err)

		// What sells nothing is not held: a rename of the plan with no end.
		n := "renamed"
		if _, err := p.clients.Update(ctx, old.ID, UpdateInput{Name: &n}); err != nil {
			t.Fatalf("%s: renaming a customer sold before the rules: %v", mode, err)
		}
		// The owner is not held to an operator's rules.
		if _, err := p.clients.Update(context.Background(), c.ID, UpdateInput{ExpiresAt: OptionalTime{Set: true, Value: &far}}); err != nil {
			t.Fatalf("%s: the owner extending: %v", mode, err)
		}
		p.balanced(t)
	}
}

// A reseller under an admin is held to the admin's rules as well as their
// own, and an admin cannot give a reseller looser rules than they have.
func TestAResellerIsHeldToTheirAdminsRules(t *testing.T) {
	p := newSeatPanel(t)
	ad := p.operator(t, "adm", model.RoleAdmin, model.CapacityConsumable, 50, nil)
	p.rules(t, ad, 30, 0, false)
	ad, _ = p.admins.Get(context.Background(), ad.ID)

	// Made by the admin with no rules said: the admin's own.
	r := p.operator(t, "rsa", model.RoleReseller, "", 10, ad)
	if got, _ := p.admins.Get(context.Background(), r.ID); got.SaleMaxDays != 30 {
		t.Fatalf("a reseller made by an admin held to 30 days is held to %d", got.SaleMaxDays)
	}
	asAdmin := operators.WithCaller(WithScope(context.Background(), ScopeFor(ad)), ad)
	for name, in := range map[string]operators.AdminInput{
		"longer":     {SaleMaxDays: ptrInt(60)},
		"no rule":    {SaleMaxDays: ptrInt(0)},
		"free links": {RekeyFree: ptrBool(true)},
	} {
		if _, err := p.admins.Update(asAdmin, r.ID, in); err == nil {
			t.Fatalf("an admin gave their reseller %s rules than their own", name)
		}
	}
	if _, err := p.admins.Update(asAdmin, r.ID, operators.AdminInput{SaleMaxDays: ptrInt(20)}); err != nil {
		t.Fatalf("an admin tightening their reseller's rules: %v", err)
	}

	// Loosened behind the form's back, the admin's rule still holds.
	p.db.Model(&model.Admin{}).Where("id = ?", r.ID).Update("sale_max_days", 0)
	_, err := sell(p, asReseller(r, 1), "long", 40, 0)
	refusedBySale(t, "40 days under an admin held to 30", err)
	if _, err := sell(p, asReseller(r, 1), "short", 30, 0); err != nil {
		t.Fatalf("30 days under an admin held to 30: %v", err)
	}
	p.balanced(t)
}

// On consumable users new credentials hand a running plan on, and cost the
// users they were given to: all of them for the customer's own link, one
// for one user's files. Let off, open, ended or done by the owner, nothing.
func TestNewCredentialsCostOnConsumable(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityConsumable, 20, nil)
	ctx := asReseller(r, 1)
	subs := NewSubscriptions(p.db, nil, nil, quietLog())
	c := p.make(t, ctx, "c", 2) // 2
	if _, err := subs.EnsureToken(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		what  string
		do    func() error
		spent int64
	}{
		{"a first link made", func() error { _, err := subs.EnsureToken(ctx, c.ID); return err }, 2},
		{"every file's keys", func() error { _, err := p.clients.RotateKeys(ctx, c.ID, RotateInput{}); return err }, 4},
		{"user 1's keys", func() error { _, err := p.clients.RotateKeys(ctx, c.ID, RotateInput{User: 1}); return err }, 5},
		{"the customer's link", func() error { _, err := subs.RotateToken(ctx, c.ID); return err }, 7},
		{"keys and link at once", func() error {
			_, err := p.clients.RotateKeys(ctx, c.ID, RotateInput{SubToken: true})
			return err
		}, 9},
		{"by the owner", func() error { _, err := subs.RotateToken(context.Background(), c.ID); return err }, 9},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.what, err)
		}
		if got := p.acc(t, r).Used; got != s.spent {
			t.Fatalf("after %s: spent %d, want %d", s.what, got, s.spent)
		}
	}

	// Not enough left: refused, and the link is the one they had.
	p.db.Model(&model.CapacityAccount{}).Where("id = ?", p.acc(t, r).ID).Update("granted", 10)
	p.db.Model(&model.CapacityEntry{}).Where("account_id = ? AND field = 'granted'", p.acc(t, r).ID).Update("delta", 10)
	var was model.Client
	p.db.First(&was, c.ID)
	_, err := subs.RotateToken(ctx, c.ID)
	refusedForUsers(t, "a new link with 1 user left", err)
	var now model.Client
	p.db.First(&now, c.ID)
	if now.SubToken != was.SubToken {
		t.Fatal("a refused new link was kept")
	}

	// Ended: handed on for free; the extension serving them again pays.
	endBy(p, t, c)
	if _, err := subs.RotateToken(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 9 {
		t.Fatalf("a new link for an ended customer spent %d", got-9)
	}

	// Let off by the owner: free.
	p.rules(t, r, 0, 0, true)
	d := p.make(t, ctx, "d", 1) // 10
	if _, err := subs.RotateToken(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 10 {
		t.Fatalf("a reseller let off new links spent %d on one", got-10)
	}
	p.balanced(t)
}

// Seats held at once are not spent, so new credentials cost nothing there.
func TestNewCredentialsAreFreeOnOpen(t *testing.T) {
	p := newSeatPanel(t)
	r := p.operator(t, "reza", model.RoleReseller, model.CapacityOpen, 5, nil)
	ctx := asReseller(r, 1)
	c := p.make(t, ctx, "c", 2)
	if _, err := p.clients.RotateKeys(ctx, c.ID, RotateInput{SubToken: true}); err != nil {
		t.Fatal(err)
	}
	if got := p.acc(t, r).Used; got != 2 {
		t.Fatalf("open: new credentials moved the seats to %d", got)
	}
	p.balanced(t)
}

func ptrInt(n int) *int    { return &n }
func ptrBool(b bool) *bool { return &b }
