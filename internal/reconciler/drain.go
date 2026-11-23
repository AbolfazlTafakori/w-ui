package reconciler

import (
	"context"
	"sort"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// Which usage counters a tick drains.
//
// Draining every counter in the table each tick costs the kernel more than
// in proportion to the table: measured on the test server (arm64), 46 ms for
// 10,000 counters and 977 ms for 50,000 -- twenty-one times the cost for
// five times the counters -- while a counter asked for by name costs 27 µs
// whatever the table holds. Most files are idle at any moment, and their
// counters hold nothing.
//
// So a tick drains the files that moved, and the drivers already say which:
// each reports its files' bytes every tick, read for the online indicator
// anyway. To that are added the files that were reporting and stopped (a
// session that ended right after sending), and a slice of every other file,
// turning, so each is drained at least every sweepTicks ticks whatever its
// driver said. A tick whose drivers could not all be read drains everything,
// as before.
//
// Nothing is lost by not draining a counter: it goes on counting in the
// kernel until it is drained. What a missed file costs is lateness, at most
// sweepTicks ticks, in stored usage -- never in enforcement, which is the
// kernel's quota, to the byte.

// fullDrainByNameAbove is enforce.FullDrainByNameAbove, here so a test can
// reach it without sixty thousand rows.
var fullDrainByNameAbove = enforce.FullDrainByNameAbove

// sweepTicks is how many ticks the turning slice takes to cover every file:
// a minute at the two-second tick.
const sweepTicks = 30

// drainState is what drainPlan keeps between ticks.
type drainState struct {
	heard map[uint]uint64 // account -> bytes its driver last reported
	turn  int             // which slice of the files this tick's sweep takes
	// startDrained is that the start drained every counter
	// (recoverKernelUsage) and no tick has known the files since: the first
	// tick, run before the files are read, need not drain them all again.
	startDrained bool
}

// drainAllAtStart drains every counter of this server's files, once, at
// start. The table has not been written by this run yet, so its names come
// from the database; a server with more than enforce.FullDrainByNameAbove
// counters is read by name, which a dump of it would take tens of seconds
// over (27.8 s for 200,000 counters, measured).
func (r *Reconciler) drainAllAtStart(ctx context.Context) ([]enforce.Usage, error) {
	r.drain.startDrained = true
	var files []struct {
		ID       uint
		ClientID uint
	}
	if err := r.db.WithContext(ctx).Model(&model.Account{}).Select("id, client_id").
		Where("node_id = ?", r.localNodeID).Scan(&files).Error; err != nil {
		r.log.Warn("could not list this server's files to drain them by name at start; draining the table whole",
			"error", err)
		return r.enforcer.DrainCounters(ctx)
	}
	if 2*len(files) <= fullDrainByNameAbove {
		return r.enforcer.DrainCounters(ctx)
	}
	refs := make([]enforce.FileRef, 0, len(files))
	for _, f := range files {
		refs = append(refs, enforce.FileRef{Key: enforce.Key(f.ClientID), Account: f.ID})
	}
	return r.enforcer.DrainFiles(ctx, refs)
}

// drainPlan says which files this tick drains, or full for all of them.
// stats is every driver's report this tick; failed, that some could not be
// read.
func (r *Reconciler) drainPlan(stats []backend.Stat, failed bool) (files []enforce.FileRef, full bool) {
	local := r.conc.localFiles()
	if len(local) == 0 && !failed && r.drain.startDrained {
		// The first tick after start, before the files are known: the start
		// drained every counter a moment ago, and what came since waits in
		// them for the next tick.
		return nil, false
	}
	r.drain.startDrained = false
	if failed || len(local) == 0 {
		r.drain.heard = nil // started over on the next full read of the drivers
		return nil, true
	}
	if r.drain.heard == nil {
		r.drain.heard = make(map[uint]uint64, len(stats))
	}

	want := make(map[uint]bool)
	now := make(map[uint]uint64, len(stats))
	for _, s := range stats {
		now[s.AccountID] += s.RX + s.TX
	}
	for acc, n := range now {
		if was, ok := r.drain.heard[acc]; !ok || was != n {
			want[acc] = true // moved, or newly reporting
		}
	}
	for acc := range r.drain.heard {
		if _, still := now[acc]; !still {
			want[acc] = true // stopped reporting: drained once more
		}
	}
	r.drain.heard = now

	// The turning slice: the local files in a fixed order, a thirtieth of
	// them each tick.
	ids := make([]uint, 0, len(local))
	for acc := range local {
		ids = append(ids, acc)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	r.drain.turn = (r.drain.turn + 1) % sweepTicks
	from, to := len(ids)*r.drain.turn/sweepTicks, len(ids)*(r.drain.turn+1)/sweepTicks
	for _, acc := range ids[from:to] {
		want[acc] = true
	}

	files = make([]enforce.FileRef, 0, len(want))
	for acc := range want {
		client, ok := local[acc]
		if !ok {
			continue // not served here: no counters in this table
		}
		files = append(files, enforce.FileRef{Key: enforce.Key(client), Account: acc})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Account < files[j].Account })
	return files, false
}
