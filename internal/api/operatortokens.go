package api

import (
	"fmt"
	"net/http"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// API tokens for admins and resellers: issued by the owner, or by an admin
// for themselves and their own resellers out of the admin's token capacity;
// never by a reseller. Made, listed and revoked only signed in (the routes
// are Operator, so a token cannot reach them and no token can mint another).
// The owner sees and revokes everyone's; an admin their own and their
// resellers'; a reseller their own.
//
// Where an operator has nothing -- an admin with no token capacity and no
// token, a reseller who was issued none -- the section is not theirs: their
// menu does not show it, and these routes refuse them too.

// tokensOpenTo reports whether the token section is an operator's: the
// owner's always; an admin's with capacity or a token; a reseller's with a
// token issued to them.
func (s *Server) tokensOpenTo(r *http.Request, me *model.Admin) (bool, error) {
	if me == nil || me.Role == model.RoleOwner {
		return true, nil
	}
	tc, err := s.admins.TokensOf(r.Context(), me)
	if err != nil {
		return false, err
	}
	return tc.Granted > 0 || tc.Held > 0, nil
}

func (s *Server) refuseNoTokens(w http.ResponseWriter, r *http.Request) bool {
	open, err := s.tokensOpenTo(r, adminFrom(r.Context()))
	if err != nil {
		fail(w, s.log, err)
		return true
	}
	if !open {
		writeError(w, http.StatusForbidden, "you have no API tokens")
		return true
	}
	return false
}

func (s *Server) handleListOperatorTokens(w http.ResponseWriter, r *http.Request) {
	if s.refuseNoTokens(w, r) {
		return
	}
	list, err := s.admins.ListTokens(r.Context(), adminFrom(r.Context()))
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleIssueOperatorToken(w http.ResponseWriter, r *http.Request) {
	me := adminFrom(r.Context())
	// A reseller's tokens are their admin's to issue. Refused before the
	// body is read, whatever it says.
	if me != nil && me.Role == model.RoleReseller {
		writeError(w, http.StatusForbidden, "a reseller's API tokens are issued by their admin")
		return
	}
	var in struct {
		Name string `json:"name"`
		// Days is how long it lasts: absent is the default, zero is never.
		Days *int `json:"days"`
		// For is whose token it is: absent or zero is the caller's own (an
		// admin's); an admin names one of their resellers; the owner any
		// admin or reseller.
		For uint `json:"forAdminId"`
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
	target := me
	if in.For != 0 && (me == nil || in.For != me.ID) {
		t, err := s.admins.ForSession(r.Context(), fmt.Sprint(in.For))
		// Someone the caller may not issue for is not found, so whether they
		// exist is not told (IssueToken checks the same again).
		if err != nil || (me != nil && me.Role == model.RoleAdmin && t.ParentAdminID != me.ID) {
			writeError(w, http.StatusNotFound, "operator not found")
			return
		}
		target = t
	}
	issued, err := s.admins.IssueToken(r.Context(), me, target, in.Name, in.Days)
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
	if s.refuseNoTokens(w, r) {
		return
	}
	if err := s.admins.RevokeToken(r.Context(), adminFrom(r.Context()), id); err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
