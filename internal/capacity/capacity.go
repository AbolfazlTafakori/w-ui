// Package capacity keeps how many users and API tokens each admin and
// reseller has, and moves it.
//
// One system for both. An operator's account of a kind holds three running
// figures -- granted, used, handed on -- and every change to one is a single
// conditional UPDATE that refuses to take a figure past what is free, plus a
// ledger entry, both inside the caller's transaction. So:
//
//   - a ceiling cannot be passed by two requests at once: the second UPDATE
//     finds nothing free and changes no row, whatever order they ran in;
//   - nothing is taken for work that failed: the caller's rollback takes the
//     UPDATE and the entry with it;
//   - nothing is counted on a request: the figures are read, not summed;
//   - every figure is the sum of its ledger entries, always.
//
// What a customer, an extension or a token costs, and when a seat is given
// back, is decided by the callers (internal/service, internal/operators);
// this package only moves units and refuses what would overdraw.
package capacity

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// ErrFull is a change refused for want of free units. Callers wrap it with
// the sentence their caller is shown.
var ErrFull = errors.New("capacity: not enough left")

// ErrTooLarge is a count no plan could mean: units are counted in int64, and
// a request for more than MaxUnits at once is refused before any arithmetic.
var ErrTooLarge = errors.New("capacity: too many units at once")

// MaxUnits bounds one change. A million users or tokens in one go is far past
// any real sale and far short of overflowing a figure.
const MaxUnits = 1_000_000

// Entry describes one change for the ledger: why, by whom, and for what.
type Entry struct {
	Reason   string
	ActorID  uint
	ClientID uint
	TokenID  uint
}

// Account is an operator's account of a kind, or nil when they have none:
// for users, no ceiling; for tokens, no tokens.
func Account(tx *gorm.DB, adminID uint, kind string) (*model.CapacityAccount, error) {
	var a model.CapacityAccount
	err := tx.Where("admin_id = ? AND kind = ?", adminID, kind).Take(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("capacity: read %s account: %w", kind, err)
	}
	return &a, nil
}

// Free is what is left to take or hand on.
func Free(a *model.CapacityAccount) int64 {
	if a == nil {
		return 0
	}
	return a.Granted - a.Used - a.Handed
}

// The conditions a move is refused under. Each is checked by the database in
// the UPDATE itself, never read first and written after.
const (
	whenFree     = "granted - used - handed >= ?" // taking: what is free covers it
	whenUsed     = "used >= ?"                    // giving back: never below zero
	whenHanded   = "handed >= ?"                  // taking back from a reseller
	whenGranted  = "granted >= ?"                 // a debit: never below zero
	alwaysAllows = ""
)

