package logger

import (
	"testing"
	"time"
)

// A line read back from the journal has to come apart the way it went together,
// or the page shows one long string with the timestamp in it twice and no level
// to colour — which is what a log viewer is for.
func TestAPanelLineComesBackApart(t *testing.T) {
	line := `time=2026-09-05T05:19:20.688Z level=WARN msg="node is not in step with this panel" node=berlin error="could not reach it"`

	at, level, msg, fields, ok := parseSlogText(line)
	if !ok {
		t.Fatal("a line this panel wrote was not recognised as one")
	}
	if level != "WARN" {
		t.Errorf("level = %q, want WARN", level)
	}
	if msg != "node is not in step with this panel" {
		t.Errorf("msg = %q", msg)
	}
	if fields["node"] != "berlin" {
		t.Errorf("node = %v, want berlin", fields["node"])
	}
	// The quoted value keeps its spaces. Splitting on every space would turn
	// one field into three keys that mean nothing.
	if fields["error"] != "could not reach it" {
		t.Errorf("error = %v, want the whole sentence", fields["error"])
	}
	if want := time.Date(2026, 9, 5, 5, 19, 20, 688000000, time.UTC); !at.Equal(want) {
		t.Errorf("time = %v, want %v", at, want)
	}
}

// Everything else is left whole. A panic, a line from the Go runtime, a message
// from systemd itself — none of those have columns, and inventing some would
// lose the text an operator is trying to read.
func TestALineWeDidNotWriteIsLeftAlone(t *testing.T) {
	for _, line := range []string{
		"panic: runtime error: invalid memory address",
		"Started W-UI — WireGuard and OpenVPN panel.",
		"",
		"goroutine 1 [running]:",
		"some=thing that is not ours",
	} {
		if _, _, _, _, ok := parseSlogText(line); ok {
			t.Errorf("%q was taken apart as if this panel had written it", line)
		}
	}
}

// A message with an equals sign in it, which happens the moment anything logs a
// configuration value or a URL.
func TestAnEqualsSignInsideAMessageSurvives(t *testing.T) {
	_, _, msg, _, ok := parseSlogText(`time=2026-09-05T05:19:20Z level=INFO msg="listening url=http://127.0.0.1:37169/x/"`)
	if !ok {
		t.Fatal("the line was not recognised")
	}
	if msg != "listening url=http://127.0.0.1:37169/x/" {
		t.Errorf("msg = %q; the message was cut at its own equals sign", msg)
	}
}

// An escaped quote inside a message. Getting this wrong ends the quoted section
// early and turns the rest of the line into keys.
func TestAnEscapedQuoteDoesNotEndTheMessage(t *testing.T) {
	_, _, msg, fields, ok := parseSlogText(`time=2026-09-05T05:19:20Z level=ERROR msg="cannot open \"wg0\"" iface=wg0`)
	if !ok {
		t.Fatal("the line was not recognised")
	}
	if msg != `cannot open "wg0"` {
		t.Errorf("msg = %q", msg)
	}
	if fields["iface"] != "wg0" {
		t.Errorf("the field after an escaped quote was lost: %v", fields)
	}
}

// ── what the journal wraps around it ────────────────────────────────────────

func TestJournalPrioritiesBecomeLevels(t *testing.T) {
	cases := map[string]string{
		"0": "ERROR", "3": "ERROR", "4": "WARN", "6": "INFO", "7": "DEBUG", "": "INFO",
	}
	for priority, want := range cases {
		if got := priorityLevel(priority); got != want {
			t.Errorf("priority %q became %q, want %q", priority, got, want)
		}
	}
}
