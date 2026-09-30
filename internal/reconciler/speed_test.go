package reconciler

import (
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/enforce"
)

// A customer's speed is what the kernel counted them moving, each way, over
// the last few ticks -- averaged, so one busy second does not read as a
// busy customer -- and they drop out once they have stopped.
func TestSpeedIsTheRecentTicksEachWay(t *testing.T) {
	s := newSpeedTracker()
	t0 := time.Now()

	// The first tick after a start measures nothing: it covers however long
	// the panel was down.
	s.local(map[uint]usageDelta{1: {Up: 999_999, Down: 999_999}}, t0)
	if got := s.speeds([]uint{1}, t0); len(got) != 0 {
		t.Fatalf("the first tick was read as a speed: %v", got)
	}

	// Two ticks two seconds apart: 2 MB down and 200 KB up over four seconds.
	s.local(map[uint]usageDelta{1: {Up: 100_000, Down: 1_000_000}}, t0.Add(2*time.Second))
	s.local(map[uint]usageDelta{1: {Up: 100_000, Down: 1_000_000}}, t0.Add(4*time.Second))
	got := s.speeds([]uint{1, 2}, t0.Add(4*time.Second))
	if got[1].Down != 500_000 || got[1].Up != 50_000 {
		t.Errorf("speed = %+v, want 500000 down and 50000 up a second", got[1])
	}
	if _, ok := got[2]; ok {
		t.Error("a customer who moved nothing has a speed")
	}

	// Idle ticks: the speed falls, and past the window it is gone.
	s.local(map[uint]usageDelta{}, t0.Add(6*time.Second))
	s.local(map[uint]usageDelta{}, t0.Add(8*time.Second))
	if sp := s.speeds([]uint{1}, t0.Add(8*time.Second))[1]; sp.Down >= 500_000 {
		t.Errorf("idle ticks did not lower the speed: %+v", sp)
	}
	s.local(map[uint]usageDelta{}, t0.Add(10*time.Second))
	s.local(map[uint]usageDelta{}, t0.Add(12*time.Second))
	if got := s.speeds([]uint{1}, t0.Add(12*time.Second)); len(got) != 0 {
		t.Errorf("a customer idle for longer than the window still has a speed: %v", got)
	}
}

// A customer on another node is known from that node's reports: the traffic
// in one, over the time it covers, standing until it is stale.
func TestANodesCustomerHasTheSpeedOfItsReport(t *testing.T) {
	s := newSpeedTracker()
	now := time.Now()
	s.fromNode(7, 20_000_000, 200_000_000, 20*time.Second, now)
	got := s.speeds([]uint{7}, now.Add(5*time.Second))
	if got[7].Down != 10_000_000 || got[7].Up != 1_000_000 {
		t.Errorf("node customer speed = %+v, want 10 MB/s down and 1 MB/s up", got[7])
	}
	if got := s.speeds([]uint{7}, now.Add(remoteSpeedStands)); len(got) != 0 {
		t.Errorf("a stale node report still stands: %v", got)
	}
}

// Through the reconciler's own collection: what the kernel counters report for
// a customer, each way, is their speed on the list.
func TestCollectionFeedsTheSpeed(t *testing.T) {
	r, _, enf, _ := newRig(t)
	ctx := t.Context()

	// The first collection sets the clock; the second is measured against it.
	if _, err := r.collect(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	enf.drain = []enforce.Usage{{Key: keyFromClientID(3), Bytes: 4_400_000, Up: 400_000, Down: 4_000_000}}
	if _, err := r.collect(ctx); err != nil {
		t.Fatal(err)
	}

	sp, ok := r.Speeds([]uint{3, 4})[3]
	if !ok {
		t.Fatal("a customer the kernel counted has no speed")
	}
	// Ten times as much down as up, as counted; the exact figure depends on
	// the time between the two collections.
	if sp.Down == 0 || sp.Up == 0 || sp.Down/sp.Up < 9 || sp.Down/sp.Up > 11 {
		t.Errorf("speed = %+v, want down ten times up", sp)
	}
	if _, ok := r.Speeds([]uint{4})[4]; ok {
		t.Error("a customer the kernel did not count has a speed")
	}
}
