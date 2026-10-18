// Package idset is the few things done to a list of row ids: keeping each
// once, adding or taking some away, and asking whether two lists hold the
// same ones. Kept in the order given, so two identical requests write
// identical rows.
package idset

// Unique keeps the first occurrence of each id, and no zero.
func Unique(ids []uint) []uint {
	out := make([]uint, 0, len(ids))
	seen := map[uint]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Combine is the list have becomes once change is added to it, or taken
// away from it.
func Combine(have, change []uint, add bool) []uint {
	in := map[uint]bool{}
	for _, id := range have {
		in[id] = true
	}
	for _, id := range change {
		if add {
			in[id] = true
		} else {
			delete(in, id)
		}
	}

	out := make([]uint, 0, len(in))
	// Built from the two inputs rather than by ranging the map, so the result
	// is in a stable order and two identical requests produce identical rows.
	for _, id := range have {
		if in[id] {
			out = append(out, id)
			in[id] = false
		}
	}
	for _, id := range change {
		if in[id] {
			out = append(out, id)
			in[id] = false
		}
	}
	return out
}

// Same reports whether a and b hold the same ids, as many times each, in
// any order.
func Same(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[uint]int{}
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
