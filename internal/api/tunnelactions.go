package api

import (
	"net/http"

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

	n, err := s.hosts.Bulk(r.Context(), service.HostBulkAction(in.Action), in.IDs)
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
