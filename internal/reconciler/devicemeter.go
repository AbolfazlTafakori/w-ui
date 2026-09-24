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

// apportion shares what the kernel billed each customer out among the
// files that carried it, in proportion to what each file's tunnel counted.
//
// The tunnel's own counters are not the bill: WireGuard counts its
// encrypted packets -- a header, a tag and padding on every one, and the
// keepalives and handshakes besides -- so they run a few percent above the
// traffic the kernel charges the customer for, and a per-tunnel table
// summed from them came out larger than the customer's total. The shares
// come from the tunnels; the amounts from the bill, so the table adds up
// to the total exactly. A customer billed with nothing measured on any
// file this tick -- the first sight of a file is only its baseline --
// leaves that tick out of the table; a file measured with no bill -- the kernel counting nothing, as
// the stand-in enforcer does -- keeps its own count.
func apportion(billed map[uint]usageDelta, grown map[uint]usageDelta, clientOf map[uint]uint) map[uint]usageDelta {
	out := map[uint]usageDelta{}
	byClient := map[uint][]uint{}
	for acc := range grown {
		byClient[clientOf[acc]] = append(byClient[clientOf[acc]], acc)
	}
	for acc, g := range grown {
		if _, ok := billed[clientOf[acc]]; !ok {
			out[acc] = g
		}
	}
	for client, bill := range billed {
		accs := byClient[client]
		if len(accs) == 0 {
			continue
		}
		up, down := bill.Up, bill.Down
		if up == 0 && down == 0 {
			// No direction from the kernel: the total, split the way the
			// tunnels saw it.
			var gu, gd uint64
			for _, a := range accs {
				gu += grown[a].Up
				gd += grown[a].Down
			}
			if gu+gd == 0 {
				down = bill.Bytes
			} else {
				up = bill.Bytes * gu / (gu + gd)
				down = bill.Bytes - up
			}
		}
		share := func(total uint64, weight func(usageDelta) uint64) map[uint]uint64 {
			res := map[uint]uint64{}
			var sum uint64
			for _, a := range accs {
				sum += weight(grown[a])
			}
			if sum == 0 {
				// Nothing to weigh by: an even split.
				for i, a := range accs {
					res[a] = total / uint64(len(accs))
					if i == 0 {
						res[a] += total % uint64(len(accs))
					}
				}
				return res
			}
			var given uint64
			var last uint
			for _, a := range accs {
				w := weight(grown[a])
				v := uint64(float64(total) * float64(w) / float64(sum))
				res[a] = v
				given += v
				if w > 0 {
					last = a
				}
			}
			res[last] += total - given // rounding lands on a file that moved
			return res
		}
		ups := share(up, func(d usageDelta) uint64 { return d.Up })
		downs := share(down, func(d usageDelta) uint64 { return d.Down })
		for _, a := range accs {
			if ups[a]+downs[a] > 0 {
				out[a] = usageDelta{Up: ups[a], Down: downs[a]}
			}
		}
	}
	return out
}
