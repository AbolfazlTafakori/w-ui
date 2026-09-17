package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Who did what.
//
// Every change made through the API is written to the log as one line: the
// action, who asked for it (the administrator's name, or the token), where
// they asked from, and how it went. It is the line an operator goes looking
// for when a customer is gone, a tunnel's port has moved or a setting is
// not what it was -- and the line a second administrator reads to see what
// the first one did. Reads are not logged: a list opened is not an event.
// The traffic between a panel and its nodes is not either; it runs every few
// seconds and says the same thing each time.

// quietPaths are the machine-to-machine calls that would drown the log.
var quietPaths = []string{"/api/node/", "/api/auth/login"}

// logAction wraps a mutating route so the outcome is written once the
// handler has answered.
func (s *Server) logAction(method string, next http.HandlerFunc) http.HandlerFunc {
	if method == http.MethodGet || method == http.MethodHead {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		for _, p := range quietPaths {
			if strings.HasPrefix(r.URL.Path, p) {
				next(w, r)
				return
			}
		}
		start := time.Now()
		rec := &actionRecorder{statusRecorder: statusRecorder{ResponseWriter: w, status: http.StatusOK}}
		next(rec, r)

		by := "token"
		if a := adminFrom(r.Context()); a != nil {
			by = a.Username
		}
		attrs := []any{
			"action", method + " " + r.URL.Path,
			"by", by, "ip", clientIP(r),
			"status", rec.status, "took", time.Since(start).Round(time.Millisecond).String(),
		}
		if rec.status >= 400 {
			// The reason, from the answer itself, so the line says what was
			// wrong rather than only that something was.
			attrs = append(attrs, "reason", rec.reason())
		}
		if rec.status < 400 && s.subHub != nil {
			// Whatever changed, every open subscription page hears of it
			// now; each asks whether its own files moved.
			s.subHub.Broadcast()
		}
		switch {
		case rec.status >= 500:
			s.log.Error("action failed", attrs...)
		case rec.status >= 400:
			s.log.Warn("action refused", attrs...)
		default:
			s.log.Info("action", attrs...)
		}
	}
}

// actionRecorder keeps the first part of a refused answer, which is the
// error message the caller saw.
type actionRecorder struct {
	statusRecorder
	body []byte
}

func (a *actionRecorder) Write(b []byte) (int, error) {
	if a.status >= 400 && len(a.body) < 300 {
		a.body = append(a.body, b[:min(len(b), 300-len(a.body))]...)
	}
	return a.ResponseWriter.Write(b)
}

func (a *actionRecorder) reason() string {
	var v struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(a.body, &v) == nil && v.Error != "" {
		return v.Error
	}
	return strings.TrimSpace(string(a.body))
}
