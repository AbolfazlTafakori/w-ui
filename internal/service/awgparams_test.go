package service

import "testing"

// The obfuscation profile has to be usable every time, not almost every time.
//
// A generator that produces a rejected profile once in a couple of hundred
// tunnels is worse than one that always does: the failure lands on whichever
// customer happens to be unlucky, and looks like a broken panel rather than a
// bad value.
func TestAGeneratedProfileIsAlwaysUsable(t *testing.T) {
	for i := 0; i < 20000; i++ {
		p := NewAWGParams()

		// Padding that makes a 148-byte initiation the same size as a 92-byte
		// response undoes the only thing the padding is there for.
		if p.S2-p.S1 == handshakeLengthGap {
			t.Fatalf("S1=%d S2=%d pad the handshake to one length", p.S1, p.S2)
		}
		// Header protection draws its nonce from the first 12 bytes of the
		// padding, so anything shorter cannot be switched on later without
		// reissuing every client config.
		for name, v := range map[string]int{"S1": p.S1, "S2": p.S2, "S3": p.S3, "S4": p.S4} {
			if v < 12 {
				t.Fatalf("%s=%d is too short for header protection", name, v)
			}
		}
		if p.Jmax <= p.Jmin {
			t.Fatalf("junk range is empty: Jmin=%d Jmax=%d", p.Jmin, p.Jmax)
		}
		// The four message types have to stay distinguishable to the far side,
		// and none may collide with the real WireGuard types 1-4.
		seen := map[uint32]string{}
		for name, h := range map[string]uint32{"H1": p.H1, "H2": p.H2, "H3": p.H3, "H4": p.H4} {
			if h <= 4 {
				t.Fatalf("%s=%d collides with a real WireGuard message type", name, h)
			}
			if other, dup := seen[h]; dup {
				t.Fatalf("%s and %s are both %d", name, other, h)
			}
			seen[h] = name
		}
	}
}

// Two interfaces must not share a profile. The reason for a second tunnel is
// usually that the first one is being blocked, and two tunnels that pad and
// label their packets identically look like one tunnel to whatever is doing the
// blocking.
func TestTwoProfilesDiffer(t *testing.T) {
	a, b := NewAWGParams(), NewAWGParams()
	if a == b {
		t.Error("two generated profiles are identical")
	}
}
