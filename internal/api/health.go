package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/reconciler"
)

// Watching the panel from outside.
//
// /api/health answers anyone, and says only whether the panel is doing its
// job: a load balancer or an uptime checker needs that and nothing more, and
// it is under the panel's own path, so it tells a stranger nothing the
// sign-in page does not. /api/metrics is for someone signed in, in the text
// format Prometheus reads: the tunnels up and down, the loop, the usage
// writes, the nodes and the backups.

// stalled is how many passes the loop may miss before the panel is not
// healthy: a slow database can hold one, three is a loop that stopped.
const stalled = 3

// healthProblems is why the panel is not doing its job, or nothing.
func healthProblems(h reconciler.Health, now time.Time) []string {
	var out []string
	every := h.Every
	if every <= 0 {
		every = 2 * time.Second
	}
	// Measured from the last pass, or from the start before the first.
	switch {
	case h.Ticks == 0 && time.Since(startedAt) > stalled*every+30*time.Second:
		out = append(out, "the loop has not run since the panel started")
	case h.Ticks > 0 && now.Sub(h.LastRun) > stalled*every+30*time.Second:
		out = append(out, fmt.Sprintf("the loop last ran %s ago", now.Sub(h.LastRun).Round(time.Second)))
	}
	if h.UsageFailing {
		out = append(out, "usage is not being written; it is kept for the next try")
	}
	return out
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.rec == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	problems := healthProblems(s.rec.Health(), time.Now())
	if len(problems) > 0 {
		// The reasons go to whoever is signed in; a stranger is told no more
		// than that something is wrong.
		if s.signedIn(r) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "problems": problems})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// metrics is the text a scrape is answered with, built up a family at a
// time.
type metrics struct{ b strings.Builder }

func (m *metrics) family(name, kind, help string) {
	fmt.Fprintf(&m.b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func (m *metrics) value(name string, v float64, labels ...string) {
	m.b.WriteString(name)
	if len(labels) > 0 {
		m.b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				m.b.WriteByte(',')
			}
			fmt.Fprintf(&m.b, "%s=%q", labels[i], labels[i+1])
		}
		m.b.WriteByte('}')
	}
	fmt.Fprintf(&m.b, " %g\n", v)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	var m metrics

	m.family("wui_up", "gauge", "Whether the panel is doing its job (see /api/health).")
	healthy := true
	if s.rec != nil {
		h := s.rec.Health()
		healthy = len(healthProblems(h, now)) == 0
		m.value("wui_up", boolValue(healthy))

		m.family("wui_reconcile_passes_total", "counter", "Passes of the loop that applies the database to the kernel.")
		m.value("wui_reconcile_passes_total", float64(h.Ticks))
		m.family("wui_reconcile_last_run_seconds", "gauge", "Seconds since the loop last ran.")
		if !h.LastRun.IsZero() {
			m.value("wui_reconcile_last_run_seconds", now.Sub(h.LastRun).Seconds())
		}
		m.family("wui_reconcile_failing", "gauge", "Whether the last pass of the loop stopped on an error.")
		m.value("wui_reconcile_failing", boolValue(h.LastError != ""))
		m.family("wui_usage_write_failing", "gauge", "Whether the last write of usage failed; what it carried is kept.")
		m.value("wui_usage_write_failing", boolValue(h.UsageFailing))
		m.family("wui_usage_write_failures_total", "counter", "Writes of usage that failed since the panel started.")
		m.value("wui_usage_write_failures_total", float64(h.UsageFailures))
		m.family("wui_usage_held_clients", "gauge", "Customers whose usage is waiting for the next write.")
		m.value("wui_usage_held_clients", float64(h.UsageHeld))
	} else {
		m.value("wui_up", 1)
	}

	// The tunnels on this server: each enabled one up or not.
	if s.ifaces != nil && s.pool != nil {
		list, err := s.ifaces.List(ctx)
		if err != nil {
			fail(w, s.log, err)
			return
		}
		m.family("wui_tunnel_up", "gauge", "Whether an enabled tunnel on this server is running.")
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		for _, iface := range list {
			if !iface.Enabled || iface.NodeID != s.localNodeID || iface.Protocol == model.ProtocolXray {
				continue
			}
			_, open := s.pool.Get(iface.ID)
			up := open && s.pool.ErrorFor(iface.ID) == nil
			m.value("wui_tunnel_up", boolValue(up), "tunnel", iface.Name, "protocol", string(iface.Protocol))
		}
	}

	// The other servers.
	if s.nodes != nil {
		list, err := s.nodes.List(ctx)
		if err != nil {
			fail(w, s.log, err)
			return
		}
		m.family("wui_node_reachable", "gauge", "Whether a node answered the last time it was asked.")
		for _, n := range list {
			if n.Kind == model.KindLocal || !n.Enabled {
				continue
			}
			m.value("wui_node_reachable", boolValue(n.Reachable), "node", n.Name)
		}
	}

	// The backups kept here.
	if s.backups != nil {
		list, err := s.backups.List()
		if err == nil {
			m.family("wui_backups", "gauge", "Backups kept on this server.")
			m.value("wui_backups", float64(len(list)))
			var newest time.Time
			for _, a := range list {
				if a.Taken.After(newest) {
					newest = a.Taken
				}
			}
			m.family("wui_backup_last_seconds", "gauge", "Seconds since the newest backup kept on this server was taken.")
			if !newest.IsZero() {
				m.value("wui_backup_last_seconds", now.Sub(newest).Seconds())
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(m.b.String())) // the client hung up; nothing to do
}
