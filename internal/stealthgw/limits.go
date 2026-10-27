package stealthgw

import "time"

// The limits a session is opened under.
//
// Every one of them is spent only by an opening that a device's secret sealed
// and the replay rule passed: a stranger reaches none of this, and gets
// nothing back that would say a limit exists.

const (
	// sourceDevices is how many devices one source address and port may
	// have sessions of at once.
	sourceDevices = 4
	// devicePending is how many sessions a device may have waiting for
	// WireGuard at once.
	devicePending = 2
	// deviceSlots is how many source addresses one device may be reached at.
	deviceSlots = 8
	// defaultSlots bounds the gateway's sessions; each holds one socket to
	// the tunnel.
	defaultSlots = 4096
	// maxSources bounds the per-address allowances kept at once.
	maxSources = 65_536
)

// Allowances, as a burst and a rate per second.
var (
	deviceProofs      = allowance{burst: 16, rate: 1}
	deviceSpent       = allowance{burst: 32, rate: 2}
	sourceProofs      = allowance{burst: 32, rate: 4}
	sessionHandshakes = allowance{burst: 3, rate: 1}
)

// Timers.
const (
	// pendingFor is how long a session may wait for WireGuard's first word.
	pendingFor = 10 * time.Second
	// oldReadable is how long a session that a newer one of the same source
	// and device replaced is still read.
	oldReadable = 10 * time.Second
	// idleFor ends a session nothing authentic has crossed for this long.
	idleFor = 180 * time.Second
	// echoDelay bounds the wait before a keepalive is echoed, and
	// keepaliveDelay the hold on a lone keepalive WireGuard sends; both break
	// WireGuard's own steady rhythm.
	echoDelay      = 3 * time.Second
	keepaliveDelay = 3 * time.Second
	// maxEchoes is how many echoes of one session wait at once.
	maxEchoes = 4
	// indexFor is how long a WireGuard session index the tunnel gave a
	// device is remembered: past WireGuard's own reject-after time, a
	// session under it is dead anyway.
	indexFor = 200 * time.Second
)

type allowance struct{ burst, rate float64 }

// bucket is a token bucket. It is not safe for concurrent use; the gateway
// holds its lock.
type bucket struct {
	tokens float64
	at     time.Time
}

func newBucket(a allowance, now time.Time) bucket { return bucket{tokens: a.burst, at: now} }

// take spends one token if there is one.
func (b *bucket) take(a allowance, now time.Time) bool {
	b.refill(a, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// full says whether the bucket is back to its burst, and so says nothing.
func (b *bucket) full(a allowance, now time.Time) bool {
	b.refill(a, now)
	return b.tokens >= a.burst
}

func (b *bucket) refill(a allowance, now time.Time) {
	if d := now.Sub(b.at).Seconds(); d > 0 {
		b.tokens = min(a.burst, b.tokens+d*a.rate)
	}
	b.at = now
}
