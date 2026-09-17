package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Telling open subscription pages the moment something changed.
//
// Polling every three seconds is a three-second wait. A page that holds a
// stream open instead is told at once -- the panel writes one line into the
// stream when anything is changed through the API, or when the reconciler
// ends or starts a plan -- and the page asks for its fingerprint right then.
// The change reaches the customer in the time a request takes, not in the
// time a poll interval does. The poll stays, for a network that will not
// hold a stream open; the stream is what makes the page immediate.

// subHub is every open page, waiting.
type subHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newSubHub() *subHub { return &subHub{subs: map[chan struct{}]struct{}{}} }

// Broadcast wakes every open page. Non-blocking: a page that has not yet
// read its last wake gets no second one, and one wake is enough.
func (h *subHub) Broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *subHub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *subHub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// maxSubStreams bounds how many pages may hold a stream at once; the
// pages beyond it fall back to polling, which they do anyway.
const maxSubStreams = 2000

func (h *subHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// serveSubEvents holds one page's stream open and writes a line into it
// whenever the hub is woken, with a comment every 25 seconds so nothing on
// the way -- a proxy, a NAT -- closes it as idle. The page reacts to any
// line by fetching its status, which carries the fingerprint it compares.
func (s *Server) serveSubEvents(w http.ResponseWriter, r *http.Request) {
	// Through whatever wrapped the writer on the way here.
	rc := http.NewResponseController(w)
	flusher := http.Flusher(flushVia{rc})
	if s.subHub.count() >= maxSubStreams {
		http.Error(w, "streaming unavailable", http.StatusServiceUnavailable)
		return
	}
	// The server's write deadline is for requests that answer and finish;
	// this one stays open on purpose, so its deadline is lifted.
	_ = rc.SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	ch := s.subHub.subscribe()
	defer s.subHub.unsubscribe(ch)
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "event: changed\ndata: 1\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// flushVia flushes through http.ResponseController, which walks Unwrap.
type flushVia struct{ rc *http.ResponseController }

func (f flushVia) Flush() { _ = f.rc.Flush() }
