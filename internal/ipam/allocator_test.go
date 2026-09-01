package ipam

import (
	"errors"
	"net/netip"
	"testing"
)

func mustNew(t *testing.T, cidr string) *Allocator {
	t.Helper()
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", cidr, err)
	}
	a, err := New(p)
	if err != nil {
		t.Fatalf("new allocator %q: %v", cidr, err)
	}
	return a
}

func TestAllocateSkipsReservedAddresses(t *testing.T) {
	a := mustNew(t, "10.66.0.0/16")

	got, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if want := netip.MustParseAddr("10.66.0.2"); got != want {
		t.Errorf("first address = %s, want %s (network and gateway reserved)", got, want)
	}
	if gw := a.Gateway(); gw != netip.MustParseAddr("10.66.0.1") {
		t.Errorf("gateway = %s, want 10.66.0.1", gw)
	}
}

func TestAllocateDoesNotImmediatelyReuseReleasedAddress(t *testing.T) {
	a := mustNew(t, "10.66.0.0/24")

	first, _ := a.Allocate()
	second, _ := a.Allocate()

	if err := a.Release(first); err != nil {
		t.Fatalf("release %s: %v", first, err)
	}

	third, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate after release: %v", err)
	}
	if third == first {
		t.Errorf("released address %s was reissued immediately; the cursor must "+
			"move past it so abuse reports stay unambiguous", first)
	}
	if third == second {
		t.Errorf("allocate returned %s twice", second)
	}
}

func TestReleasedAddressComesBackAfterWrap(t *testing.T) {
	a := mustNew(t, "10.66.0.0/24") // 253 usable

	var all []netip.Addr
	for {
		addr, err := a.Allocate()
		if errors.Is(err, ErrPoolFull) {
			break
		}
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		all = append(all, addr)
	}
	if len(all) != a.Capacity() {
		t.Fatalf("allocated %d addresses, capacity says %d", len(all), a.Capacity())
	}

	freed := all[10]
	if err := a.Release(freed); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate after wrap: %v", err)
	}
	if got != freed {
		t.Errorf("allocate = %s, want the only free address %s", got, freed)
	}
}

func TestPoolFull(t *testing.T) {
	a := mustNew(t, "10.0.0.0/29") // 8 addresses, 5 usable

	for i := 0; i < a.Capacity(); i++ {
		if _, err := a.Allocate(); err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
	}
	if _, err := a.Allocate(); !errors.Is(err, ErrPoolFull) {
		t.Errorf("error = %v, want ErrPoolFull", err)
	}
}

func TestReserveReplaysStoredAllocations(t *testing.T) {
	a := mustNew(t, "10.66.0.0/16")

	stored := []string{"10.66.0.7", "10.66.1.200", "10.66.0.2"}
	for _, s := range stored {
		if err := a.Reserve(netip.MustParseAddr(s)); err != nil {
			t.Fatalf("reserve %s: %v", s, err)
		}
	}
	if got := a.InUse(); got != len(stored) {
		t.Errorf("in use = %d, want %d", got, len(stored))
	}

	// Reserving twice must be reported, not silently accepted: a duplicate
	// means two accounts hold the same address.
	err := a.Reserve(netip.MustParseAddr("10.66.0.7"))
	if !errors.Is(err, ErrAlreadyInUse) {
		t.Errorf("double reserve error = %v, want ErrAlreadyInUse", err)
	}

	next, err := a.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	for _, s := range stored {
		if next == netip.MustParseAddr(s) {
			t.Errorf("allocate returned reserved address %s", next)
		}
	}
}

func TestRejectsAddressOutsidePool(t *testing.T) {
	a := mustNew(t, "10.66.0.0/16")

	if err := a.Reserve(netip.MustParseAddr("192.168.1.5")); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("reserve outside pool = %v, want ErrOutOfRange", err)
	}
	if err := a.Release(netip.MustParseAddr("10.66.0.1")); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("release of gateway = %v, want ErrOutOfRange", err)
	}
	if err := a.Release(netip.MustParseAddr("10.66.5.5")); !errors.Is(err, ErrNotAllocated) {
		t.Errorf("release of free address = %v, want ErrNotAllocated", err)
	}
}

func TestRejectsOversizedPrefix(t *testing.T) {
	p := netip.MustParsePrefix("10.0.0.0/8")
	if _, err := New(p); !errors.Is(err, ErrPrefixTooWide) {
		t.Errorf("new /8 = %v, want ErrPrefixTooWide", err)
	}
}

func TestCapacityMatchesSubnetSize(t *testing.T) {
	for _, tc := range []struct {
		cidr string
		want int
	}{
		{"10.66.0.0/24", 253},
		{"10.66.0.0/16", 65533},
		{"10.0.0.0/12", 1048573},
	} {
		if got := mustNew(t, tc.cidr).Capacity(); got != tc.want {
			t.Errorf("%s capacity = %d, want %d", tc.cidr, got, tc.want)
		}
	}
}

func BenchmarkAllocateNearlyFullPool(b *testing.B) {
	p := netip.MustParsePrefix("10.66.0.0/16")
	a, _ := New(p)
	for i := 0; i < a.Capacity()-1; i++ {
		if _, err := a.Allocate(); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		addr, err := a.Allocate()
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		_ = a.Release(addr)
		b.StartTimer()
	}
}
