package sysinfo

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Keeping more than the last four minutes.
//
// The overview holds a short window in memory, which answers "what is this
// server doing now" and nothing else. The questions an operator actually
// arrives with are the other kind: was it like this last night, when did the
// disk start filling, was that spike when the customers complained. None of
// those could be answered from the panel at all.
//
// Storing every two-second sample for a week would be two hundred thousand
// points per series. Instead each sample is fed into three rings of decreasing
// resolution — the shape RRDtool settled on decades ago and 3x-ui uses too.
// Recent minutes stay exact; older ones become averages. About six thousand
// points per series covers a live view through a week, and the whole store is
// small enough to write to disk and read back on the next start.

// tier is one resolution layer: how wide a bucket is, and how many are kept.
type tier struct {
	// Resolution in seconds, and Capacity in buckets. Exported for the gob
	// encoder, which ignores unexported fields and would otherwise write
	// nothing at all.
	Resolution int
	Capacity   int

	Samples []Sample

	// The bucket being filled. Averaged into Samples when the next one starts,
	// so a coarse tier holds one mean per bucket rather than every raw point.
	Open      bool
	OpenStart int64
	OpenSum   float64
	OpenCount int
}

// Sample is one point: a Unix second and a value.
type Sample struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// tierSpec is the ladder every series gets.
//
// Two seconds is the collector's own interval, so the first tier is lossless
// for the last hour. A week at ten minutes is coarse, and coarse is the right
// answer for a week: nobody reads a seven-day chart to find a two-second spike.
var tierSpecs = []struct{ resolution, capacity int }{
	{2, 1800},   // 1 hour, exactly as sampled
	{60, 2880},  // 48 hours, one point a minute
	{600, 1008}, // 7 days, one point per ten minutes
}

func (t *tier) add(at int64, v float64) {
	res := int64(t.Resolution)
	bucket := (at / res) * res

	if t.Open && bucket != t.OpenStart {
		t.flush()
	}
	t.Open = true
	t.OpenStart = bucket
	t.OpenSum += v
	t.OpenCount++
}

func (t *tier) flush() {
	if t.OpenCount == 0 {
		t.Open = false
		return
	}
	t.Samples = append(t.Samples, Sample{T: t.OpenStart, V: t.OpenSum / float64(t.OpenCount)})
	if len(t.Samples) > t.Capacity {
		t.Samples = t.Samples[len(t.Samples)-t.Capacity:]
	}
	t.Open = false
	t.OpenSum, t.OpenCount = 0, 0
}

// read returns the closed buckets plus the one still filling.
//
// Including the open bucket is what makes the newest point visible before its
// boundary closes; without it a chart of the last hour would end up to two
// seconds in the past, and one of the last week up to ten minutes.
func (t *tier) read() []Sample {
	out := make([]Sample, len(t.Samples), len(t.Samples)+1)
	copy(out, t.Samples)
	if t.OpenCount > 0 {
		out = append(out, Sample{T: t.OpenStart, V: t.OpenSum / float64(t.OpenCount)})
	}
	return out
}

// span is how far back this tier reaches.
func (t *tier) span() int64 { return int64(t.Resolution) * int64(t.Capacity) }

// seriesLadder is one metric at every resolution.
type seriesLadder struct {
	Tiers []*tier
}

func newLadder() *seriesLadder {
	l := &seriesLadder{Tiers: make([]*tier, len(tierSpecs))}
	for i, spec := range tierSpecs {
		l.Tiers[i] = &tier{Resolution: spec.resolution, Capacity: spec.capacity}
	}
	return l
}

func (l *seriesLadder) add(at int64, v float64) {
	for _, t := range l.Tiers {
		t.add(at, v)
	}
}

// pick returns the finest tier that still covers the asked-for span.
func (l *seriesLadder) pick(span int64) *tier {
	for _, t := range l.Tiers {
		if t.span() >= span {
			return t
		}
	}
	return l.Tiers[len(l.Tiers)-1]
}

// Store holds every series.
type Store struct {
	mu     sync.Mutex
	series map[string]*seriesLadder
	path   string
}

