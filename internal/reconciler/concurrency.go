package reconciler

import (
	"sort"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// Holding a customer to the connections their plan allows.
//
// A plan sold to one person is one connection at a time, however many device
// files they hold: switching from the laptop to the phone is what the files
// are for, both at once is what the limit is for. The tunnel cannot count
// people, so it counts what it can see -- credentials with traffic moving
// right now, and the public addresses that traffic comes from -- and when
// more of those are live than the plan allows, the newest are held off for a
// short while. A WireGuard device held off has its peer removed and comes
// back on its own when the hold ends; an OpenVPN session is ended.
//
// What is deliberately not counted: a phone walking from wifi onto mobile
// data changes address once, which is not a second person. One credential
// used from two devices at the same time shows as an address that keeps
// flipping back and forth, and that is.

const (
	// activeWindow is how long since bytes last moved a session still counts
	// as connected. A WireGuard device with keepalive on sends something every
	// 25 seconds; three missed is gone.
	activeWindow = 75 * time.Second

	// holdFor is how long a device held off stays off. Long enough for the
	// customer to notice which device won, short enough that a mistake costs
	// them a couple of minutes.
	holdFor = 2 * time.Minute
)

// activity is what has been seen of one credential.
type activity struct {
	bytes uint64    // RX+TX at the last reading
	moved time.Time // when bytes last changed
	since time.Time // when this run of activity started
	addr  string    // the address the traffic comes from now
	// flips counts endpoint changes inside the window, flipAt the last one.
	// Two or more means two devices fighting over one credential.
	flips  int
	flipAt time.Time
	addrs  map[string]time.Time // address -> when traffic last came from it
}

// concurrency is the state the reconciler keeps between ticks for this.
type concurrency struct {
	mu   sync.Mutex
	acts map[uint]*activity
	held map[uint]time.Time // account -> until when it is held off
}

func newConcurrency() *concurrency {
	return &concurrency{acts: map[uint]*activity{}, held: map[uint]time.Time{}}
}

// observe folds one tick's readings in.
func (c *concurrency) observe(stats []backend.Stat, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range stats {
		total := s.RX + s.TX
		a := c.acts[s.AccountID]
		if a == nil {
			// First sight: remembered, not counted. A peer that was on before
			// the panel started would otherwise look live for a window.
			c.acts[s.AccountID] = &activity{bytes: total, addr: hostOf(s.Endpoint), addrs: map[string]time.Time{}}
			continue
		}
		if total == a.bytes {
			continue
		}
		if total < a.bytes {
			// Counters start over when a peer is re-added or the tunnel
			// restarts; that is not traffic.
			a.bytes = total
			continue
		}
		wasLive := now.Sub(a.moved) < activeWindow
		a.bytes = total
		a.moved = now
		if !wasLive {
			a.since = now
			a.flips = 0
		}
		host := hostOf(s.Endpoint)
		if host != "" {
			if a.addr != "" && host != a.addr {
				if now.Sub(a.flipAt) > activeWindow {
					a.flips = 0
				}
				a.flips++
				a.flipAt = now
			}
			a.addr = host
			a.addrs[host] = now
		}
		for h, t := range a.addrs {
			if now.Sub(t) >= activeWindow {
				delete(a.addrs, h)
			}
		}
	}
}

// connections is how many devices one credential is carrying right now:
// none when idle, one when live, and the live addresses when they fight.
func (a *activity) connections(now time.Time) int {
	if a == nil || now.Sub(a.moved) >= activeWindow {
		return 0
	}
	if a.flips >= 2 && now.Sub(a.flipAt) < activeWindow && len(a.addrs) > 1 {
		return len(a.addrs)
	}
	return 1
}

// heldOff reports whether the account is currently held off.
func (c *concurrency) heldOff(id uint, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.held[id]
	if !ok {
		return false
	}
	if now.After(until) {
		delete(c.held, id)
		// Its counters start over when the peer comes back; a fresh start
		// keeps a stale reading from counting it live at once.
		if a := c.acts[id]; a != nil {
			a.moved = time.Time{}
		}
		return false
	}
	return true
}

// heldAccount is one device held off, for the log and the driver.
type heldAccount struct {
	Account  model.Account
	ClientID uint
	Until    time.Time
}

// enforce looks at one customer's credentials and holds off whatever is
// over their limit, oldest connection kept. It returns what it held this
// tick, so the caller can end the sessions and say so.
func (c *concurrency) enforce(client *model.Client, accs []model.Account, now time.Time) []heldAccount {
	if client.DeviceLimit <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	type live struct {
		acc   model.Account
		n     int
		since time.Time
	}
	var lives []live
	total := 0
	for _, a := range accs {
		if until, ok := c.held[a.ID]; ok && now.Before(until) {
			continue // already off; not counted, not counted again
		}
		act := c.acts[a.ID]
		n := act.connections(now)
		if n == 0 {
			continue
		}
		total += n
		lives = append(lives, live{acc: a, n: n, since: act.since})
	}
	if total <= client.DeviceLimit {
		return nil
	}
	// Oldest first: the device that was already connected stays.
	sort.Slice(lives, func(i, j int) bool { return lives[i].since.Before(lives[j].since) })
	var out []heldAccount
	kept := 0
	for _, l := range lives {
		// A credential fighting between two devices is over the limit on its
		// own; nothing but holding it off ends the fight.
		if l.n == 1 && kept+1 <= client.DeviceLimit {
			kept++
			continue
		}
		until := now.Add(holdFor)
		c.held[l.acc.ID] = until
		out = append(out, heldAccount{Account: l.acc, ClientID: client.ID, Until: until})
	}
	return out
}
