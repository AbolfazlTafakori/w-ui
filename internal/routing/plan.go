package routing

import (
	"fmt"
	"sort"
)

// AllocateMark returns the mark and routing table for an outbound.
//
// Both are derived from the outbound's database id rather than handed out from
// a counter. That means they survive a restart, a backup restore and a row
// being deleted and recreated, and two panels reading the same database agree
// without coordinating. A counter would drift and quietly re-point a hop at
// another hop's table.
func AllocateMark(outboundID uint) (mark uint32, table int, err error) {
	// The mask leaves 16 bits, and the id has to fit in them with room to
	// spare. A panel with sixty thousand outbounds is not a case worth
	// supporting, but silently wrapping around and colliding is not either.
	if outboundID == 0 || outboundID > 0xFFF0 {
		return 0, 0, fmt.Errorf("%w: outbound id %d cannot be given a routing mark",
			ErrInvalidPolicy, outboundID)
	}
	mark = MarkBase | uint32(outboundID)
	table = TableBase + int(outboundID)
	return mark, table, nil
}

// OwnsMark reports whether a mark is one this panel allocated.
//
// Used before removing anything from the kernel: a rule carrying a mark outside
// our range belongs to something else on the machine and must be left alone.
func OwnsMark(mark uint32) bool {
	return mark&MarkMask == MarkBase&MarkMask && mark&^MarkMask != 0
}

// OwnsTable reports whether a routing table id is one this panel allocated.
func OwnsTable(table int) bool {
	return table > TableBase && table <= TableBase+0xFFF0
}

// Statement is one kernel routing instruction, as arguments to `ip`.
//
// Kept as argument slices rather than a command line so nothing an operator
// typed is ever concatenated into a string a shell will parse.
type Statement struct {
	// Args are passed to `ip` directly.
	Args []string
	// Describe is what to say if this one fails, in an operator's terms.
	Describe string
}

// Plan is the full set of routing statements for the current outbounds.
//
// Add is applied in order and Remove is applied first, so a hop that changed
// its device is torn down before being rebuilt rather than ending up with two
// default routes in one table.
type Plan struct {
	Remove []Statement
	Add    []Statement
}

// BuildPlan renders the ip-rule and route statements for a set of hops.
//
// Every hop gets a rule matching its mark and a table holding one default
// route. Hops that are disabled, or that have no device to route through
// because they are dialled in userspace, get their rule removed instead: a
// stale rule pointing at an empty table is a black hole that looks like a
// network fault.
func BuildPlan(hops []Hop) Plan {
	sorted := append([]Hop(nil), hops...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Mark < sorted[j].Mark })

	var p Plan
	for _, h := range sorted {
		if h.Mark == 0 || !OwnsMark(h.Mark) || !OwnsTable(h.Table) {
			continue
		}

		markArg := fmt.Sprintf("0x%08x", h.Mark)
		tableArg := fmt.Sprintf("%d", h.Table)

		// Removed unconditionally before being added. `ip rule del` on a rule
		// that is not there is a harmless error, and doing it every time is
		// what keeps repeated applies from stacking duplicate rules — which is
		// the classic way a policy-routing setup slowly stops working.
		p.Remove = append(p.Remove, Statement{
			Args:     []string{"rule", "del", "fwmark", markArg, "table", tableArg},
			Describe: fmt.Sprintf("clear the old routing rule for %q", h.Tag),
		})

		if !h.Enabled || h.Device == "" {
			// A proxy hop needs no route: the connection is made in userspace
			// and leaves through whatever the main table says. Removing the
			// rule is the whole job.
			p.Remove = append(p.Remove, Statement{
				Args:     []string{"route", "flush", "table", tableArg},
				Describe: fmt.Sprintf("empty the routing table for %q", h.Tag),
			})
			continue
		}

		p.Add = append(p.Add,
			Statement{
				Args: []string{"route", "replace", "default", "dev", h.Device, "table", tableArg},
				Describe: fmt.Sprintf("point %q at %s", h.Tag, h.Device),
			},
			Statement{
				// The priority keeps our rules together and below the kernel's
				// own, so they are consulted after local delivery and before
				// the default lookup.
				Args: []string{"rule", "add", "fwmark", markArg, "table", tableArg,
					"priority", fmt.Sprintf("%d", rulePriority(h.Mark))},
				Describe: fmt.Sprintf("send marked traffic to %q", h.Tag),
			},
		)
	}
	return p
}

// rulePriority spaces our rules in a band of their own.
//
// Starting at 20000 leaves the kernel's own rules (0, 32766, 32767) and the
// low numbers other tools claim well alone, and ordering by mark keeps the list
// stable so a reordering never changes which hop wins.
func rulePriority(mark uint32) int {
	return 20000 + int(mark&^MarkMask)
}
