// Package ipam allocates tunnel addresses out of an interface's subnet.
//
// The panel deliberately does not number customers sequentially. A pool that
// hands out .60 to the sixtieth client caps the interface at 253 users and
// leaks the customer count to anyone holding a config; a /16 with recycling
// removes both problems.
package ipam

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
)

// Errors returned by Allocator.
var (
	ErrPoolFull      = errors.New("ipam: address pool exhausted")
	ErrOutOfRange    = errors.New("ipam: address outside pool")
	ErrAlreadyInUse  = errors.New("ipam: address already allocated")
	ErrNotAllocated  = errors.New("ipam: address not allocated")
	ErrPrefixTooWide = errors.New("ipam: prefix too wide")
)

// maxPoolBits caps how large a pool may be. /10 is 4M addresses (512 KiB of
// bitmap), far beyond any single interface, and the limit keeps a typo in the
// subnet field from allocating gigabytes.
const maxPoolBits = 22

// Allocator hands out addresses from a single IPv4 prefix.
//
// It is the authority on which addresses are free at runtime, but not the
// source of truth: on boot the caller replays the addresses already stored in
// the database through Reserve, and the allocator rebuilds its map from that.
type Allocator struct {
	mu     sync.Mutex
	prefix netip.Prefix
	base   uint32 // network address as a host-order integer
	size   uint32 // addresses in the prefix
	used   *bitset
	cursor uint32
}

// New builds an allocator for prefix. The network address, the last address and
// the gateway (offset 1, where the server itself sits) are reserved up front.
//
// Extra offsets may be reserved by the caller for anything else that lives
// inside the subnet.
func New(prefix netip.Prefix, extraReserved ...uint32) (*Allocator, error) {
	prefix = prefix.Masked()
	addr := prefix.Addr().Unmap()
	if !addr.Is4() {
		return nil, fmt.Errorf("ipam: %s is not IPv4", prefix)
	}

	hostBits := 32 - prefix.Bits()
	if hostBits > maxPoolBits {
		return nil, fmt.Errorf("%w: %s has %d host bits, limit is %d",
			ErrPrefixTooWide, prefix, hostBits, maxPoolBits)
	}
	if hostBits < 2 {
		return nil, fmt.Errorf("ipam: %s is too small to hold clients", prefix)
	}

	size := uint32(1) << hostBits
	a := &Allocator{
		prefix: prefix,
		base:   addrToU32(addr),
		size:   size,
		used:   newBitset(size),
		cursor: 2,
	}

	// Network address, gateway, broadcast.
	a.used.set(0)
	a.used.set(1)
	a.used.set(size - 1)
	for _, off := range extraReserved {
		a.used.set(off)
	}
	return a, nil
}

// Prefix returns the pool's network.
func (a *Allocator) Prefix() netip.Prefix { return a.prefix }

// Gateway returns the address the server itself holds inside the pool.
func (a *Allocator) Gateway() netip.Addr { return u32ToAddr(a.base + 1) }

// Capacity returns how many addresses can ever be handed out.
func (a *Allocator) Capacity() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return int(a.size) - 3 // network, gateway, broadcast
}

// InUse returns how many addresses are currently allocated, excluding the
// permanently reserved ones.
func (a *Allocator) InUse() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used.used() - 3
}

// Allocate returns the next free address.
//
// The search starts from a cursor that advances past every address handed out
// and never rewinds within a pass, so a freed address is not immediately
// reissued to the next customer. That matters for forensics: an address reused
// minutes after release makes an abuse report ambiguous between two customers,
// and the previous holder's installed config would still point at the slot.
func (a *Allocator) Allocate() (netip.Addr, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	off, ok := a.used.nextFree(a.cursor)
	if !ok {
		return netip.Addr{}, ErrPoolFull
	}
	a.used.set(off)

	a.cursor = off + 1
	if a.cursor >= a.size {
		a.cursor = 2
	}
	return u32ToAddr(a.base + off), nil
}

// Reserve marks addr as allocated. It is how stored allocations are replayed
// into a fresh allocator at startup.
func (a *Allocator) Reserve(addr netip.Addr) error {
	off, err := a.offset(addr)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.used.set(off) {
		return fmt.Errorf("%w: %s", ErrAlreadyInUse, addr)
	}
	if off >= a.cursor {
		a.cursor = off + 1
		if a.cursor >= a.size {
			a.cursor = 2
		}
	}
	return nil
}

// Release returns addr to the pool.
func (a *Allocator) Release(addr netip.Addr) error {
	off, err := a.offset(addr)
	if err != nil {
		return err
	}
	if off == 0 || off == 1 || off == a.size-1 {
		return fmt.Errorf("%w: %s is reserved", ErrOutOfRange, addr)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.used.clear(off) {
		return fmt.Errorf("%w: %s", ErrNotAllocated, addr)
	}
	return nil
}

// Holds reports whether addr is currently allocated.
func (a *Allocator) Holds(addr netip.Addr) bool {
	off, err := a.offset(addr)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used.test(off)
}

// offset converts an address to its index within the pool.
func (a *Allocator) offset(addr netip.Addr) (uint32, error) {
	addr = addr.Unmap()
	if !addr.Is4() {
		return 0, fmt.Errorf("%w: %s is not IPv4", ErrOutOfRange, addr)
	}
	if !a.prefix.Contains(addr) {
		return 0, fmt.Errorf("%w: %s is not in %s", ErrOutOfRange, addr, a.prefix)
	}
	return addrToU32(addr) - a.base, nil
}

func addrToU32(a netip.Addr) uint32 {
	b := a.As4()
	return binary.BigEndian.Uint32(b[:])
}

func u32ToAddr(v uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return netip.AddrFrom4(b)
}
