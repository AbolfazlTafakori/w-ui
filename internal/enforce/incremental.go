package enforce

// Changing the table a customer at a time.
//
// The table holds every customer's counters, quota and chains. Rebuilding it
// whole -- deleting the table and writing it again -- is right when it is
// first written and when something else has removed it, and wrong on every
// other tick: each rebuild makes every counter anew at zero, so whatever any
// customer sent between the last drain and the rebuild is never counted, and
// on a server of twenty thousand customers it is half a million lines for the
// kernel to take in. The quota's `used` was written into the table too, so
// any customer's traffic changed the table's text and rebuilt it: on a busy
// server, every tick. A third of a test's traffic went uncounted that way.
//
// So after the first write the table is changed, not rebuilt. What was
// written for each customer is remembered; each tick what is wanted is
// compared with it, and only what differs is sent, in one nft transaction,
// in an order the kernel accepts: map entries out, the changed chains
// emptied, what is no longer wanted deleted, then what is new added and the
// map entries in. A customer's traffic changes nothing: the kernel counts
// it, the counters are drained, and the quota keeps its own tally.
//
// A quota is written again -- with what the customer has used, from the
// database -- when its limit changes, when the customer's stored usage goes
// down (a renewal, a reset, an operator's edit: they are to be let back in at
// once), and when the kernel's tally and the stored usage have drifted far
// apart (usage on the customer's other servers, which this kernel never
// sees, or an operator raising it). The margins are wide enough that the
// lag between the kernel counting a byte and the database storing it never
// reaches them.

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// clientState is what the table holds for one customer.
type clientState struct {
	files []File   // the files the kernel tells apart, in order
	down  []string // the rules of the download chain
	up    []string // the rules of the upload chain
	quota uint64   // the quota's limit; 0 for none
	// used is the stored usage the quota was last written with, and seen
	// the stored usage on the last tick: a fall in it is a renewal.
	used uint64
	seen uint64
}

// tableState is what the table holds, by customer key.
type tableState map[string]clientState

// desiredState is what the table should hold for these rules.
func desiredState(rules []Rule, caps Caps) (tableState, error) {
	sorted, err := sortedRules(rules)
	if err != nil {
		return nil, err
	}
	owned := ownedFiles(sorted)
	out := make(tableState, len(sorted))
	for _, r := range sorted {
		files := owned[r.Key]
		st := clientState{
			files: files,
			down:  chainRules(r, files, caps, "daddr", downCounter),
			up:    chainRules(r, files, caps, "saddr", upCounter),
			used:  r.UsedBytes,
			seen:  r.UsedBytes,
		}
		if caps.Quota && !r.Unlimited() {
			st.quota = r.QuotaBytes
		}
		out[r.Key] = st
	}
	return out, nil
}

// The margins for writing a quota again when the kernel's tally and the
// stored usage drift apart.
const (
	driftStoredAheadMin  = 256 << 20 // 256 MiB
	driftKernelAheadMin  = 1 << 30   // 1 GiB
	driftStoredAheadPart = 100       // 1% of the limit
	driftKernelAheadPart = 10        // 10% of the limit
)

// needsReseed says whether a customer's quota is to be written again from
// stored usage: stored is what the database says now, prev what it said on
// the last tick, kernel what the kernel's quota has used (ok false when it
// could not be read).
func needsReseed(limit, stored, prev, kernel uint64, ok bool) bool {
	if stored < prev {
		return true // a renewal, a reset, an edit down: let them back in now
	}
	if !ok {
		return false
	}
	seeded := min64(stored, limit)
	switch {
	case seeded > kernel:
		return seeded-kernel > max(driftStoredAheadMin, limit/driftStoredAheadPart)
	case kernel > seeded:
		return kernel-seeded > max(driftKernelAheadMin, limit/driftKernelAheadPart)
	}
	return false
}

