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

// The kernel's bill is shared out among a customer's files in proportion
// to what each tunnel counted, so the per-tunnel figures add up to the
// customer's total exactly -- the tunnels' own counters, which include
// their encryption overhead, run a few percent high.
func TestApportionMatchesTheBill(t *testing.T) {
	billed := map[uint]usageDelta{7: {Bytes: 1000, Up: 100, Down: 900}}
	grown := map[uint]usageDelta{1: {Up: 30, Down: 700}, 2: {Up: 80, Down: 240}, 3: {Up: 5, Down: 5}}
	clientOf := map[uint]uint{1: 7, 2: 7, 3: 9}
	out := apportion(billed, grown, clientOf)
	var up, down uint64
	for _, a := range []uint{1, 2} {
		up += out[a].Up
		down += out[a].Down
	}
	if up != 100 || down != 900 {
		t.Fatalf("the files add up to the bill: up %d down %d (%+v)", up, down, out)
	}
	if out[1].Down <= out[2].Down {
		t.Fatalf("shares follow the tunnels: %+v", out)
	}
	if out[3] != (usageDelta{Up: 5, Down: 5}) {
		t.Fatalf("a file with no bill keeps its own count: %+v", out[3])
	}
}
