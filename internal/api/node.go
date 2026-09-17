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
	if !decodeLenient(w, r, &state) {
		return
	}
	if s.rec != nil {
		s.rec.PanelSpoke()
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
	if s.rec != nil {
		s.rec.PanelSpoke()
	}
	// The same call carries which tunnels the panel still has here, so
	// one it deleted is taken down on this side. A panel older than this
	// sends no body and nothing is pruned.
	var in struct {
		Keep *[]uint `json:"keep"`
	}
	if r.ContentLength != 0 {
		if !decodeLenient(w, r, &in) {
			return
		}
	}
	if in.Keep != nil {
		if _, err := s.nodeSync.Prune(r.Context(), s.localNodeID, *in.Keep); err != nil {
			s.log.Warn("could not remove withdrawn tunnels", "error", err)
		}
	}
	usage, err := s.nodeSync.Drain(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if usage == nil {
		usage = []service.NodeUsage{}
	}
	devices, err := s.nodeSync.DrainDevices(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if devices == nil {
		devices = []service.NodeDeviceUsage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage, "devices": devices})
}

// handleNodeSessions reports what is live on this server, for the panel that
// manages it to count a customer's connections across every server.
func (s *Server) handleNodeSessions(w http.ResponseWriter, r *http.Request) {
	sessions := []service.NodeSession{}
	if s.rec != nil {
		if got := s.rec.Sessions(); got != nil {
			sessions = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// handleNodeHold takes the panel's decision that a device is over its plan's
// connections at once, and holds it off here until the time given.
func (s *Server) handleNodeHold(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Holds []service.NodeHold `json:"holds"`
	}
	if !decodeLenient(w, r, &in) {
		return
	}
	n, err := s.nodeSync.Hold(r.Context(), in.Holds)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"held": n})
}
