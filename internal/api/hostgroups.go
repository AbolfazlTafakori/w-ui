package api

import (
	"net/http"

	"github.com/abolfazl/w-ui/internal/service"
)

// The hosts page's own endpoints: groups, as the classic panel's page speaks in them.
// The row endpoints stay for the backup and the importer.

func (s *Server) handleListHostGroups(w http.ResponseWriter, r *http.Request) {
	list, err := s.hosts.ListGroups(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateHostGroup(w http.ResponseWriter, r *http.Request) {
	var in service.HostGroup
	if !decode(w, r, &in) {
		return
	}
	g, err := s.hosts.CreateGroup(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleUpdateHostGroup(w http.ResponseWriter, r *http.Request) {
	var in service.HostGroup
	if !decode(w, r, &in) {
		return
	}
	g, err := s.hosts.UpdateGroup(r.Context(), r.PathValue("gid"), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteHostGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.hosts.DeleteGroup(r.Context(), r.PathValue("gid")); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBulkHostGroups(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action   string   `json:"action"`
		GroupIDs []string `json:"groupIds"`
	}
	if !decode(w, r, &in) {
		return
	}
	var n int64
	var err error
	switch in.Action {
	case "enable", "disable":
		n, err = s.hosts.SetGroupsEnabled(r.Context(), in.GroupIDs, in.Action == "enable")
	case "delete":
		n, err = s.hosts.DeleteGroups(r.Context(), in.GroupIDs)
	default:
		writeError(w, http.StatusBadRequest, "action must be enable, disable or delete")
		return
	}
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": n})
}

func (s *Server) handleReorderHostGroups(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GroupIDs []string `json:"groupIds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.hosts.ReorderGroups(r.Context(), in.GroupIDs); err != nil {
		fail(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHostTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.hosts.AllTags(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	list, err := s.clients.Profiles(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
