package api

import "net/http"

// Moving many customers between servers at once.
//
// The operation the multi-server work made necessary and did not supply: a
// operator who rents a new node has to give it to the customers who already
// exist, and doing that one at a time is the difference between an afternoon
// and a click.

type serverBulkRequest struct {
	IDs          []uint `json:"ids"`
	InterfaceIDs []uint `json:"interfaceIds"`
}

func (s *Server) handleAttachServers(w http.ResponseWriter, r *http.Request) {
	s.moveServers(w, r, true)
}

func (s *Server) handleDetachServers(w http.ResponseWriter, r *http.Request) {
	s.moveServers(w, r, false)
}

func (s *Server) moveServers(w http.ResponseWriter, r *http.Request, add bool) {
	var in serverBulkRequest
	if !decode(w, r, &in) {
		return
	}

	move := s.clients.DetachServers
	if add {
		move = s.clients.AttachServers
	}

	res, err := move(r.Context(), in.IDs, in.InterfaceIDs)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.log.Info("customers moved between servers in bulk",
		"add", add, "customers", len(in.IDs), "servers", len(in.InterfaceIDs),
		"changed", res.Changed, "by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, res)
}
