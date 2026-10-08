package nodes

import (
	"math/bits"
	"sort"

	"github.com/abolfazl/w-ui/internal/service"
)

// Turning what a node counted into what its customers are charged.
//
// A node's usage coefficient is a price: an expensive server counts double,
// a cheap one half. It applies to everything the node reports, the customers'
// totals and their files' rows alike, or the per-user table of a customer on
// that node could never add up to their usage. And it is applied so that the
// two still agree to the byte: a customer's converted total is shared among
// their files by what each file actually carried, the rounding remainder
// going to the files nearest the next byte. That is a change of unit applied
// to measured numbers, not an estimate of them.

// chargeReport applies a node's coefficient to one report. clientOf maps each
// file's id on this panel to its customer's.
func chargeReport(usage []service.NodeUsage, devices []service.NodeDeviceUsage,
	coefficient float64, clientOf map[uint]uint) ([]service.NodeUsage, []service.NodeDeviceUsage) {
	// Zero is a node created before the column existed, and negative is
	// nonsense: neither is an operator asking to give traffic away.
	if coefficient <= 0 || coefficient == 1 {
		return usage, devices
	}

	charged := make([]service.NodeUsage, 0, len(usage))
	byClient := make(map[uint]service.NodeUsage, len(usage)) // raw, by customer
	chargedBy := make(map[uint]service.NodeUsage, len(usage))
	for _, u := range usage {
		c := service.NodeUsage{OriginID: u.OriginID}
		if u.Up+u.Down == u.Bytes {
			// The total is the two directions, so it stays their sum.
			c.Up = atLeastOne(u.Up, coefficient)
			c.Down = atLeastOne(u.Down, coefficient)
			c.Bytes = c.Up + c.Down
		} else {
			c.Bytes = atLeastOne(u.Bytes, coefficient)
			c.Up = atLeastOne(u.Up, coefficient)
			c.Down = atLeastOne(u.Down, coefficient)
		}
		charged = append(charged, c)
		byClient[u.OriginID] = u
		chargedBy[u.OriginID] = c
	}

	// Files grouped by customer, in id order so the remainder always goes the
	// same way for the same report.
	groups := map[uint][]service.NodeDeviceUsage{}
	var alone []service.NodeDeviceUsage
	for _, d := range devices {
		client, ok := clientOf[d.OriginID]
		if _, reported := byClient[client]; !ok || !reported {
			alone = append(alone, d)
			continue
		}
		groups[client] = append(groups[client], d)
	}

	out := make([]service.NodeDeviceUsage, 0, len(devices))
	for client, files := range groups {
		sort.Slice(files, func(i, j int) bool { return files[i].OriginID < files[j].OriginID })
		raw, total := byClient[client], chargedBy[client]

		ups := make([]uint64, len(files))
		downs := make([]uint64, len(files))
		var rawUp, rawDown uint64
		for i, f := range files {
			ups[i], downs[i] = f.Up, f.Down
			rawUp += f.Up
			rawDown += f.Down
		}
		// The files are the customer's bytes split by device only when they
		// add up to them -- a node of this version drains both together.
		// An older node's files may not, and are then converted each on its
		// own rather than forced to a total they were not counted towards.
		if rawUp == raw.Up {
			ups = share(total.Up, ups)
		} else {
			ups = each(ups, coefficient)
		}
		if rawDown == raw.Down {
			downs = share(total.Down, downs)
		} else {
			downs = each(downs, coefficient)
		}
		for i, f := range files {
			out = append(out, service.NodeDeviceUsage{OriginID: f.OriginID, Up: ups[i], Down: downs[i]})
		}
	}
	for _, d := range alone {
		out = append(out, service.NodeDeviceUsage{
			OriginID: d.OriginID,
			Up:       atLeastOne(d.Up, coefficient),
			Down:     atLeastOne(d.Down, coefficient),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OriginID < out[j].OriginID })
	return charged, out
}

// share divides total among weights in proportion, exactly: the parts sum to
// total, each is within one byte of its exact share, and the bytes left by
// rounding down go to the largest remainders, the earliest first on a tie.
func share(total uint64, weights []uint64) []uint64 {
	out := make([]uint64, len(weights))
	var sum uint64
	for _, w := range weights {
		sum += w
	}
	if sum == 0 || total == 0 {
		return out
	}
	rems := make([]uint64, len(weights))
	var given uint64
	for i, w := range weights {
		// total*w can exceed 64 bits on a busy node; the 128-bit product is
		// exact, and the quotient fits because w <= sum.
		hi, lo := bits.Mul64(total, w)
		out[i], rems[i] = bits.Div64(hi, lo, sum)
		given += out[i]
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return rems[order[a]] > rems[order[b]] })
	for k := 0; given < total; k++ {
		out[order[k%len(order)]]++
		given++
	}
	return out
}

// each converts every value on its own.
func each(values []uint64, coefficient float64) []uint64 {
	out := make([]uint64, len(values))
	for i, v := range values {
		out[i] = atLeastOne(v, coefficient)
	}
	return out
}

// atLeastOne converts one count. Traffic that happened is never charged as
// none: a coefficient small enough to round a real transfer to zero would let
// a customer use that node for free, one small transfer at a time.
func atLeastOne(v uint64, coefficient float64) uint64 {
	if v == 0 {
		return 0
	}
	scaled := uint64(float64(v) * coefficient)
	if scaled == 0 {
		return 1
	}
	return scaled
}
