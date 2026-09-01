package enforce

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// TableName is the nftables table the panel owns entirely. Nothing else should
// write to it: the panel replaces its contents wholesale on every apply.
const TableName = "wui"

// Key derives the stable nftables identifier for a client.
//
// Identifiers are built from the numeric id rather than from any operator-typed
// text, so nothing a customer or an admin can name ever reaches the generated
// script. That is what makes string-building safe here.
func Key(clientID uint) string { return fmt.Sprintf("c%d", clientID) }

func quotaName(key string) string   { return "q_" + key }
func counterName(key string) string { return "n_" + key }
func chainName(key string) string   { return "cl_" + key }

// validKey reports whether a key is one this package generated.
func validKey(k string) bool {
	if len(k) < 2 || len(k) > 24 || k[0] != 'c' {
		return false
	}
	for _, r := range k[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Caps describes what the running kernel can actually do.
//
// Not every kernel ships nft_quota — WSL2, some OpenVZ and LXC hosts, and a few
// stripped cloud images omit it. Because `nft -f` is atomic, a single
// unsupported quota object rejects the whole program, which would leave the
// server with no enforcement at all rather than partial enforcement. Knowing
// the capability up front lets the panel emit a program the kernel will accept
// and say plainly what it cannot do.
type Caps struct {
	// Quota is whether named quota objects can be created. Without it there is
	// no byte-exact volume cap; counting and blocking still work.
	Quota bool
}

// FullCaps is a kernel with everything the enforcer wants.
func FullCaps() Caps { return Caps{Quota: true} }

// BuildRuleset renders the program for a fully capable kernel.
func BuildRuleset(rules []Rule) (string, error) {
	return BuildRulesetWithCaps(rules, FullCaps())
}

// BuildRulesetWithCaps renders the complete nftables program for the given
// rules, using only features the kernel supports.
//
// The script is declarative and total: it deletes the table and recreates it,
// so applying it makes the kernel match `rules` exactly with no diffing and no
// possibility of a stale rule surviving. `nft -f` runs the whole thing in one
// transaction, so there is never a moment where some customers are metered and
// others are not.
func BuildRulesetWithCaps(rules []Rule, caps Caps) (string, error) {
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	var b strings.Builder

	// Deleting a table that does not exist is an error that would abort the
	// transaction, so it is created first and then destroyed. This is the
	// standard idiom for an idempotent flush.
	fmt.Fprintf(&b, "add table inet %s\n", TableName)
	fmt.Fprintf(&b, "delete table inet %s\n", TableName)
	fmt.Fprintf(&b, "table inet %s {\n", TableName)

	for _, r := range sorted {
		if !validKey(r.Key) {
			return "", fmt.Errorf("%w: rule key %q is not one we generate", ErrInvalidRule, r.Key)
		}

		// The reporting counter is separate from the quota on purpose. The
		// quota is cumulative and only cleared on renewal; the counter is
		// drained on every collection tick and folded into the history.
		fmt.Fprintf(&b, "\tcounter %s { }\n", counterName(r.Key))

		if caps.Quota && !r.Unlimited() {
			// Seeding `used` is what lets a reboot resume where the customer
			// left off instead of handing their allowance back.
			fmt.Fprintf(&b, "\tquota %s { over %d bytes used %d bytes }\n",
				quotaName(r.Key), r.QuotaBytes, min64(r.UsedBytes, r.QuotaBytes))
		}
	}

	b.WriteString("\n")
	for _, r := range sorted {
		fmt.Fprintf(&b, "\tchain %s {\n", chainName(r.Key))
		switch {
		case r.Blocked:
			// An admin switched this client off: nothing else needs evaluating.
			b.WriteString("\t\tdrop\n")
		case r.Unlimited(), !caps.Quota:
			// Without kernel quota support the client is still counted, and the
			// reconciler cuts them off once the stored total crosses the limit —
			// a tick late instead of a packet late.
			fmt.Fprintf(&b, "\t\tcounter name \"%s\"\n", counterName(r.Key))
		default:
			// Order matters. `drop` ends rule evaluation, so once the quota is
			// over the counter below is never reached and dropped bytes are not
			// billed as usage.
			fmt.Fprintf(&b, "\t\tquota name \"%s\" drop\n", quotaName(r.Key))
			fmt.Fprintf(&b, "\t\tcounter name \"%s\"\n", counterName(r.Key))
		}
		b.WriteString("\t}\n")
	}

	dl, ul := mapElements(sorted)

	b.WriteString("\n")
	fmt.Fprintf(&b, "\tmap dl {\n\t\ttype ipv4_addr : verdict\n")
	if dl != "" {
		fmt.Fprintf(&b, "\t\telements = { %s }\n", dl)
	}
	b.WriteString("\t}\n")
	fmt.Fprintf(&b, "\tmap ul {\n\t\ttype ipv4_addr : verdict\n")
	if ul != "" {
		fmt.Fprintf(&b, "\t\telements = { %s }\n", ul)
	}
	b.WriteString("\t}\n")

	// A verdict map is a hash lookup, so the cost of this chain does not grow
	// with the number of customers: ten thousand clients cost the same probe as
	// three. A rule per client would be a linear scan on every packet.
	b.WriteString("\n\tchain forward {\n")
	b.WriteString("\t\ttype filter hook forward priority filter; policy accept;\n")
	b.WriteString("\t\tip daddr vmap @dl\n")
	b.WriteString("\t\tip saddr vmap @ul\n")
	b.WriteString("\t}\n")

	b.WriteString("}\n")
	return b.String(), nil
}

// mapElements renders the address-to-chain entries for both directions.
//
// Download and upload share one chain per client, so a customer's traffic is
// counted against a single quota whichever way it flows — which is what makes
// the allowance apply to the client rather than to each direction separately.
func mapElements(rules []Rule) (dl, ul string) {
	var dlParts, ulParts []string
	seen := map[netip.Addr]bool{}

	for _, r := range rules {
		for _, a := range r.Addrs {
			a = a.Unmap()
			// The map is keyed on ipv4_addr; a v6 address would not fit and
			// silently corrupt the element list.
			if !a.Is4() || seen[a] {
				continue
			}
			seen[a] = true
			entry := fmt.Sprintf("%s : jump %s", a.String(), chainName(r.Key))
			dlParts = append(dlParts, entry)
			ulParts = append(ulParts, entry)
		}
	}
	return strings.Join(dlParts, ", "), strings.Join(ulParts, ", ")
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
