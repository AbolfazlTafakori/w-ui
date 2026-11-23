package service

import (
	"github.com/abolfazl/w-ui/internal/database"
	"github.com/abolfazl/w-ui/internal/fault"
)

// FieldError is a validation error that names its input (see fault).
type FieldError = fault.FieldError

// invalidField builds a validation error attached to one input.
func invalidField(field, format string, args ...any) error {
	return fault.Field(field, format, args...)
}

// FieldOf returns the input an error belongs to, or "" when it belongs to none.
func FieldOf(err error) string { return fault.FieldOf(err) }

// IsBusy reports whether a change failed because another writer held the
// database past the wait (database.IsBusy): not made, and worth asking for
// again. Here so the handlers can tell it apart without reaching the store.
func IsBusy(err error) bool { return database.IsBusy(err) }
