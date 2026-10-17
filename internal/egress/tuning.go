package egress

import (
	"sync/atomic"
	"time"
)

// Tuning is what the engine page steers on the outbound side, read on every
// probe and every routing pass without a database call: where a check goes,
// how often the balancers measure, whether checks run side by side, and
// whether the kernel counts each outbound's bytes.
//
// One is made at start and handed to the outbounds and the routing, and to
// the engine settings, which write it. A nil Tuning reads as the defaults.
type Tuning struct {
	probeURL      atomic.Value // string
	probeInterval atomic.Int64 // seconds
	sequential    atomic.Bool
	noCounters    atomic.Bool
}

// NewTuning is the defaults.
func NewTuning() *Tuning { return &Tuning{} }

// DefaultProbeURL is what a check fetches unless told otherwise. Only this
// trace endpoint yields an egress address and country; anything else is
// measured alone.
const DefaultProbeURL = traceURL

// ProbeURL is what an outbound check fetches.
func (t *Tuning) ProbeURL() string {
	if t != nil {
		if u, _ := t.probeURL.Load().(string); u != "" {
			return u
		}
	}
	return traceURL
}

// ProbeEvery is how often the balancers measure their members.
func (t *Tuning) ProbeEvery() time.Duration {
	if t == nil || t.probeInterval.Load() == 0 {
		return time.Minute
	}
	return time.Duration(t.probeInterval.Load()) * time.Second
}

// ProbeTogether is whether checks run side by side rather than one at a
// time.
func (t *Tuning) ProbeTogether() bool { return t == nil || !t.sequential.Load() }

// Counters is whether the kernel counts each outbound's bytes.
func (t *Tuning) Counters() bool { return t == nil || !t.noCounters.Load() }

// Set puts the engine page's values in place.
func (t *Tuning) Set(probeURL string, probeEvery time.Duration, together, counters bool) {
	t.probeURL.Store(probeURL)
	t.probeInterval.Store(int64(probeEvery / time.Second))
	t.sequential.Store(!together)
	t.noCounters.Store(!counters)
}
