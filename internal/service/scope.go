package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/scope"
)

// The service layer's view of who is asking. The mechanism is in
// internal/scope, which sits low enough that the database package can
// register it; these are the parts that need to know what an Admin is.

// Scope is re-exported so callers have one import for this.
type Scope = scope.Scope

// ScopeFor returns the scope an operator's requests are served under.
//
// An owner and a panel administrator see everything, so they are served
// unrestricted -- not with a scope that happens to match every row, which
// would be a filter waiting to be got wrong.
func ScopeFor(admin *model.Admin) Scope {
	if admin == nil || admin.Role.SeesEveryone() {
		return Scope{}
	}
	return Scope{AdminID: admin.ID, Restricted: true, Hidden: admin.GroupName}
}

// WithScope attaches a scope to a context.
func WithScope(ctx context.Context, s Scope) context.Context { return scope.With(ctx, s) }

// ScopeOf reads it back.
func ScopeOf(ctx context.Context) Scope { return scope.Of(ctx) }

// OwnerOf is the id to stamp on something this request creates.
func OwnerOf(ctx context.Context) uint { return scope.OwnerOf(ctx) }

// SuspendedOwners lists the operators whose customers must be off right now.
//
// Read in one query and applied where service is decided, rather than being
// written onto the customers themselves. A reseller who runs out of
// allowance on Tuesday and is topped up on Wednesday has their customers
// back exactly as they were, because nothing about them was ever changed.
func SuspendedOwners(ctx context.Context, db *gorm.DB, now time.Time) (map[uint]bool, error) {
	var admins []model.Admin
	if err := db.WithContext(ctx).Where("role <> ?", model.RoleOwner).Find(&admins).Error; err != nil {
		return nil, err
	}
	out := map[uint]bool{}
	for i := range admins {
		if admins[i].Suspended(now) {
			out[admins[i].ID] = true
		}
	}
	return out, nil
}
