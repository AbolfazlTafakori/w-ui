package model

import (
	"time"

	"gorm.io/gorm"
)

// Timestamps are normalised to UTC before they are written.
//
// The SQLite driver stores a time.Time as its RFC3339 text, offset and all, and
// SQLite compares those values as strings. A row saved as 11:18+03:30 therefore
// sorts after a query bound to 07:53Z even though it is the earlier instant, so
// a comparison like "expires_at <= now" silently matches nothing on any server
// not running in UTC. Expiry would appear to work in development and quietly
// never fire in production.
//
// Doing it in a hook rather than at each call site means no path — API, batch
// import, group action, future driver — can put a local time in the database
// by forgetting to convert.

func utc(t **time.Time) {
	if *t != nil {
		v := (**t).UTC()
		*t = &v
	}
}

// BeforeSave normalises the client's operator-supplied timestamps.
func (c *Client) BeforeSave(*gorm.DB) error {
	utc(&c.ExpiresAt)
	utc(&c.LastResetAt)
	return nil
}

// BeforeSave normalises the timestamps the collector writes.
func (a *Account) BeforeSave(*gorm.DB) error {
	utc(&a.LastHandshake)
	utc(&a.LastSeenAt)
	return nil
}

// BeforeSave normalises the lease window used to answer abuse reports.
func (l *IPLease) BeforeSave(*gorm.DB) error {
	l.FromTS = l.FromTS.UTC()
	utc(&l.ToTS)
	return nil
}

// BeforeSave keeps every traffic bucket on the same clock.
func (t *TrafficSample) BeforeSave(*gorm.DB) error {
	t.BucketTS = t.BucketTS.UTC()
	return nil
}
