package reconciler

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// A panel killed mid-flight: the kernel's quota counted 900 bytes for the
// customer, the database has 500, and the counters still hold 100 the panel
// never drained -- 300 were drained and lost with the writer's buffer. At
// start all of it is charged, once, and the first apply seeds the quota
// from 900, not 500: nothing billed is lost, and nothing is given free.
func TestAPanelKilledMidFlightLosesNoUsage(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 10_000, UsedBytes: 500}, "10.66.0.2")
	key := enforce.Key(id)
	enf.drain = []enforce.Usage{{Key: key, Bytes: 100, Up: 40, Down: 60}}
	enf.quotas = []enforce.Usage{{Key: key, Bytes: 900}}

	r.recoverKernelUsage(context.Background())
	var c model.Client
	db.First(&c, id)
	if c.UsedBytes != 900 {
		t.Fatalf("stored usage %d after the restart, want the kernel's 900", c.UsedBytes)
	}
	r.Tick(context.Background())
	rule, ok := enf.ruleFor(key)
	if !ok || rule.UsedBytes != 900 {
		t.Fatalf("the quota is seeded from %d, want 900", rule.UsedBytes)
	}
}

// A clean restart: everything was written, and the counters hold only what
// moved since. Charged once, not twice.
func TestACleanRestartChargesNothingTwice(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 10_000, UsedBytes: 500}, "10.66.0.2")
	key := enforce.Key(id)
	enf.drain = []enforce.Usage{{Key: key, Bytes: 100}}
	enf.quotas = []enforce.Usage{{Key: key, Bytes: 600}}
	r.recoverKernelUsage(context.Background())
	var c model.Client
	db.First(&c, id)
	if c.UsedBytes != 600 {
		t.Fatalf("stored usage %d, want 600", c.UsedBytes)
	}
}

// A reboot: no table, no counters, no quotas. Nothing to recover, nothing
// changed. A quota trailing usage on other servers (its tally under what
// is stored) adds nothing either.
func TestARebootOrATrailingQuotaChargesNothing(t *testing.T) {
	r, db, enf, _ := newRig(t)
	id := seed(t, db, model.Client{Name: "Ali", QuotaBytes: 10_000, UsedBytes: 500}, "10.66.0.2")
	r.recoverKernelUsage(context.Background())
	enf.quotas = []enforce.Usage{{Key: enforce.Key(id), Bytes: 300}}
	r.recoverKernelUsage(context.Background())
	var c model.Client
	db.First(&c, id)
	if c.UsedBytes != 500 {
		t.Fatalf("stored usage %d, want it left at 500", c.UsedBytes)
	}
}
