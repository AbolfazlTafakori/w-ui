package l2tpagent

import "testing"

// Both ways ocserv has written its version are read.
func TestTheOcservVersionIsReadEitherWay(t *testing.T) {
	for out, want := range map[string]bool{
		"ocserv 1.1.6\n\nCompiled with: seccomp, oath, radius": false,
		"OpenConnect VPN Server 1.3.0\n\nCompiled with: seccomp": true,
		"ocserv 1.2.4\n": true,
		"ocserv 0.12.6\n": false,
		"OpenConnect VPN Server 2.0\n": true,
		"":   false,
		"command not found\n": false,
	} {
		if got := camouflageIn(out); got != want {
			t.Errorf("%q: %v, want %v", out, got, want)
		}
	}
}
