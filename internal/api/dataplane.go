package api

import (
	"context"
	"net/http"
	"time"

	"github.com/abolfazl/w-ui/internal/service"
)

// The two controls an operator reaches for when something is wrong; what
// they do is service.DataPlane's.

// dataPlaneReport is what the page is told.
type dataPlaneReport struct {
	// Interfaces acted on.
	Interfaces int `json:"interfaces"`

	// Failures names the tunnels that would not come up, because that is the
	// whole content of the answer when a restart does not fix it.
	Failures map[string]string `json:"failures,omitempty"`
}

// report turns what the data plane did into the page's answer, the reasons
// put as a person reads them.
func report(res service.DataPlaneResult) dataPlaneReport {
	out := dataPlaneReport{Interfaces: res.Acted}
	if len(res.Failures) > 0 {
		out.Failures = make(map[string]string, len(res.Failures))
		for name, err := range res.Failures {
			out.Failures[name] = humanMessage(err)
		}
	}
	return out
}

func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request) {
	res, err := s.dataPlane.RestartAll(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	// The kernel is rebuilt from the database on the next tick anyway, but
	// waiting two seconds after pressing restart reads as nothing having
	// happened.
	s.tickNow()
	s.log.Warn("every tunnel on this server was restarted",
		"restarted", res.Acted, "failed", len(res.Failures),
		"by", adminName(r), "ip", clientIP(r))
	writeJSON(w, http.StatusOK, report(res))
}

func (s *Server) handleStopAll(w http.ResponseWriter, r *http.Request) {
	res, err := s.dataPlane.StopAll(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	s.log.Warn("every tunnel on this server was switched off",
		"interfaces", res.Acted, "by", adminName(r), "ip", clientIP(r),
		"note", "customers cannot connect until they are turned back on")
	writeJSON(w, http.StatusOK, report(res))
}

// handleStartAll turns them back on.
func (s *Server) handleStartAll(w http.ResponseWriter, r *http.Request) {
	res, err := s.dataPlane.StartAll(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	s.tickNow()
	s.log.Warn("every tunnel on this server was switched back on",
		"started", res.Acted, "failed", len(res.Failures),
		"by", adminName(r), "ip", clientIP(r))
	writeJSON(w, http.StatusOK, report(res))
}

// tickNow runs one reconciliation without waiting for the loop's own timer.
//
// On its own context, not the request's: that one is cancelled the moment the
// response is written, which is the same as not starting the tick at all. The
// kernel would be rebuilt on the next tick anyway, but two seconds of nothing
// after pressing restart reads as the button not having worked.
func (s *Server) tickNow() {
	if s.rec == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.rec.Tick(ctx)
	}()
}
