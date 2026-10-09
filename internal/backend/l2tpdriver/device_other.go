//go:build !linux

package l2tpdriver

import (
	"context"
	"errors"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// ErrUnsupported is the answer everywhere but Linux.
var ErrUnsupported = errors.New("l2tpdriver: L2TP is only served on Linux")

func ensureDevice(context.Context, *model.Interface) error { return ErrUnsupported }
func removeDevice(context.Context, *model.Interface) error { return nil }