// changeScript is the one nft transaction that turns what the table holds
// into what is wanted, and what the table then holds. kernelUsed is what
// each quota has used, as the kernel says (nil when it could not be read).
// An empty script is nothing to change.
func changeScript(have, want tableState, kernelUsed map[string]uint64) (string, tableState) {
	keys := make([]string, 0, len(have)+len(want))
	seen := map[string]bool{}
	for k := range have {
		keys = append(keys, k)
		seen[k] = true
	}
	for k := range want {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	t := "inet " + TableName
	var outEl, flush, delChain, delObj, addObj, addChain, addRule, inEl []string
	next := make(tableState, len(want))

	for _, k := range keys {
		h, had := have[k]
		w, wanted := want[k]

		if !wanted {
			// Gone: its entries, its chains, its objects.
			for _, f := range h.files {
				outEl = append(outEl, f.Addr.String())
			}
			flush = append(flush, downChain(k), upChain(k))
			delChain = append(delChain, downChain(k), upChain(k))
			for _, f := range h.files {
				delObj = append(delObj, "counter "+t+" "+downCounter(k, f.Account), "counter "+t+" "+upCounter(k, f.Account))
			}
			if h.quota > 0 {
				delObj = append(delObj, "quota "+t+" "+quotaName(k))
			}
			continue
		}

		if !had {
			// New: its objects, its chains with their rules, its entries.
			for _, f := range w.files {
				addObj = append(addObj, "counter "+t+" "+downCounter(k, f.Account)+" { }", "counter "+t+" "+upCounter(k, f.Account)+" { }")
			}
			if w.quota > 0 {
				addObj = append(addObj, quotaLine(t, k, w.quota, w.used))
			}
			addChain = append(addChain, downChain(k), upChain(k))
			addRule = append(addRule, rulesFor(t, downChain(k), w.down)...)
			addRule = append(addRule, rulesFor(t, upChain(k), w.up)...)
			for _, f := range w.files {
				inEl = append(inEl, element(f.Addr, k))
			}
			next[k] = w
			continue
		}

		// Kept: what changed, and only that.
		st := w
		st.used = h.used
		reseed := false
		switch {
		case w.quota != h.quota:
			reseed = true
		case w.quota > 0:
			used, ok := kernelUsed[quotaName(k)]
			reseed = needsReseed(w.quota, w.seen, h.seen, used, ok && kernelUsed != nil)
		}
		if reseed {
			st.used = w.used
		}
		rulesChanged := !equalLines(w.down, h.down) || !equalLines(w.up, h.up)
		filesChanged := !equalFiles(w.files, h.files)

		if rulesChanged || reseed || filesChanged {
			// The chains are emptied and written again: a rule names the
			// quota and the counters, so none of those can go while a rule
			// still names it, and a changed one goes in afresh.
			flush = append(flush, downChain(k), upChain(k))
			addRule = append(addRule, rulesFor(t, downChain(k), w.down)...)
			addRule = append(addRule, rulesFor(t, upChain(k), w.up)...)
		}
		if filesChanged {
			hf, wf := fileSet(h.files), fileSet(w.files)
			for _, f := range h.files {
				if _, ok := wf[f.Account]; !ok {
					delObj = append(delObj, "counter "+t+" "+downCounter(k, f.Account), "counter "+t+" "+upCounter(k, f.Account))
				}
			}
			for _, f := range w.files {
				if _, ok := hf[f.Account]; !ok {
					addObj = append(addObj, "counter "+t+" "+downCounter(k, f.Account)+" { }", "counter "+t+" "+upCounter(k, f.Account)+" { }")
				}
			}
			// Every entry of the customer is taken out and put back: an
			// address that moved from one file to another, or to another
			// customer, is put back pointing where it now belongs.
			for _, f := range h.files {
				outEl = append(outEl, f.Addr.String())
			}
			for _, f := range w.files {
				inEl = append(inEl, element(f.Addr, k))
			}
		}
		if reseed {
			if h.quota > 0 {
				delObj = append(delObj, "quota "+t+" "+quotaName(k))
			}
			if w.quota > 0 {
				addObj = append(addObj, quotaLine(t, k, w.quota, w.used))
			}
		}
		next[k] = st
	}

	if len(outEl)+len(flush)+len(delChain)+len(delObj)+len(addObj)+len(addChain)+len(addRule)+len(inEl) == 0 {
		return "", next
	}

	var b strings.Builder
	if len(outEl) > 0 {
		outEl = dedupeStrings(outEl)
		fmt.Fprintf(&b, "delete element %s dl { %s }\n", t, strings.Join(outEl, ", "))
		fmt.Fprintf(&b, "delete element %s ul { %s }\n", t, strings.Join(outEl, ", "))
	}
	for _, c := range dedupeStrings(flush) {
		fmt.Fprintf(&b, "flush chain %s %s\n", t, c)
	}
	for _, c := range delChain {
		fmt.Fprintf(&b, "delete chain %s %s\n", t, c)
	}
	for _, o := range delObj {
		fmt.Fprintf(&b, "delete %s\n", o)
	}
	for _, o := range addObj {
		fmt.Fprintf(&b, "add %s\n", o)
	}
	for _, c := range addChain {
		fmt.Fprintf(&b, "add chain %s %s\n", t, c)
	}
	for _, r := range addRule {
		fmt.Fprintf(&b, "add rule %s\n", r)
	}
	if len(inEl) > 0 {
		var dl, ul []string
		for _, e := range inEl {
			addr, key, _ := strings.Cut(e, "|")
			dl = append(dl, addr+" : jump "+downChain(key))
			ul = append(ul, addr+" : jump "+upChain(key))
		}
		fmt.Fprintf(&b, "add element %s dl { %s }\n", t, strings.Join(dl, ", "))
		fmt.Fprintf(&b, "add element %s ul { %s }\n", t, strings.Join(ul, ", "))
	}
	return b.String(), next
}

func quotaLine(t, key string, limit, used uint64) string {
	// As the full table writes it: what has been used, never past the limit.
	return fmt.Sprintf("quota %s %s { over %d bytes used %d bytes }", t, quotaName(key), limit, min64(used, limit))
}

func rulesFor(t, chain string, rules []string) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, t+" "+chain+" "+r)
	}
	return out
}

// element is a map entry to add, as address|key.
func element(a netip.Addr, key string) string { return a.String() + "|" + key }

func fileSet(files []File) map[uint]netip.Addr {
	out := make(map[uint]netip.Addr, len(files))
	for _, f := range files {
		out[f.Account] = f.Addr
	}
	return out
}

func equalFiles(a, b []File) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
