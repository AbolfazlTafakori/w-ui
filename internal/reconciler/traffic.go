package reconciler

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// flushInterval is how often the buffer is drained to the database.
//
// SQLite serialises writers, so ten thousand clients each producing a write
// every two seconds would spend their time fighting for the same lock. Usage
// is accumulated in memory between flushes instead, so a busy server writes a
// handful of batches a minute rather than thousands of rows a second.
const flushInterval = 5 * time.Second

// trafficUpdate is one thing learned during a tick.
type trafficUpdate struct {
	// Set for a usage update.
	Key   string
	Bytes uint64

	// The same bytes split by direction, from the customer's point of view:
	// Up is what they sent, Down is what they received. What a report does
	// not split -- zero for both, or less than Bytes -- is written as
	// received (model.Directions), so the two always add up to the total.
	Up   uint64
	Down uint64

	// Set for a liveness update.
	AccountID uint
	Handshake time.Time
	Endpoint  string

	// Set for a file update: what one file carried, by direction, for its
	// row in the customer's per-user table and its tunnel's total.
	DevUp   uint64
	DevDown uint64

	At time.Time
}

type trafficWriter struct {
	db  *gorm.DB
	log *slog.Logger

	mu       sync.Mutex
	usage    map[uint]usageDelta // client id -> bytes since last flush
	devices  map[uint]usageDelta // account id -> bytes since last flush
	liveness map[uint]trafficUpdate
	// failing is whether the last flush failed, so the failure is said
	// once and its recovery once, not on every attempt in between.
	failing bool
	// failures is how many flushes have failed since the panel started.
	failures uint64
}

// usageDelta is what one client or file accumulated between two flushes.
type usageDelta struct {
	Bytes uint64
	Up    uint64
	Down  uint64
}

func (d usageDelta) add(o usageDelta) usageDelta {
	return usageDelta{Bytes: d.Bytes + o.Bytes, Up: d.Up + o.Up, Down: d.Down + o.Down}
}

func newTrafficWriter(db *gorm.DB, log *slog.Logger) *trafficWriter {
	return &trafficWriter{
		db:       db,
		log:      log,
		usage:    map[uint]usageDelta{},
		devices:  map[uint]usageDelta{},
		liveness: map[uint]trafficUpdate{},
	}
}

// submit folds updates into what the next flush writes.
//
// Nothing is ever dropped. Usage is what customers are billed and what the
// per-user table is made of, and an update thrown away under load -- as a
// bounded queue does while the database is busy with the last flush -- is
// traffic that happened and was never charged. Folding is a few map writes
// under a lock, so it does not hold up the collection tick either.
//
// Everything passed in one call is folded under one lock: a customer's usage
// and what each of their files carried, handed over together, land in the
// same flush, so no flush writes one without the other.
func (w *trafficWriter) submit(updates ...trafficUpdate) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, u := range updates {
		w.absorbLocked(u)
	}
}

func (w *trafficWriter) start(ctx context.Context) {
	go func() {
		t := time.NewTicker(flushInterval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				w.flush(context.WithoutCancel(ctx))
				return
			case <-t.C:
				w.flush(ctx)
			}
		}
	}()
}

// absorbLocked folds one update into the in-memory totals. w.mu is held.
//
// A customer's usage is split by direction so the two add up to the bytes
// spent (model.Directions). Their app shows upload plus download, and a
// share whose direction was not told -- a node of an older release reports
// the total alone -- went into the total and not the two: the app showed
// less than the panel charged.
func (w *trafficWriter) absorbLocked(u trafficUpdate) {
	if u.Key != "" && u.Bytes > 0 {
		if id, ok := clientIDFromKey(u.Key); ok {
			up, down := model.Directions(u.Bytes, u.Up, u.Down)
			w.usage[id] = w.usage[id].add(usageDelta{Bytes: u.Bytes, Up: up, Down: down})
		}
	}
	if u.AccountID == 0 {
		return
	}
	if u.DevUp > 0 || u.DevDown > 0 {
		w.devices[u.AccountID] = w.devices[u.AccountID].add(usageDelta{Up: u.DevUp, Down: u.DevDown})
		return
	}
	w.liveness[u.AccountID] = u
}

