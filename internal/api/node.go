package api

import (
	"net/http"

	"github.com/abolfazl/w-ui/internal/service"
)

// The two endpoints a panel serves when another panel is using it as a node.
//
// They are on the same API, behind the same token, as everything else. A node
// is another W-UI panel rather than a purpose-built agent, so there is one
// implementation to secure and one to keep working instead of a second protocol
// that only runs in one direction and is exercised by nobody.

// handleNodeSync takes the desired state for one tunnel and makes this panel
// match it.
//
// The whole state every time, never a command. That is what lets a node that
// was unreachable for an hour catch up on its next successful call with no
// replay, no queue and no ordering to get wrong — and what makes a sync that
// arrives twice do nothing the second time.
func (s *Server) handleNodeSync(w http.ResponseWriter, r *http.Request) {
	var state service.NodeState
	if !decode(w, r, &state) {
		return
	}
	if err := s.nodeSync.Apply(r.Context(), s.localNodeID, state); err != nil {
		fail(w, s.log, err)
		return
	}

	// The reconciler is what carries this to the kernel, and it runs on its own
	// clock. Saying so here means the panel that called is told the state was
	// accepted, not that peers are already up.
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted":  true,
		"interface": state.Interface.Name,
		"clients":   len(state.Clients),
		"appliedIn": "the next reconcile tick",
	})
}

// handleNodeUsage reports what each customer spent here and resets the counters.
//
// A read that resets, in one transaction, for the same reason the kernel
// counters are drained rather than polled: the panel asking is about to add
// these to a total that spans every node, and a figure returned twice would
// bill a customer for traffic they never sent.
func (s *Server) handleNodeUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.nodeSync.Drain(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if usage == nil {
		usage = []service.NodeUsage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}
