package enforce

import (
	"fmt"
	"math/rand"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// kernelModel is the panel's table as the kernel holds it, enough to apply
// both the whole table and a change to it, and to refuse what the kernel
// refuses: an object or a chain deleted while something still names it, an
// entry deleted that is not there or added twice, a rule naming something
// that does not exist.
type kernelModel struct {
	counters map[string]bool
	quotas   map[string][2]uint64 // name -> over, used
	chains   map[string][]string
	maps     map[string]map[string]string // dl/ul -> addr -> chain
}

func newModel() *kernelModel {
	return &kernelModel{counters: map[string]bool{}, quotas: map[string][2]uint64{},
		chains: map[string][]string{}, maps: map[string]map[string]string{"dl": {}, "ul": {}}}
}

// loadFull reads a whole table as BuildRulesetWithCaps writes it.
func loadFull(t *testing.T, script string) *kernelModel {
	t.Helper()
	m := newModel()
	var chain, mapName string
	for _, raw := range strings.Split(script, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "add table") || strings.HasPrefix(line, "delete table") ||
			strings.HasPrefix(line, "table ") || strings.HasPrefix(line, "type "):
		case line == "}":
			chain, mapName = "", ""
		case chain != "":
			// Inside a chain every line is a rule, a quota one included.
			if !isBaseChain(chain) {
				m.chains[chain] = append(m.chains[chain], line)
			}
		case strings.HasPrefix(line, "counter "):
			m.counters[strings.Fields(line)[1]] = true
		case strings.HasPrefix(line, "quota "):
			f := strings.Fields(line)
			over, _ := strconv.ParseUint(f[4], 10, 64)
			used, _ := strconv.ParseUint(f[7], 10, 64)
			m.quotas[f[1]] = [2]uint64{over, used}
		case strings.HasPrefix(line, "chain "):
			chain = strings.Fields(line)[1]
			if !isBaseChain(chain) {
				m.chains[chain] = []string{}
			}
		case strings.HasPrefix(line, "map "):
			mapName = strings.Fields(line)[1]
		case strings.HasPrefix(line, "elements = {"):
			body := strings.TrimSuffix(strings.TrimPrefix(line, "elements = { "), " }")
			for _, e := range strings.Split(body, ", ") {
				addr, target, _ := strings.Cut(e, " : jump ")
				m.maps[mapName][addr] = target
			}
		}
	}
	return m
}

func isBaseChain(c string) bool { return c == "forward" || c == "input" || c == "output" }

// apply runs a change script, refusing what the kernel would refuse.
func (m *kernelModel) apply(script string) error {
	for _, line := range strings.Split(strings.TrimSpace(script), "\n") {
		if line == "" {
			continue
		}
		if err := m.applyLine(line); err != nil {
			return fmt.Errorf("%q: %w", line, err)
		}
	}
	return nil
}

