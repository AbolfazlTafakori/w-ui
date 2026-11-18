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
	// byAddr is how many streams each address holds, so one address --
	// one link opened in a loop -- cannot take every stream there is.
	byAddr map[string]int
}

func newSubHub() *subHub {
	return &subHub{subs: map[chan struct{}]struct{}{}, byAddr: map[string]int{}}
}

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

// admit opens a stream for a page at addr, or says no: the panel holds
// maxSubStreams at most, and one address maxSubStreamsPerAddr of them.
// Checked and taken under one lock, so a burst cannot slip past either.
func (h *subHub) admit(addr string) (chan struct{}, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= maxSubStreams || (addr != "" && h.byAddr[addr] >= maxSubStreamsPerAddr) {
		return nil, false
	}
	ch := make(chan struct{}, 1)
	h.subs[ch] = struct{}{}
	if addr != "" {
		h.byAddr[addr]++
	}
	return ch, true
}

func (h *subHub) release(ch chan struct{}, addr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
	if addr != "" {
		if h.byAddr[addr] <= 1 {
			delete(h.byAddr, addr)
		} else {
			h.byAddr[addr]--
		}
	}
}

// maxSubStreams bounds how many pages may hold a stream at once; the
// pages beyond it fall back to polling, which they do anyway.
const maxSubStreams = 2000

// maxSubStreamsPerAddr is how many of them one address may hold. Generous,
// since many customers can share one address behind a carrier's NAT; a page
// refused falls back to polling and works as before.
const maxSubStreamsPerAddr = 32

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
	addr := clientIP(r)
	ch, ok := s.subHub.admit(addr)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusServiceUnavailable)
		return
	}
	defer s.subHub.release(ch, addr)
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

// Cleanup on the way out: nothing is left to do if it fails, and the error that brought us here is the one reported.
func (f flushVia) Flush() { _ = f.rc.Flush() }
