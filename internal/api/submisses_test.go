package api

import (
	"testing"
	"time"
)

// A scanner guessing links is told to wait after enough misses; a miss from
// another address, or after the window, starts fresh.
func TestGuessedLinksAreThrottledPerAddress(t *testing.T) {
	m := newSubMisses()
	t0 := time.Now()
	for i := 0; i < subMissLimit; i++ {
		if b, _ := m.blocked("1.1.1.1", t0); b {
			t.Fatalf("blocked after %d misses", i)
		}
		m.miss("1.1.1.1", t0)
	}
	if b, wait := m.blocked("1.1.1.1", t0.Add(time.Second)); !b || wait <= 0 {
		t.Fatal("not blocked after the limit")
	}
	if b, _ := m.blocked("2.2.2.2", t0); b {
		t.Fatal("another address was blocked")
	}
	if b, _ := m.blocked("1.1.1.1", t0.Add(subMissWindow+time.Second)); b {
		t.Fatal("still blocked after the window")
	}
}
