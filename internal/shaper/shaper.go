// Package shaper limits how fast a customer may move data, as opposed to how
// much of it package enforce lets them move.
//
// Shaping is done with HTB classes on the egress side of each device a
// customer's traffic leaves by. Classification does not use tc filters: the
// nftables chain that already exists per client stamps the packet with the
// class to use, and HTB reads that stamp directly. That matters at scale —
// a filter list is walked per packet, so a thousand customers would mean a
// thousand comparisons, while the stamp comes from a hash lookup that costs the
// same for three customers as for ten thousand.
package shaper

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Errors this package returns.
var (
	ErrUnavailable = errors.New("shaper: traffic shaping is unavailable")
	ErrNoTool      = errors.New("shaper: tc is not installed (install iproute2)")
)

const (
	// Major number of the HTB hierarchy. One is conventional and it only has to
	// be distinct from other qdiscs on the same device.
	major = 1

	// The class everything unclassified lands in. It is deliberately given a
	// rate far above any real link so that unshaped traffic — the panel's own,
	// and every customer without a limit — is not throttled by passing through
	// the hierarchy.
	defaultMinor = 0xffff
	defaultRate  = "10gbit"

	// Below this a class is not meaningfully schedulable and tc starts
	// rejecting the burst arithmetic.
	minRateBits = 8000
)

// Client is one customer's shaping requirement.
type Client struct {
	// Key is the same stable identifier package enforce uses, so the nftables
	// chain and the tc class are named from one source.
	Key string

	// RateBitsPerSec is the ceiling in bits per second. Zero means unshaped,
	// and such a client is left out of the hierarchy entirely rather than
	// given a very large class.
	RateBitsPerSec uint64
}

// Shaper programs the rate limits.
type Shaper interface {
	// Apply makes the given devices carry exactly these limits.
	Apply(ctx context.Context, devices []string, clients []Client) error

	// Health reports whether shaping can be programmed at all.
	Health(ctx context.Context) error

	// Close releases resources. It deliberately leaves the hierarchy in place:
	// tearing it down would un-throttle every customer at the moment the panel
	// stops, which is the opposite of what an operator wants from a crash.
	Close() error
}

// Minor derives the HTB class minor number for a client key.
//
// The key is "c<id>", so the client's own id is used. That keeps the class
// number readable in `tc class show` and stable across restarts, which is what
// lets a running hierarchy be adopted rather than rebuilt.
func Minor(key string) (uint16, error) {
	digits := strings.TrimPrefix(key, "c")
	if digits == key || digits == "" {
		return 0, fmt.Errorf("shaper: unrecognised client key %q", key)
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("shaper: client key %q: %w", key, err)
	}
	// Zero is not a valid minor and the top value is reserved for the default
	// class, so the usable range is one short of the full 16 bits.
	if n == 0 || n >= defaultMinor {
		return 0, fmt.Errorf("shaper: client id %d is outside the class range 1..%d", n, defaultMinor-1)
	}
	return uint16(n), nil
}

// ClassID renders a class handle as tc writes it.
func ClassID(minor uint16) string { return fmt.Sprintf("%d:%x", major, minor) }

// desired maps class minor to the rate it should carry.
type desired map[uint16]uint64

func toDesired(clients []Client) (desired, []error) {
	out := make(desired, len(clients))
	var problems []error

	for _, c := range clients {
		if c.RateBitsPerSec == 0 {
			continue // unshaped clients stay out of the hierarchy
		}
		minor, err := Minor(c.Key)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		rate := c.RateBitsPerSec
		if rate < minRateBits {
			// A limit below this cannot be scheduled. Refusing it outright
			// would leave the customer unlimited, which is the wrong way to
			// fail, so it is raised to the floor instead.
			rate = minRateBits
		}
		out[minor] = rate
	}
	return out, problems
}

// diff is the set of class operations that brings one device into line.
type diff struct {
	add    []uint16
	change []uint16
	remove []uint16
}

