package reconciler

import (
	"errors"
	"testing"
)

// A tick that cannot read which resellers are suspended keeps the last
// answer rather than treating every reseller as in good standing -- which
// would put a switched-off reseller's customers back on the tunnels for as
// long as the database stayed unreachable.
func TestSuspensionIsKeptThroughAFailedRead(t *testing.T) {
	r := &Reconciler{log: quietLog()}

	got := r.lastSuspended(map[uint]bool{7: true}, nil)
	if !got[7] {
		t.Fatal("a suspended reseller was not reported suspended")
	}

	got = r.lastSuspended(nil, errors.New("database is locked"))
	if !got[7] {
		t.Fatal("a failed read restored a switched-off reseller's customers")
	}

	// A good read replaces it, including with nobody suspended.
	got = r.lastSuspended(map[uint]bool{}, nil)
	if got[7] {
		t.Fatal("a reseller switched back on stayed suspended")
	}
}
