package service

import (
	"sync"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Live is what is happening right now on every server, as the reconciler
// counts it: the same counts the limits are enforced on. A service without
// one -- a CLI, a test with no data plane -- falls back to what is on record.
type Live interface {
	// ConnectionsNow is how many connections each of these customers has.
	ConnectionsNow(clientIDs []uint) map[uint]int
	// Speeds is what each of these customers is moving.
	Speeds(clientIDs []uint) map[uint]model.Speed
	// LiveClients is every customer connected now.
	LiveClients() []uint
	// FileSpeeds is what each file is moving, by account id.
	FileSpeeds() map[uint]model.Speed
}

// Changes is what the panel says when something a customer sees has changed
// -- a plan ended or started, a device added, a bulk change -- so an open
// subscription page refreshes at once. Its listeners are the API's open
// pages; anything may say it, and a nil Changes says nothing.
type Changes struct {
	mu        sync.Mutex
	listeners []func()
}

// NewChanges is a Changes nobody listens to yet.
func NewChanges() *Changes { return &Changes{} }

// Listen adds f to what Notify calls.
func (c *Changes) Listen(f func()) {
	c.mu.Lock()
	c.listeners = append(c.listeners, f)
	c.mu.Unlock()
}

// Notify tells every listener.
func (c *Changes) Notify() {
	if c == nil {
		return
	}
	c.mu.Lock()
	ls := append([]func(){}, c.listeners...)
	c.mu.Unlock()
	for _, f := range ls {
		f()
	}
}
