// Package reconciler keeps the kernel matching the database.
//
// Nothing else in the panel calls the data plane. Services write what an
// operator wants to the database; this loop is the only thing that carries that
// down to WireGuard, OpenVPN and nftables. Everything here is written as
// reconciliation rather than as commands: on every tick it reads the desired
// state in full, reads what the kernel actually holds, and applies the
// difference.
//
// That shape buys three properties an imperative "add peer / remove peer"
// design cannot have. It is idempotent, so a tick that runs twice is harmless.
// It is self-healing, so a rebooted host rebuilds itself from the database
// rather than needing a resync. And it cannot drift, because no code path
// changes the kernel without going through the same comparison.
package reconciler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
)

// Options configures the loop.
type Options struct {
	DB       *gorm.DB
	Enforcer enforce.Enforcer
	Backends map[uint]backend.Backend // by interface id
	Interval time.Duration
	Log      *slog.Logger
}

// Stats is what the last tick did, for the settings page and the logs.
type Stats struct {
	Ticks        uint64    `json:"ticks"`
	LastRun      time.Time `json:"lastRun"`
	LastDuration string    `json:"lastDuration"`
	Clients      int       `json:"clients"`
	Accounts     int       `json:"accounts"`
	BytesCounted uint64    `json:"bytesCounted"`
	Exhausted    int64     `json:"exhausted"`
	Expired      int64     `json:"expired"`
	LastError    string    `json:"lastError,omitempty"`
}

// Reconciler is the loop.
type Reconciler struct {
	db       *gorm.DB
	enforcer enforce.Enforcer
	backends map[uint]backend.Backend
	interval time.Duration
	log      *slog.Logger
	writer   *trafficWriter

	mu    sync.RWMutex
	stats Stats
}

// New builds a reconciler.
func New(o Options) *Reconciler {
	interval := o.Interval
	if interval < time.Second {
		interval = 2 * time.Second
	}
	return &Reconciler{
		db:       o.DB,
		enforcer: o.Enforcer,
		backends: o.Backends,
		interval: interval,
		log:      o.Log,
		writer:   newTrafficWriter(o.DB, o.Log),
	}
}

// Start runs the loop until ctx is done.
func (r *Reconciler) Start(ctx context.Context) {
	r.writer.start(ctx)

	go func() {
		t := time.NewTicker(r.interval)
		defer t.Stop()

		// An immediate first pass so a restarted panel rebuilds kernel state
		// straight away instead of leaving customers unmetered for a tick.
		r.Tick(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.Tick(ctx)
			}
		}
	}()
}

// Stats returns what the last tick did.
func (r *Reconciler) Stats() Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stats
}

// Tick runs one full reconciliation. Exported so tests can drive it directly
// rather than waiting on wall-clock time.
func (r *Reconciler) Tick(ctx context.Context) {
	started := time.Now()
	err := r.reconcile(ctx)

	r.mu.Lock()
	r.stats.Ticks++
	r.stats.LastRun = started
	r.stats.LastDuration = time.Since(started).Round(time.Millisecond).String()
	if err != nil {
		r.stats.LastError = err.Error()
	} else {
		r.stats.LastError = ""
	}
	r.mu.Unlock()

	if err != nil {
		r.log.Error("reconcile failed", "error", err)
	}
}

func (r *Reconciler) reconcile(ctx context.Context) error {
	// 1. Collect first. Usage decides who is still allowed to be online, so
	//    counting before evaluating means a client who ran out during this
	//    interval is cut off in the same tick rather than the next one.
	counted, err := r.collect(ctx)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}

	// 2. Evaluate limits in one statement rather than looping in Go. With ten
	//    thousand clients this is the difference between a query and a stampede
	//    of round trips.
	exhausted, expired, err := r.evaluate(ctx)
	if err != nil {
		return fmt.Errorf("evaluate: %w", err)
	}

	// 3. Push the resulting desired state down.
	clients, accounts, err := r.apply(ctx)
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	r.mu.Lock()
	r.stats.BytesCounted += counted
	r.stats.Clients = clients
	r.stats.Accounts = accounts
	r.stats.Exhausted = exhausted
	r.stats.Expired = expired
	r.mu.Unlock()
	return nil
}

// collect drains the kernel counters and folds them into stored usage.
//
// Draining is a read-and-zero in one operation, so there is no delta arithmetic
// and no reset detection: whatever comes back is exactly what flowed since the
// last tick. That is what makes the accounting correct across an interface
// restart, which is where counter-polling designs lose bytes.
func (r *Reconciler) collect(ctx context.Context) (uint64, error) {
	drained, err := r.enforcer.DrainCounters(ctx)
	if err != nil {
		return 0, err
	}

	var total uint64
	now := time.Now().UTC()
	for _, d := range drained {
		if d.Bytes == 0 {
			continue // idle clients are not worth a write
		}
		total += d.Bytes
		r.writer.submit(trafficUpdate{Key: d.Key, Bytes: d.Bytes, At: now})
	}

	// Handshakes and endpoints come from the drivers, not from nftables, and
	// are what the online indicator and the sharing detector read.
	for ifaceID, b := range r.backends {
		stats, err := b.Stats(ctx)
		if err != nil {
			r.log.Warn("driver stats unavailable", "interface", ifaceID, "error", err)
			continue
		}
		for _, s := range stats {
			if s.LastHandshake.IsZero() {
				continue
			}
			r.writer.submit(trafficUpdate{
				AccountID: s.AccountID,
				Handshake: s.LastHandshake,
				Endpoint:  s.Endpoint,
				At:        now,
			})
		}
	}
	return total, nil
}

