//go:build linux

package shaper

import "testing"

// A device no longer shaped is forgotten, so a tunnel removed and made again
// under its name is laid down as new, not warned about as tampered with.
func TestADeviceNoLongerShapedIsForgotten(t *testing.T) {
	s := New(nil)
	s.rooted["wg0"], s.rooted["ike0"] = true, true
	s.forgetGone([]string{"wg0"})
	if !s.rooted["wg0"] || s.rooted["ike0"] {
		t.Fatalf("remembered = %v", s.rooted)
	}
}