func (m *kernelModel) applyLine(line string) error {
	const t = "inet " + TableName + " "
	switch {
	case strings.HasPrefix(line, "delete element "+t):
		name, elems := elementArgs(line, "delete element "+t)
		for _, e := range elems {
			if _, ok := m.maps[name][e]; !ok {
				return fmt.Errorf("no element %s in %s", e, name)
			}
			delete(m.maps[name], e)
		}
	case strings.HasPrefix(line, "add element "+t):
		name, elems := elementArgs(line, "add element "+t)
		for _, e := range elems {
			addr, target, _ := strings.Cut(e, " : jump ")
			if _, ok := m.maps[name][addr]; ok {
				return fmt.Errorf("element %s already in %s", addr, name)
			}
			if _, ok := m.chains[target]; !ok {
				return fmt.Errorf("element jumps to missing chain %s", target)
			}
			m.maps[name][addr] = target
		}
	case strings.HasPrefix(line, "flush chain "+t):
		c := strings.TrimPrefix(line, "flush chain "+t)
		if _, ok := m.chains[c]; !ok {
			return fmt.Errorf("no chain %s", c)
		}
		m.chains[c] = []string{}
	case strings.HasPrefix(line, "delete chain "+t):
		c := strings.TrimPrefix(line, "delete chain "+t)
		if len(m.chains[c]) > 0 {
			return fmt.Errorf("chain %s still has rules", c)
		}
		for name, mp := range m.maps {
			for addr, target := range mp {
				if target == c {
					return fmt.Errorf("chain %s still named by %s in %s", c, addr, name)
				}
			}
		}
		delete(m.chains, c)
	case strings.HasPrefix(line, "delete counter "+t), strings.HasPrefix(line, "delete quota "+t):
		f := strings.Fields(line)
		name := f[4]
		if m.named(name) {
			return fmt.Errorf("%s still named by a rule", name)
		}
		if f[1] == "counter" {
			if !m.counters[name] {
				return fmt.Errorf("no counter %s", name)
			}
			delete(m.counters, name)
		} else {
			if _, ok := m.quotas[name]; !ok {
				return fmt.Errorf("no quota %s", name)
			}
			delete(m.quotas, name)
		}
	case strings.HasPrefix(line, "add counter "+t):
		name := strings.Fields(line)[4]
		if m.counters[name] {
			return fmt.Errorf("counter %s already there", name)
		}
		m.counters[name] = true
	case strings.HasPrefix(line, "add quota "+t):
		f := strings.Fields(line)
		over, _ := strconv.ParseUint(f[7], 10, 64)
		used, _ := strconv.ParseUint(f[10], 10, 64)
		if _, ok := m.quotas[f[4]]; ok {
			return fmt.Errorf("quota %s already there", f[4])
		}
		m.quotas[f[4]] = [2]uint64{over, used}
	case strings.HasPrefix(line, "add chain "+t):
		c := strings.TrimPrefix(line, "add chain "+t)
		if _, ok := m.chains[c]; ok {
			return fmt.Errorf("chain %s already there", c)
		}
		m.chains[c] = []string{}
	case strings.HasPrefix(line, "add rule "+t):
		rest := strings.TrimPrefix(line, "add rule "+t)
		c, rule, _ := strings.Cut(rest, " ")
		if _, ok := m.chains[c]; !ok {
			return fmt.Errorf("no chain %s", c)
		}
		if name := namedIn(rule); name != "" && !m.counters[name] {
			if _, ok := m.quotas[name]; !ok {
				return fmt.Errorf("rule names missing %s", name)
			}
		}
		m.chains[c] = append(m.chains[c], rule)
	default:
		return fmt.Errorf("a line the model does not know")
	}
	return nil
}

func elementArgs(line, prefix string) (string, []string) {
	rest := strings.TrimPrefix(line, prefix)
	name, body, _ := strings.Cut(rest, " { ")
	body = strings.TrimSuffix(body, " }")
	return name, strings.Split(body, ", ")
}

// namedIn is the counter or quota a rule names, if any.
func namedIn(rule string) string {
	_, after, ok := strings.Cut(rule, " name \"")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(after, "\"")
	return name
}

func (m *kernelModel) named(name string) bool {
	for _, rules := range m.chains {
		for _, r := range rules {
			if namedIn(r) == name {
				return true
			}
		}
	}
	return false
}

