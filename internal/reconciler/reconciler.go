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
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/enforce"
	"github.com/abolfazl/w-ui/internal/notify"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/shaper"
)

// Options configures the loop.
// PolicySource hands the reconciler the routing policy for this tick.
//
// A function rather than the service itself, so the reconciler does not have to
// import the service package that imports it back.
type PolicySource func(context.Context) (routing.Policy, error)

// HopSource lists the upstream tunnels that should be up.
type HopSource func(context.Context) ([]routing.HopSpec, error)

type Options struct {
	DB       *gorm.DB
	Enforcer enforce.Enforcer
	Shaper   shaper.Shaper

	// Router applies the policy. Nil disables routing entirely, which is what
	// a panel with no outbounds configured wants.
	Router   *routing.Applier
	Hops     *routing.HopManager
	Policy   PolicySource
	HopsOf   HopSource
	Notifier *notify.Notifier
	// Pool holds the open drivers. The reconciler brings it in line with the
	// database on every tick, so an interface created from the panel starts
	// working within one tick instead of at the next restart.
	Pool     *backend.Pool
	Interval time.Duration
	Log      *slog.Logger

	// LocalNodeID is the node this panel runs on.
	//
	// Tunnels belonging to another node are skipped entirely: their kernel is
	// on another machine, and programming their peers here would build an
	// interface nobody dials while leaving the one customers actually use
	// untouched. The panel that runs there is sent the same desired state and
	// applies it with its own reconciler.
	LocalNodeID uint
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
	shaper   shaper.Shaper
	router   *routing.Applier
	hops     *routing.HopManager
	policyOf PolicySource
	hopsOf   HopSource
	notifier *notify.Notifier
	pool     *backend.Pool
	interval time.Duration
	log      *slog.Logger
	writer   *trafficWriter
	// localNodeID is the node this panel programs. Everything belonging to
	// another node is another panel's to apply.
	localNodeID uint

	// conc holds customers to the connections their plan allows.
	conc *concurrency
	// meter turns each tunnel's cumulative counters into what each file
	// carried since the last tick.
	meter *deviceMeter
	// OnHold carries a hold decided here to the node serving the device.
	// Set by the panel's node syncer; nil when there are no nodes.
	OnHold func(nodeID uint, hold service.NodeHold)

	mu    sync.RWMutex
	stats Stats
	// lastRouteErr is the routing failure already reported, for the same reason
	// as lastShapeErr below.
	lastRouteErr string
	// lastShapeErr is the shaping failure already reported. A server whose
	// kernel cannot shape would otherwise log the same line every couple of
	// seconds for as long as it runs, which buries everything else.
	lastShapeErr string
	// lastPrune is when stale connection addresses were last swept.
	lastPrune time.Time
}

