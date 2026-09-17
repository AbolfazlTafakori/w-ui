package reconciler

import "sync"

// Charging each tunnel with what crossed it.
//
// A customer's allowance is one number spent over every tunnel and server
// they reach, and that is counted where it is enforced, in the kernel, by
// customer. What the interfaces page wants is different: how much went
// through this tunnel, as opposed to that one. The tunnels keep their own
// counters per peer or session, cumulative from when the peer was added or
// the session began; the meter reads them each tick and hands on only what
// grew, so each file's own counters can be summed by tunnel.

// deviceMeter remembers the last reading of every file's counters.
type deviceMeter struct {
	mu   sync.Mutex
	last map[uint]reading
}

type reading struct{ rx, tx uint64 }

func newDeviceMeter() *deviceMeter { return &deviceMeter{last: map[uint]reading{}} }

// step folds in one reading and returns what was carried since the last
// one, as the customer sees it: up is what they sent (the tunnel's RX),
// down what they received. The first sight of a file only sets its
// baseline, so a tunnel that was up before the panel started does not get
// its whole history counted as one tick. A counter that went backwards
// started over -- a peer re-added, a session begun -- and what it shows
// now is what was carried since.
func (m *deviceMeter) step(accountID uint, rx, tx uint64) (up, down uint64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, seen := m.last[accountID]
	m.last[accountID] = reading{rx, tx}
	if !seen {
		return 0, 0, false
	}
	up = grew(prev.rx, rx)
	down = grew(prev.tx, tx)
	return up, down, up+down > 0
}

// keep drops every file not in this tick's readings. An OpenVPN session
// that ended is gone from the list, and the next one begins at zero: had
// its old reading stayed, a new session that outgrew it in one tick would
// be charged only the excess.
func (m *deviceMeter) keep(seen map[uint]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.last {
		if !seen[id] {
			delete(m.last, id)
		}
	}
}

func grew(prev, now uint64) uint64 {
	if now >= prev {
		return now - prev
	}
	return now
}
