package database

import (
	"encoding/json"

	"gorm.io/gorm"
)

// Each id in a WHERE ... IN ? is a parameter of its own, and SQLite refuses a
// statement with 32,767 or more of them, PostgreSQL one with 65,535 or more.
// Where a list can be that long and the statement cannot be cut in parts --
// a condition inside a paged listing, a count -- InIDs sends it as one
// parameter instead: a JSON array the database unpacks itself.

// inlineIDs is how many ids are still bound one by one: the plain IN reads
// best and plans best, and is safe far below either engine's limit.
const inlineIDs = 900

// InIDs is the condition "column IN ids" and its arguments, safe however
// many ids there are. None matches nothing.
func InIDs(db *gorm.DB, column string, ids []uint) (string, []any) {
	return idsCond(db, column, ids, false)
}

// NotInIDs is "column NOT IN ids"; none excludes nothing.
func NotInIDs(db *gorm.DB, column string, ids []uint) (string, []any) {
	return idsCond(db, column, ids, true)
}

func idsCond(db *gorm.DB, column string, ids []uint, not bool) (string, []any) {
	op := " IN "
	if not {
		op = " NOT IN "
	}
	if len(ids) == 0 {
		if not {
			return "1 = 1", nil
		}
		return "1 = 0", nil
	}
	if len(ids) <= inlineIDs {
		return column + op + "?", []any{ids}
	}
	raw, _ := json.Marshal(ids) // a slice of numbers always marshals
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "postgres" {
		return column + op + "(SELECT jsonb_array_elements_text(?::jsonb)::bigint)", []any{string(raw)}
	}
	return column + op + "(SELECT value FROM json_each(?))", []any{string(raw)}
}
