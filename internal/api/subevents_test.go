package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A page holding the stream is told the moment the hub is woken, and the
// stream ends cleanly when the page goes away.
func TestOpenPagesAreToldTheMomentSomethingChanges(t *testing.T) {
	s := &Server{subHub: newSubHub()}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/sub/x?view=events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.serveSubEvents(rec, req); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for s.subHub.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.subHub.count() != 1 {
		t.Fatal("the page did not subscribe")
	}
	s.subHub.Broadcast()
	// The recorder is read only once the handler has returned, so the
	// reading and the writing never overlap.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end with the request")
	}
	if !strings.Contains(rec.Body.String(), "event: changed") {
		t.Fatalf("no change event written: %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	if s.subHub.count() != 0 {
		t.Fatal("the page was not unsubscribed")
	}
}
