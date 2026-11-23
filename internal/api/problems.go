package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/reconciler"
	"github.com/abolfazl/w-ui/internal/service"
)

// Problem codes beside the loop's (reconciler.Problem*).
const (
	problemNodeUnreachable = "node.unreachable"
	problemNodeLimit       = "node.limitPartial"
	problemTunnelDown      = "tunnel.down"
)

// problemView is one problem as a page shows it: its code, what it is
// about by name, and -- for the owner only -- what failed.
type problemView struct {
	reconciler.Problem
	// Node and Tunnel name what it is about, when it is about one.
	Node   string `json:"node,omitempty"`
	Tunnel string `json:"tunnel,omitempty"`
}

// handleProblems is what is going wrong now: the loop's failures, nodes
// that cannot be reached or whose connections are not all counted, and
// tunnels that will not run.
//
// The owner is told all of it, with what failed. An admin or a reseller is
// told what is about their own customers, the nodes and tunnels they sell
// on, and -- by its code alone, never its detail -- what stops the panel
// holding anybody to their plan; never another operator's customer, and
// never a path, a query or an address from an error.
func (s *Server) handleProblems(w http.ResponseWriter, r *http.Request) {
	me := adminFrom(r.Context())
	owner := me == nil || me.Role == model.RoleOwner
	sc := service.ScopeOf(r.Context())

	var out []problemView
	if s.rec != nil {
		var mine map[uint]bool
		if !owner {
			// Their own customers, by id: the list is narrowed for them.
			ids, err := s.clients.OwnIDs(r.Context())
			if err != nil {
				fail(w, s.log, err)
				return
			}
			mine = make(map[uint]bool, len(ids))
			for _, id := range ids {
				mine[id] = true
			}
		}
		for _, p := range s.rec.Problems() {
			switch {
			case owner:
			case p.ClientID != 0 && mine[p.ClientID]:
				p.Detail = ""
			case p.ClientID == 0:
				p.Detail = ""
			default:
				continue // another operator's customer
			}
			out = append(out, problemView{Problem: p})
		}
	}

	ifaces, nodes, err := s.ifaces.Running(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	// The nodes an operator sells on: those carrying a tunnel they may sell.
	sells := map[uint]bool{}
	for _, f := range ifaces {
		if owner || sc.Allows(f.ID) {
			sells[f.NodeID] = true
		}
	}
	for _, n := range nodes {
		if !owner && !sells[n.ID] {
			continue
		}
		detail := func(d string) string {
			if owner {
				return d
			}
			return ""
		}
		if !n.Reachable && n.LastError != "" {
			out = append(out, problemView{Problem: reconciler.Problem{Code: problemNodeUnreachable, Detail: detail(n.LastError),
				Since: since(n.LastSeenAt)}, Node: n.Name})
		}
		if n.LimitWarning != "" {
			out = append(out, problemView{Problem: reconciler.Problem{Code: problemNodeLimit, Detail: n.LimitWarning}, Node: n.Name})
		}
	}
	if s.pool != nil {
		for _, f := range ifaces {
			if !owner && !sc.Allows(f.ID) {
				continue
			}
			if err := s.pool.ErrorFor(f.ID); err != nil {
				d := ""
				if owner {
					d = err.Error()
				}
				out = append(out, problemView{Problem: reconciler.Problem{Code: problemTunnelDown, Detail: d,
					Since: s.pool.FailingSince(f.ID)}, Tunnel: f.Name})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	if out == nil {
		out = []problemView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// since is when a node was last heard from, as the time its problem began.
func since(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
