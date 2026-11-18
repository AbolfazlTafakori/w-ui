package egress

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

// A resolver that hangs -- a filtered or slow one, as on many Iranian hosts
// -- does not hold the panel's start: StartResolver returns within its
// short wait, and the names land in the cache once the lookup answers.
func TestASlowResolverDoesNotHoldTheStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := testDB(t)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := NewOutbounds(db, nil, quietLog()).EnsureBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewRouting(db, nil, quietLog())
	if _, err := r.SaveBasic(ctx, BasicRouting{BlockDomains: []string{"blocked.example"}}); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	was, wasWait := lookupNetIP, firstPassWait
	t.Cleanup(func() { lookupNetIP, firstPassWait = was, wasWait })
	firstPassWait = 100 * time.Millisecond
	lookupNetIP = func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		select {
		case <-release:
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	start := time.Now()
	r.StartResolver(ctx)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("start waited %v for a hanging resolver", took)
	}
	if got := r.ResolverStatus().Addresses; got != 0 {
		t.Fatalf("resolved %d before the lookup answered", got)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for r.ResolverStatus().Addresses == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the late answer never reached the cache")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