// move changes one figure of an account by delta, if cond holds of n, and
// writes the entry. It is the only write this package makes to an account.
func move(tx *gorm.DB, a *model.CapacityAccount, field string, delta int64, cond string, e Entry) error {
	if a == nil {
		return ErrFull
	}
	n := delta
	if n < 0 {
		n = -n
	}
	if n == 0 {
		return nil
	}
	if n > MaxUnits {
		return ErrTooLarge
	}
	q := tx.Model(&model.CapacityAccount{}).Where("id = ?", a.ID)
	if cond != alwaysAllows {
		q = q.Where(cond, n)
	}
	res := q.Updates(map[string]any{field: gorm.Expr(field+" + ?", delta), "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return fmt.Errorf("capacity: move %s: %w", field, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrFull
	}
	switch field {
	case "granted":
		a.Granted += delta
	case "used":
		a.Used += delta
	case "handed":
		a.Handed += delta
	}
	entry := model.CapacityEntry{AccountID: a.ID, AdminID: a.AdminID, Kind: a.Kind, Field: field, Delta: delta,
		Reason: e.Reason, ActorID: e.ActorID, ClientID: e.ClientID, TokenID: e.TokenID, At: time.Now().UTC()}
	if err := tx.Create(&entry).Error; err != nil {
		return fmt.Errorf("capacity: write ledger: %w", err)
	}
	return nil
}

// Take uses n units: a seat held, a unit spent, a token issued.
func Take(tx *gorm.DB, a *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	return move(tx, a, "used", n, whenFree, e)
}

// Hold takes n seats of an open account whatever is free. It is for what
// the owner and the panel itself do -- the owner extending a reseller's
// customer, the panel reviving one on its monthly reset -- so the seats a
// reseller holds always match the customers being served, even past their
// ceiling. Nothing an operator asks for goes through it.
func Hold(tx *gorm.DB, a *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	return move(tx, a, "used", n, alwaysAllows, e)
}

// Recount sets the used figure to what it is now found to be, by an entry
// of the difference. It is for the moment users start or stop being counted
// a way -- an operator given a ceiling, a mode switched by the owner -- and
// for nothing an operator asks.
func Recount(tx *gorm.DB, a *model.CapacityAccount, used int64, e Entry) error {
	if used < 0 || used > 1<<40 {
		return ErrTooLarge
	}
	if a == nil || used == a.Used {
		return nil
	}
	d := used - a.Used
	if d > MaxUnits || d < -MaxUnits {
		// Past a single move's bound: in steps, each an entry.
		step := int64(MaxUnits)
		if d < 0 {
			step = -MaxUnits
		}
		if err := move(tx, a, "used", step, alwaysAllows, e); err != nil {
			return err
		}
		return Recount(tx, a, used, e)
	}
	return move(tx, a, "used", d, alwaysAllows, e)
}

// Give hands n seats back. Only open (concurrent) accounts give anything
// back; a spent unit or a revoked token is gone, and asking here for one is
// a mistake in the caller, refused rather than honoured.
func Give(tx *gorm.DB, a *model.CapacityAccount, n int64, e Entry) error {
	if a == nil || n <= 0 {
		return nil
	}
	if a.Kind != model.CapacityUsers || a.Mode != model.CapacityOpen {
		return fmt.Errorf("capacity: a %s %s account gives nothing back", a.Mode, a.Kind)
	}
	return move(tx, a, "used", -n, whenUsed, e)
}

// Charge adds n to what an operator was given; Debit takes n away. A debit
// may leave less than is in use -- nobody is cut off, nothing new is let in
// until it is free again -- but never below zero.
func Charge(tx *gorm.DB, a *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	return move(tx, a, "granted", n, alwaysAllows, e)
}

func Debit(tx *gorm.DB, a *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	return move(tx, a, "granted", -n, whenGranted, e)
}

// HandDown passes n of the parent's free units to the child: the parent's
// handed figure and the child's granted one move together, or neither does.
// It is a sale: the parent can never hand on more than they have free.
func HandDown(tx *gorm.DB, parent, child *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	if parent == nil || child == nil || parent.Kind != child.Kind {
		return ErrFull
	}
	pe, ce := e, e
	pe.Reason, ce.Reason = model.CapacityHandDown, model.CapacityHandDown
	if err := move(tx, parent, "handed", n, whenFree, pe); err != nil {
		return err
	}
	return move(tx, child, "granted", n, alwaysAllows, ce)
}

// HandBack takes n units the child has free back to the parent. Only what
// the child has not used or handed on can come back.
func HandBack(tx *gorm.DB, parent, child *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	if parent == nil || child == nil || parent.Kind != child.Kind {
		return ErrFull
	}
	pe, ce := e, e
	pe.Reason, ce.Reason = model.CapacityHandBack, model.CapacityHandBack
	if err := move(tx, child, "granted", -n, whenFree, ce); err != nil {
		return err
	}
	return move(tx, parent, "handed", -n, whenHanded, pe)
}

// Withdraw lowers what a parent handed on by n, when the reseller holding it
// is removed with seats that were only lent.
func Withdraw(tx *gorm.DB, parent *model.CapacityAccount, n int64, e Entry) error {
	if n < 0 {
		return ErrTooLarge
	}
	return move(tx, parent, "handed", -n, whenHanded, e)
}

// Open makes an operator's account of a kind, empty, in a mode. An existing
// one is returned as it is.
func Open(tx *gorm.DB, adminID uint, kind, mode string) (*model.CapacityAccount, error) {
	if a, err := Account(tx, adminID, kind); err != nil || a != nil {
		return a, err
	}
	a := model.CapacityAccount{AdminID: adminID, Kind: kind, Mode: mode, UpdatedAt: time.Now().UTC()}
	if err := tx.Create(&a).Error; err != nil {
		return nil, fmt.Errorf("capacity: open %s account: %w", kind, err)
	}
	return &a, nil
}

// Close removes an operator's accounts when the operator is removed. Their
// ledger stays, as the record of what they had and spent; their parent's
// handed figure is given the child's unspent grant back first (HandBack),
// which the caller does before this.
func Close(tx *gorm.DB, adminID uint) error {
	return tx.Where("admin_id = ?", adminID).Delete(&model.CapacityAccount{}).Error
}

// Balanced reports whether every figure of every account equals the sum of
// its ledger entries; the accounts that do not are named. A test holds the
// whole panel to it after every rule is exercised.
func Balanced(tx *gorm.DB) ([]uint, error) {
	var bad []uint
	var rows []struct {
		ID      uint
		Granted int64
		Used    int64
		Handed  int64
		G       int64
		U       int64
		H       int64
	}
	err := tx.Raw(`
		SELECT a.id, a.granted, a.used, a.handed,
		       COALESCE(SUM(CASE WHEN e.field = 'granted' THEN e.delta END), 0) AS g,
		       COALESCE(SUM(CASE WHEN e.field = 'used'    THEN e.delta END), 0) AS u,
		       COALESCE(SUM(CASE WHEN e.field = 'handed'  THEN e.delta END), 0) AS h
		FROM capacity_accounts a LEFT JOIN capacity_entries e ON e.account_id = a.id
		GROUP BY a.id, a.granted, a.used, a.handed`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Granted != r.G || r.Used != r.U || r.Handed != r.H || r.Granted < 0 || r.Used < 0 || r.Handed < 0 {
			bad = append(bad, r.ID)
		}
	}
	return bad, nil
}
