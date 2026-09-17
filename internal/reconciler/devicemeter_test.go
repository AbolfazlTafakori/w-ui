package reconciler

import "testing"

// The meter charges a tunnel with what grew between readings: nothing on
// first sight, the difference after, and what a restarted counter shows.
func TestDeviceMeterChargesWhatGrew(t *testing.T) {
	m := newDeviceMeter()
	if _, _, ok := m.step(1, 1000, 2000); ok {
		t.Fatal("first sight is a baseline, not traffic")
	}
	up, down, ok := m.step(1, 1500, 2600)
	if !ok || up != 500 || down != 600 {
		t.Fatalf("second reading: up %d down %d ok %v", up, down, ok)
	}
	if _, _, ok := m.step(1, 1500, 2600); ok {
		t.Fatal("an idle file carries nothing")
	}
	up, down, ok = m.step(1, 40, 70)
	if !ok || up != 40 || down != 70 {
		t.Fatalf("a counter that started over: up %d down %d", up, down)
	}
	m.keep(map[uint]bool{})
	if _, _, ok := m.step(1, 900, 900); ok {
		t.Fatal("a file that left the readings starts from a baseline again")
	}
}
