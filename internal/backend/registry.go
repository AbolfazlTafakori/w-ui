package backend

import (
	"fmt"
	"sort"
	"sync"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Factory builds a driver instance for one interface.
type Factory func() Backend

var (
	regMu    sync.RWMutex
	registry = map[model.Protocol]Factory{}
)

// Register makes a driver available under its protocol. Drivers register from
// their package init, so linking a driver in is what enables the protocol.
//
// It panics on a duplicate registration: two drivers claiming one protocol is a
// build-time mistake, not a runtime condition.
func Register(p model.Protocol, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()

	if _, dup := registry[p]; dup {
		panic(fmt.Sprintf("backend: protocol %q registered twice", p))
	}
	registry[p] = f
}

// New builds a driver for the given protocol.
func New(p model.Protocol) (Backend, error) {
	regMu.RLock()
	f, ok := registry[p]
	regMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("backend: no driver registered for protocol %q", p)
	}
	return f(), nil
}

// Registered lists the protocols that have a driver linked in, sorted for
// stable output.
func Registered() []model.Protocol {
	regMu.RLock()
	defer regMu.RUnlock()

	out := make([]model.Protocol, 0, len(registry))
	for p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Supports reports whether a driver exists for p.
func Supports(p model.Protocol) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := registry[p]
	return ok
}
