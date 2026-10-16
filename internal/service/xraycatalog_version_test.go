package service

import "testing"

// A panel's version decides what is spoken to it and offered for it, so it
// is read as the panel writes it.
func TestVersionBelow(t *testing.T) {
	for _, c := range []struct {
		v            string
		major, minor int
		want         bool
	}{
		{"3.10.2", 3, 9, false}, // numbers, not text: 3.10 is newer than 3.9
		{"3.9.4", 3, 10, true},
		{"v2.9.4", 2, 8, false},
		{"2.7.1", 2, 8, true},
		{"3.10-beta", 3, 10, false},
		{"3.1", 3, 1, false},
		{"3", 3, 1, false},   // not read: not older
		{"", 3, 1, false},    // nor this
		{"x.y", 3, 1, false}, // nor this
	} {
		if got := VersionBelow(c.v, c.major, c.minor); got != c.want {
			t.Errorf("VersionBelow(%q, %d, %d) = %v, want %v", c.v, c.major, c.minor, got, c.want)
		}
	}
}
