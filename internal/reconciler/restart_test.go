package reconciler

import (
	"context"
	"errors"
	"strings"
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

// A customer near their allowance has what they used written in the same
// tick, so a reboot right after cannot lose bytes that would have stopped
// them; one far from theirs waits for the flush, as before.
func TestUsageNearTheAllowanceIsWrittenAtOnce(t *testing.T) {
	r, db, enf, _ := newRig(t)
	near := seed(t, db, model.Client{Name: "near", QuotaBytes: 1 << 30, UsedBytes: (1 << 30) - (10 << 20)}, "10.66.0.2")
	far := seed(t, db, model.Client{Name: "far", QuotaBytes: 100 << 30, UsedBytes: 1 << 30}, "10.66.0.3")
	ctx := context.Background()
	r.Tick(ctx) // learns who is near
	enf.drain = []enforce.Usage{{Key: enforce.Key(near), Bytes: 5000}, {Key: enforce.Key(far), Bytes: 7000}}
	r.Tick(ctx)
	var n, f model.Client
	db.First(&n, near)
	db.First(&f, far)
	if n.UsedBytes != (1<<30)-(10<<20)+5000 {
		t.Errorf("near: stored %d after the tick, want the 5000 written at once", n.UsedBytes-((1<<30)-(10<<20)))
	}
	if f.UsedBytes != 1<<30 {
		t.Errorf("far: stored %d more after the tick, want it held for the flush", f.UsedBytes-(1<<30))
	}
	r.writer.flush(ctx)
	db.First(&f, far)
	if f.UsedBytes != (1<<30)+7000 {
		t.Errorf("far after the flush: %d more, want 7000", f.UsedBytes-(1<<30))
	}
}

func TestWhoIsNearTheirAllowance(t *testing.T) {
	cases := []struct {
		quota, used uint64
		want        bool
	}{
		{0, 5, false},                           // no allowance
		{100 << 30, 1 << 30, false},             // far
		{100 << 30, 96 << 30, true},             // under five percent left
		{1 << 30, (1 << 30) - (63 << 20), true}, // a small plan: under 64MB left
		{1 << 30, 100 << 20, false},
		{1 << 30, 2 << 30, true}, // past it
	}
	for _, c := range cases {
		if got := nearAllowance(c.quota, c.used); got != c.want {
			t.Errorf("quota %d used %d: near %v, want %v", c.quota, c.used, got, c.want)
		}
	}
}

// A failure the loop meets is kept, under its code, while it lasts, and
// taken away when the thing works again.
func TestAFailureIsShownWhileItLasts(t *testing.T) {
	r, db, enf, _ := newRig(t)
	seed(t, db, model.Client{Name: "Ali", QuotaBytes: 10_000}, "10.66.0.2")
	ctx := context.Background()
	enf.applyErr = errors.New("nft: Could not process rule: No such file or directory")
	r.Tick(ctx)
	codes := map[string]string{}
	for _, p := range r.Problems() {
		codes[p.Code] = p.Detail
	}
	if !strings.Contains(codes[ProblemEnforceFailed], "Could not process rule") || codes[ProblemLoopFailed] == "" {
		t.Fatalf("while the firewall table cannot be written: %v", codes)
	}
	enf.applyErr = nil
	r.Tick(ctx)
	if p := r.Problems(); len(p) != 0 {
		t.Fatalf("after it works again: %+v", p)
	}
}