// evaluate moves clients out of active when they run out of allowance or time.
func (r *Reconciler) evaluate(ctx context.Context) (exhausted, expired int64, err error) {
	now := time.Now().UTC()
	db := r.db.WithContext(ctx)

	res := db.Model(&model.Client{}).
		Where("status = ? AND quota_bytes > 0 AND used_bytes >= quota_bytes", model.StatusActive).
		Update("status", model.StatusExhausted)
	if res.Error != nil {
		return 0, 0, res.Error
	}
	if res.RowsAffected > 0 {
		r.log.Info("clients cut off for reaching their allowance", "count", res.RowsAffected)
	}

	// Passed as plain strings: a slice of a named string type is not expanded
	// into an IN list, and the sweep silently matches nothing.
	res2 := db.Model(&model.Client{}).
		Where("status IN (?) AND expires_at IS NOT NULL AND expires_at <= ?",
			[]string{string(model.StatusActive), string(model.StatusExhausted)}, now).
		Update("status", model.StatusExpired)
	if res2.Error != nil {
		return 0, 0, res2.Error
	}
	if res2.RowsAffected > 0 {
		r.log.Info("clients expired", "count", res2.RowsAffected)
	}

	return res.RowsAffected, res2.RowsAffected, nil
}

// desired is the state the database says should exist.
type desired struct {
	rules    []enforce.Rule
	perIface map[uint][]backend.DesiredAccount
	clients  int
	accounts int
}

// apply reads the desired state and pushes it to the enforcer and the drivers.
func (r *Reconciler) apply(ctx context.Context) (int, int, error) {
	d, err := r.readDesired(ctx)
	if err != nil {
		return 0, 0, err
	}

	// The enforcer goes first. If a client just ran out, the kernel should stop
	// their traffic before the driver gets round to removing the peer, not
	// after.
	if err := r.enforcer.Apply(ctx, d.rules); err != nil {
		return 0, 0, fmt.Errorf("enforcer: %w", err)
	}

	for ifaceID, b := range r.backends {
		want := d.perIface[ifaceID]
		if want == nil {
			want = []backend.DesiredAccount{}
		}
		rep, err := b.Sync(ctx, want)
		if err != nil {
			// One broken interface must not stop the others from being
			// reconciled, so this is logged and the loop continues.
			r.log.Error("driver sync failed", "interface", ifaceID, "error", err)
			continue
		}
		if rep.Changed() {
			r.log.Info("driver state changed",
				"interface", ifaceID,
				"added", rep.Added, "removed", rep.Removed, "updated", rep.Updated)
		}
	}
	return d.clients, d.accounts, nil
}

// readDesired builds the target state from the database in two queries.
func (r *Reconciler) readDesired(ctx context.Context) (*desired, error) {
	db := r.db.WithContext(ctx)

	var clients []model.Client
	if err := db.Find(&clients).Error; err != nil {
		return nil, fmt.Errorf("load clients: %w", err)
	}

	var accounts []model.Account
	if err := db.Find(&accounts).Error; err != nil {
		return nil, fmt.Errorf("load accounts: %w", err)
	}

	byClient := make(map[uint][]model.Account, len(clients))
	for _, a := range accounts {
		byClient[a.ClientID] = append(byClient[a.ClientID], a)
	}

	d := &desired{
		rules:    make([]enforce.Rule, 0, len(clients)),
		perIface: map[uint][]backend.DesiredAccount{},
		clients:  len(clients),
		accounts: len(accounts),
	}

	for _, c := range clients {
		accs := byClient[c.ID]
		if len(accs) == 0 {
			continue
		}

		addrs := make([]netip.Addr, 0, len(accs))
		for _, a := range accs {
			if ip, err := netip.ParseAddr(a.IP); err == nil {
				addrs = append(addrs, ip)
			}
		}

		serviceable := c.Status.Serviceable()

		// Every client gets a rule, including the cut-off ones. A rule that
		// drops is how the kernel refuses their traffic in the instant between
		// the cut-off and the driver removing their peer.
		d.rules = append(d.rules, enforce.Rule{
			Key:            enforce.Key(c.ID),
			Addrs:          addrs,
			QuotaBytes:     c.QuotaBytes,
			UsedBytes:      c.UsedBytes,
			RateBitsPerSec: c.RateBitsPerSec,
			Blocked:        !serviceable,
		})

		// Accounts of a non-serviceable client are simply left out of the
		// desired set, and Sync removes whatever it finds that is not listed.
		if !serviceable {
			continue
		}
		for _, a := range accs {
			if !a.Enabled {
				continue
			}
			ip, err := netip.ParseAddr(a.IP)
			if err != nil {
				r.log.Warn("account has an unparseable address",
					"account", a.ID, "ip", a.IP)
				continue
			}
			d.perIface[a.InterfaceID] = append(d.perIface[a.InterfaceID], backend.DesiredAccount{
				ID:           a.ID,
				DeviceName:   a.DeviceName,
				IP:           ip,
				PublicKey:    a.PublicKey,
				PresharedKey: a.PresharedKey,
				Username:     a.Username,
				Secret:       a.Secret,
			})
		}
	}
	return d, nil
}

var errClosed = errors.New("reconciler: closed")