// restore puts back what a failed flush took, so the next one writes it.
//
// Usage is added to whatever has arrived since; a liveness reading is put
// back only where no newer one has come in.
func (w *trafficWriter) restore(usage, devices map[uint]usageDelta, liveness map[uint]trafficUpdate) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, d := range usage {
		w.usage[id] = w.usage[id].add(d)
	}
	for id, d := range devices {
		w.devices[id] = w.devices[id].add(d)
	}
	for id, u := range liveness {
		if _, newer := w.liveness[id]; !newer {
			w.liveness[id] = u
		}
	}
}

// flush writes what has accumulated, in one transaction.
//
// What it takes is swapped out under the lock, so submissions carry on into
// fresh maps while the database works, and a failed transaction hands it all
// back to be written next time. Safe to call directly without the loop
// running, which is how shutdown writes its last bytes.
func (w *trafficWriter) flush(ctx context.Context) {
	w.mu.Lock()
	usage := w.usage
	devices := w.devices
	liveness := w.liveness
	w.usage = map[uint]usageDelta{}
	w.devices = map[uint]usageDelta{}
	w.liveness = map[uint]trafficUpdate{}
	w.mu.Unlock()

	if len(usage) == 0 && len(liveness) == 0 && len(devices) == 0 {
		return
	}

	db := w.db.WithContext(ctx)
	now := time.Now().UTC()
	bucket := now.Truncate(5 * time.Minute)

	// Which reseller each of these customers belongs to, read once for the
	// batch. A reseller's allowance is what their customers have carried
	// between them, and it is kept as a running total rather than summed on
	// demand: the page that shows it and the sweep that enforces it both
	// read it every pass, and a panel with a hundred thousand customers
	// cannot aggregate that each time.
	owners := map[uint]uint{}
	if len(usage) > 0 {
		ids := make([]uint, 0, len(usage))
		for id := range usage {
			ids = append(ids, id)
		}
		var rows []struct {
			ID      uint
			OwnerID uint
		}
		if err := db.Model(&model.Client{}).Select("id, owner_id").
			Where("id IN ? AND owner_id <> 0", ids).Scan(&rows).Error; err != nil {
			w.log.Warn("could not read which operator these customers belong to", "error", err)
		}
		for _, row := range rows {
			owners[row.ID] = row.OwnerID
		}
	}
	// Written a few hundred rows to a statement (traffic_batch.go).
	err := db.Transaction(func(tx *gorm.DB) error {
		return writeFlush(tx, usage, devices, liveness, owners, bucket)
	})
	if err != nil {
		// The transaction wrote nothing, so everything it carried goes back
		// to be written by the next flush rather than being lost: a locked
		// or briefly unreachable database delays the bill, it does not
		// erase it.
		w.restore(usage, devices, liveness)
		w.mu.Lock()
		first := !w.failing
		w.failing = true
		w.failures++
		w.mu.Unlock()
		if first {
			w.log.Error("could not write usage; keeping it to write on the next try", "error", err,
				"clients", len(usage), "files", len(devices))
		}
		return
	}
	w.mu.Lock()
	recovered := w.failing
	w.failing = false
	w.mu.Unlock()
	if recovered {
		w.log.Info("usage is being written again; nothing held back was lost")
	}
}

// clientIDFromKey turns an enforcement key back into a client id.
func clientIDFromKey(key string) (uint, bool) {
	if !strings.HasPrefix(key, "c") {
		return 0, false
	}
	n, err := strconv.ParseUint(key[1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(n), true
}

// keyFromClientID is the inverse, kept next to its pair so the two cannot drift.
func keyFromClientID(id uint) string { return enforce.Key(id) }

// health is whether usage is being written, how many writes have failed,
// and how many customers' usage is waiting for the next one.
func (w *trafficWriter) health() (failing bool, failures uint64, held int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failing, w.failures, len(w.usage)
}
