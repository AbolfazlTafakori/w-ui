package service

import "testing"

// Where the panel listens after a restart: what the settings page saved,
// over what it was started with, a part at a time. A host saved without a
// port keeps the port the panel already has, rather than moving it to the
// subscription service's.
func TestSavedListenMergesOverTheCurrentAddress(t *testing.T) {
	for _, c := range []struct {
		current, host string
		port          int
		want          string
	}{
		{"0.0.0.0:2053", "", 0, "0.0.0.0:2053"},
		{"0.0.0.0:2053", "", 8443, "0.0.0.0:8443"},
		{"0.0.0.0:2053", "127.0.0.1", 0, "127.0.0.1:2053"},
		{"0.0.0.0:2053", "panel.example.com", 9443, "panel.example.com:9443"},
		{"[::]:2053", "::1", 0, "[::1]:2053"},
		{":2053", "", 0, ":2053"},
	} {
		if got := MergeListen(c.current, c.host, c.port); got != c.want {
			t.Errorf("MergeListen(%q, %q, %d) = %q, want %q", c.current, c.host, c.port, got, c.want)
		}
	}
}