// sameAs says how m differs from want, quota usage aside unless usedToo.
func (m *kernelModel) sameAs(want *kernelModel, usedToo bool) string {
	var diffs []string
	for c := range want.counters {
		if !m.counters[c] {
			diffs = append(diffs, "missing counter "+c)
		}
	}
	for c := range m.counters {
		if !want.counters[c] {
			diffs = append(diffs, "extra counter "+c)
		}
	}
	for q, v := range want.quotas {
		got, ok := m.quotas[q]
		switch {
		case !ok:
			diffs = append(diffs, "missing quota "+q)
		case got[0] != v[0]:
			diffs = append(diffs, fmt.Sprintf("quota %s over %d, want %d", q, got[0], v[0]))
		case usedToo && got[1] != v[1]:
			diffs = append(diffs, fmt.Sprintf("quota %s used %d, want %d", q, got[1], v[1]))
		}
	}
	for q := range m.quotas {
		if _, ok := want.quotas[q]; !ok {
			diffs = append(diffs, "extra quota "+q)
		}
	}
	for c, rules := range want.chains {
		got, ok := m.chains[c]
		if !ok {
			diffs = append(diffs, "missing chain "+c)
		} else if strings.Join(got, "|") != strings.Join(rules, "|") {
			diffs = append(diffs, fmt.Sprintf("chain %s is %q, want %q", c, got, rules))
		}
	}
	for c := range m.chains {
		if _, ok := want.chains[c]; !ok {
			diffs = append(diffs, "extra chain "+c)
		}
	}
	for name, mp := range want.maps {
		for a, c := range mp {
			if m.maps[name][a] != c {
				diffs = append(diffs, fmt.Sprintf("%s %s -> %q, want %s", name, a, m.maps[name][a], c))
			}
		}
		for a := range m.maps[name] {
			if _, ok := mp[a]; !ok {
				diffs = append(diffs, fmt.Sprintf("extra %s %s", name, a))
			}
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 8 {
		diffs = append(diffs[:8], fmt.Sprintf("... %d more", len(diffs)-8))
	}
	return strings.Join(diffs, "\n")
}

// start is the table written whole for rules, and what is remembered of it.
func start(t *testing.T, rules []Rule, caps Caps) (*kernelModel, tableState) {
	t.Helper()
	script, err := BuildRulesetWithCaps(rules, caps)
	if err != nil {
		t.Fatal(err)
	}
	st, err := desiredState(rules, caps)
	if err != nil {
		t.Fatal(err)
	}
	return loadFull(t, script), st
}

// step changes the table to rules and checks it now holds what writing it
// whole would have.
func step(t *testing.T, m *kernelModel, have tableState, rules []Rule, caps Caps, kernelUsed map[string]uint64) (string, tableState) {
	t.Helper()
	want, err := desiredState(rules, caps)
	if err != nil {
		t.Fatal(err)
	}
	script, next := changeScript(have, want, kernelUsed)
	if err := m.apply(script); err != nil {
		t.Fatalf("the kernel would refuse the change: %v\nscript:\n%s", err, script)
	}
	full, _ := BuildRulesetWithCaps(rules, caps)
	if d := m.sameAs(loadFull(t, full), false); d != "" {
		t.Fatalf("after the change the table is not what writing it whole gives:\n%s\nscript:\n%s", d, script)
	}
	return script, next
}

func rule(id uint, quota, used uint64, addrs ...string) Rule {
	r := Rule{Key: Key(id), QuotaBytes: quota, UsedBytes: used}
	for i, a := range addrs {
		r.Files = append(r.Files, File{Account: id*100 + uint(i) + 1, Addr: netip.MustParseAddr(a)})
	}
	return r
}

// Traffic is not a change: a customer whose stored usage grew sends the
// kernel nothing.
func TestTrafficChangesNothing(t *testing.T) {
	caps := FullCaps()
	rules := []Rule{rule(1, 10<<30, 5<<20, "10.0.0.2"), rule(2, 0, 7<<20, "10.0.0.3", "10.0.0.4")}
	m, st := start(t, rules, caps)
	rules[0].UsedBytes += 300 << 20
	rules[1].UsedBytes += 900 << 20
	kernel := map[string]uint64{quotaName("c1"): 305 << 20}
	script, _ := step(t, m, st, rules, caps, kernel)
	if script != "" {
		t.Fatalf("traffic changed the table:\n%s", script)
	}
}

// A renewal lets the customer back in at once: stored usage went down, so
// the quota is written again from it.
func TestARenewalWritesTheQuotaAgain(t *testing.T) {
	caps := FullCaps()
	rules := []Rule{rule(1, 1<<30, 1<<30, "10.0.0.2"), rule(2, 1<<30, 5, "10.0.0.3")}
	m, st := start(t, rules, caps)
	rules[0].UsedBytes = 0
	script, _ := step(t, m, st, rules, caps, map[string]uint64{"q_c1": 1 << 30, "q_c2": 5})
	if got := m.quotas["q_c1"]; got != [2]uint64{1 << 30, 0} {
		t.Fatalf("after a renewal the quota is %v\n%s", got, script)
	}
	if strings.Contains(script, "c2") {
		t.Fatalf("a customer who was not renewed was touched:\n%s", script)
	}
}

// Usage on the customer's other servers, which this kernel never sees, is
// written into the quota once it is more than the margin.
func TestUsageElsewhereIsWrittenInPastTheMargin(t *testing.T) {
	caps := FullCaps()
	limit := uint64(100 << 30)
	rules := []Rule{rule(1, limit, 0, "10.0.0.2")}
	m, st := start(t, rules, caps)

	rules[0].UsedBytes = 200 << 20 // under the 1 GiB margin (1% of 100 GiB)
	script, st := step(t, m, st, rules, caps, map[string]uint64{"q_c1": 0})
	if script != "" {
		t.Fatalf("written within the margin:\n%s", script)
	}
	rules[0].UsedBytes = 2 << 30
	step(t, m, st, rules, caps, map[string]uint64{"q_c1": 0})
	if got := m.quotas["q_c1"][1]; got != 2<<30 {
		t.Fatalf("quota used %d after usage elsewhere, want %d", got, uint64(2<<30))
	}
}

func TestNeedsReseed(t *testing.T) {
	const G = uint64(1 << 30)
	for _, tc := range []struct {
		name                        string
		limit, stored, prev, kernel uint64
		ok, want                    bool
	}{
		{"usage went down", 10 * G, 1, 5 * G, 5 * G, true, true},
		{"usage went down, kernel unread", 10 * G, 1, 5 * G, 0, false, true},
		{"in step", 10 * G, 5 * G, 5 * G, 5*G + 100<<20, true, false},
		{"stored ahead a little", 10 * G, 5*G + 200<<20, 5 * G, 5 * G, true, false},
		{"stored ahead past the margin", 10 * G, 6 * G, 5 * G, 5 * G, true, true},
		{"kernel ahead within 1 GiB", 10 * G, 5 * G, 5 * G, 5*G + 900<<20, true, false},
		{"kernel ahead past 1 GiB", 10 * G, 5 * G, 5 * G, 7 * G, true, true},
		{"over the limit, kernel counting dropped bytes", 10 * G, 12 * G, 12 * G, 10*G + 500<<20, true, false},
		{"unread and steady", 10 * G, 5 * G, 5 * G, 0, false, false},
	} {
		if got := needsReseed(tc.limit, tc.stored, tc.prev, tc.kernel, tc.ok); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// An address that moves to another customer points at its new owner's
// chains, and nothing is left behind.
func TestAnAddressMovesBetweenCustomers(t *testing.T) {
	caps := FullCaps()
	rules := []Rule{rule(1, 0, 0, "10.0.0.2", "10.0.0.3"), rule(2, 0, 0, "10.0.0.4")}
	m, st := start(t, rules, caps)
	rules = []Rule{rule(1, 0, 0, "10.0.0.2"), rule(2, 0, 0, "10.0.0.4", "10.0.0.3")}
	step(t, m, st, rules, caps, nil)
	if m.maps["dl"]["10.0.0.3"] != "cd_c2" {
		t.Fatalf("10.0.0.3 jumps to %q", m.maps["dl"]["10.0.0.3"])
	}
}

// Every kind of change, at random, many times over: after each, the table
// is what writing it whole would give, and the kernel refused nothing.
func TestRandomChangesKeepTheTableExact(t *testing.T) {
	for _, caps := range []Caps{FullCaps(), {Quota: false}} {
		rng := rand.New(rand.NewSource(7))
		pool := make([]netip.Addr, 0, 64)
		for i := 2; i < 66; i++ {
			pool = append(pool, netip.MustParseAddr(fmt.Sprintf("10.9.0.%d", i)))
		}
		type cust struct {
			quota, used, rate uint64
			blocked           bool
			addrs             []netip.Addr
		}
		custs := map[uint]*cust{}
		build := func() []Rule {
			var out []Rule
			for id, c := range custs {
				r := Rule{Key: Key(id), QuotaBytes: c.quota, UsedBytes: c.used, RateBitsPerSec: c.rate, Blocked: c.blocked}
				for i, a := range c.addrs {
					r.Files = append(r.Files, File{Account: id*100 + uint(i) + 1, Addr: a})
				}
				out = append(out, r)
			}
			return out
		}
		for id := uint(1); id <= 8; id++ {
			custs[id] = &cust{quota: uint64(rng.Intn(3)) << 30, addrs: []netip.Addr{pool[id]}}
		}
		m, st := start(t, build(), caps)
		for i := 0; i < 400; i++ {
			ids := make([]uint, 0, len(custs))
			for id := range custs {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
			pick := func() *cust {
				if len(ids) == 0 {
					return nil
				}
				return custs[ids[rng.Intn(len(ids))]]
			}
			switch rng.Intn(10) {
			case 0: // a new customer
				id := uint(9 + rng.Intn(40))
				if custs[id] == nil {
					custs[id] = &cust{quota: uint64(rng.Intn(3)) << 30, addrs: []netip.Addr{pool[rng.Intn(len(pool))]}}
				}
			case 1: // one goes
				if len(ids) > 0 {
					delete(custs, ids[rng.Intn(len(ids))])
				}
			case 2: // switched off or on
				if c := pick(); c != nil {
					c.blocked = !c.blocked
				}
			case 3: // a plan changes
				if c := pick(); c != nil {
					c.quota = uint64(rng.Intn(4)) << 30
				}
			case 4: // a rate limit
				if c := pick(); c != nil {
					c.rate = uint64(rng.Intn(2)) * 10_000_000
				}
			case 5: // a device added, perhaps on an address someone holds
				if c := pick(); c != nil {
					c.addrs = append(c.addrs, pool[rng.Intn(len(pool))])
				}
			case 6: // a device removed
				if c := pick(); c != nil && len(c.addrs) > 0 {
					c.addrs = c.addrs[:len(c.addrs)-1]
				}
			case 7: // renewed
				if c := pick(); c != nil {
					c.used = 0
				}
			default: // traffic
				for _, id := range ids {
					custs[id].used += uint64(rng.Intn(50)) << 20
				}
			}
			kernel := map[string]uint64{}
			for id, c := range custs {
				kernel[quotaName(Key(id))] = c.used
			}
			_, st = step(t, m, st, build(), caps, kernel)
		}
	}
}
