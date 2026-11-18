package operators

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
	"github.com/abolfazl/w-ui/internal/scope"
)

// An operator's users, as the operator form sets them.
//
// The form says how many users an operator has (clientLimit, as it always
// has) and, for the owner, how they are counted (userMode). Both become
// moves of the operator's capacity account, inside the transaction that
// saves the operator: the owner's grant is a charge or a debit; an admin's
// grant to a reseller of theirs is a hand-down from the admin's own account,
// and can never be more than the admin has free. A reseller is counted the
// way their admin is.

// userModeOf is the mode a new account is opened in: the parent's, for a
// reseller under an admin with an account; what the owner asked for, or
// seats at once, otherwise.
func userModeOf(parent *model.CapacityAccount, asked string) string {
	if parent != nil {
		return parent.Mode
	}
	if asked == model.CapacityConsumable {
		return model.CapacityConsumable
	}
	return model.CapacityOpen
}

// setUsers brings an operator's user account to limit users. actor is who is
// doing it (zero for the owner). limit zero is no ceiling -- only possible
// for an operator whose parent has none, since a reseller under a counted
// admin would otherwise sell past the admin's ceiling.
func setUsers(tx *gorm.DB, actor uint, target *model.Admin, limit int64, askedMode string, ownerAsks bool) error {
	if limit < 0 || limit > capacity.MaxUnits {
		return fault.Field("clientLimit", "users are given as a number from 0 to %d", capacity.MaxUnits)
	}
	var parent *model.CapacityAccount
	if target.ParentAdminID != 0 {
		var err error
		if parent, err = capacity.Account(tx, target.ParentAdminID, model.CapacityUsers); err != nil {
			return err
		}
	}
	acc, err := capacity.Account(tx, target.ID, model.CapacityUsers)
	if err != nil {
		return err
	}
	e := capacity.Entry{ActorID: actor}

	if limit == 0 {
		if parent != nil {
			return fault.Field("clientLimit", "a reseller under an admin with a user ceiling is given a number of users")
		}
		if acc == nil {
			return nil
		}
		// No ceiling from now on: the account goes, its ledger stays.
		return capacity.Close(tx, target.ID)
	}

	mode := userModeOf(parent, askedMode)
	if acc == nil {
		if acc, err = capacity.Open(tx, target.ID, model.CapacityUsers, mode); err != nil {
			return err
		}
		if acc.Mode == model.CapacityOpen {
			// Seats are held by the customers being served: an operator
			// given a ceiling who already sells starts from what they hold.
			if err := holdServed(tx, acc, e); err != nil {
				return err
			}
		}
	} else if ownerAsks && askedMode != "" && askedMode != acc.Mode && parent == nil {
		if err := switchMode(tx, acc, askedMode, e); err != nil {
			return err
		}
	}

	diff := limit - acc.Granted
	switch {
	case diff == 0:
		return nil
	case parent != nil && diff > 0:
		err = capacity.HandDown(tx, parent, acc, diff, e)
		if errors.Is(err, capacity.ErrFull) {
			return fault.Field("clientLimit", "that is %d more users than you have free (%d)", diff, max(capacity.Free(parent), 0))
		}
	case parent != nil:
		err = capacity.HandBack(tx, parent, acc, -diff, e)
		if errors.Is(err, capacity.ErrFull) {
			return fault.Field("clientLimit", "only users the reseller has not used can be taken back: %d", max(capacity.Free(acc), 0))
		}
	case diff > 0:
		e.Reason = model.CapacityCharge
		err = capacity.Charge(tx, acc, diff, e)
	default:
		e.Reason = model.CapacityDebit
		// The owner may take a ceiling below what is in use: nobody is cut
		// off, nothing new is let in until enough has ended.
		err = capacity.Debit(tx, acc, -diff, e)
	}
	return err
}

// holdServed records the seats an operator's customers being served hold,
// once, when their seats start being counted.
func holdServed(tx *gorm.DB, acc *model.CapacityAccount, e capacity.Entry) error {
	var held int64
	// Vouched: the statement is narrowed to one operator by owner_id, and
	// that operator is the one being saved -- the owner's to save, or a
	// reseller of the admin saving them (ownedReseller has checked).
	q := tx.WithContext(scope.Vouch(tx.Statement.Context))
	if err := q.Raw(`SELECT COALESCE(SUM(device_limit), 0) FROM clients
		WHERE owner_id = ? AND status NOT IN ('expired', 'exhausted')`, acc.AdminID).Scan(&held).Error; err != nil {
		return fmt.Errorf("count the seats held: %w", err)
	}
	e.Reason = model.CapacityModeSwitch
	return capacity.Recount(tx, acc, held, e)
}

// switchMode counts an operator's users another way from now on, and every
// reseller of theirs with them. The customers they have are left exactly as
// they are and nothing is charged for them: to consumable, what was spent so
// far starts at nothing; to open, the seats held are counted once, here.
func switchMode(tx *gorm.DB, acc *model.CapacityAccount, mode string, e capacity.Entry) error {
	if mode != model.CapacityOpen && mode != model.CapacityConsumable {
		return fault.Field("userMode", "users are counted %q or %q", model.CapacityOpen, model.CapacityConsumable)
	}
	accounts := []*model.CapacityAccount{acc}
	var kids []model.CapacityAccount
	if err := tx.Where("kind = ? AND admin_id IN (SELECT id FROM admins WHERE parent_admin_id = ?)",
		model.CapacityUsers, acc.AdminID).Find(&kids).Error; err != nil {
		return err
	}
	for i := range kids {
		accounts = append(accounts, &kids[i])
	}
	for _, a := range accounts {
		if err := tx.Model(&model.CapacityAccount{}).Where("id = ?", a.ID).Update("mode", mode).Error; err != nil {
			return err
		}
		a.Mode = mode
		e.Reason = model.CapacityModeSwitch
		if mode == model.CapacityConsumable {
			if err := capacity.Recount(tx, a, 0, e); err != nil {
				return err
			}
			continue
		}
		if err := holdServed(tx, a, e); err != nil {
			return err
		}
	}
	return nil
}

// releaseUsers settles an operator's users as they are removed. Counted as
// seats at once, what they were given goes back to the admin who gave it:
// their customers are no longer counted under them. Counted as spent units,
// nothing comes back -- what was handed to them is spent with them -- and
// the parent's handed figure keeps it. Their account goes; its ledger stays.
func releaseUsers(tx *gorm.DB, actor uint, target *model.Admin) error {
	acc, err := capacity.Account(tx, target.ID, model.CapacityUsers)
	if err != nil || acc == nil {
		return err
	}
	if target.ParentAdminID != 0 && acc.Mode == model.CapacityOpen && acc.Granted > 0 {
		parent, err := capacity.Account(tx, target.ParentAdminID, model.CapacityUsers)
		if err != nil {
			return err
		}
		if parent != nil {
			if err := capacity.Withdraw(tx, parent, acc.Granted, capacity.Entry{Reason: model.CapacityRelease, ActorID: actor}); err != nil {
				return err
			}
		}
	}
	return capacity.Close(tx, target.ID)
}

// UserCapacity is an operator's user account, or nil when they have no
// ceiling.
func (s *Admins) UserCapacity(ctx context.Context, adminID uint) (*model.CapacityAccount, error) {
	return capacity.Account(s.db.WithContext(ctx), adminID, model.CapacityUsers)
}
