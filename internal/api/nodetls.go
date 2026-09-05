package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/nodes"
)

// Reading a node's certificate so it can be pinned.
//
// The alternative is an operator running openssl and copying a hash by hand,
// and that is how certificate checking ends up switched off instead. It is
// offered with what it actually is written beside it: nothing is verified
// during the fetch, so it records whoever answers at that moment.

const fetchPinTimeout = 10 * time.Second

func (s *Server) handleFetchPin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Address string `json:"address"`
		// Sent from the same form as the node's own setting, so reading a
		// fingerprint from a node on a private network is possible exactly when
		// talking to it would be.
		AllowPrivateAddress bool `json:"allowPrivateAddress"`
	}
	if !decode(w, r, &in) {
		return
	}

	pin, err := nodes.FetchPin(in.Address, fetchPinTimeout, in.AllowPrivateAddress)
	if err != nil {
		// A refusal with a reason, not a fault: an unreachable address or a
		// plain-HTTP one is something the operator can act on.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.log.Info("read a node's certificate fingerprint",
		"address", in.Address, "by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, map[string]any{"tlsPin": pin})
}

// handleMTLSIdentity hands back the authority this panel signs its own client
// certificate with, to be pasted into a node.
//
// The public half only: the key stays here, which is the whole reason a
// certificate is worth more than a token.
func (s *Server) handleMTLSIdentity(w http.ResponseWriter, r *http.Request) {
	id, err := nodes.EnsureIdentity(s.db)
	if err != nil {
		fail(w, s.log, err)
		return
	}

	s.log.Info("the node authority was read for copying to a node",
		"by", adminName(r), "ip", clientIP(r))

	writeJSON(w, http.StatusOK, map[string]any{"caCert": id.CACertPEM})
}

// handleSetMTLSTrust stores the authority this panel accepts when it is the
// node being managed. An empty value turns the requirement off.
func (s *Server) handleSetMTLSTrust(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CACert string `json:"caCert"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.CACert = strings.TrimSpace(in.CACert)

	// Checked before it is stored. An authority that cannot be read would
	// otherwise be found out by every request arriving afterwards.
	if in.CACert != "" {
		if _, err := nodes.TrustPool(in.CACert); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := database.PutSetting(s.db, nodes.KeyMTLSTrustCA, in.CACert); err != nil {
		fail(w, s.log, err)
		return
	}

	if in.CACert == "" {
		s.log.Warn("this panel no longer requires a client certificate from the panel managing it",
			"by", adminName(r), "ip", clientIP(r))
	} else {
		s.log.Warn("this panel now requires a client certificate from the panel managing it",
			"by", adminName(r), "ip", clientIP(r))
	}

	writeJSON(w, http.StatusOK, map[string]any{"required": in.CACert != ""})
}
