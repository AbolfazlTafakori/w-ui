//go:build !linux

package ikev2driver

import (
	"context"

	"github.com/abolfazl/w-ui/internal/backend/agentkit"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// IfID is Linux's; elsewhere there is no tunnel to tie a session to.
func IfID(*model.Interface) uint32 { return 0 }

func ensureDevice(ctx context.Context, iface *model.Interface) (uint32, error) {
	return 0, agentkit.EnsureDevice(ctx, iface)
}

func removeDevice(ctx context.Context, iface *model.Interface) error {
	return agentkit.RemoveDevice(ctx, iface)
}
