package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Changing many resellers at once.
//
// Every action goes through Update or Delete, the same paths one reseller
// changed from its dialog goes through. So a selection is held to exactly the
// rules one row is -- the owner cannot be switched off, a server has to exist,
// a reseller switched off is signed out at once -- and there is no second
// implementation of any of it to drift from the first.
//
// One reseller failing does not stop the rest: an owner switching off forty
// resellers wants the thirty-nine, and a list of who was not changed and why.

// AdminBulkAction is what to do to every reseller selected.
type AdminBulkAction string

const (
	AdminBulkEnable        AdminBulkAction = "enable"
	AdminBulkDisable       AdminBulkAction = "disable"
	AdminBulkResetUsage    AdminBulkAction = "resetUsage"
	AdminBulkExtend        AdminBulkAction = "extend"
	AdminBulkSetQuota      AdminBulkAction = "setQuota"
	AdminBulkSetLimit      AdminBulkAction = "setClientLimit"
	AdminBulkAddServers    AdminBulkAction = "addServers"
	AdminBulkRemoveServers AdminBulkAction = "removeServers"
	AdminBulkDelete        AdminBulkAction = "delete"
)

// AdminBulkInput is a selection and what to do to it. Only the fields the
// action reads are looked at.
type AdminBulkInput struct {
	Action AdminBulkAction `json:"action"`
	IDs    []uint          `json:"ids"`

	// Days, for extend: added to a running term, or to a term on hold. A
	// term that has ended is extended from now, so an expired reseller
	// renewed for thirty days gets thirty days.
	Days int `json:"days"`
	// QuotaBytes, for setQuota; zero is no limit.
	QuotaBytes *uint64 `json:"quotaBytes"`
	// ClientLimit, for setClientLimit; zero is no limit.
	ClientLimit *int `json:"clientLimit"`
	// InterfaceIDs, for addServers and removeServers.
	InterfaceIDs []uint `json:"interfaceIds"`
	// Mode, for delete: what becomes of their customers.
	Mode DeleteMode `json:"mode"`
}

// AdminBulkResult says how it went, reseller by reseller.
type AdminBulkResult struct {
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
	// Failures names each reseller not changed and why, by username.
	Failures map[string]string `json:"failures,omitempty"`
}

// maxAdminBulk bounds one request. A panel has tens of resellers, not
// thousands; a bigger selection is a mistake in the caller.
const maxAdminBulk = 500

// errUnchanged is a reseller the action had nothing to do to.
var errUnchanged = errors.New("nothing to change")

// Bulk applies one action to a selection of operators.
func (s *Admins) Bulk(ctx context.Context, in AdminBulkInput, clients *Clients) (*AdminBulkResult, error) {
	ids := dedupe(in.IDs)
	if len(ids) == 0 {
		return nil, invalidField("ids", "no resellers selected")
	}
	if len(ids) > maxAdminBulk {
		return nil, invalidField("ids", "%d resellers selected; at most %d at once", len(ids), maxAdminBulk)
	}
	if err := in.validate(); err != nil {
		return nil, err
	}

	var admins []model.Admin
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&admins).Error; err != nil {
		return nil, fmt.Errorf("service: load the selection: %w", err)
	}
	if err := s.fill(ctx, adminPtrs(admins)); err != nil {
		return nil, err
	}

	res := &AdminBulkResult{Failures: map[string]string{}}
	for i := range admins {
		a := &admins[i]
		err := s.bulkOne(ctx, a, in, clients)
		switch {
		case err == nil:
			res.Changed++
		case errors.Is(err, errUnchanged):
			res.Unchanged++
		default:
			res.Failures[a.Username] = humanReason(err)
		}
	}
	// Selected ids that no longer exist are not a failure of any reseller,
	// but they were not changed either.
	res.Unchanged += len(ids) - len(admins)
	if len(res.Failures) == 0 {
		res.Failures = nil
	}
	s.log.Info("resellers changed in bulk", "action", in.Action,
		"selected", len(ids), "changed", res.Changed, "unchanged", res.Unchanged,
		"failed", len(res.Failures))
	return res, nil
}

