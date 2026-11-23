package ovpndriver

import (
	"strconv"
	"testing"
	"time"
)

// A status report is the answer only while OpenVPN is still writing it: by
// the time on its own TIME line, within three of its ten-second rewrites.
func TestAStatusReportIsFreshOnlyWhileItIsBeingWritten(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) string {
		return "TITLE,OpenVPN 2.6.19\r\nTIME,x," + strconv.FormatInt(now.Add(d).Unix(), 10) + "\r\nEND\r\n"
	}
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"just written", at(0), true},
		{"one rewrite ago", at(-10 * time.Second), true},
		{"three rewrites missed", at(-40 * time.Second), false},
		{"six days old", at(-6 * 24 * time.Hour), false},
		{"far ahead of this clock", at(10 * time.Minute), false},
		{"no time line", "TITLE,OpenVPN\nEND\n", false},
		{"a time that is not a number", "TIME,x,soon\nEND\n", false},
	} {
		if got := statusFresh(tc.raw, now); got != tc.want {
			t.Errorf("%s: fresh = %v, want %v", tc.name, got, tc.want)
		}
	}
}
