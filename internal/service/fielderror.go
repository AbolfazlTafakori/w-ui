package service

import (
	"errors"
	"fmt"
)

// Saying which field was wrong, not only that something was.
//
// A validation message on its own arrives at the interface as a sentence with
// nowhere to put it, so the panel could only show a toast and leave the operator
// to work out which of nine inputs it meant. Carrying the field name lets the
// message sit under the input it is about, which is the difference between
// reading an error and acting on one.
//
// The field name is the JSON name the form already uses, so nothing has to
// translate between what the API calls a thing and what the page calls it.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// invalidField builds a validation error attached to one input.
func invalidField(field, format string, args ...any) error {
	return &FieldError{
		Field: field,
		Err:   fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...),
	}
}

// FieldOf returns the input an error belongs to, or "" when it belongs to none.
func FieldOf(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}
