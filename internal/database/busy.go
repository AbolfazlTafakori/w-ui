package database

import (
	"errors"
	"strings"
)

// SQLite's own result codes for a database another writer holds: the
// extended codes keep these in their low byte.
const (
	sqliteBusy   = 5
	sqliteLocked = 6
)

// IsBusy reports whether err is the database being held by another writer
// past the wait the panel gives it (busy_timeout): a change that was not
// made, and would be if asked again in a moment. It is told from any other
// fault so the caller can say exactly that, rather than "unexpected".
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case sqliteBusy, sqliteLocked:
			return true
		}
	}
	// A driver that loses the code on the way still says so in words.
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "SQLITE_BUSY")
}
