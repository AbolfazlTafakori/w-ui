package enforce

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/shaper"
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

func quotaName(key string) string { return "q_" + key }

// Counters come in pairs, one per direction, and one pair per file.
//
// A counter's name carries both the client and the file -- nd_c7_a12 is what
// account 12 of client 7 received -- so a drained counter says whose it is
// on its own. Nothing has to be remembered between applying a ruleset and
// reading it back, which is what keeps a panel restart from losing the bytes
// counted while it was down.
const (
	downPrefix = "nd_"
	upPrefix   = "nu_"
)

func downCounter(key string, account uint) string { return fileCounter(downPrefix, key, account) }
func upCounter(key string, account uint) string   { return fileCounter(upPrefix, key, account) }

func fileCounter(prefix, key string, account uint) string {
	return prefix + key + "_a" + strconv.FormatUint(uint64(account), 10)
}

func downChain(key string) string { return "cd_" + key }
func upChain(key string) string   { return "cu_" + key }

// validKey reports whether a key is one this package generated.
func validKey(k string) bool {
	if len(k) < 2 || len(k) > 24 || k[0] != 'c' {
		return false
	}
	return digits(k[1:])
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseCounter takes a counter's name apart: which client, which file and
// which direction. A name with no file part is a whole client's counter, as
// a panel before per-file counting wrote them; account is zero for it.
func parseCounter(name string) (key string, account uint, down, ok bool) {
	switch {
	case strings.HasPrefix(name, downPrefix):
		name, down = name[len(downPrefix):], true
	case strings.HasPrefix(name, upPrefix):
		name = name[len(upPrefix):]
	default:
		return "", 0, false, false
	}
	key, file, hasFile := strings.Cut(name, "_a")
	if !validKey(key) {
		return "", 0, false, false
	}
	if !hasFile {
		return key, 0, down, true
	}
	if !digits(file) {
		return "", 0, false, false
	}
	n, err := strconv.ParseUint(file, 10, 64)
	if err != nil || n == 0 {
		return "", 0, false, false
	}
	return key, uint(n), down, true
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
	sorted, err := sortedRules(rules)
	if err != nil {
		return "", err
	}
	owned := ownedFiles(sorted)

	var b strings.Builder

	// Deleting a table that does not exist is an error that would abort the
	// transaction, so it is created first and then destroyed. This is the
	// standard idiom for an idempotent flush.
	fmt.Fprintf(&b, "add table inet %s\n", TableName)
	fmt.Fprintf(&b, "delete table inet %s\n", TableName)
	fmt.Fprintf(&b, "table inet %s {\n", TableName)

	for _, r := range sorted {
		// The reporting counters are separate from the quota on purpose. The
		// quota is cumulative and only cleared on renewal; the counters are
		// drained on every collection tick and folded into the history.
		//
		// Two per file, because "how much have I uploaded" is a different
		// question from "how much have I left", and the kernel is the only
		// thing in a position to tell the two directions apart.
		for _, f := range owned[r.Key] {
			fmt.Fprintf(&b, "\tcounter %s { }\n", downCounter(r.Key, f.Account))
			fmt.Fprintf(&b, "\tcounter %s { }\n", upCounter(r.Key, f.Account))
		}

		if caps.Quota && !r.Unlimited() {
			// Seeding `used` is what lets a reboot resume where the customer
			// left off instead of handing their allowance back.
			fmt.Fprintf(&b, "\tquota %s { over %d bytes used %d bytes }\n",
				quotaName(r.Key), r.QuotaBytes, min64(r.UsedBytes, r.QuotaBytes))
		}
	}

	b.WriteString("\n")
	for _, r := range sorted {
		// One chain per direction, identical but for the side of the packet
		// its files are matched on and the counters the bytes land in. The
		// quota is shared between them: an allowance is spent in both
		// directions and is one number.
		writeClientChain(&b, r, owned[r.Key], caps, downChain(r.Key), "daddr", downCounter)
		writeClientChain(&b, r, owned[r.Key], caps, upChain(r.Key), "saddr", upCounter)
	}

	dl, ul := mapElements(sorted, owned)

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

	// A verdict map is a hash lookup, so the cost of these chains does not grow
	// with the number of customers: ten thousand clients cost the same probe as
	// three. A rule per client would be a linear scan on every packet.
	b.WriteString("\n\tchain forward {\n")
	b.WriteString("\t\ttype filter hook forward priority filter; policy accept;\n")
	b.WriteString("\t\tip daddr vmap @dl\n")
	b.WriteString("\t\tip saddr vmap @ul\n")
	b.WriteString("\t}\n")

	// Traffic that ends at this machine rather than passing through it never
	// reaches the forward hook. Without these two chains a customer's dealings
	// with the server itself are neither billed nor stopped: a resolver running
	// here would carry their data for free, and a customer who has been cut off
	// would still reach every service on the box, the panel included.
	//
	// A packet is seen by exactly one of the three hooks, so nothing is counted
	// twice. Only the customer's own side of each is matched — the other address
	// is the server's and is not in the map.
	b.WriteString("\n\tchain input {\n")
	b.WriteString("\t\ttype filter hook input priority filter; policy accept;\n")
	b.WriteString("\t\tip saddr vmap @ul\n")
	b.WriteString("\t}\n")

	b.WriteString("\n\tchain output {\n")
	b.WriteString("\t\ttype filter hook output priority filter; policy accept;\n")
	b.WriteString("\t\tip daddr vmap @dl\n")
	b.WriteString("\t}\n")

	b.WriteString("}\n")
	return b.String(), nil
}

// sortedRules copies the rules in key order and refuses any key this package
// did not generate.
func sortedRules(rules []Rule) ([]Rule, error) {
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, r := range sorted {
		if !validKey(r.Key) {
			return nil, fmt.Errorf("%w: rule key %q is not one we generate", ErrInvalidRule, r.Key)
		}
	}
	return sorted, nil
}

// ownedFiles decides which files the kernel will tell apart: for each rule,
// its files whose address reaches its chains.
//
// The maps are keyed on ipv4_addr, so a v6 address is left out -- it would
// not fit and would corrupt the element list. An address can be in the maps
// once only, so of two files with the same address the first, in key order,
// keeps it. A file left out reaches no chain, and so no counter of its own
// could ever move: it gets none, and the client's usage is exactly the sum of
// the counters it does have.
func ownedFiles(sorted []Rule) map[string][]File {
	out := make(map[string][]File, len(sorted))
	seen := map[netip.Addr]bool{}
	for _, r := range sorted {
		for _, f := range r.Files {
			a := f.Addr.Unmap()
			if !a.Is4() || seen[a] || f.Account == 0 {
				continue
			}
			seen[a] = true
			out[r.Key] = append(out[r.Key], File{Account: f.Account, Addr: a})
		}
	}
	return out
}

// writeClientChain emits one customer's chain for one direction.
//
// Both directions get the same treatment, so they are written by the same code:
// a rate limit that applied only to downloads, or a block that stopped only
// uploads, would be a very quiet way to give service away.
//
// side is the half of the packet that is the customer's: the destination of
// what they receive, the source of what they send. It is what the map that
// jumped here was keyed on, so every packet in the chain matches exactly one
// of the file rules below and lands in exactly one counter.
func writeClientChain(b *strings.Builder, r Rule, files []File, caps Caps, chain, side string,
	counter func(key string, account uint) string) {
	fmt.Fprintf(b, "\tchain %s {\n", chain)

	// Stamp the packet with its traffic class before anything else. HTB
	// reads this stamp directly, so a rate limit needs no tc filter and the
	// cost of classifying stays flat as customers are added — a filter list
	// would be walked once per packet per customer. A blocked client is
	// about to be dropped and needs no class.
	if !r.Blocked && r.RateBitsPerSec > 0 {
		if minor, err := shaper.Minor(r.Key); err == nil {
			fmt.Fprintf(b, "\t\tmeta priority set %s\n", shaper.ClassID(minor))
		}
	}

	switch {
	case r.Blocked:
		// An admin switched this client off: nothing else needs evaluating.
		b.WriteString("\t\tdrop\n")
	default:
		// Order matters. `drop` ends rule evaluation, so once the quota is
		// over the counters below are never reached and dropped bytes are not
		// billed as usage. Without kernel quota support the client is still
		// counted, and the reconciler cuts them off once the stored total
		// crosses the limit — a tick late instead of a packet late.
		if caps.Quota && !r.Unlimited() {
			fmt.Fprintf(b, "\t\tquota name \"%s\" drop\n", quotaName(r.Key))
		}
		for _, f := range files {
			fmt.Fprintf(b, "\t\tip %s %s counter name \"%s\"\n", side, f.Addr, counter(r.Key, f.Account))
		}
	}
	b.WriteString("\t}\n")
}

// mapElements renders the address-to-chain entries for both directions.
//
// Every address of a client jumps to that client's one chain per direction,
// so a customer's traffic is counted against a single quota whichever device
// and whichever way it flows — which is what makes the allowance apply to the
// client rather than to each device separately.
func mapElements(sorted []Rule, owned map[string][]File) (dl, ul string) {
	var dlParts, ulParts []string
	for _, r := range sorted {
		for _, f := range owned[r.Key] {
			// The one place the two maps stop being copies of each other:
			// which way a packet was going is decided here and nowhere else.
			dlParts = append(dlParts,
				fmt.Sprintf("%s : jump %s", f.Addr, downChain(r.Key)))
			ulParts = append(ulParts,
				fmt.Sprintf("%s : jump %s", f.Addr, upChain(r.Key)))
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
