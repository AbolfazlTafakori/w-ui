package service

import (
	"strings"
	"testing"
	"time"
)

// A reseller who has been switched off cannot store up value for when they
// are switched back on again.
//
// Their customers are already off, so an edit changes nothing being served
// today. What it would change is tomorrow: a reseller could push every
// customer a year out and top each one to half a terabyte while barred, pay
// for a single day, and walk away with a year of service. Reading is left
// alone, because they are still being asked to pay for that book.
func TestABarredResellerCannotChangeWhatTheyHold(t *testing.T) {
	_, clients, admins := resellerDB(t)
	reza := newReseller(t, admins, "reza", 0)
	live := asReseller(reza, 1)

	held, err := clients.Create(live, CreateInput{
		Name: "kept", InterfaceIDs: []uint{1}, QuotaBytes: 1 << 30, DurationDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	second, err := clients.Create(live, CreateInput{Name: "also-kept", InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}

	// Every reason an operator is barred, since they arrive by different
	// routes -- switched off by the owner, a term that ran out, an allowance
	// spent -- and all three have to stop the same things.
	for _, why := range []string{
		"your account has been switched off",
		"your account's term has ended",
		"your data allowance is used up",
	} {
		t.Run(why, func(t *testing.T) {
			sc := ScopeFor(reza)
			sc.ClientLimit = reza.ClientLimit
			sc.Interfaces = map[uint]bool{1: true}
			sc.Barred = why
			ctx := WithScope(t.Context(), sc)

			year := time.Now().UTC().Add(365 * 24 * time.Hour)
			big := uint64(500) << 30
			refuses := map[string]error{}
			_, refuses["sell another customer"] = clients.Create(ctx,
				CreateInput{Name: "new", InterfaceIDs: []uint{1}})
			_, refuses["push the expiry out"] = clients.Update(ctx, held.ID,
				UpdateInput{ExpiresAt: OptionalTime{Set: true, Value: &year}})
			_, refuses["raise the allowance"] = clients.Update(ctx, held.ID,
				UpdateInput{QuotaBytes: &big})
			_, refuses["reset the counters"] = clients.ResetTraffic(ctx, held.ID)
			_, refuses["adjust in bulk"] = clients.Adjust(ctx,
				AdjustInput{IDs: []uint{held.ID, second.ID}, QuotaBytes: &big})
			_, refuses["reset everything"] = clients.ResetAllTraffic(ctx)
			_, refuses["add a device"] = clients.AddDevice(ctx, held.ID, "extra")
			refuses["delete a customer"] = clients.Delete(ctx, second.ID)

			for what, err := range refuses {
				if err == nil {
					t.Errorf("a barred reseller could still %s", what)
					continue
				}
				if !strings.Contains(err.Error(), why) {
					t.Errorf("%s was refused without saying why: %v", what, err)
				}
			}

			// Reading is theirs. They are being asked to pay for this book, so
			// they have to be able to see it.
			if _, err := clients.Get(ctx, held.ID); err != nil {
				t.Errorf("a barred reseller cannot read their own customer: %v", err)
			}
			page, err := clients.List(ctx, ListFilter{})
			if err != nil {
				t.Errorf("a barred reseller cannot list their customers: %v", err)
			} else if len(page.Items) != 2 {
				t.Errorf("a barred reseller sees %d of their 2 customers", len(page.Items))
			}
		})
	}

	// And nothing was changed by any of it.
	after, err := clients.Get(live, held.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.QuotaBytes != 1<<30 {
		t.Fatalf("the allowance moved to %d while the reseller was barred", after.QuotaBytes)
	}

	// Switched back on, the same operator may do all of it again.
	if _, err := clients.Update(live, held.ID, UpdateInput{Name: strptr("renamed")}); err != nil {
		t.Fatalf("an operator in good standing was refused: %v", err)
	}
}

// strptr is the pointer form the update input takes for a field that may be
// left alone.
func strptr(s string) *string { return &s }
