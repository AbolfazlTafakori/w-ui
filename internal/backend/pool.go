package backend

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Pool is the set of drivers that are currently open, by interface id.
//
// It exists because the drivers used to be opened once, at startup, into a map
// that nothing could add to afterwards. An interface created from the panel
// therefore produced a database row and nothing else: no device, no peers, no
// configuration to hand a customer, and no indication that anything was wrong
// until somebody restarted the whole panel. Which is to say the first thing a
// new operator does did not work.
//
// Everything that needs a driver now asks the pool, and the reconciler brings
// it in line with the database on every tick — the same way it treats the
// kernel. A driver that failed to open is retried rather than written off,
// because the reasons drivers fail are mostly temporary: a port still held by
// something shutting down, a tool being installed, a module not yet loaded.
type Pool struct {
	log *slog.Logger

	mu   sync.RWMutex
	open map[uint]entry
}

type entry struct {
	drv Backend
	// fingerprint of the interface as it was when opened, so a change to the
	// port or the key reopens it and an unchanged one is left alone. Reopening
	// a WireGuard device drops every customer on it.
	fp string
	// lastErr is why the most recent attempt failed, kept so the panel can say
	// so rather than showing an interface that looks configured and is not.
	lastErr error
}

// NewPool builds an empty pool.
func NewPool(log *slog.Logger) *Pool {
	return &Pool{log: log, open: map[uint]entry{}}
}

// Get returns the driver for an interface, if one is open.
func (p *Pool) Get(id uint) (Backend, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.open[id]
	return e.drv, ok && e.drv != nil
}

// All returns a snapshot of the open drivers.
//
// A copy, because the reconciler iterates this while a request may be opening
// or closing one.
func (p *Pool) All() map[uint]Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make(map[uint]Backend, len(p.open))
	for id, e := range p.open {
		if e.drv != nil {
			out[id] = e.drv
		}
	}
	return out
}

// ErrorFor returns why an interface's driver is not open, or nil.
func (p *Pool) ErrorFor(id uint) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.open[id].lastErr
}

// Open builds and opens a driver for one interface, replacing any existing one.
//
// Used when an operator creates an interface or asks for one to be restarted,
// so neither has to wait for the next reconciler tick to see whether it worked.
func (p *Pool) Open(ctx context.Context, iface *model.Interface) error {
	if err := p.openLocked(ctx, iface, true); err != nil {
		return err
	}
	return nil
}

// Put registers an already-built and already-open driver.
//
// For tests, which supply a driver that needs no kernel. It takes the interface
// rather than only the id so the stored fingerprint is the real one -- with a
// made-up fingerprint, Sync would decide the driver was stale on the very next
// tick and try to reopen it through the registry.
//
// Production code goes through Open, which builds the driver and opens it.
func (p *Pool) Put(iface *model.Interface, drv Backend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open[iface.ID] = entry{drv: drv, fp: fingerprint(iface)}
}

// Close shuts one driver down and forgets it.
func (p *Pool) Close(id uint) {
	p.mu.Lock()
	e, ok := p.open[id]
	delete(p.open, id)
	p.mu.Unlock()

	if ok && e.drv != nil {
		if err := e.drv.Close(); err != nil {
			p.log.Warn("closing a driver failed", "interface", id, "error", err)
		}
	}
}

// CloseAll shuts everything down, for panel shutdown.
func (p *Pool) CloseAll() {
	p.mu.Lock()
	all := p.open
	p.open = map[uint]entry{}
	p.mu.Unlock()

	for id, e := range all {
		if e.drv == nil {
			continue
		}
		if err := e.drv.Close(); err != nil {
			p.log.Warn("closing a driver failed", "interface", id, "error", err)
		}
	}
}

// Sync makes the pool match the interfaces it is given.
//
// Opens what is missing, reopens what has changed, closes what is gone or has
// been switched off. Called on every reconciler tick, so an interface created
// from the panel starts working within one tick and a driver that failed to
// open is retried until it does.
//
// Errors are not returned: one interface that will not come up must not stop
// the others from being brought in line. They are recorded per interface and
// reported through ErrorFor.
func (p *Pool) Sync(ctx context.Context, ifaces []model.Interface) {
	want := make(map[uint]bool, len(ifaces))

	for i := range ifaces {
		iface := ifaces[i]
		if !iface.Enabled {
			continue
		}
		want[iface.ID] = true

		p.mu.RLock()
		e, have := p.open[iface.ID]
		p.mu.RUnlock()

		if have && e.drv != nil && e.fp == fingerprint(&iface) {
			continue // unchanged and working; leave its customers alone
		}
		_ = p.openLocked(ctx, &iface, false)
	}

	// Anything no longer wanted.
	p.mu.RLock()
	var stale []uint
	for id := range p.open {
		if !want[id] {
			stale = append(stale, id)
		}
	}
	p.mu.RUnlock()

	for _, id := range stale {
		p.log.Info("closing a driver for an interface that is gone or switched off",
			"interface", id)
		p.Close(id)
	}
}

// openLocked does the work. `loud` reports a failure to the caller as well as
// recording it, for the paths where an operator is waiting on an answer.
func (p *Pool) openLocked(ctx context.Context, iface *model.Interface, loud bool) error {
	drv, err := New(iface.Protocol)
	if err != nil {
		err = fmt.Errorf("no driver for %s", iface.Protocol)
		p.record(iface.ID, nil, "", err)
		return err
	}
	if withLogger, ok := drv.(interface{ SetLogger(*slog.Logger) }); ok {
		withLogger.SetLogger(p.log)
	}

	if err := drv.Open(ctx, iface); err != nil {
		p.record(iface.ID, nil, "", err)
		if !loud {
			// The reconciler retries every tick; saying so each time would bury
			// the log. The state is readable through ErrorFor.
			p.logOnce(iface, err)
		}
		return err
	}

	// The previous driver for this interface, if any, is closed after the new
	// one is open: a gap with neither would drop traffic that the reopen was
	// meant to preserve.
	p.mu.Lock()
	old := p.open[iface.ID]
	p.open[iface.ID] = entry{drv: drv, fp: fingerprint(iface)}
	p.mu.Unlock()

	if old.drv != nil {
		_ = old.drv.Close()
	}

	p.log.Info("interface driver open", "interface", iface.Name, "protocol", iface.Protocol)
	return nil
}

func (p *Pool) record(id uint, drv Backend, fp string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open[id] = entry{drv: drv, fp: fp, lastErr: err}
}

// logOnce keeps a repeatedly failing interface from filling the log.
func (p *Pool) logOnce(iface *model.Interface, err error) {
	p.mu.RLock()
	prev := p.open[iface.ID].lastErr
	p.mu.RUnlock()

	if prev == nil || prev.Error() != err.Error() {
		p.log.Error("interface driver would not open",
			"interface", iface.Name, "error", err)
	}
}

// fingerprint is what has to change for a driver to be worth reopening.
//
// Deliberately narrow. A rename or a note should not tear down a live tunnel,
// but a key, a port or a subnet means the device is no longer the one the
// database describes.
func fingerprint(i *model.Interface) string {
	return fmt.Sprintf("%s|%s|%d|%s|%s|%d|%s",
		i.Name, i.Protocol, i.ListenPort, i.Subnet, i.PublicKey, i.MTU, i.Mode)
}
