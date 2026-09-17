package api

import (
	"sync"
	"time"
)

// Guessing at subscription links.
//
// A drawn token is 192 bits and cannot be guessed. An id an operator typed
// can be short and memorable, and a customer's link is their configuration
// with the keys in it -- so an address that keeps asking for links that do
// not exist is told to wait. Real customers never miss: their app has the
// link. The count is per address, so a scan slows only the scanner.

const (
	subMissWindow = 10 * time.Minute
	subMissLimit  = 40
)

type subMisses struct {
	mu   sync.Mutex
	seen map[string]*missRecord
}

type missRecord struct {
	count int
	since time.Time
}

func newSubMisses() *subMisses { return &subMisses{seen: map[string]*missRecord{}} }

// blocked reports whether the address has missed too often lately, and for
// how much longer it is refused.
func (m *subMisses) blocked(ip string, now time.Time) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.seen[ip]
	if rec == nil {
		return false, 0
	}
	if now.Sub(rec.since) >= subMissWindow {
		delete(m.seen, ip)
		return false, 0
	}
	if rec.count >= subMissLimit {
		return true, subMissWindow - now.Sub(rec.since)
	}
	return false, 0
}

// miss records one request for a link that does not exist.
func (m *subMisses) miss(ip string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Kept small: a scan from many addresses would otherwise grow the map
	// without bound. Old records go first; the newest scanner is still counted.
	if len(m.seen) > 10000 {
		for k, rec := range m.seen {
			if now.Sub(rec.since) >= subMissWindow {
				delete(m.seen, k)
			}
		}
		if len(m.seen) > 10000 {
			m.seen = map[string]*missRecord{}
		}
	}
	rec := m.seen[ip]
	if rec == nil || now.Sub(rec.since) >= subMissWindow {
		m.seen[ip] = &missRecord{count: 1, since: now}
		return
	}
	rec.count++
}
