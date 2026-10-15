//go:build !linux

package ikev2driver

import (
	"context"

	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// IfID is Linux's; elsewhere there is no tunnel to tie a session to.
func IfID(*model.Interface) uint32 { return 0 }

func ensureDevice(ctx context.Context, iface *model.Interface) (uint32, error) {
	return 0, l2tpdriver.EnsureDevice(ctx, iface)
}

func removeDevice(ctx context.Context, iface *model.Interface) error {
	return l2tpdriver.RemoveDevice(ctx, iface)
}
