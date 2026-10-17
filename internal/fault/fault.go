// Package fault is the kinds of failure every part of the panel answers
// with -- a request that is wrong, a thing that is not there -- so the API
// can tell them apart however deep they come from. It depends on nothing.
package fault

import (
	"errors"
	"fmt"
)

// The kinds. Their text is what an operator reads once the API has taken
// the "service: " off, and stays as it was.
var (
	ErrInvalid  = errors.New("service: invalid input")
	ErrNotFound = errors.New("service: not found")
)

// FieldError says which field was wrong, not only that something was.
//
// A validation message on its own arrives at the interface as a sentence
// with nowhere to put it. Carrying the field name lets the message sit under
// the input it is about. The name is the JSON name the form already uses.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// Field is a validation error attached to one input.
func Field(field, format string, args ...any) error {
	return &FieldError{
		Field: field,
		Err:   fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...),
	}
}

// FieldOf is the input an error belongs to, or "" when it belongs to none.
func FieldOf(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}
