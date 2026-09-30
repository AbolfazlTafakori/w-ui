package reconciler

import (
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Each customer's speed right now, in each direction.
//
// Worked out from what the kernel counted each tick -- the bytes a customer
// moved up and down since the last one -- rather than from the stored totals,
// which reach the database a flush later and move in steps. A few ticks are
// averaged, so the number read on the customer list does not jump with every
// two-second sample; a customer who stops moving traffic drops out of it once
// that window has passed.
//
// A customer on another node is known only from that node's report, every
// twenty seconds or so: their speed is that report spread over the time it
// covers, and it stands until the next.

// speedWindow is how much of the recent past a local speed is averaged over.
const speedWindow = 6 * time.Second

// remoteSpeedStands is how long a node's report is taken as the speed; a
// little more than the time between reports, so a node that answers late
// does not flicker its customers to nothing.
const remoteSpeedStands = 30 * time.Second

type speedSample struct {
	at    time.Time
	span  time.Duration
	bytes map[uint]usageDelta
}

type remoteSpeed struct {
	speed model.Speed
	at    time.Time
}

type speedTracker struct {
	mu      sync.Mutex
	samples []speedSample // newest last, within speedWindow
	last    time.Time     // when the previous local tick was read
	remote  map[uint]remoteSpeed
}

func newSpeedTracker() *speedTracker {
	return &speedTracker{remote: map[uint]remoteSpeed{}}
}

// local records one tick of what this server's customers moved.
func (s *speedTracker) local(billed map[uint]usageDelta, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.IsZero() {
		// The first tick after a start has nothing to be measured against:
		// the counters it drained cover however long the panel was down.
		copied := make(map[uint]usageDelta, len(billed))
		for id, d := range billed {
			copied[id] = d
		}
		s.samples = append(s.samples, speedSample{at: now, span: now.Sub(s.last), bytes: copied})
	}
	s.last = now
	s.prune(now)
}

// fromNode records what a node reported one customer moving over span.
func (s *speedTracker) fromNode(clientID uint, up, down uint64, span time.Duration, now time.Time) {
	if span <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	secs := span.Seconds()
	s.remote[clientID] = remoteSpeed{
		speed: model.Speed{Up: uint64(float64(up) / secs), Down: uint64(float64(down) / secs)},
		at:    now,
	}
}

func (s *speedTracker) prune(now time.Time) {
	keep := s.samples[:0]
	for _, x := range s.samples {
		if now.Sub(x.at) < speedWindow {
			keep = append(keep, x)
		}
	}
	s.samples = keep
	for id, r := range s.remote {
		if now.Sub(r.at) >= remoteSpeedStands {
			delete(s.remote, id)
		}
	}
}

// speeds answers for these customers; one not moving anything is absent.
func (s *speedTracker) speeds(ids []uint, now time.Time) map[uint]model.Speed {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)

	var span time.Duration
	for _, x := range s.samples {
		span += x.span
	}
	out := map[uint]model.Speed{}
	for _, id := range ids {
		var up, down uint64
		for _, x := range s.samples {
			if d, ok := x.bytes[id]; ok {
				up += d.Up
				down += d.Down
			}
		}
		var sp model.Speed
		if span > 0 && up+down > 0 {
			secs := span.Seconds()
			sp = model.Speed{Up: uint64(float64(up) / secs), Down: uint64(float64(down) / secs)}
		}
		if r, ok := s.remote[id]; ok {
			sp.Up += r.speed.Up
			sp.Down += r.speed.Down
		}
		if sp.Up+sp.Down > 0 {
			out[id] = sp
		}
	}
	return out
}
