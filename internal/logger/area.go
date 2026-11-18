package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// Debug lines by area.
//
// The panel runs at info. When something goes wrong on a server -- units
// that did not move as expected, a customer cut off late, a device held off
// that should not have been, a node that would not sync -- the operator can
// turn on the debug lines of that one part, from WUI_DEBUG, without the
// whole panel's debug output burying them:
//
//	WUI_DEBUG=capacity,cutoff     two areas
//	WUI_DEBUG=all                 every area
//
// The areas are capacity (units taken, given and refused), traffic (usage
// written), cutoff (customers ended and why), limit (connections counted and
// held off), nodes (what nodes were sent and said) and bot (the Telegram and
// Bale bots). Off, an area's debug call is one look in a small map and
// nothing is formatted (BenchmarkAnAreaOff).
const (
	AreaCapacity = "capacity"
	AreaTraffic  = "traffic"
	AreaCutoff   = "cutoff"
	AreaLimit    = "limit"
	AreaNodes    = "nodes"
	AreaBot      = "bot"
)

// areasOn is the areas turned on; nil is none.
var areasOn atomic.Pointer[map[string]bool]

func init() { SetDebugAreas(os.Getenv("WUI_DEBUG")) }

// SetDebugAreas turns on the debug lines of the areas named, comma
// separated, or every area for "all"; anything else is off.
func SetDebugAreas(list string) {
	on := map[string]bool{}
	for _, a := range strings.Split(list, ",") {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			on[a] = true
		}
	}
	areasOn.Store(&on)
}

// DebugOn reports whether an area's debug lines are on.
func DebugOn(area string) bool {
	on := areasOn.Load()
	return on != nil && ((*on)[area] || (*on)["all"])
}

// Area is the logger for one area: what it logs at info and above goes
// where the panel's log goes, as always; its debug lines are written only
// while the area is on, whatever the panel's level, and say which area
// they are from. base nil is the panel's default logger, as it is at the
// time of each line.
func Area(base *slog.Logger, area string) *slog.Logger {
	return slog.New(&areaHandler{base: base, area: area})
}

type areaHandler struct {
	base  *slog.Logger
	area  string
	attrs []slog.Attr
	group string
}

func (h *areaHandler) inner() slog.Handler {
	if h.base != nil {
		return h.base.Handler()
	}
	return slog.Default().Handler()
}

func (h *areaHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < slog.LevelInfo {
		return DebugOn(h.area)
	}
	return h.inner().Enabled(ctx, level)
}

func (h *areaHandler) Handle(ctx context.Context, rec slog.Record) error {
	inner := h.inner()
	if h.group != "" {
		inner = inner.WithGroup(h.group)
	}
	if len(h.attrs) > 0 {
		inner = inner.WithAttrs(h.attrs)
	}
	if rec.Level < slog.LevelInfo {
		rec.AddAttrs(slog.String("area", h.area))
	}
	// The inner handler's own level would drop a debug line at info; the
	// area said yes already, so it is handed over as it is.
	return inner.Handle(ctx, rec)
}

func (h *areaHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &c
}

func (h *areaHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.group = name
	return &c
}
