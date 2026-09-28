package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// Pausing a reseller and resuming them is a switch over their customers, not
// a rewrite of them.
//
// A customer is served when both switches allow it: their own, and their
// reseller's standing. So pausing the reseller takes every one of their
// customers off at once, and resuming brings back exactly the ones whose own
// switch was on -- a customer switched off before the pause, or during it,
// stays off. Nothing about the customers is written by any of this, which is
// the only way the second half can be true: had the pause switched each of
// them off, resuming could no longer tell who had been off before.
func TestPausingAResellerIsASwitchOverTheirCustomers(t *testing.T) {
	r, db, enf, drv := newRig(t)
	ctx := context.Background()

	reseller := model.Admin{Username: "reza", PasswordHash: "x", Role: model.RoleReseller, Enabled: true}
	if err := db.Create(&reseller).Error; err != nil {
		t.Fatal(err)
	}
	theirs := func(name, ip string, status model.ClientStatus) uint {
		id := seed(t, db, model.Client{Name: name, Status: status}, ip)
		if err := db.Model(&model.Client{}).Where("id = ?", id).Update("owner_id", reseller.ID).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	live := theirs("live", "10.66.0.2", model.StatusActive)
	offBefore := theirs("off-before", "10.66.0.3", model.StatusDisabled)
	offDuring := theirs("off-during", "10.66.0.4", model.StatusActive)
	owners := seed(t, db, model.Client{Name: "owners-own"}, "10.66.0.5")

	served := func() map[uint]bool {
		out := map[uint]bool{}
		for _, id := range []uint{live, offBefore, offDuring, owners} {
			rule, ok := enf.ruleFor(enforce.Key(id))
			if !ok {
				t.Fatalf("customer %d has no kernel rule", id)
			}
			out[id] = !rule.Blocked
		}
		// The driver is the other half: a blocked customer must also have no
		// peer, or a stale handshake could carry them past the kernel rule's
		// replacement on the next restart.
		if got, want := len(drv.Accounts()), countTrue(out); got != want {
			t.Fatalf("the driver holds %d peers, the kernel serves %d customers", got, want)
		}
		return out
	}
	statuses := func() map[uint]model.ClientStatus {
		var rows []model.Client
		db.Find(&rows)
		out := map[uint]model.ClientStatus{}
		for _, c := range rows {
			out[c.ID] = c.Status
		}
		return out
	}
	expect := func(when string, got map[uint]bool, want map[uint]bool) {
		t.Helper()
		names := map[uint]string{live: "live", offBefore: "off-before", offDuring: "off-during", owners: "owners-own"}
		for id, w := range want {
			if got[id] != w {
				t.Errorf("%s: %s served=%v, want %v", when, names[id], got[id], w)
			}
		}
	}

	// Before: the reseller is in good standing, so each customer's own
	// switch decides.
	r.Tick(ctx)
	before := statuses()
	expect("before the pause", served(), map[uint]bool{live: true, offBefore: false, offDuring: true, owners: true})

	// Paused: every one of the reseller's customers is off in the same tick,
	// the owner's own customer untouched -- and no customer row changed.
	db.Model(&model.Admin{}).Where("id = ?", reseller.ID).Update("enabled", false)
	r.Tick(ctx)
	expect("while paused", served(), map[uint]bool{live: false, offBefore: false, offDuring: false, owners: true})
	for id, s := range statuses() {
		if s != before[id] {
			t.Errorf("pausing the reseller wrote customer %d's status: %s -> %s", id, before[id], s)
		}
	}

	// The owner switches one of them off while the reseller is paused. That
	// is the customer's own switch, and it has to be remembered.
	db.Model(&model.Client{}).Where("id = ?", offDuring).Update("status", model.StatusDisabled)
	r.Tick(ctx)

	// Resumed: back exactly as each customer's own switch says.
	db.Model(&model.Admin{}).Where("id = ?", reseller.ID).Update("enabled", true)
	r.Tick(ctx)
	expect("after resuming", served(), map[uint]bool{live: true, offBefore: false, offDuring: false, owners: true})
}

// A term that ends and an allowance that runs out pause the reseller the same
// way, and lifting them brings the customers back.
func TestAResellersTermAndAllowancePauseTheirCustomersToo(t *testing.T) {
	r, db, enf, _ := newRig(t)
	ctx := context.Background()

	past := time.Now().UTC().Add(-time.Hour)
	reseller := model.Admin{Username: "sara", PasswordHash: "x", Role: model.RoleReseller, Enabled: true, ExpiresAt: &past}
	if err := db.Create(&reseller).Error; err != nil {
		t.Fatal(err)
	}
	id := seed(t, db, model.Client{Name: "theirs"}, "10.66.0.2")
	db.Model(&model.Client{}).Where("id = ?", id).Update("owner_id", reseller.ID)

	blocked := func() bool {
		rule, ok := enf.ruleFor(enforce.Key(id))
		return !ok || rule.Blocked
	}

	r.Tick(ctx)
	if !blocked() {
		t.Fatal("a reseller whose term has ended still serves their customers")
	}

	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	db.Model(&model.Admin{}).Where("id = ?", reseller.ID).Updates(map[string]any{
		"expires_at": future, "quota_bytes": 1000, "used_bytes": 1000,
	})
	r.Tick(ctx)
	if !blocked() {
		t.Fatal("a reseller whose traffic is used up still serves their customers")
	}

	db.Model(&model.Admin{}).Where("id = ?", reseller.ID).Update("used_bytes", 0)
	r.Tick(ctx)
	if blocked() {
		t.Fatal("renewing the reseller did not bring their customer back")
	}
}

func countTrue(m map[uint]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}
