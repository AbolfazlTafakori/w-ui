package api

import (
	"net/http"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/endpoints"
	"github.com/abolfazl/w-ui/internal/service"
)

// Whole-tunnel actions, and bulk work on the addresses a tunnel is reached at.

func (s *Server) handleResetTunnelUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	n, err := s.ifaces.ResetTunnelUsage(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.log.Warn("usage reset for everyone on a tunnel",
		"interface", id, "customers", n, "by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, map[string]any{"customers": n})
}

// handleRemoveTunnel removes a tunnel with its customers (service.RemoveTunnel).
func (s *Server) handleRemoveTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.TunnelRemoval
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if in.DryRun {
		changedNothing(w)
	}
	res, err := service.RemoveTunnel(r.Context(), s.clients, s.ifaces, id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if !in.DryRun {
		s.log.Warn("tunnel removed with its customers", "tunnel", res.Tunnel, "customers", res.Customers,
			"moved", res.Moved, "deleted", res.Deleted, "by", adminName(r), "ip", clientIP(r))
		s.reconcileNow()
	}
	writeJSON(w, http.StatusOK, res)
}

// handleRemoveNode removes a server with its tunnels and customers: emptied
// by the rules of a tunnel (service.EmptyNode), a W-UI node told to take
// down what this panel gave it, then the node itself.
func (s *Server) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.TunnelRemoval
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	node, err := s.nodes.Get(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if node.Kind == model.KindLocal {
		writeError(w, http.StatusBadRequest, "this panel's own server cannot be removed")
		return
	}
	if in.DryRun {
		changedNothing(w)
	}
	res, err := service.EmptyNode(r.Context(), s.clients, s.ifaces, *node, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	out := map[string]any{"result": res}
	if in.DryRun {
		writeJSON(w, http.StatusOK, out)
		return
	}
	// Told before its entry goes, while its address and token are still
	// here. A node that cannot be reached is removed all the same -- it may
	// be gone for good -- and the operator is told what it still runs.
	if err := s.clients.WithdrawFromNode(r.Context(), *node); err != nil {
		s.log.Warn("could not tell a node being removed to take down its tunnels", "node", node.Name, "error", err)
		out["warning"] = "it could not be reached to take down the tunnels it was given; they keep running there until it is reached again or the panel there is removed"
	}
	if err := s.nodes.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Warn("server removed with its tunnels and customers", "node", node.Name, "tunnels", len(res.Tunnels),
		"customers", res.Customers, "moved", res.Moved, "deleted", res.Deleted, "by", adminName(r), "ip", clientIP(r))
	s.reconcileNow()
	writeJSON(w, http.StatusOK, out)
}

// handleClearTunnel takes every customer off a tunnel.
//
// Through the same path a bulk detach uses, so the rule that nobody is left
// with no server at all holds here too rather than being written twice.
func (s *Server) handleClearTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	ids, err := s.ifaces.CustomersOn(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"changed": 0, "unchanged": 0})
		return
	}

	res, err := s.clients.DetachServers(r.Context(), ids, []uint{id})
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.log.Warn("customers taken off a tunnel",
		"interface", id, "changed", res.Changed, "refused", len(res.Failures),
		"by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, res)
}

// ── the addresses a tunnel is reached at ────────────────────────────────────

func (s *Server) handleBulkHosts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		IDs    []uint `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}

	n, err := s.hosts.Bulk(r.Context(), endpoints.HostBulkAction(in.Action), in.IDs)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"affected": n})
}

func (s *Server) handleReorderHosts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uint `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}

	n, err := s.hosts.Reorder(r.Context(), in.IDs)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ordered": n})
}
