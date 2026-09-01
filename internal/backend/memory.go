package backend

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Memory is an in-process Backend that keeps its account set in a map.
//
// It exists so the reconciler, the enforcer and the API can be built and tested
// before the kernel drivers land, and so those tests run anywhere rather than
// only on a Linux host with the right modules loaded. It is not a driver: it
// never touches a network interface.
type Memory struct {
	proto model.Protocol

	mu       sync.Mutex
	iface    *model.Interface
	accounts map[uint]DesiredAccount
	stats    map[uint]Stat
	closed   bool
}

// NewMemory builds an in-memory backend for the given protocol.
func NewMemory(p model.Protocol) *Memory {
	return &Memory{
		proto:    p,
		accounts: map[uint]DesiredAccount{},
		stats:    map[uint]Stat{},
	}
}

func (m *Memory) Protocol() model.Protocol { return m.proto }

func (m *Memory) Open(_ context.Context, iface *model.Interface) error {
	if iface.Protocol != m.proto {
		return fmt.Errorf("%w: interface %q is %s, driver is %s",
			ErrWrongProtocol, iface.Name, iface.Protocol, m.proto)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.iface = iface
	m.closed = false
	// A fresh bind starts empty, the way a driver attaching to a just-booted
	// host finds no peers. The reconciler is expected to rebuild from the
	// database, and this is what lets a test prove that it does.
	m.accounts = map[uint]DesiredAccount{}
	m.stats = map[uint]Stat{}
	return nil
}

func (m *Memory) Sync(_ context.Context, desired []DesiredAccount) (SyncReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.iface == nil || m.closed {
		return SyncReport{}, ErrNotOpen
	}

	var rep SyncReport
	seen := make(map[uint]struct{}, len(desired))

	for _, want := range desired {
		seen[want.ID] = struct{}{}
		have, ok := m.accounts[want.ID]
		switch {
		case !ok:
			rep.Added++
		case have != want:
			rep.Updated++
		default:
			rep.Unchanged++
			continue
		}
		m.accounts[want.ID] = want
	}

	for id := range m.accounts {
		if _, keep := seen[id]; !keep {
			delete(m.accounts, id)
			delete(m.stats, id)
			rep.Removed++
		}
	}
	return rep, nil
}

func (m *Memory) Stats(_ context.Context) ([]Stat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.iface == nil || m.closed {
		return nil, ErrNotOpen
	}

	out := make([]Stat, 0, len(m.accounts))
	for id := range m.accounts {
		s, ok := m.stats[id]
		if !ok {
			s = Stat{AccountID: id}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out, nil
}

func (m *Memory) Kick(_ context.Context, accountID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// WireGuard has no sessions to terminate; removing the peer is the
	// equivalent action and belongs to Sync.
	if m.proto == model.ProtocolWireGuard {
		return ErrNotSupported
	}
	if _, ok := m.accounts[accountID]; !ok {
		return fmt.Errorf("%w: %d", ErrUnknownAcct, accountID)
	}
	delete(m.stats, accountID)
	return nil
}

func (m *Memory) Render(_ context.Context, acc *model.Account, iface *model.Interface) (ClientProfile, error) {
	body := fmt.Sprintf("# %s profile for %s on %s\n# generated %s\n",
		m.proto, acc.DeviceName, iface.Name, time.Now().UTC().Format(time.RFC3339))
	return ClientProfile{
		Filename: fmt.Sprintf("%s.txt", acc.DeviceName),
		MIMEType: "text/plain",
		Body:     []byte(body),
	}, nil
}

func (m *Memory) Health(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.iface == nil || m.closed {
		return ErrNotOpen
	}
	return nil
}

func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// SetStat seeds observed counters for an account. Test-only.
func (m *Memory) SetStat(s Stat) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats[s.AccountID] = s
}

// Accounts returns the account IDs the backend currently holds, sorted.
// Test-only.
func (m *Memory) Accounts() []uint {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]uint, 0, len(m.accounts))
	for id := range m.accounts {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