func computeDiff(want desired, have map[uint16]uint64) diff {
	var d diff

	for minor, rate := range want {
		current, exists := have[minor]
		switch {
		case !exists:
			d.add = append(d.add, minor)
		case current != rate:
			// Changed rather than removed and re-added: deleting a class drops
			// the packets queued in it, so a customer whose plan was edited
			// would see a blip for no reason.
			d.change = append(d.change, minor)
		}
	}

	for minor := range have {
		if minor == defaultMinor {
			continue // the catch-all is ours and always stays
		}
		if _, keep := want[minor]; !keep {
			d.remove = append(d.remove, minor)
		}
	}

	sortMinors(d.add)
	sortMinors(d.change)
	sortMinors(d.remove)
	return d
}

func sortMinors(s []uint16) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

func (d diff) empty() bool {
	return len(d.add) == 0 && len(d.change) == 0 && len(d.remove) == 0
}

// BuildScript renders the tc batch that applies a diff to one device.
//
// It is returned as a script rather than run command by command so the whole
// device can be updated in a single tc invocation: a customer's limit changing
// should not cost one process spawn per class on a panel that reconciles every
// two seconds.
func BuildScript(device string, want desired, d diff) string {
	var b strings.Builder

	for _, minor := range d.add {
		fmt.Fprintf(&b, "class add dev %s parent %d: classid %s htb rate %dbit ceil %dbit\n",
			device, major, ClassID(minor), want[minor], want[minor])
		// Without a leaf qdisc HTB queues in a plain FIFO, and a customer at
		// their ceiling accumulates a queue deep enough to make interactive
		// traffic unusable while a download runs. fq_codel keeps the latency of
		// a saturated class close to an idle one.
		fmt.Fprintf(&b, "qdisc add dev %s parent %s handle %x: fq_codel\n",
			device, ClassID(minor), minor)
	}

	for _, minor := range d.change {
		fmt.Fprintf(&b, "class change dev %s parent %d: classid %s htb rate %dbit ceil %dbit\n",
			device, major, ClassID(minor), want[minor], want[minor])
	}

	for _, minor := range d.remove {
		// The leaf goes first: a class with a qdisc attached cannot be deleted.
		fmt.Fprintf(&b, "qdisc del dev %s parent %s\n", device, ClassID(minor))
		fmt.Fprintf(&b, "class del dev %s classid %s\n", device, ClassID(minor))
	}

	return b.String()
}

// RootScript renders the one-time hierarchy for a device.
func RootScript(device string) string {
	var b strings.Builder
	// `replace` rather than `add`: re-running must not fail on a device that is
	// already set up, and must still correct one whose root is wrong.
	fmt.Fprintf(&b, "qdisc replace dev %s root handle %d: htb default %x\n",
		device, major, defaultMinor)
	b.WriteString(RootClassScript(device))
	return b.String()
}

// RootClassScript is the catch-all class on its own.
//
// For a device whose root qdisc this panel already laid down, on a previous
// run. The kernel carries out `tc qdisc replace` over an existing htb as a
// change, and htb has no change operation, so asking for the root a second
// time fails outright — which left the panel unable to adopt its own
// hierarchy after a restart, and every speed limit on that device unapplied
// until somebody deleted the qdisc by hand. Classes have no such problem:
// `class replace` is how a customer's rate is edited in the first place.
func RootClassScript(device string) string {
	return fmt.Sprintf("class replace dev %s parent %d: classid %s htb rate %s ceil %s\n",
		device, major, ClassID(defaultMinor), defaultRate, defaultRate)
}

// parseMinor pulls the minor number out of a "1:a" handle.
func parseMinor(handle string) (uint16, bool) {
	parts := strings.SplitN(handle, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(n), true
}

// parseRate normalises what tc reports as a rate.
//
// Its JSON gives a number on some versions and a string like "1Mbit" on others,
// so both are accepted rather than assuming whichever this host happens to ship.
func parseRate(v any) (uint64, bool) {
	switch t := v.(type) {
	case float64:
		return uint64(t), true
	case string:
		return parseRateString(t)
	default:
		return 0, false
	}
}

func parseRateString(s string) (uint64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	multiplier := uint64(1)
	for _, suffix := range []struct {
		text string
		mult uint64
	}{
		{"gbit", 1_000_000_000}, {"mbit", 1_000_000}, {"kbit", 1_000}, {"bit", 1},
	} {
		if strings.HasSuffix(s, suffix.text) {
			s = strings.TrimSuffix(s, suffix.text)
			multiplier = suffix.mult
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return uint64(n * float64(multiplier)), true
}
