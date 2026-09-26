package api

import (
	"net/http"
	"strings"

	"github.com/abolfazl/w-ui/internal/service"
)

// The operator list: who signs in to this panel, and how much of it they get.
//
// Everything here is the owner's, enforced by requireAdminManager on the
// route. An administrator who could add administrators would be an owner
// with extra steps, and a reseller who could see the list would know who
// else is selling on the same machine.

func (s *Server) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := s.admins.List(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": admins})
}

func (s *Server) handleGetAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	admin, err := s.admins.Get(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, admin)
}

func (s *Server) handleCreateAdmin(w http.ResponseWriter, r *http.Request) {
	var in service.AdminInput
	if !decode(w, r, &in) {
		return
	}
	admin, err := s.admins.Create(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Warn("operator added", "username", admin.Username, "role", admin.Role,
		"by", adminName(r), "ip", clientIP(r))
	writeJSON(w, http.StatusCreated, admin)
}

func (s *Server) handleUpdateAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in service.AdminInput
	if !decode(w, r, &in) {
		return
	}
	// The owner is the account this request is being made from, and an
	// owner who could edit themselves here could take away their own
	// access with no way back in.
	if me := adminFrom(r.Context()); me != nil && me.ID == id {
		writeError(w, http.StatusBadRequest,
			"change your own account from the security settings, not from here")
		return
	}
	admin, err := s.admins.Update(r.Context(), id, in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, admin)
}

func (s *Server) handleDeleteAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if me := adminFrom(r.Context()); me != nil && me.ID == id {
		writeError(w, http.StatusBadRequest, "you cannot delete the account you are signed in with")
		return
	}
	mode := service.DeleteMode(strings.TrimSpace(r.URL.Query().Get("clients")))
	if err := s.admins.Delete(r.Context(), id, mode, s.clients); err != nil {
		fail(w, s.log, err)
		return
	}
	s.reconcileNow()
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleResetAdminUsage starts a reseller's allowance again -- the owner
// taking another month's payment.
func (s *Server) handleResetAdminUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.admins.ResetUsage(r.Context(), id); err != nil {
		fail(w, s.log, err)
		return
	}
	// Their customers were stopped by a computed fact, so they come back
	// as soon as the fact changes; the sweep is nudged rather than waited
	// for, because the operator who just took the payment is watching.
	s.reconcileNow()
	admin, err := s.admins.Get(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, admin)
}

// reconcileNow asks the sweep to run rather than waiting for its next tick.
// A change an owner just made to an operator must show on the customers'
// tunnels now, not in a minute.
func (s *Server) reconcileNow() {
	s.tickNow()
	if service.SubscriptionsChanged != nil {
		service.SubscriptionsChanged()
	}
}
