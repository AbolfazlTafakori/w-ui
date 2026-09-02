package api

import (
	"net/http"
	"strings"

	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/service"
)

// ── outbounds ────────────────────────────────────────────────────────────────

func (s *Server) handleListOutbounds(w http.ResponseWriter, r *http.Request) {
	list, err := s.outbounds.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateOutbound(w http.ResponseWriter, r *http.Request) {
	var in service.OutboundInput
	if !decode(w, r, &in) {
		return
	}
	ob, err := s.outbounds.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, ob)
}

func (s *Server) handleUpdateOutbound(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.OutboundInput
	if !decode(w, r, &in) {
		return
	}
	ob, err := s.outbounds.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, ob)
}

func (s *Server) handleDeleteOutbound(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.outbounds.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderOutbounds(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uint `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.outbounds.Reorder(r.Context(), in.IDs); err != nil {
		fail(w, s.log, err)
		return
	}
	list, err := s.outbounds.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCheckOutbound(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := s.outbounds.Check(r.Context(), id, checkMode(r))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCheckAllOutbounds(w http.ResponseWriter, r *http.Request) {
	res, err := s.outbounds.CheckAll(r.Context(), checkMode(r))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

// checkMode reads how to measure. Anything unrecognised falls back to TCP
// rather than erroring: a probe is a convenience, and refusing to run one over
// a query string is not worth an error page.
func checkMode(r *http.Request) string {
	if strings.EqualFold(r.URL.Query().Get("mode"), "http") {
		return "http"
	}
	return "tcp"
}

// ── routing ──────────────────────────────────────────────────────────────────

func (s *Server) handleGetRouting(w http.ResponseWriter, r *http.Request) {
	basic, err := s.routing.Basic(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	rules, err := s.routing.ListRules(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}

	var health string
	if s.router != nil {
		if err := s.router.Health(r.Context()); err != nil {
			// Cleaned the same way the security panel cleans it. The two sit on
			// screen together, and one of them showing a Go error while the
			// other shows a sentence makes the panel look half-finished.
			health = service.PlainReason(err.Error())
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"basic":    basic,
		"rules":    rules,
		"groups":   routing.GroupNames(),
		"resolver": s.routing.ResolverStatus(),
		// Empty when routing is working. The page shows it as a banner, so an
		// operator cannot configure an exit and never learn it is inert.
		"inactive": health,
	})
}

func (s *Server) handleSaveRouting(w http.ResponseWriter, r *http.Request) {
	var in service.BasicRouting
	if !decode(w, r, &in) {
		return
	}
	out, err := s.routing.SaveBasic(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateRoutingRule(w http.ResponseWriter, r *http.Request) {
	var in service.RuleInput
	if !decode(w, r, &in) {
		return
	}
	rule, err := s.routing.CreateRule(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleUpdateRoutingRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.RuleInput
	if !decode(w, r, &in) {
		return
	}
	rule, err := s.routing.UpdateRule(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) handleDeleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.routing.DeleteRule(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderRoutingRules(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uint `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.routing.ReorderRules(r.Context(), in.IDs); err != nil {
		fail(w, s.log, err)
		return
	}
	rules, err := s.routing.ListRules(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (s *Server) handleTestRoute(w http.ResponseWriter, r *http.Request) {
	var in service.RouteTest
	if !decode(w, r, &in) {
		return
	}
	ans, err := s.routing.TestRoute(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// ── hosts ────────────────────────────────────────────────────────────────────

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	list, err := s.hosts.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateHost(w http.ResponseWriter, r *http.Request) {
	var in service.HostInput
	if !decode(w, r, &in) {
		return
	}
	h, err := s.hosts.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

func (s *Server) handleUpdateHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.HostInput
	if !decode(w, r, &in) {
		return
	}
	h, err := s.hosts.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.hosts.Delete(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCheckHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := s.hosts.Check(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ── generated configuration ──────────────────────────────────────────────────

// handleGeneratedConfigs shows exactly what the panel is asking the kernel and
// the drivers to do.
//
// A panel that only shows its own opinion of the state is a panel an operator
// has to take on trust. This is the actual text: the nftables program that
// meters and blocks, the routing policy, and the shaping plan. Read-only —
// editing them here would create a second source of truth that the reconciler
// would silently overwrite two seconds later.
func (s *Server) handleGeneratedConfigs(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}

	if e, ok := s.enforcer.(interface{ Ruleset() string }); ok {
		out["enforcement"] = e.Ruleset()
	}
	if s.router != nil {
		if p, err := s.routing.Policy(r.Context()); err == nil {
			if text, err := routing.BuildRuleset(p); err == nil {
				out["routing"] = text
			} else {
				out["routingError"] = err.Error()
			}
		}
	}
	if sh, ok := s.shaper.(interface{ Plan() string }); ok {
		out["shaping"] = sh.Plan()
	}

	// What each engine can actually do here, so a page showing a program can
	// also say whether the kernel is running it.
	health := map[string]string{}
	if err := s.enforcer.Health(r.Context()); err != nil {
		health["enforcement"] = err.Error()
	}
	if s.router != nil {
		if err := s.router.Health(r.Context()); err != nil {
			health["routing"] = err.Error()
		}
	}
	if s.shaper != nil {
		if err := s.shaper.Health(r.Context()); err != nil {
			health["shaping"] = err.Error()
		}
	}
	out["health"] = health

	writeJSON(w, http.StatusOK, out)
}