func (in AdminBulkInput) validate() error {
	switch in.Action {
	case AdminBulkEnable, AdminBulkDisable, AdminBulkResetUsage:
	case AdminBulkExtend:
		if in.Days < 1 || in.Days > maxTermDays {
			return invalidField("days", "a term of 1 to %d days", maxTermDays)
		}
	case AdminBulkSetQuota:
		if in.QuotaBytes == nil {
			return invalidField("quotaBytes", "give the traffic allowance, or 0 for no limit")
		}
	case AdminBulkSetLimit:
		if in.ClientLimit == nil || *in.ClientLimit < 0 {
			return invalidField("clientLimit", "give the customer limit, or 0 for no limit")
		}
	case AdminBulkAddServers, AdminBulkRemoveServers:
		if len(dedupe(in.InterfaceIDs)) == 0 {
			return invalidField("interfaceIds", "choose at least one server")
		}
	case AdminBulkDelete:
		if in.Mode != DeleteKeepClients && in.Mode != DeleteWithClients {
			return invalidField("mode", "say whether to keep their customers under your own account or delete them too")
		}
	default:
		return invalidField("action", "unknown action %q", in.Action)
	}
	return nil
}

// bulkOne applies the action to one operator, through the one-row paths.
func (s *Admins) bulkOne(ctx context.Context, a *model.Admin, in AdminBulkInput, clients *Clients) error {
	if a.Role == model.RoleOwner {
		return fmt.Errorf("%w: the panel's owner is not changed from a selection", ErrInvalid)
	}
	// What only a reseller has -- a ceiling, a term, servers -- is not
	// applied to a panel administrator, who holds none of them.
	capped := func() error {
		if !a.Role.Capped() {
			return fmt.Errorf("%w: a panel administrator has no ceiling to change", ErrInvalid)
		}
		return nil
	}

	on, off := true, false
	var upd AdminInput
	switch in.Action {
	case AdminBulkEnable:
		if a.Enabled {
			return errUnchanged
		}
		upd.Enabled = &on
	case AdminBulkDisable:
		if !a.Enabled {
			return errUnchanged
		}
		upd.Enabled = &off

	case AdminBulkResetUsage:
		if err := capped(); err != nil {
			return err
		}
		if a.UsedBytes == 0 {
			return errUnchanged
		}
		return s.ResetUsage(ctx, a.ID)

	case AdminBulkExtend:
		if err := capped(); err != nil {
			return err
		}
		switch {
		case a.ExpiresAt == nil && a.DurationDays > 0:
			d := min(a.DurationDays+in.Days, maxTermDays)
			upd.DurationDays = &d
		case a.ExpiresAt == nil:
			// No end date: there is nothing to extend, and giving them one
			// would be the opposite of what "extend" means.
			return errUnchanged
		default:
			base := time.Now().UTC()
			if a.ExpiresAt.After(base) {
				base = *a.ExpiresAt
			}
			next := base.Add(time.Duration(in.Days) * 24 * time.Hour)
			upd.ExpiresAt = OptionalTime{Set: true, Value: &next}
		}

	case AdminBulkSetQuota:
		if err := capped(); err != nil {
			return err
		}
		if a.QuotaBytes == *in.QuotaBytes {
			return errUnchanged
		}
		upd.QuotaBytes = in.QuotaBytes

	case AdminBulkSetLimit:
		if err := capped(); err != nil {
			return err
		}
		if a.ClientLimit == *in.ClientLimit {
			return errUnchanged
		}
		upd.ClientLimit = in.ClientLimit

	case AdminBulkAddServers, AdminBulkRemoveServers:
		if err := capped(); err != nil {
			return err
		}
		next := combine(a.InterfaceIDs, dedupe(in.InterfaceIDs), in.Action == AdminBulkAddServers)
		if sameSet(a.InterfaceIDs, next) {
			return errUnchanged
		}
		upd.InterfaceIDs = next
		if upd.InterfaceIDs == nil {
			// Every server taken away is a real state -- a reseller kept on
			// with nothing to sell -- and has to reach Update as an empty
			// list, not as "leave it alone".
			upd.InterfaceIDs = []uint{}
		}

	case AdminBulkDelete:
		return s.Delete(ctx, a.ID, in.Mode, clients)
	}

	_, err := s.Update(ctx, a.ID, upd)
	return err
}

func adminPtrs(admins []model.Admin) []*model.Admin {
	out := make([]*model.Admin, len(admins))
	for i := range admins {
		out[i] = &admins[i]
	}
	return out
}
