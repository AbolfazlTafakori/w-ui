package api

import (
	"context"
	"net/http"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The two controls an operator reaches for when something is wrong.
//
// Every panel of this kind has them on its front page, and this one had
// neither: the only way to bring a stuck tunnel back was to restart the whole
// service over SSH, which disconnects every customer on every other tunnel to
// fix one — and the only way to cut everybody off in a hurry was to disable
// interfaces one at a time.
//
// Restart reopens the drivers. It does not touch the database, so nobody's
// allowance, expiry or standing changes: a customer who was allowed on before
// is allowed on after, and the reconciler pushes them back within a tick.
//
// Stop switches every tunnel off, which is a decision about service rather than
// about a fault, so it is recorded as one: the interfaces are marked disabled
// and stay that way across a restart until somebody turns them back on. Anything
// less — killing the drivers and leaving the rows enabled — would come back by
// itself on the next tick and look like the button had not worked.

type dataPlaneReport struct {
	// Interfaces acted on.
	Interfaces int `json:"interfaces"`

	// Failures names the tunnels that would not come up, because that is the
	// whole content of the answer when a restart does not fix it.
	Failures map[string]string `json:"failures,omitempty"`
}

func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request) {
	var interfaces []model.Interface
	if err := s.db.WithContext(r.Context()).
		Where("enabled = ? AND node_id = ?", true, s.localNodeID).
		Find(&interfaces).Error; err != nil {
		fail(w, s.log, err)
		return
	}

	report := dataPlaneReport{Failures: map[string]string{}}
	for i := range interfaces {
		iface := interfaces[i]
		if err := s.pool.Open(r.Context(), &iface); err != nil {
			// One tunnel that will not come up must not stop the others being
			// restarted; an operator pressing this has a problem already.
			s.log.Warn("interface did not restart", "interface", iface.Name, "error", err)
			report.Failures[iface.Name] = humanMessage(err)
			continue
		}
		report.Interfaces++
	}

	// The kernel is rebuilt from the database on the next tick anyway, but
	// waiting two seconds after pressing restart reads as nothing having
	// happened.
	s.tickNow()

	s.log.Warn("every tunnel on this server was restarted",
		"restarted", report.Interfaces, "failed", len(report.Failures),
		"by", adminName(r), "ip", clientIP(r))

	if len(report.Failures) == 0 {
		report.Failures = nil
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleStopAll(w http.ResponseWriter, r *http.Request) {
	var interfaces []model.Interface
	if err := s.db.WithContext(r.Context()).
		Where("enabled = ? AND node_id = ?", true, s.localNodeID).
		Find(&interfaces).Error; err != nil {
		fail(w, s.log, err)
		return
	}

	// Marked in the database rather than only closed. A driver closed with the
	// row still enabled is reopened by the reconciler within a tick, and the
	// button would look broken.
	ids := make([]uint, 0, len(interfaces))
	for _, iface := range interfaces {
		ids = append(ids, iface.ID)
	}
	if len(ids) > 0 {
		if err := s.db.WithContext(r.Context()).Model(&model.Interface{}).
			Where("id IN ?", ids).Update("enabled", false).Error; err != nil {
			fail(w, s.log, err)
			return
		}
	}
	for _, id := range ids {
		s.pool.Close(id)
	}

	s.log.Warn("every tunnel on this server was switched off",
		"interfaces", len(ids), "by", adminName(r), "ip", clientIP(r),
		"note", "customers cannot connect until they are turned back on")

	writeJSON(w, http.StatusOK, dataPlaneReport{Interfaces: len(ids)})
}

// handleStartAll turns them back on, because a stop nobody can undo from the
// same page is a stop nobody will press.
func (s *Server) handleStartAll(w http.ResponseWriter, r *http.Request) {
	var interfaces []model.Interface
	if err := s.db.WithContext(r.Context()).
		Where("enabled = ? AND node_id = ?", false, s.localNodeID).
		Find(&interfaces).Error; err != nil {
		fail(w, s.log, err)
		return
	}

	report := dataPlaneReport{Failures: map[string]string{}}
	for i := range interfaces {
		iface := interfaces[i]
		if err := s.db.WithContext(r.Context()).Model(&model.Interface{}).
			Where("id = ?", iface.ID).Update("enabled", true).Error; err != nil {
			report.Failures[iface.Name] = humanMessage(err)
			continue
		}
		iface.Enabled = true
		if err := s.pool.Open(r.Context(), &iface); err != nil {
			s.log.Warn("interface did not start", "interface", iface.Name, "error", err)
			report.Failures[iface.Name] = humanMessage(err)
			continue
		}
		report.Interfaces++
	}

	s.tickNow()

	s.log.Warn("every tunnel on this server was switched back on",
		"started", report.Interfaces, "failed", len(report.Failures),
		"by", adminName(r), "ip", clientIP(r))

	if len(report.Failures) == 0 {
		report.Failures = nil
	}
	writeJSON(w, http.StatusOK, report)
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
