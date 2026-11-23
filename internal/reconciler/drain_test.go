package reconciler

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// fileOf is a customer's one file.
func fileOf(t *testing.T, r *Reconciler, client uint) uint {
	t.Helper()
	var acc model.Account
	if err := r.db.Where("client_id = ?", client).First(&acc).Error; err != nil {
		t.Fatal(err)
	}
	return acc.ID
}

func used(t *testing.T, r *Reconciler, client uint) uint64 {
	t.Helper()
	r.writer.flush(context.Background())
	var c model.Client
	r.db.First(&c, client)
	return c.UsedBytes
}

// A tick drains the files that moved, and leaves the idle ones' counters to
// the turning sweep -- which reaches each within sweepTicks ticks, so what a
// counter held is billed late at most, never lost.
func TestATickDrainsTheFilesThatMovedAndTheSweepTheRest(t *testing.T) {
	r, db, enf, drv := newRig(t)
	ctx := context.Background()
	busy := seed(t, db, model.Client{Name: "busy"}, "10.66.0.2")
	quiet := seed(t, db, model.Client{Name: "quiet"}, "10.66.0.3")
	r.Tick(ctx)
	r.Tick(ctx) // the files are known, and their drivers heard once
	busyFile, quietFile := fileOf(t, r, busy), fileOf(t, r, quiet)

	// The quiet file's counter holds bytes its driver did not show moving:
	// the case the sweep is for.
	enf.add(enforce.Usage{Key: enforce.Key(quiet), Bytes: 700, Up: 100, Down: 600,
		Files: []enforce.FileUsage{{Account: quietFile, Up: 100, Down: 600}}})
	enf.add(enforce.Usage{Key: enforce.Key(busy), Bytes: 900, Up: 400, Down: 500,
		Files: []enforce.FileUsage{{Account: busyFile, Up: 400, Down: 500}}})
	moved(drv, busyFile)
	r.Tick(ctx)
	if got := used(t, r, busy); got != 900 {
		t.Fatalf("the busy file's 900 bytes: stored %d after its tick", got)
	}
	if got := used(t, r, quiet); got != 0 {
		t.Fatalf("the quiet file was drained on the busy file's tick (%d): every counter is still read each tick", got)
	}

	for i := 0; i < sweepTicks && used(t, r, quiet) == 0; i++ {
		r.Tick(ctx)
	}
	if got := used(t, r, quiet); got != 700 {
		t.Fatalf("the quiet file's 700 bytes: stored %d after a whole sweep", got)
	}
	if len(enf.drain) != 0 {
		t.Fatalf("bytes left in the counters after a whole sweep: %+v", enf.drain)
	}
}

// A file whose driver stopped reporting it -- a session that ended right
// after sending -- is drained on the tick it stopped, not a minute later.
func TestAFileThatStoppedReportingIsDrainedAtOnce(t *testing.T) {
	r, db, enf, drv := newRig(t)
	ctx := context.Background()
	id := seed(t, db, model.Client{Name: "gone"}, "10.66.0.2")
	r.Tick(ctx)
	r.Tick(ctx)
	file := fileOf(t, r, id)
	other := seed(t, db, model.Client{Name: "other"}, "10.66.0.3") // keeps the driver reporting something
	r.Tick(ctx)
	_ = other

	drv.SetStat(backend.Stat{AccountID: file, RX: 5})
	r.Tick(ctx) // heard with traffic
	enf.add(enforce.Usage{Key: enforce.Key(id), Bytes: 300, Up: 100, Down: 200,
		Files: []enforce.FileUsage{{Account: file, Up: 100, Down: 200}}})
	drv.Forget(file) // the session ends: its driver no longer reports it
	r.Tick(ctx)
	if got := used(t, r, id); got != 300 {
		t.Fatalf("a file that stopped reporting: stored %d, want its last 300 bytes at once", got)
	}
}

// A tick whose drivers could not all be read drains every counter, as
// before: what moved is not known, so nothing is left to chance.
func TestATickWithADriverUnreadDrainsEverything(t *testing.T) {
	r, db, _, drv := newRig(t)
	ctx := context.Background()
	seed(t, db, model.Client{Name: "a"}, "10.66.0.2")
	r.Tick(ctx)
	r.Tick(ctx)

	files, full := r.drainPlan(nil, true)
	if !full || files != nil {
		t.Fatalf("a failed read: files %v, full %v; want a full drain", files, full)
	}
	_ = drv
}

// At start a server with more counters than a dump can take in good time
// drains them by name, from the database's list of its files; a smaller
// one is dumped, as before.
func TestTheStartDrainsALargeTableByName(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold int
		byName    bool
	}{{"small", 1_000, false}, {"large", 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r, db, enf, _ := newRig(t)
			was := fullDrainByNameAbove
			fullDrainByNameAbove = tc.threshold
			t.Cleanup(func() { fullDrainByNameAbove = was })
			a := seed(t, db, model.Client{Name: "a"}, "10.66.0.2")
			b := seed(t, db, model.Client{Name: "b"}, "10.66.0.3")
			fa, fb := fileOf(t, r, a), fileOf(t, r, b)
			enf.add(enforce.Usage{Key: enforce.Key(a), Bytes: 10, Up: 4, Down: 6, Files: []enforce.FileUsage{{Account: fa, Up: 4, Down: 6}}})
			enf.add(enforce.Usage{Key: enforce.Key(b), Bytes: 20, Up: 5, Down: 15, Files: []enforce.FileUsage{{Account: fb, Up: 5, Down: 15}}})

			got, err := r.drainAllAtStart(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var total uint64
			for _, u := range got {
				total += u.Bytes
			}
			if total != 30 || len(enf.drain) != 0 {
				t.Fatalf("drained %d bytes, %d left; want all 30 drained", total, len(enf.drain))
			}
			if byName := enf.fullDrains == 0; byName != tc.byName {
				t.Fatalf("by name: %v, want %v", byName, tc.byName)
			}
		})
	}
}

// The first tick after start, run before the files are known, drains
// nothing -- the start just drained everything -- and what came in since
// is drained on a later tick, not lost.
func TestTheFirstTickAfterStartLeavesTheCountersToTheNext(t *testing.T) {
	r, db, enf, drv := newRig(t)
	ctx := context.Background()
	id := seed(t, db, model.Client{Name: "a"}, "10.66.0.2")
	if _, err := r.drainAllAtStart(ctx); err != nil {
		t.Fatal(err)
	}
	full := enf.fullDrains
	files, isFull := r.drainPlan(nil, false)
	if isFull || len(files) != 0 || enf.fullDrains != full {
		t.Fatalf("the first tick after start: files %v, full %v; want nothing drained", files, isFull)
	}
	r.Tick(ctx)
	file := fileOf(t, r, id)
	enf.add(enforce.Usage{Key: enforce.Key(id), Bytes: 500, Up: 200, Down: 300,
		Files: []enforce.FileUsage{{Account: file, Up: 200, Down: 300}}})
	moved(drv, file)
	for i := 0; i < 3 && used(t, r, id) == 0; i++ {
		r.Tick(ctx)
	}
	if got := used(t, r, id); got != 500 {
		t.Fatalf("what came in after the start: %d stored, want 500", got)
	}
}
