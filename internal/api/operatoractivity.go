package api

import (
	"net/http"
	"strconv"
	"time"
)

// What an admin's tree changed, or a reseller's own, when the owner granted
// them the activity page (grantForRoute).

// operatorActionView is one change as an operator is shown it: who, what,
// how it ended, when, and whether it came from a token. Its own struct, so
// no field added to the record reaches them by being forgotten here.
type operatorActionView struct {
	ID       uint      `json:"id"`
	Operator string    `json:"operator"`
	Action   string    `json:"action"`
	Status   int       `json:"status"`
	ViaToken bool      `json:"viaToken"`
	At       time.Time `json:"at"`
}

func (s *Server) handleOperatorActivity(w http.ResponseWriter, r *http.Request) {
	// A number with a fallback: what does not read is the default, as the code below says.
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.admins.Activity(r.Context(), adminFrom(r.Context()), limit)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	out := make([]operatorActionView, 0, len(rows))
	for _, a := range rows {
		out = append(out, operatorActionView{
			ID: a.ID, Operator: a.Username, Action: a.Method + " " + a.Path,
			Status: a.Status, ViaToken: a.ViaToken, At: a.At,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleOperatorLedger is the capacity ledger the caller may see: their own
// account's movements, an admin's resellers' too, every account's for the
// owner. Narrowed in operators.Ledger, never by what the request names.
func (s *Server) handleOperatorLedger(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// A number with a fallback: what does not read is the default, as the code below says.
	limit, _ := strconv.Atoi(q.Get("limit"))
	// A number with a fallback: what does not read is the default, as the code below says.
	before, _ := strconv.ParseUint(q.Get("before"), 10, 32)
	rows, err := s.admins.Ledger(r.Context(), adminFrom(r.Context()), q.Get("kind"), uint(before), limit)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}
