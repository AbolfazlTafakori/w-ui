//go:build !linux

package logger

import (
	"context"
	"fmt"
)

// Journal is not available off Linux.
//
// The journal is systemd's, and the panel is developed on machines that do not
// have one. Saying so plainly is better than an empty list, which would read as
// "nothing happened" rather than "this cannot be read here".
func Journal(context.Context, int, string, string) ([]Entry, error) {
	return nil, fmt.Errorf("the system journal can only be read on Linux")
}
