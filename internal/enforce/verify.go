package enforce

import "sort"

// Checking that the kernel still holds what we last wrote.
//
// The enforcer skips an apply whose text matches the last one, so a steady
// server does not reload its firewall every two seconds. That is worth having,
// but on its own it makes the panel trust a memory of the kernel rather than
// the kernel.
//
// A table that has been deleted outright is already covered: the next nft call
// fails, and a failing call drops the cached script. The gap is the quiet case
// -- the table is still there and our rules are not, because something else
// rewrote its contents. Nothing fails, so nothing is noticed, and every
// customer runs unmetered and uncapped until an unrelated change happens to
// alter the ruleset text.

// missingKeys reports which of the keys we last applied the kernel no longer
// has, so the caller can rewrite instead of trusting its cache.
//
// Sorted, because it goes into a log line an operator reads.
func missingKeys(applied map[string]struct{}, seen []Usage) []string {
	if len(applied) == 0 {
		return nil
	}

	have := make(map[string]struct{}, len(seen))
	for _, u := range seen {
		have[u.Key] = struct{}{}
	}

	var gone []string
	for k := range applied {
		if _, ok := have[k]; !ok {
			gone = append(gone, k)
		}
	}
	sort.Strings(gone)
	return gone
}

// ruleKeys is the set of keys a ruleset covers.
func ruleKeys(rules []Rule) map[string]struct{} {
	out := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		out[r.Key] = struct{}{}
	}
	return out
}
