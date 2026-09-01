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

// queueSize is how many updates may wait to be written.
//
// SQLite serialises writers, so ten thousand clients each producing a write
// every two seconds would spend their time fighting for the same lock. The
// updates are funnelled through one goroutine and flushed in batches instead —
// the shape 3x-ui arrived at for the same reason.
const queueSize = 512

// flushInterval is how often the buffer is drained to the database.
//
// It is deliberately slower than the collection tick: usage is accumulated in
// memory between flushes, so a busy server writes a handful of rows a second
// rather than thousands.
const flushInterval = 5 * time.Second

// trafficUpdate is one thing learned during a tick.
type trafficUpdate struct {
	// Set for a usage update.
	Key   string
	Bytes uint64

	// Set for a liveness update.
	AccountID uint
	Handshake time.Time
	Endpoint  string

	At time.Time
}

type trafficWriter struct {
	db  *gorm.DB
	log *slog.Logger
	ch  chan trafficUpdate

	mu       sync.Mutex
	usage    map[uint]uint64 // client id -> bytes since last flush
	liveness map[uint]trafficUpdate
	dropped  uint64
}

func newTrafficWriter(db *gorm.DB, log *slog.Logger) *trafficWriter {
	return &trafficWriter{
		db:       db,
		log:      log,
		ch:       make(chan trafficUpdate, queueSize),
		usage:    map[uint]uint64{},
		liveness: map[uint]trafficUpdate{},
	}
}

// submit queues an update. It never blocks: a full queue drops the update and
// counts it, because stalling the reconciler would stop collection for every
// client to protect the bookkeeping of one.
func (w *trafficWriter) submit(u trafficUpdate) {
	select {
	case w.ch <- u:
	default:
		w.mu.Lock()
		w.dropped++
		w.mu.Unlock()
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
			case u := <-w.ch:
				w.absorb(u)
			case <-t.C:
				w.flush(ctx)
			}
		}
	}()
}

// absorb folds an update into the in-memory totals.
func (w *trafficWriter) absorb(u trafficUpdate) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if u.Key != "" && u.Bytes > 0 {
		if id, ok := clientIDFromKey(u.Key); ok {
			w.usage[id] += u.Bytes
		}
	}
	if u.AccountID != 0 {
		w.liveness[u.AccountID] = u
	}
}

// flush writes what has accumulated.
//
// Anything still sitting in the channel is absorbed first: on shutdown the last
// flush must not leave queued bytes unbilled, and it makes flush correct to
// call directly without the loop running.
func (w *trafficWriter) flush(ctx context.Context) {
	for {
		select {
		case u := <-w.ch:
			w.absorb(u)
			continue
		default:
		}
		break
	}

	w.mu.Lock()
	usage := w.usage
	liveness := w.liveness
	dropped := w.dropped
	w.usage = map[uint]uint64{}
	w.liveness = map[uint]trafficUpdate{}
	w.dropped = 0
	w.mu.Unlock()

	if dropped > 0 {
		w.log.Warn("traffic updates dropped; the write queue is saturated",
			"count", dropped)
	}
	if len(usage) == 0 && len(liveness) == 0 {
		return
	}

	db := w.db.WithContext(ctx)
	now := time.Now().UTC()
	bucket := now.Truncate(5 * time.Minute)

	err := db.Transaction(func(tx *gorm.DB) error {
		for id, bytes := range usage {
			// Incremented in SQL rather than read-modify-written in Go, so a
			// concurrent reset from the UI cannot be silently overwritten by a
			// stale total.
			if err := tx.Model(&model.Client{}).
				Where("id = ?", id).
				UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", bytes)).
				Error; err != nil {
				return err
			}

			// The time series is bucketed on write, so the table grows with
			// time rather than with the number of samples taken.
			var sample model.TrafficSample
			err := tx.Where("client_id = ? AND bucket_ts = ? AND granularity = ?",
				id, bucket, model.GranularityFine).First(&sample).Error
			switch {
			case err == nil:
				if err := tx.Model(&sample).
					UpdateColumn("rx", gorm.Expr("rx + ?", bytes)).Error; err != nil {
					return err
				}
			default:
				if err := tx.Create(&model.TrafficSample{
					ClientID:    id,
					BucketTS:    bucket,
					Granularity: model.GranularityFine,
					RX:          bytes,
				}).Error; err != nil {
					return err
				}
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
		return nil
	})
	if err != nil {
		w.log.Error("flush traffic", "error", err,
			"usageRows", len(usage), "livenessRows", len(liveness))
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
