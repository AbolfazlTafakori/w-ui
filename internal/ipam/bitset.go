package ipam

import "math/bits"

// bitset is a fixed-size bit vector. A /16 pool needs 8 KiB and a /12 needs
// 128 KiB, so the whole allocation map stays in memory and every lookup is a
// word test rather than a database round trip.
type bitset struct {
	words []uint64
	n     uint32
	count int
}

func newBitset(n uint32) *bitset {
	return &bitset{
		words: make([]uint64, (int(n)+63)/64),
		n:     n,
	}
}

func (b *bitset) test(i uint32) bool {
	if i >= b.n {
		return false
	}
	return b.words[i>>6]&(uint64(1)<<(i&63)) != 0
}

// set marks i as used and reports whether that changed anything.
func (b *bitset) set(i uint32) bool {
	if i >= b.n || b.test(i) {
		return false
	}
	b.words[i>>6] |= uint64(1) << (i & 63)
	b.count++
	return true
}

// clear marks i as free and reports whether that changed anything.
func (b *bitset) clear(i uint32) bool {
	if i >= b.n || !b.test(i) {
		return false
	}
	b.words[i>>6] &^= uint64(1) << (i & 63)
	b.count--
	return true
}

// nextFree returns the first free index at or after start, wrapping once
// through the whole range. The second result is false when the pool is full.
//
// The scan skips fully-occupied 64-bit words outright, so a nearly-full pool
// still finds its remaining gaps in a few hundred word reads.
func (b *bitset) nextFree(start uint32) (uint32, bool) {
	if b.count >= int(b.n) {
		return 0, false
	}
	if start >= b.n {
		start = 0
	}

	if i, ok := b.scan(start, b.n); ok {
		return i, true
	}
	return b.scan(0, start)
}

// scan searches [from, to) for a free index.
func (b *bitset) scan(from, to uint32) (uint32, bool) {
	for i := from; i < to; {
		w := i >> 6
		word := b.words[w]

		if word == ^uint64(0) {
			// Whole word taken: jump to the next word boundary.
			i = (w + 1) << 6
			continue
		}

		// Mask off the bits before i within this word, then take the lowest
		// zero bit that remains.
		masked := word | ((uint64(1) << (i & 63)) - 1)
		if masked != ^uint64(0) {
			cand := (w << 6) + uint32(bits.TrailingZeros64(^masked))
			if cand >= to {
				return 0, false
			}
			return cand, true
		}
		i = (w + 1) << 6
	}
	return 0, false
}

func (b *bitset) used() int { return b.count }