// New builds a reconciler.
func New(o Options) *Reconciler {
	interval := o.Interval
	if interval < time.Second {
		interval = 2 * time.Second
	}
	return &Reconciler{
		conc:     newConcurrency(),
		meter:    newDeviceMeter(),
		db:       o.DB,
		enforcer: o.Enforcer,
		shaper:   o.Shaper,
		router:   o.Router,
		hops:     o.Hops,
		policyOf: o.Policy,
		hopsOf:   o.HopsOf,
		notifier: o.Notifier,
		pool:     o.Pool,
		interval: interval,
		log:      o.Log,
		writer:   newTrafficWriter(o.DB, o.Log),
		// Zero would silently match nothing and leave a panel programming an
		// empty kernel, so it falls back to the first node, which is this one on
		// every install that has never added a second.
		localNodeID: max64(o.LocalNodeID, 1),
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

// AddNodeUsage folds traffic a remote node counted into the customer's total.
//
// The same total the local kernel feeds, through the same batching writer, so a
// customer's allowance is one number spent across every server they reach. That
// is the whole point of the shared plan: a node counting its own share and
// enforcing against it would cut somebody off at a third of what they bought.
//
// Deliberately takes plain numbers rather than the node package's type. This
// package is the only thing that talks to the data plane and imports nothing
// that imports it back; a shared struct here would be the first strand of a
// cycle.
// AddDeviceUsage folds what a node saw one file carry into that file's
// own counters, for the tunnel's total on the interfaces page.
func (r *Reconciler) AddDeviceUsage(accountID uint, up, down uint64) {
	if accountID == 0 || up+down == 0 {
		return
	}
	r.writer.submit(trafficUpdate{AccountID: accountID, DevUp: up, DevDown: down, At: time.Now().UTC()})
}

func (r *Reconciler) AddNodeUsage(clientID uint, total, up, down uint64) {
	if clientID == 0 || total == 0 {
		return
	}
	r.writer.submit(trafficUpdate{
		Key:   keyFromClientID(clientID),
		Bytes: total,
		Up:    up,
		Down:  down,
		At:    time.Now().UTC(),
	})
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

	// What this machine itself carried, against its own host's allowance.
	// Nodes are accounted for by the syncer as their usage arrives; this is the
	// same thing for the panel's own server, which is a server like any other.
	if err := service.RecordNodeTraffic(ctx, r.db, r.localNodeID, counted, time.Now().UTC()); err != nil {
		r.log.Warn("could not record what this server carried", "error", err)
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
		r.writer.submit(trafficUpdate{
			Key: d.Key, Bytes: d.Bytes, Up: d.Up, Down: d.Down, At: now,
		})
	}

	// Handshakes and endpoints come from the drivers, not from nftables, and
	// are what the online indicator and the sharing detector read.
	seen := map[uint]string{}
	metered := map[uint]bool{}
	allRead := true
	for ifaceID, b := range r.pool.All() {
		stats, err := b.Stats(ctx)
		if err != nil {
			r.log.Warn("driver stats unavailable", "interface", ifaceID, "error", err)
			allRead = false
			continue
		}
		r.conc.observe(stats, now)
		for _, s := range stats {
			metered[s.AccountID] = true
			if up, down, ok := r.meter.step(s.AccountID, s.RX, s.TX); ok {
				r.writer.submit(trafficUpdate{AccountID: s.AccountID, DevUp: up, DevDown: down, At: now})
			}
			if s.LastHandshake.IsZero() {
				continue
			}
			r.writer.submit(trafficUpdate{
				AccountID: s.AccountID,
				Handshake: s.LastHandshake,
				Endpoint:  s.Endpoint,
				At:        now,
			})
			// Only a live session marks its address as in use: a peer that
			// stopped hours ago still reports the address it last had, and
			// refreshing that would count a switched-off phone as connected.
			if s.Endpoint != "" && now.Sub(s.LastHandshake) < liveWindow() {
				seen[s.AccountID] = s.Endpoint
			}
		}
	}

	if allRead {
		r.meter.keep(metered)
	}

	// Written straight through rather than queued behind the traffic writer:
	// this is an upsert of at most one row per connected account, and it must
	// not be dropped when that buffer is full, because a missed address is a
	// missed sharing case rather than a few bytes of usage.
	if err := recordEndpoints(ctx, r.db, seen, r.conc.clients(), now); err != nil {
		r.log.Warn("could not record connection addresses", "error", err)
	}
	r.maybePrune(ctx, now)

	return total, nil
}

// maybePrune sweeps stale addresses on a slow schedule of its own.
func (r *Reconciler) maybePrune(ctx context.Context, now time.Time) {
	r.mu.Lock()
	due := now.Sub(r.lastPrune) >= pruneEvery
	if due {
		r.lastPrune = now
	}
	r.mu.Unlock()

	if !due {
		return
	}
	if err := pruneEndpoints(ctx, r.db, now); err != nil {
		r.log.Warn("could not prune connection addresses", "error", err)
	}
}

// evaluate moves clients out of active when they run out of allowance or time.
func (r *Reconciler) evaluate(ctx context.Context) (exhausted, expired int64, err error) {
	now := time.Now().UTC()
	db := r.db.WithContext(ctx)

	// Plans that start on first use are turned into real dates before expiry
	// is evaluated, so a client cannot be expired in the same tick that starts
	// their clock.
	if _, err := r.activate(ctx, now); err != nil {
		r.log.Warn("could not start pending plans", "error", err)
	}

	// Who is about to be cut off is read before the sweep. Afterwards the rows
	// no longer match the condition, so there would be no way to say whose
	// service just stopped — and "someone was cut off" is not a useful message.
	exhaustedNames := r.namesMatching(ctx,
		"status = ? AND quota_bytes > 0 AND used_bytes >= quota_bytes", model.StatusActive)

	res := db.Model(&model.Client{}).
		Where("status = ? AND quota_bytes > 0 AND used_bytes >= quota_bytes", model.StatusActive).
		Update("status", model.StatusExhausted)
	if res.Error != nil {
		return 0, 0, res.Error
	}
	if res.RowsAffected > 0 {
		r.log.Info("clients cut off for reaching their allowance", "count", res.RowsAffected, "clients", strings.Join(exhaustedNames, ", "))
		if service.SubscriptionsChanged != nil {
			service.SubscriptionsChanged()
		}
		r.announce(notify.KindExhausted, "Allowance used up", exhaustedNames,
			"stopped: their data allowance is gone")
	}

	// Passed as plain strings: a slice of a named string type is not expanded
	// into an IN list, and the sweep silently matches nothing.
	expiredNames := r.namesMatching(ctx,
		"status IN (?) AND expires_at IS NOT NULL AND expires_at <= ?",
		[]string{string(model.StatusActive), string(model.StatusExhausted)}, now)

	res2 := db.Model(&model.Client{}).
		Where("status IN (?) AND expires_at IS NOT NULL AND expires_at <= ?",
			[]string{string(model.StatusActive), string(model.StatusExhausted)}, now).
		Update("status", model.StatusExpired)
	if res2.Error != nil {
		return 0, 0, res2.Error
	}
	if res2.RowsAffected > 0 {
		r.log.Info("clients expired", "count", res2.RowsAffected, "clients", strings.Join(expiredNames, ", "))
		if service.SubscriptionsChanged != nil {
			service.SubscriptionsChanged()
		}
		r.announce(notify.KindExpired, "Access expired", expiredNames,
			"stopped: their time is up")
	}

	return res.RowsAffected, res2.RowsAffected, nil
}

// namesMatching reads the client names a condition selects, capped so a mass
// expiry does not build a message nobody can read.
func (r *Reconciler) namesMatching(ctx context.Context, where string, args ...any) []string {
	if r.notifier == nil {
		return nil // nobody is listening; do not pay for the query
	}
	var names []string
	err := r.db.WithContext(ctx).Model(&model.Client{}).
		Where(where, args...).
		Order("id").Limit(20).Pluck("name", &names).Error
	if err != nil {
		r.log.Debug("could not read client names for a notification", "error", err)
		return nil
	}
	return names
}

// announce sends one message about a group of clients.
func (r *Reconciler) announce(kind notify.Kind, title string, names []string, what string) {
	if r.notifier == nil || len(names) == 0 {
		return
	}
	body := strings.Join(names, ", ") + " — " + what
	if len(names) == 20 {
		body += " (and possibly more)"
	}
	r.notifier.Send(notify.Event{Kind: kind, Title: title, Body: body, At: time.Now().UTC()})
}

// desired is the state the database says should exist.
type desired struct {
	// interfaces is what the pool is brought in line with.
	interfaces []model.Interface
	rules      []enforce.Rule
	shaping    []shaper.Client
	devices    []string
	perIface   map[uint][]backend.DesiredAccount
	clients    int
	accounts   int
}

// apply reads the desired state and pushes it to the enforcer and the drivers.
func (r *Reconciler) apply(ctx context.Context) (int, int, error) {
	d, err := r.readDesired(ctx)
	if err != nil {
		return 0, 0, err
	}

	// Drivers first, and from the database rather than from whatever was open
	// at startup. An interface added since then has no driver yet, and one that
	// failed to open gets another attempt: the reasons they fail are mostly
	// temporary, and the alternative is an interface that looks configured and
	// silently carries nothing until the panel is restarted.
	if r.pool != nil {
		r.pool.Sync(ctx, d.interfaces)
	}

	// The enforcer goes first. If a client just ran out, the kernel should stop
	// their traffic before the driver gets round to removing the peer, not
	// after.
	if err := r.enforcer.Apply(ctx, d.rules); err != nil {
		return 0, 0, fmt.Errorf("enforcer: %w", err)
	}

	// Shaping comes after the ruleset, because the nftables chain is what
	// stamps a packet with the class the hierarchy below is built from. A class
	// that exists before anything is stamped with it is merely idle; a stamp
	// pointing at a class that does not exist yet would fall through to the
	// default and leave the customer briefly unshaped.
	if r.shaper != nil && len(d.devices) > 0 {
		err := r.shaper.Apply(ctx, d.devices, d.shaping)
		switch {
		case err != nil && err.Error() != r.shapeErr():
			// A shaping failure is not a reason to stop enforcing quotas, which
			// is the guarantee customers are actually sold.
			r.log.Warn("rate limits are not being applied", "error", err)
			r.setShapeErr(err.Error())
		case err == nil && r.shapeErr() != "":
			r.log.Info("rate limits are being applied again")
			r.setShapeErr("")
		}
	}

	// Routing comes after enforcement and before the drivers.
	//
	// After enforcement, because a customer who has just run out should be
	// stopped rather than re-routed. Before the drivers, because a peer that is
	// about to be added should find its exit already in place — the reverse
	// order gives the customer a working tunnel that briefly leaves through the
	// wrong address, which is the one thing a foreign exit is bought to avoid.
	r.applyRouting(ctx)

	for ifaceID, b := range r.pool.All() {
		want := d.perIface[ifaceID]
		if want == nil {
			want = []backend.DesiredAccount{}
		}
		rep, err := b.Sync(ctx, want)
		if err != nil {
			// A cancelled context is the panel shutting down, not a fault.
			// Logged as an error it made every restart look like something
			// broke, which is how a real failure gets read past.
			if ctx.Err() != nil {
				r.log.Debug("driver sync stopped by shutdown",
					"interface", ifaceID, "error", err)
				continue
			}
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

// ApplyRouting does the routing part of a tick now, for the Save button on
// the outbounds page, and reports how it went.
func (r *Reconciler) ApplyRouting(ctx context.Context) error {
	r.applyRouting(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastRouteErr != "" {
		return fmt.Errorf("routing: %s", r.lastRouteErr)
	}
	return nil
}

// applyRouting brings the hops up and pushes the policy to the kernel.
//
// Failures here are logged once and never fatal. A panel that stopped enforcing
// quotas because an upstream hop was unreachable would turn one operator's
// misconfiguration into free traffic for everybody.
func (r *Reconciler) applyRouting(ctx context.Context) {
	if r.router == nil || r.policyOf == nil {
		return
	}

	// Hops first: the policy that follows references their devices, and a
	// routing rule pointing at an interface that does not exist yet would drop
	// traffic into a table with no route.
	if r.hops != nil && r.hopsOf != nil {
		if specs, err := r.hopsOf(ctx); err != nil {
			r.noteRouteErr(err)
			return
		} else if err := r.hops.Sync(ctx, specs); err != nil {
			r.noteRouteErr(err)
			// Carrying on deliberately: the hops that did come up should still
			// get their rules, and the ones that did not will simply have no
			// traffic steered at them.
		}
	}

	policy, err := r.policyOf(ctx)
	if err != nil {
		r.noteRouteErr(err)
		return
	}
	if err := r.router.Apply(ctx, policy); err != nil {
		r.noteRouteErr(err)
		return
	}
	r.clearRouteErr()
}

func (r *Reconciler) noteRouteErr(err error) {
	msg := err.Error()
	r.mu.Lock()
	repeat := r.lastRouteErr == msg
	r.lastRouteErr = msg
	r.mu.Unlock()
	if !repeat {
		// Said once. A server whose kernel cannot do this would otherwise log
		// the same line every two seconds and bury everything else.
		r.log.Warn("traffic routing is not being applied", "error", msg)
	}
}

func (r *Reconciler) clearRouteErr() {
	r.mu.Lock()
	had := r.lastRouteErr != ""
	r.lastRouteErr = ""
	r.mu.Unlock()
	if had {
		r.log.Info("traffic routing is being applied again")
	}
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

	var interfaces []model.Interface
	if err := db.Where("enabled = ? AND node_id = ?", true, r.localNodeID).
		Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("load interfaces: %w", err)
	}

	// A machine past the transfer allowance its host gives it carries nobody,
	// the same as any node. The interfaces stay up and configured; what stops
	// is the peers on them, so the month rolling over puts everyone back
	// without rebuilding a thing.
	if r.overAllowance(ctx) {
		interfaces = nil
	}

	// Accounts on another node's tunnel are that node's to program. Kept out of
	// the desired state here so this kernel is never asked for a peer on an
	// interface it does not have.
	local := make(map[uint]bool, len(interfaces))
	for _, iface := range interfaces {
		local[iface.ID] = true
	}
	//
	// They are still counted: the connections limit is the customer's, and
	// a device on another node is one of their connections. So every
	// account is grouped by customer for the limit, and only the local ones
	// go on to the desired set.
	clientNames := make(map[uint]string, len(clients))
	for _, c := range clients {
		clientNames[c.ID] = c.Name
	}
	r.conc.remember(accounts, clientNames, r.localNodeID, time.Now().UTC())
	// What the tunnels saw since the last tick: a device that came on, one
	// that went quiet. One line each, the way a session log reads.
	for _, ev := range r.conc.Events() {
		switch ev.Kind {
		case "connected":
			r.log.Info("device connected", "device", ev.Name, "from", ev.Addr)
		case "disconnected":
			r.log.Info("device disconnected", "device", ev.Name, "last", ev.Addr, "after", ev.For.Round(time.Second).String())
		}
	}
	allByClient := make(map[uint][]model.Account, len(clients))
	for _, a := range accounts {
		allByClient[a.ClientID] = append(allByClient[a.ClientID], a)
	}
	kept := accounts[:0]
	for _, a := range accounts {
		if local[a.InterfaceID] {
			kept = append(kept, a)
		}
	}
	accounts = kept

	byClient := make(map[uint][]model.Account, len(clients))
	for _, a := range accounts {
		byClient[a.ClientID] = append(byClient[a.ClientID], a)
	}
	now := time.Now().UTC()
	// On a node, the panel that owns a customer decides for them, seeing
	// every server at once; this server decides only for its own customers,
	// and for managed ones only while that panel has gone quiet.
	panelSilent := r.conc.panelSilent(now)

	d := &desired{
		rules:      make([]enforce.Rule, 0, len(clients)),
		shaping:    make([]shaper.Client, 0, len(clients)),
		perIface:   map[uint][]backend.DesiredAccount{},
		clients:    len(clients),
		accounts:   len(accounts),
		interfaces: interfaces,
		devices:    shapedDevices(interfaces),
	}

	for _, c := range clients {
		accs := byClient[c.ID]
		// A customer with every device on other nodes still has a limit to
		// be held to, so the limit runs before the local check below.
		if c.Status.Serviceable() && (c.OriginID == 0 || panelSilent) {
			r.holdOver(ctx, &c, allByClient[c.ID], now)
		}
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

		// A cut-off client is dropped outright, so a class for them would only
		// shape traffic that never leaves.
		if serviceable && c.RateBitsPerSec > 0 {
			d.shaping = append(d.shaping, shaper.Client{
				Key:            enforce.Key(c.ID),
				RateBitsPerSec: c.RateBitsPerSec,
			})
		}

		// Accounts of a non-serviceable client are simply left out of the
		// desired set, and Sync removes whatever it finds that is not listed.
		if !serviceable {
			continue
		}
		// A device held off -- by this server or by the panel that owns the
		// customer -- is left out of the desired set: a WireGuard peer is
		// removed by Sync, an OpenVPN session was ended when the hold began.
		for _, a := range accs {
			if !a.Enabled || r.conc.heldOff(a.ID, now) {
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

// shapedDevices lists the devices a customer's traffic leaves by.
//
// The tunnel device carries what is sent to them, which is the direction a
// customer experiences as their speed. The egress interface carries what they
// send outward, already decapsulated, so the same class applies there. Both are
// egress paths, which is the only side a queue can be scheduled on.
//
// Anything not stamped with a class — the panel's own traffic, unlimited
// customers, and the encrypted tunnel packets themselves — falls into the
// default class, which is deliberately wider than any real link.
func shapedDevices(interfaces []model.Interface) []string {
	seen := map[string]bool{}
	var out []string

	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	for _, iface := range interfaces {
		// Only the tunnel devices, which this panel created and nothing else
		// uses. The egress interface is deliberately left alone: it is the
		// machine's main network card, and replacing its root qdisc would throw
		// away whatever the distribution, the host, or another program running
		// on this server had configured there, and would put our hierarchy in
		// front of all of that traffic rather than only the tunnel's.
		//
		// The cost is that shaping applies to what a customer is sent. What
		// they send is not shaped.
		add(iface.Name)
	}
	sort.Strings(out)
	return out
}

func (r *Reconciler) shapeErr() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastShapeErr
}

func (r *Reconciler) setShapeErr(msg string) {
	r.mu.Lock()
	r.lastShapeErr = msg
	r.mu.Unlock()
}

// max64 keeps a node id usable when the caller left it unset.
func max64(v, floor uint) uint {
	if v < floor {
		return floor
	}
	return v
}

// overAllowance reports whether this machine has carried more this period than
// its host allows.
//
// Read every tick rather than cached: an operator raising the cap, or clearing
// the counter, should put customers back on within a tick and not after a
// restart.
func (r *Reconciler) overAllowance(ctx context.Context) bool {
	var node model.Node
	if err := r.db.WithContext(ctx).First(&node, r.localNodeID).Error; err != nil {
		return false // no row to read a limit from is no limit
	}
	if !node.OverAllowance {
		return false
	}
	r.log.Warn("this server has used its whole transfer allowance; customers are off it",
		"used", node.UsedBytes, "allowance", node.DataLimitBytes,
		"fix", "raise the allowance on the Servers page, or clear the counter")
	return true
}

// liveWindow is how recent a handshake has to be for a session to count as
// connected -- the panel's online window.
func liveWindow() time.Duration { return time.Duration(service.OnlineWithin.Load()) * time.Second }

// holdOver holds a customer to the connections their plan allows, across
// every server they reach. What is over the limit is held off: a session on
// this server is ended here, one on a node is carried to it.
func (r *Reconciler) holdOver(ctx context.Context, c *model.Client, accs []model.Account, now time.Time) {
	for _, h := range r.conc.enforce(c, accs, now) {
		where := "here"
		if h.Node != 0 {
			where = fmt.Sprintf("node %d", h.Node)
		}
		r.log.Info("connection limit reached; device held off",
			"client", c.Name, "device", h.Account.DeviceName, "on", where,
			"limit", c.DeviceLimit, "until", h.Until.Format(time.Kitchen))
		if h.Node != 0 {
			if r.OnHold != nil {
				r.OnHold(h.Node, service.NodeHold{OriginID: h.Account.ID, Until: h.Until})
			}
			continue
		}
		r.kick(ctx, h.Account.InterfaceID, h.Account.ID, h.Account.DeviceName)
	}
}

// kick ends a session on this server, for the protocols that have one.
func (r *Reconciler) kick(ctx context.Context, ifaceID, accountID uint, device string) {
	if r.pool == nil {
		return
	}
	if b, ok := r.pool.Get(ifaceID); ok {
		if err := b.Kick(ctx, accountID); err != nil && !errors.Is(err, backend.ErrNotSupported) {
			r.log.Warn("could not end a held-off session", "device", device, "error", err)
		}
	}
}

// Hold applies a hold the panel that owns the customer decided: the account
// is off until then. Its session is ended now; a WireGuard peer goes at the
// next tick.
func (r *Reconciler) Hold(ctx context.Context, accountID uint, until time.Time) {
	r.conc.hold(accountID, until)
	r.conc.panelSpoke(time.Now().UTC())
	var a model.Account
	if err := r.db.WithContext(ctx).First(&a, accountID).Error; err != nil {
		return
	}
	r.log.Info("device held off by the panel", "device", a.DeviceName, "until", until.Format(time.Kitchen))
	r.kick(ctx, a.InterfaceID, a.ID, a.DeviceName)
}

// Sessions is what this server reports to the panel that manages it: every
// managed credential live here, by its id there.
func (r *Reconciler) Sessions() []service.NodeSession {
	now := time.Now().UTC()
	r.conc.panelSpoke(now)
	return r.conc.sessions(now)
}

// PanelSpoke notes that the managing panel is in touch, for the fallback.
func (r *Reconciler) PanelSpoke() { r.conc.panelSpoke(time.Now().UTC()) }

// SetRemoteSessions stores what one node just reported live on it.
func (r *Reconciler) SetRemoteSessions(nodeID uint, sessions []service.NodeSession) {
	r.conc.setRemote(nodeID, sessions, time.Now().UTC())
}

// Holds is every hold in force, by account id, for the syncer to carry to
// the nodes with each push.
func (r *Reconciler) Holds() map[uint]time.Time { return r.conc.holds(time.Now().UTC()) }

// ConnectionsNow is how many connections each customer has right now,
// across every server, for the list.
func (r *Reconciler) ConnectionsNow(clientIDs []uint) map[uint]int {
	return r.conc.connectionsNow(clientIDs, time.Now().UTC())
}

// LiveClients is every customer connected right now, for the counters.
func (r *Reconciler) LiveClients() []uint { return r.conc.liveClients(time.Now().UTC()) }
