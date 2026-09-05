package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Keeping recent log lines so the panel can show them.
//
// Diagnosing a customer's problem otherwise means opening an SSH session and
// reading journalctl, which is a different skill and a different machine from
// the panel someone is already looking at. The recent past is kept in memory:
// enough to answer "what just happened", not a substitute for the real log.

// ringSize is how many entries are kept.
//
// A thousand, because searching is only as good as what there is to search: a
// filter that can only see the last two hundred lines finds nothing from the
// hour the problem started. Still under a megabyte, and still not a substitute
// for the real log — see the journal source for that.
const ringSize = 1000

// Entry is one log line, already parsed.
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// Ring holds the most recent entries.
//
// It is a fixed-size circular buffer rather than a slice that is trimmed,
// because trimming copies the whole thing on every line and this is written to
// from the hot path of a panel doing work every two seconds.
type Ring struct {
	mu      sync.RWMutex
	entries []Entry
	next    int
	filled  bool
}

// NewRing builds an empty ring.
func NewRing() *Ring {
	return &Ring{entries: make([]Entry, ringSize)}
}

// Add stores one entry.
func (r *Ring) Add(e Entry) {
	r.mu.Lock()
	r.entries[r.next] = e
	r.next = (r.next + 1) % len(r.entries)
	if r.next == 0 {
		r.filled = true
	}
	r.mu.Unlock()
}

// Recent returns up to n entries, newest first, optionally filtered.
//
// query matches anywhere in the message or in a field, case insensitively.
// Applied before the count rather than after: a search that could only look at
// the last twenty lines would answer "not found" for something that is there,
// which is worse than no search at all.
func (r *Ring) Recent(n int, minLevel, query string) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if n <= 0 || n > len(r.entries) {
		n = len(r.entries)
	}
	threshold := levelRank(minLevel)
	needle := strings.ToLower(strings.TrimSpace(query))

	out := make([]Entry, 0, n)
	// Walk backwards from the newest so the caller does not have to reverse a
	// list that is already in the wrong order for reading.
	count := len(r.entries)
	if !r.filled {
		count = r.next
	}
	for i := 0; i < count && len(out) < n; i++ {
		idx := (r.next - 1 - i + len(r.entries)*2) % len(r.entries)
		e := r.entries[idx]
		if e.Time.IsZero() {
			continue
		}
		if levelRank(e.Level) < threshold {
			continue
		}
		if needle != "" && !matches(e, needle) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// matches reports whether an entry contains the needle, which is already lower
// case.
//
// The fields are searched as well as the message, because what an operator
// actually types is a customer's name, an interface, or an address — and those
// are values, not words in the sentence.
func matches(e Entry, needle string) bool {
	if strings.Contains(strings.ToLower(e.Message), needle) {
		return true
	}
	for k, v := range e.Fields {
		if strings.Contains(strings.ToLower(k), needle) {
			return true
		}
		if strings.Contains(strings.ToLower(fmt.Sprint(v)), needle) {
			return true
		}
	}
	return false
}

// levelRank orders levels so a filter can be a comparison. An unknown level
// sorts with info, which is where a line from an unexpected source is least
// likely to be either hidden or alarming.
func levelRank(level string) int {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return 0
	case "WARN", "WARNING":
		return 2
	case "ERROR":
		return 3
	default:
		return 1
	}
}

// ringHandler forwards every record to a Ring as well as to the real handler.
type ringHandler struct {
	slog.Handler
	ring  *Ring
	attrs []slog.Attr
}

// Tee wraps a handler so everything it writes is also kept in the ring.
func Tee(h slog.Handler, ring *Ring) slog.Handler {
	return &ringHandler{Handler: h, ring: ring}
}

func (h *ringHandler) Handle(ctx context.Context, rec slog.Record) error {
	fields := make(map[string]any, rec.NumAttrs()+len(h.attrs))
	for _, a := range h.attrs {
		fields[a.Key] = attrValue(a)
	}
	rec.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = attrValue(a)
		return true
	})

	h.ring.Add(Entry{
		Time:    rec.Time,
		Level:   rec.Level.String(),
		Message: rec.Message,
		Fields:  fields,
	})
	return h.Handler.Handle(ctx, rec)
}

func (h *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ringHandler{
		Handler: h.Handler.WithAttrs(attrs),
		ring:    h.ring,
		attrs:   append(append([]slog.Attr{}, h.attrs...), attrs...),
	}
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	return &ringHandler{Handler: h.Handler.WithGroup(name), ring: h.ring, attrs: h.attrs}
}

// attrValue reduces an attribute to something that survives JSON.
//
// A value that cannot be encoded would otherwise make the whole log response
// fail, which is the least helpful moment for the log to stop working.
func attrValue(a slog.Attr) any {
	v := a.Value.Resolve().Any()
	switch t := v.(type) {
	case error:
		return t.Error()
	case time.Time:
		return t.Format(time.RFC3339)
	case time.Duration:
		return t.String()
	}
	if _, err := json.Marshal(v); err != nil {
		return a.Value.String()
	}
	return v
}
