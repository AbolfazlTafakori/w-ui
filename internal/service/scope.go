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
	// An admin the owner has let see every customer is unrestricted too --
	// one scope that matches no condition rather than a condition over the
	// whole panel that could be got wrong.
	if admin.Role == model.RoleAdmin && admin.CanViewAllClients {
		return Scope{}
	}
	// Owners is the set the narrowing matches against. A reseller's is their
	// own id alone; an admin's starts with their own id and gains their
	// resellers where the DB is reachable (the API layer appends them). The
	// admin_id stamped on anything created is the admin at the top of the
	// chain: an admin themselves, a reseller their parent.
	s := Scope{AdminID: admin.ID, Restricted: true, Hidden: admin.GroupName, Owners: []uint{admin.ID}}
	switch admin.Role {
	case model.RoleAdmin:
		s.AdminStamp = admin.ID
	case model.RoleReseller:
		s.AdminStamp = admin.ParentAdminID
	}
	return s
}

// WithScope attaches a scope to a context.
func WithScope(ctx context.Context, s Scope) context.Context { return scope.With(ctx, s) }

// ScopeOf reads it back.
func ScopeOf(ctx context.Context) Scope { return scope.Of(ctx) }

// OwnerOf is the id to stamp on something this request creates.
func OwnerOf(ctx context.Context) uint { return scope.OwnerOf(ctx) }

// AdminStampOf is the admin_id to stamp on a customer this request creates.
func AdminStampOf(ctx context.Context) uint { return scope.AdminStampOf(ctx) }

// fillOwnerPause marks each customer whose reseller is paused, and why.
//
// Read from the operators table, which is not narrowed to the caller: a
// reseller reading their own customers is asking about their own row, and
// the owner is asking about everyone's. One query for the page.
func fillOwnerPause(ctx context.Context, db *gorm.DB, items []model.Client) error {
	// Both the direct owner and the admin above a reseller can be the reason
	// a customer is off, so both are read.
	operators := map[uint]bool{}
	for i := range items {
		if items[i].OwnerID != 0 {
			operators[items[i].OwnerID] = true
		}
		if items[i].AdminID != 0 {
			operators[items[i].AdminID] = true
		}
	}
	if len(operators) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(operators))
	for id := range operators {
		ids = append(ids, id)
	}
	var admins []model.Admin
	if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&admins).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	why := make(map[uint]string, len(admins))
	for i := range admins {
		why[admins[i].ID] = admins[i].PauseReason(now)
	}
	// The direct owner's reason first; failing that, the admin above them --
	// a reseller in good standing whose admin is switched off is still off,
	// and the panel says why.
	for i := range items {
		if r := why[items[i].OwnerID]; r != "" {
			items[i].OwnerPaused = r
		} else {
			items[i].OwnerPaused = why[items[i].AdminID]
		}
	}
	return nil
}

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
