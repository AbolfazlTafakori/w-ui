//go:build !linux

package agentkit

import (
	"context"
	"errors"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// ErrUnsupported is the answer everywhere but Linux.
var ErrUnsupported = errors.New("these tunnels are only served on Linux")

// EnsureDevice is refused off Linux.
func EnsureDevice(context.Context, *model.Interface) error { return ErrUnsupported }

// RemoveDevice has nothing to remove off Linux.
func RemoveDevice(context.Context, *model.Interface) error { return nil }
