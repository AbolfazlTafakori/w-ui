package logger

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// An area's debug lines are written only while it is on, at any level of
// the panel's own, and say their area; its info lines always are.
func TestAnAreasDebugLinesFollowItsSwitch(t *testing.T) {
	t.Cleanup(func() { SetDebugAreas("") })
	var buf bytes.Buffer
	base := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cap := Area(base, AreaCapacity)

	SetDebugAreas("")
	cap.Debug("units taken", "n", 2)
	cap.Info("an info line")
	if strings.Contains(buf.String(), "units taken") || !strings.Contains(buf.String(), "an info line") {
		t.Fatalf("off: %q", buf.String())
	}
	buf.Reset()
	SetDebugAreas("cutoff, capacity")
	cap.Debug("units taken", "n", 2)
	if !strings.Contains(buf.String(), "units taken") || !strings.Contains(buf.String(), "area=capacity") {
		t.Fatalf("on: %q", buf.String())
	}
	buf.Reset()
	SetDebugAreas("nodes")
	cap.Debug("units taken")
	if buf.Len() != 0 {
		t.Fatalf("another area on: %q", buf.String())
	}
	SetDebugAreas("all")
	cap.With("client", 7).Debug("units taken")
	if !strings.Contains(buf.String(), "client=7") {
		t.Fatalf("all, with an attribute: %q", buf.String())
	}
}

// What an area's debug line costs while the area is off: the price every
// such line in a hot path pays on a panel that never turns it on.
func BenchmarkAnAreaOff(b *testing.B) {
	SetDebugAreas("")
	l := Area(slog.New(slog.NewTextHandler(io.Discard, nil)), AreaTraffic)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Debug("usage written", "client", i, "bytes", uint64(i))
	}
}