// NewStore builds an empty store. path is where it is written; empty keeps it
// in memory only, which is what a test wants.
func NewStore(path string) *Store {
	return &Store{series: map[string]*seriesLadder{}, path: path}
}

// Add records one value.
func (s *Store) Add(metric string, at time.Time, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	l := s.series[metric]
	if l == nil {
		l = newLadder()
		s.series[metric] = l
	}
	l.add(at.Unix(), v)
}

// Metrics lists what has been recorded, sorted.
func (s *Store) Metrics() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.series))
	for k := range s.series {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Range returns up to points buckets covering the last span.
//
// Buckets are aligned to absolute Unix boundaries rather than to now, so two
// charts drawn a moment apart share an x-axis and can be read against each
// other instead of being offset by however long the second request took.
func (s *Store) Range(metric string, span time.Duration, points int) []Sample {
	if span <= 0 || points <= 0 {
		return []Sample{}
	}
	seconds := int64(span / time.Second)
	bucket := seconds / int64(points)
	if bucket < 1 {
		bucket = 1
	}

	s.mu.Lock()
	l := s.series[metric]
	if l == nil {
		s.mu.Unlock()
		return []Sample{}
	}
	raw := l.pick(seconds).read()
	s.mu.Unlock()

	cutoff := time.Now().Unix() - seconds
	out := make([]Sample, 0, points+1)

	var start int64 = -1
	var sum float64
	var n int

	flush := func() {
		if n > 0 {
			out = append(out, Sample{T: start, V: sum / float64(n)})
		}
		sum, n = 0, 0
	}

	for _, p := range raw {
		if p.T < cutoff {
			continue
		}
		b := (p.T / bucket) * bucket
		if start < 0 {
			start = b
		} else if b != start {
			flush()
			start = b
		}
		sum += p.V
		n++
	}
	flush()

	return out
}

// ── keeping it across a restart ─────────────────────────────────────────────

// Save writes the store to disk.
//
// Through a temporary file and a rename, so a crash while writing cannot leave
// a half-written file where the previous good one was. History is not worth
// risking anything else for, but losing it on every restart is what made the
// short window in memory the only thing there was.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}

	s.mu.Lock()
	snapshot := make(map[string]*seriesLadder, len(s.series))
	for name, l := range s.series {
		copyTiers := make([]*tier, len(l.Tiers))
		for i, t := range l.Tiers {
			samples := make([]Sample, len(t.Samples))
			copy(samples, t.Samples)
			copyTiers[i] = &tier{
				Resolution: t.Resolution, Capacity: t.Capacity, Samples: samples,
			}
		}
		snapshot[name] = &seriesLadder{Tiers: copyTiers}
	}
	s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("sysinfo: history directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".wui-history-*")
	if err != nil {
		return fmt.Errorf("sysinfo: history temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := gob.NewEncoder(tmp).Encode(snapshot); err != nil {
		tmp.Close()
		return fmt.Errorf("sysinfo: write history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("sysinfo: write history: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("sysinfo: install history: %w", err)
	}
	return nil
}

// Load reads a previously saved store.
//
// A missing file is the first start and not a failure. A file that cannot be
// read is reported and then ignored: starting without history is a much smaller
// problem than not starting.
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sysinfo: open history: %w", err)
	}
	defer f.Close()

	var data map[string]*seriesLadder
	if err := gob.NewDecoder(f).Decode(&data); err != nil {
		return fmt.Errorf("sysinfo: read history: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for name, l := range data {
		if l == nil || len(l.Tiers) == 0 {
			continue
		}
		// Rebuilt rather than adopted, so a file written by an older build with
		// a different ladder does not leave a series that can never roll over.
		fresh := newLadder()
		for _, t := range l.Tiers {
			for _, target := range fresh.Tiers {
				if target.Resolution == t.Resolution {
					samples := t.Samples
					if len(samples) > target.Capacity {
						samples = samples[len(samples)-target.Capacity:]
					}
					target.Samples = samples
				}
			}
		}
		s.series[name] = fresh
	}
	return nil
}
