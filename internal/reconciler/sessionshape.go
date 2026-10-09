package reconciler

import (
	"context"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/shaper"
)

// Rate limits on tunnels whose customers each arrive on a link of their own.
//
// An L2TP session is a PPP device that exists for as long as the session
// does, and what the customer is sent leaves through it rather than through
// the tunnel's own device. The limit has to be on that link, and only that
// customer's class has to be: nothing else ever leaves through it.
//
// A tunnel's own device is read back every tick, so that a hierarchy edited
// by hand is put right. Doing the same for every session would be two tc runs
// per session every tick -- thousands every two seconds on a busy server --
// for links that are the panel's alone and live minutes to hours. So what was
// laid on each link is remembered, and laid again when the session, the
// device on it or its customer's rate changes.

// sessionShapeRetry is how long a link that could not be shaped waits before
// it is tried again, rather than being tried, and logged, every tick.
const sessionShapeRetry = 30 * time.Second

type sessionShape struct {
	since   int64
	account uint
	key     string
	rate    uint64
	// failed is when laying it last failed.
	failed time.Time
}

type sessionShapes struct {
	mu sync.Mutex
	by map[string]sessionShape
}

// shapeSessions brings every session's link in line with its customer's rate.
func (r *Reconciler) shapeSessions(ctx context.Context, d *desired) {
	if r.shaper == nil || r.pool == nil {
		return
	}
	live := map[string]backend.SessionDevice{}
	partial := false
	for _, b := range r.pool.All() {
		sd, ok := b.(backend.SessionDevicer)
		if !ok {
			continue
		}
		devs, err := sd.SessionDevices(ctx)
		if err != nil {
			// The driver's health says what is wrong. What was laid is kept
			// for when its report comes back: its sessions may well still be
			// there.
			partial = true
			continue
		}
		for _, s := range devs {
			live[s.Device] = s
		}
	}

	r.sessions.mu.Lock()
	defer r.sessions.mu.Unlock()
	if r.sessions.by == nil {
		r.sessions.by = map[string]sessionShape{}
	}

	now := time.Now()
	for dev, s := range live {
		c := d.sessionRate[s.AccountID]
		want := sessionShape{since: s.Since, account: s.AccountID, key: c.Key, rate: c.RateBitsPerSec}
		prev, had := r.sessions.by[dev]
		if had && prev.since == want.since && prev.account == want.account &&
			prev.key == want.key && prev.rate == want.rate &&
			(prev.failed.IsZero() || now.Sub(prev.failed) < sessionShapeRetry) {
			continue
		}
		// A new link carries nothing of ours; one with no rate needs nothing.
		if want.rate == 0 && (!had || prev.since != want.since || prev.rate == 0) {
			r.sessions.by[dev] = want
			continue
		}
		var clients []shaper.Client
		if want.rate > 0 {
			clients = []shaper.Client{c}
		}
		if err := r.shaper.Apply(ctx, []string{dev}, clients); err != nil {
			want.failed = now
			r.log.Debug("a session's link could not be shaped", "device", dev,
				"account", s.AccountID, "error", err)
		}
		r.sessions.by[dev] = want
	}
	if partial {
		return
	}
	for dev := range r.sessions.by {
		if _, ok := live[dev]; !ok {
			delete(r.sessions.by, dev)
		}
	}
}
