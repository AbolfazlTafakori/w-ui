package api

import (
	"net/http"

	"github.com/abolfazl/w-ui/internal/service"
)

// An admin's or a reseller's own API tokens: made, listed and revoked only
// by them signed in (the routes are Operator, so a token cannot reach them
// and no token can mint another). The owner sees and revokes everyone's; an
// admin their own and their resellers'.

func (s *Server) handleListOperatorTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.admins.ListTokens(r.Context(), adminFrom(r.Context()))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleIssueOperatorToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		// Days is how long it lasts: absent is the default, zero is never.
		Days *int `json:"days"`
	}
	if !decode(w, r, &in) {
		return
	}
	// An operator paused -- term ended, allowance gone -- may read what is
	// theirs but make nothing new, and a token is a new way in.
	if why := service.ScopeOf(r.Context()).Barred; why != "" {
		writeError(w, http.StatusForbidden, why)
		return
	}
	issued, err := s.admins.IssueToken(r.Context(), adminFrom(r.Context()), in.Name, in.Days)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) handleRevokeOperatorToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.admins.RevokeToken(r.Context(), adminFrom(r.Context()), id); err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
