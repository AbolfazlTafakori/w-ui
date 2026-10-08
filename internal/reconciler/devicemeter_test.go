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
	out := apportion(billed, grown, clientOf, nil)
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

// OpenVPN rewrites its counters every ten seconds and the kernel is read
// every two, so most ticks bill a customer while no file has moved. Every
// one of those bills still reaches the table: the file total over a run of
// ticks is the bill over the same ticks, to the byte.
func TestApportionKeepsBillsWhileFilesAreStill(t *testing.T) {
	m := newDeviceMeter()
	clientOf := map[uint]uint{1: 7}
	var billedSum, fileSum uint64
	for tick := 0; tick < 10; tick++ {
		billed := map[uint]usageDelta{7: {Bytes: 1000, Up: 100, Down: 900}}
		grown := map[uint]usageDelta{}
		if tick%5 == 4 {
			grown[1] = usageDelta{Up: 520, Down: 4700}
		}
		billedSum += 1000
		for _, d := range m.apportion(billed, grown, clientOf) {
			fileSum += d.Up + d.Down
		}
	}
	if fileSum != billedSum {
		t.Fatalf("the table lost bills on still ticks: files %d, billed %d", fileSum, billedSum)
	}
}

// With several files and none moving, the bill follows the shares of the
// last tick that did move, rather than being dropped or split blindly.
func TestApportionUsesTheLastSharesWhenNothingMoves(t *testing.T) {
	m := newDeviceMeter()
	clientOf := map[uint]uint{1: 7, 2: 7}
	m.apportion(map[uint]usageDelta{7: {Bytes: 400, Down: 400}},
		map[uint]usageDelta{1: {Down: 300}, 2: {Down: 100}}, clientOf)

	out := m.apportion(map[uint]usageDelta{7: {Bytes: 800, Down: 800}}, map[uint]usageDelta{}, clientOf)
	if out[1].Down != 600 || out[2].Down != 200 {
		t.Fatalf("still tick should follow the last shares 3:1, got %+v", out)
	}
}

// A customer billed before any file has a reading at all -- the panel just
// started, every file is only a baseline -- is split evenly over their
// files, and a customer with one file gets the whole bill on it.
func TestApportionWithNoSharesYet(t *testing.T) {
	out := apportion(map[uint]usageDelta{7: {Bytes: 900, Down: 900}}, map[uint]usageDelta{},
		map[uint]uint{1: 7}, nil)
	if out[1].Down != 900 {
		t.Fatalf("one file takes the whole bill, got %+v", out)
	}
	out = apportion(map[uint]usageDelta{7: {Bytes: 901, Down: 901}}, map[uint]usageDelta{},
		map[uint]uint{1: 7, 2: 7, 3: 9}, nil)
	if out[1].Down+out[2].Down != 901 || out[3] != (usageDelta{}) {
		t.Fatalf("an even split over the customer's own files, got %+v", out)
	}
}

// A file that was deleted, or moved to another customer, stops carrying
// its old share.
func TestApportionForgetsFilesThatLeft(t *testing.T) {
	m := newDeviceMeter()
	m.apportion(map[uint]usageDelta{7: {Bytes: 100, Down: 100}},
		map[uint]usageDelta{1: {Down: 50}, 2: {Down: 50}}, map[uint]uint{1: 7, 2: 7})
	out := m.apportion(map[uint]usageDelta{7: {Bytes: 100, Down: 100}}, map[uint]usageDelta{},
		map[uint]uint{1: 7})
	if out[1].Down != 100 || out[2] != (usageDelta{}) {
		t.Fatalf("the remaining file takes it all, got %+v", out)
	}
}
