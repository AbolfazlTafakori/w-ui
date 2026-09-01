package ipam

import (
	"fmt"
	"net/netip"
	"sync"
)

// Pools holds one Allocator per interface.
//
// The allocators are runtime state rebuilt from stored allocations at startup,
// not a second source of truth. Load is what replays the database into them.
type Pools struct {
	mu sync.RWMutex
	by map[uint]*Allocator
}

// NewPools builds an empty set.
func NewPools() *Pools { return &Pools{by: map[uint]*Allocator{}} }

// Add creates the allocator for an interface from its CIDR.
func (p *Pools) Add(interfaceID uint, cidr string) (*Allocator, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, fmt.Errorf("ipam: interface %d subnet %q: %w", interfaceID, cidr, err)
	}
	a, err := New(prefix)
	if err != nil {
		return nil, fmt.Errorf("ipam: interface %d: %w", interfaceID, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.by[interfaceID] = a
	return a, nil
}

// Get returns the allocator for an interface.
func (p *Pools) Get(interfaceID uint) (*Allocator, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	a, ok := p.by[interfaceID]
	if !ok {
		return nil, fmt.Errorf("ipam: no pool for interface %d", interfaceID)
	}
	return a, nil
}

// Replay marks a stored address as allocated on its interface.
//
// A conflict here means two accounts claim one address, which the unique index
// on (interface, ip) should already prevent. It is surfaced rather than skipped
// because silently dropping one of them would route a customer's traffic into
// another customer's accounting.
func (p *Pools) Replay(interfaceID uint, addr string) error {
	a, err := p.Get(interfaceID)
	if err != nil {
		return err
	}
	parsed, err := netip.ParseAddr(addr)
	if err != nil {
		return fmt.Errorf("ipam: interface %d stored address %q: %w", interfaceID, addr, err)
	}
	if err := a.Reserve(parsed); err != nil {
		return fmt.Errorf("ipam: interface %d: %w", interfaceID, err)
	}
	return nil
}

// Len reports how many interfaces have pools.
func (p *Pools) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.by)
}
