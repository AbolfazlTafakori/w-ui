package backend

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// A tunnel that will not open is shown from when it began to fail: the
// time is kept across the attempts that fail after it, and gone once it
// opens. Seen on the test server: its problem said it began in the year 1.
func TestATunnelFailingIsShownFromWhenItBegan(t *testing.T) {
	p := NewPool(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !p.FailingSince(7).IsZero() {
		t.Fatal("a tunnel never tried is failing")
	}
	before := time.Now().UTC()
	p.record(7, nil, "", errors.New("no amneziawg module"))
	first := p.FailingSince(7)
	if first.Before(before) || first.After(time.Now().UTC()) {
		t.Fatalf("failing since %v, want the moment it first failed", first)
	}
	time.Sleep(5 * time.Millisecond)
	p.record(7, nil, "", errors.New("no amneziawg module"))
	if got := p.FailingSince(7); !got.Equal(first) {
		t.Fatalf("a second failure moved the start from %v to %v", first, got)
	}
	p.record(7, &downDriver{}, "fp", nil)
	if !p.FailingSince(7).IsZero() {
		t.Fatal("a tunnel that opened is still failing")
	}
}
