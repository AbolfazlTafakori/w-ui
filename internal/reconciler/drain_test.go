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
