package enforce

import "sort"

// Checking that the kernel still holds what we last wrote.
//
// The enforcer skips an apply whose text matches the last one, so a steady
// server does not reload its firewall every two seconds. That is worth having,
// but on its own it makes the panel trust a memory of the kernel rather than
// the kernel. Anything that clears the table behind us -- a firewall package's
// own reload, an operator running `nft flush ruleset`, a second panel on the
// same host, a container restart -- leaves the cache saying "already applied"
// and the kernel enforcing nothing. Every customer then runs unmetered and
// uncapped until something unrelated happens to change the ruleset text.
//
// The counters are drained every tick anyway, and a counter object is listed
// whether or not a byte has passed through it. So the tick already carries the
// answer to "is our ruleset still there", for free.

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
