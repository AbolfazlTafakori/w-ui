package config

import (
	"testing"
)

// WUI_DEBUG names the areas whose debug lines are wanted (internal/logger),
// and a panel started with it must start. A true or false keeps its older
// meaning, the ORM's own log, and is no list of areas.
func TestWUIDebugTakesAListOfAreasAndStillABool(t *testing.T) {
	t.Setenv("WUI_DATA_DIR", t.TempDir())
	for _, tc := range []struct {
		value string
		debug bool
	}{
		{"limit", false},
		{"capacity,cutoff", false},
		{"all", false},
		{"true", true},
		{"1", true},
		{"false", false},
	} {
		t.Setenv("WUI_DEBUG", tc.value)
		c, err := Load()
		if err != nil {
			t.Fatalf("WUI_DEBUG=%s: the panel would not start: %v", tc.value, err)
		}
		if c.Debug != tc.debug {
			t.Errorf("WUI_DEBUG=%s: Debug = %v, want %v", tc.value, c.Debug, tc.debug)
		}
	}
}
