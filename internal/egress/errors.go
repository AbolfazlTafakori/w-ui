package egress

import "github.com/abolfazl/w-ui/internal/fault"

// The kinds of failure the API tells apart (see fault).
var (
	ErrInvalid  = fault.ErrInvalid
	ErrNotFound = fault.ErrNotFound
)

// FieldError is a validation error that names its input.
type FieldError = fault.FieldError

func invalidField(field, format string, args ...any) error {
	return fault.Field(field, format, args...)
}
