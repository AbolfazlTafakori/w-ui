package api

import (
	"net/http"
	"time"

	"github.com/abolfazl/w-ui/internal/sysinfo"
)

// Reading back more than the last few minutes.
//
// The overview carries a short window so the page has something moving on it.
// This is the other question — was it like this last night, when did the disk
// start filling, was that spike at the time the customers complained — and
// until now the panel could not answer any of it. The answer was an SSH session
// and whatever the machine happened to still have.

// historyRanges are the spans offered, and how far back each really reaches.
//
// Named rather than free-form: the store keeps three resolutions, and a request
// for an arbitrary span would silently be answered from whichever tier happened
// to cover it. These line up with what the store actually holds.
var historyRanges = map[string]time.Duration{
	"5m":  5 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"48h": 48 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// historyPoints is how many buckets a chart is drawn from. Enough to show the
// shape of a week, few enough that the answer is small and the browser is not
// asked to draw ten thousand segments it cannot show.
const historyPoints = 240

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.sys == nil || s.sys.Store() == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"series": map[string][]sysinfo.Sample{},
			"notice": "this panel is not keeping a long history",
		})
		return
	}
	store := s.sys.Store()

	name := r.URL.Query().Get("range")
	if name == "" {
		name = "1h"
	}
	span, ok := historyRanges[name]
	if !ok {
		writeError(w, http.StatusBadRequest,
			"that is not a range this panel keeps. Ask for 5m, 1h, 6h, 24h, 48h or 7d")
		return
	}

	// Every series in one answer rather than one request per chart: they are
	// drawn together and read against each other, and eleven round trips to
	// build one page is eleven chances for them to disagree about when "now" is.
	series := make(map[string][]sysinfo.Sample, len(sysinfo.MetricKeys))
	for _, metric := range sysinfo.MetricKeys {
		series[metric] = store.Range(metric, span, historyPoints)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range":  name,
		"points": historyPoints,
		"series": series,
	})
}
