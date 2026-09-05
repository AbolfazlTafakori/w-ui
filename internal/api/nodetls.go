package api

import (
	"net/http"
	"time"

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
	}
	if !decode(w, r, &in) {
		return
	}

	pin, err := nodes.FetchPin(in.Address, fetchPinTimeout)
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
