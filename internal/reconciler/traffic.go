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
	// Up is what they sent, Down is what they received. Zero when the enforcer
	// could not tell the two apart, in which case only the total is recorded
	// and the split is left alone rather than being guessed at.
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
func (w *trafficWriter) absorbLocked(u trafficUpdate) {
	if u.Key != "" && u.Bytes > 0 {
		if id, ok := clientIDFromKey(u.Key); ok {
			w.usage[id] = w.usage[id].add(usageDelta{Bytes: u.Bytes, Up: u.Up, Down: u.Down})
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
	byOwner := map[uint]uint64{}

	err := db.Transaction(func(tx *gorm.DB) error {
		for id, d := range usage {
			if owner := owners[id]; owner != 0 {
				byOwner[owner] += d.Bytes
			}
			// Incremented in SQL rather than read-modify-written in Go, so a
			// concurrent reset from the UI cannot be silently overwritten by a
			// stale total.
			//
			// All three move in one statement: an allowance that was spent but
			// whose direction was not recorded would leave the groups page and
			// the customer's own client app disagreeing with the quota bar on
			// the same screen.
			cols := map[string]any{
				"used_bytes": gorm.Expr("used_bytes + ?", d.Bytes),
			}
			if d.Up > 0 {
				cols["up_bytes"] = gorm.Expr("up_bytes + ?", d.Up)
			}
			if d.Down > 0 {
				cols["down_bytes"] = gorm.Expr("down_bytes + ?", d.Down)
			}
			if err := tx.Model(&model.Client{}).
				Where("id = ?", id).
				UpdateColumns(cols).Error; err != nil {
				return err
			}

			// RX is what the customer received and TX what they sent, which is
			// the way round their own client app reports it. An enforcer that
			// cannot split the two puts the lot in RX, as this did for every
			// sample before there were two counters to read.
			rx, tx2 := d.Down, d.Up
			if rx == 0 && tx2 == 0 {
				rx = d.Bytes
			}

			// The time series is bucketed on write, so the table grows with
			// time rather than with the number of samples taken.
			var sample model.TrafficSample
			err := tx.Where("client_id = ? AND bucket_ts = ? AND granularity = ?",
				id, bucket, model.GranularityFine).First(&sample).Error
			switch {
			case err == nil:
				if err := tx.Model(&sample).UpdateColumns(map[string]any{
					"rx": gorm.Expr("rx + ?", rx),
					"tx": gorm.Expr("tx + ?", tx2),
				}).Error; err != nil {
					return err
				}
			default:
				if err := tx.Create(&model.TrafficSample{
					ClientID:    id,
					BucketTS:    bucket,
					Granularity: model.GranularityFine,
					RX:          rx,
					TX:          tx2,
				}).Error; err != nil {
					return err
				}
			}
		}

		for accountID, d := range devices {
			if err := tx.Model(&model.Account{}).Where("id = ?", accountID).
				UpdateColumns(map[string]any{
					"up_bytes":   gorm.Expr("up_bytes + ?", d.Up),
					"down_bytes": gorm.Expr("down_bytes + ?", d.Down),
				}).Error; err != nil {
				return err
			}
		}

		for accountID, u := range liveness {
			fields := map[string]any{"last_seen_at": u.At}
			if !u.Handshake.IsZero() {
				fields["last_handshake"] = u.Handshake
			}
			if u.Endpoint != "" {
				fields["last_endpoint"] = u.Endpoint
			}
			if err := tx.Model(&model.Account{}).
				Where("id = ?", accountID).Updates(fields).Error; err != nil {
				return err
			}
		}

		// The resellers' running totals move with the same deltas, in the
		// same transaction, so the two can never differ by more than a tick
		// that failed for both.
		for ownerID, delta := range byOwner {
			if delta == 0 {
				continue
			}
			if err := tx.Model(&model.Admin{}).Where("id = ?", ownerID).
				UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", delta)).Error; err != nil {
				return err
			}
		}
		return nil
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
