package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// What a customer costs an operator in users, settled in one place.
//
// Every way a customer is made, extended, grown, ended or removed -- the
// form, a batch, an import, a bulk action, a group action, the Telegram
// bot, the reconciler -- runs inside withSeats. It reads the customers it
// touches before the work and after, in the same transaction, and moves the
// owning operator's units by the difference: so no path can make, revive or
// extend a customer without paying for it, and a path added later is held to
// the same rules by going through the same door. If the units are not
// there, the transaction -- the work with it -- is rolled back.
//
// Open (seats at once): a customer being served holds as many seats as its
// users; one that ended or was removed gives them back; one revived takes
// them again, or is refused. Consumable: making a customer spends its users;
// growing it spends the difference; extending it -- a later end, more
// traffic, its usage reset -- spends its users again; nothing ending,
// shrinking or being removed ever gives a unit back.
//
// Only what an operator asks for is held to their ceiling. What the owner
// does, and what the panel does on its own, moves an open operator's seats
// to match the customers served (past the ceiling if need be: nobody is cut
// off) and spends nothing.

// seatState is what of a customer decides what it costs.
type seatState struct {
	owner    uint
	users    int64
	alive    bool
	expires  *time.Time
	duration int
	quota    uint64
	used     uint64
	// creds is what they sign in with, read only for a change an operator
	// is charged for (loadCreds); nil otherwise.
	creds *credSet
}

func seatStateOf(c *model.Client) seatState {
	return seatState{
		owner: c.OwnerID, users: int64(max(c.DeviceLimit, 0)),
		// Served, or switched off by hand: either holds its seats. Ended --
		// expired or out of traffic -- does not.
		alive:   c.Status != model.StatusExpired && c.Status != model.StatusExhausted,
		expires: c.ExpiresAt, duration: c.DurationDays*24 + c.DurationHours, quota: c.QuotaBytes, used: c.UsedBytes,
	}
}

// extended reports whether a change gave a customer more: a later end, a
// longer term not yet started, more traffic, or their usage started again.
// No end and no traffic limit are the most there is, so moving to either
// from a limit is more too.
func extended(b, a seatState) bool {
	switch {
	case b.expires != nil && (a.expires == nil || a.expires.After(*b.expires)):
		return true
	case b.expires == nil && a.expires == nil && a.duration > b.duration:
		return true
	case b.quota != 0 && (a.quota == 0 || a.quota > b.quota):
		return true
	case b.used > 0 && a.used == 0:
		return true
	}
	return false
}

// ErrNoUsers is a change refused for want of users left. The sentence the
// operator is shown is built around it (errNoUsers).
var ErrNoUsers = errors.New("no users left")

// ErrBarred is a change refused because the operator making it is paused.
var ErrBarred = errors.New("paused")

func errNoUsers(a *model.CapacityAccount, need int64) error {
	if a.Mode == model.CapacityConsumable {
		return fmt.Errorf("%w: %w: this needs %s and you have %d left; ask for more to be added",
			ErrInvalid, ErrNoUsers, usersWord(need), max(capacity.Free(a), 0))
	}
	return fmt.Errorf("%w: %w: this needs %s at once and %d of your %d are free; remove or let a customer end, or ask for more",
		ErrInvalid, ErrNoUsers, usersWord(need), max(capacity.Free(a), 0), a.Granted)
}

// usersWord is a count of users as a sentence says it: "1 user", "2 users".
func usersWord(n int64) string {
	if n == 1 {
		return "1 user"
	}
	return fmt.Sprintf("%d users", n)
}

// seatsView is the service over one transaction: the same methods, every
// read and write of theirs inside it, and the same state they keep between
// requests -- the count of refused links a reseller is stopped probing with.
func (s *Clients) seatsView(tx *gorm.DB, j *ipJournal) *Clients {
	return &Clients{db: tx, pools: s.pools, log: s.log, live: s.live, changes: s.changes, remote: s.remote, subClashes: s.subClashes, ipj: j}
}

// The addresses a transaction takes and gives back are taken and given in
// memory at once (internal/ipam), and a rollback does not undo memory. So
// each is written down, and a transaction that fails -- refused for want of
// users, or for any other reason -- has them undone in reverse: an address
// it took is released, one it gave back is taken again. Otherwise every
// refused customer would hold an address until the panel restarts, and a
// delete that rolled back would leave its address free for another.
type ipJournal struct {
	mu    sync.Mutex
	notes []ipNote
}

type ipNote struct {
	alloc *ipam.Allocator
	addr  netip.Addr
	took  bool
}

func (j *ipJournal) note(alloc *ipam.Allocator, addr netip.Addr, took bool) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.notes = append(j.notes, ipNote{alloc, addr, took})
	j.mu.Unlock()
}

func (j *ipJournal) mark() int {
	if j == nil {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.notes)
}

// undoFrom undoes, newest first, what was written down from mark on.
func (j *ipJournal) undoFrom(mark int, log *slog.Logger) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.notes) - 1; i >= mark; i-- {
		n := j.notes[i]
		var err error
		if n.took {
			err = n.alloc.Release(n.addr)
		} else {
			err = n.alloc.Reserve(n.addr)
		}
		if err != nil && log != nil {
			log.Warn("could not undo an address of a change that failed", "address", n.addr, "error", err)
		}
	}
	j.notes = j.notes[:mark]
}

// noteTaken and noteFreed write an address down on the transaction's
// journal; outside one they do nothing.
func (s *Clients) noteTaken(alloc *ipam.Allocator, addr netip.Addr) { s.ipj.note(alloc, addr, true) }
func (s *Clients) noteFreed(alloc *ipam.Allocator, addr netip.Addr) { s.ipj.note(alloc, addr, false) }

// loadSeats reads the customers whose units a change may move.
func loadSeats(tx *gorm.DB, ids []uint) (map[uint]seatState, error) {
	out := make(map[uint]seatState, len(ids))
	for start := 0; start < len(ids); start += 900 {
		var rows []model.Client
		if err := tx.Select("id, owner_id, device_limit, status, expires_at, duration_days, duration_hours, quota_bytes, used_bytes").
			Where("id IN ?", ids[start:min(start+900, len(ids))]).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("service: read customers' users: %w", err)
		}
		for i := range rows {
			out[rows[i].ID] = seatStateOf(&rows[i])
		}
	}
	return out, nil
}

// withCreds adds what each of the customers ids signs in with to their
// states: only customers there before the change can be given new ones.
func withCreds(tx *gorm.DB, states map[uint]seatState, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	creds, err := loadCreds(tx, ids)
	if err != nil {
		return err
	}
	for id, c := range creds {
		if st, ok := states[id]; ok {
			st.creds = c
			states[id] = st
		}
	}
	return nil
}

// checkSalesOf holds a change to the rules of sale of every operator whose
// customer it touched.
func checkSalesOf(tx *gorm.DB, before, after map[uint]seatState, ids []uint) error {
	owners := map[uint]bool{}
	for _, st := range after {
		if st.owner != 0 {
			owners[st.owner] = true
		}
	}
	rules, err := saleRules(tx, owners)
	if err != nil {
		return err
	}
	return checkSales(rules, before, after, ids, time.Now().UTC())
}

// SettleByPanel is SettleChange as the panel's own work, for a package that
// changes customers but cannot import this one (fleet's handover): the users
// each owner holds move to match, and nothing is spent.
func SettleByPanel(ctx context.Context, tx *gorm.DB, ids []uint, change func(tx *gorm.DB) error) error {
	return SettleChange(ctx, tx, ids, ByPanel, func(tx *gorm.DB) ([]uint, error) { return nil, change(tx) })
}

// SeatRule is who a change is settled as.
type SeatRule int

const (
	// ByCaller settles as whoever the context is: an operator held to
	// their ceiling and charged, the owner and the panel moving seats to
	// match and spending nothing.
	ByCaller SeatRule = iota
	// ByPanel is the panel's own work -- an expiry noticed, a plan ended.
	// Seats move to match the customers served; nothing is spent.
	ByPanel
	// ByPanelIfFree is a revival the panel makes on its own, the monthly
	// reset: it takes seats only if they are free, and is refused (the
	// customer stays as they were) otherwise. Nothing is spent.
	ByPanelIfFree
	// ByTransfer is customers handed to another operator (Transfer): a sale
	// to the one receiving them, whoever makes the move -- held to their
	// ceiling and their rules of sale, and charged.
	ByTransfer
)

// SettleChange runs change on the customers ids (and any it makes, which it
// returns) in one transaction, and settles their users before it commits.
// It is how every way of changing customers pays for it; the reconciler's
// own sweeps call it directly.
func SettleChange(ctx context.Context, db *gorm.DB, ids []uint, rule SeatRule, change func(tx *gorm.DB) ([]uint, error)) error {
	// What an operator asks for is held to their rules of sale and pays for
	// new credentials; reading those is spared the owner and the panel.
	_, charged := seatTerms(ScopeOf(ctx), rule)
	settle := func(tx *gorm.DB) error {
		before, err := loadSeats(tx, ids)
		if err != nil {
			return err
		}
		if charged {
			if err := withCreds(tx, before, ids); err != nil {
				return err
			}
		}
		made, err := change(tx)
		if err != nil {
			return err
		}
		all := append(append([]uint(nil), ids...), made...)
		after, err := loadSeats(tx, all)
		if err != nil {
			return err
		}
		if charged {
			if err := withCreds(tx, after, ids); err != nil {
				return err
			}
			if err := checkSalesOf(tx, before, after, all); err != nil {
				return err
			}
		}
		return settleSeats(ctx, tx, before, after, all, rule)
	}
	// Already inside one (an import making each of its rows): a savepoint
	// of its own, so one row refused is undone alone and the rest go on.
	// The work inside runs with nested transactions turned off -- a savepoint
	// for each of a customer's own writes cost a create two thirds again --
	// so this is the only savepoint a change makes.
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		sp := fmt.Sprintf("seats%d", savepoints.Add(1))
		tx := db.WithContext(ctx)
		if err := tx.SavePoint(sp).Error; err != nil {
			return err
		}
		if err := settle(tx); err != nil {
			if rerr := tx.RollbackTo(sp).Error; rerr != nil {
				return fmt.Errorf("%w (and undoing it: %v)", err, rerr)
			}
			return err
		}
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return settle(tx.Session(&gorm.Session{DisableNestedTransaction: true}))
	})
}

// savepoints names each savepoint apart.
var savepoints atomic.Uint64

// withSeats is SettleChange for the service's own methods, run on a view of
// the service over the transaction.
func (s *Clients) withSeats(ctx context.Context, ids []uint, auto bool, work func(v *Clients) ([]uint, error)) error {
	rule := ByCaller
	if auto {
		rule = ByPanel
	}
	// One journal for the outermost change and every change inside it; each
	// that fails undoes its own part, so a failure an outer change carries
	// on past (an import's row refused) gives its addresses back too.
	j := s.ipj
	if j == nil {
		j = &ipJournal{}
	}
	mark := j.mark()
	err := SettleChange(ctx, s.db, ids, rule, func(tx *gorm.DB) ([]uint, error) {
		return work(s.seatsView(tx, j))
	})
	if err != nil {
		j.undoFrom(mark, s.log)
	}
	return err
}

// seatMove is one customer's units to move.
type seatMove struct {
	client uint
	owner  uint
	delta  int64 // seats for an open account: + taken, - given back
	spend  int64 // units spent for a consumable one
	rekey  int64 // users given new credentials, spent on a consumable one unless let off
	reason string
}

// settleSeats moves each owner's units by what the change did to each of
// their customers. Seats given back are given first, so a change that frees
// one customer's seats and revives another's is not refused on the way.
func settleSeats(ctx context.Context, tx *gorm.DB, before, after map[uint]seatState, ids []uint, rule SeatRule) error {
	sc := ScopeOf(ctx)
	enforced, spends := seatTerms(sc, rule)
	actor := sc.Actor
	moves := planSeats(before, after, ids)

	accounts := map[uint]*model.CapacityAccount{}
	for _, m := range moves {
		acc, ok := accounts[m.owner]
		if !ok {
			var err error
			if acc, err = capacity.Account(tx, m.owner, model.CapacityUsers); err != nil {
				return err
			}
			accounts[m.owner] = acc
		}
		if acc == nil {
			continue // an operator with no ceiling
		}
		e := capacity.Entry{Reason: m.reason, ActorID: actor, ClientID: m.client}
		var err error
		switch {
		case acc.Mode == model.CapacityOpen && m.delta < 0:
			err = capacity.Give(tx, acc, -m.delta, e)
			if errors.Is(err, capacity.ErrFull) {
				// Fewer seats held than the customer had: a figure the
				// owner or the panel moved past. Never below zero.
				err = capacity.Give(tx, acc, acc.Used, e)
			}
		case acc.Mode == model.CapacityOpen && m.delta > 0 && enforced:
			if err = capacity.Take(tx, acc, m.delta, e); errors.Is(err, capacity.ErrFull) {
				err = errNoUsers(acc, m.delta)
			}
		case acc.Mode == model.CapacityOpen && m.delta > 0:
			err = capacity.Hold(tx, acc, m.delta, e)
		case acc.Mode == model.CapacityConsumable && m.spend > 0 && spends:
			if err = capacity.Take(tx, acc, m.spend, e); errors.Is(err, capacity.ErrFull) {
				err = errNoUsers(acc, m.spend)
			}
		}
		if err != nil {
			return err
		}
		if acc.Mode == model.CapacityConsumable && m.rekey > 0 && spends {
			if err := chargeRekey(tx, acc, m, actor); err != nil {
				return err
			}
		}
	}
	return nil
}

// chargeRekey spends the users a customer was given new credentials for,
// unless their operator was let off it.
func chargeRekey(tx *gorm.DB, acc *model.CapacityAccount, m seatMove, actor uint) error {
	rules, err := saleRules(tx, map[uint]bool{m.owner: true})
	if err != nil {
		return err
	}
	if rules[m.owner].rekeyFree {
		return nil
	}
	e := capacity.Entry{Reason: model.CapacityRekey, ActorID: actor, ClientID: m.client}
	if err := capacity.Take(tx, acc, m.rekey, e); err != nil {
		if errors.Is(err, capacity.ErrFull) {
			return fmt.Errorf("%w: %w: new credentials hand this plan on, and cost %s; you have %d left",
				ErrInvalid, ErrNoUsers, usersWord(m.rekey), max(capacity.Free(acc), 0))
		}
		return err
	}
	return nil
}

// seatTerms is how a change is held and charged: enforced, refused past the
// ceiling; spends, units spent. Any admin or reseller acting is both,
// narrowed to their tree or not; the owner and the panel move seats to
// match and spend nothing; a revival the panel makes on its own is held to
// what is free and spends nothing.
func seatTerms(sc Scope, rule SeatRule) (enforced, spends bool) {
	if rule == ByTransfer {
		return true, true
	}
	return (sc.Capped && rule == ByCaller) || rule == ByPanelIfFree, sc.Capped && rule == ByCaller
}

// planSeats is what a change did to each owner's units, customer by
// customer, gives first: the one sum both the settling and its read-only
// preview make, so the two cannot disagree.
func planSeats(before, after map[uint]seatState, ids []uint) []seatMove {
	var moves []seatMove
	seen := map[uint]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		b, had := before[id]
		a, has := after[id]
		// A customer that changed hands is two moves: gone from the old
		// owner, as a removal is, and come to the new one, as a creation
		// is. Each owner's units then follow the customers they serve.
		if had && has && b.owner != a.owner {
			for _, m := range planSeats(map[uint]seatState{id: b}, nil, []uint{id}) {
				m.reason = model.CapacityGivenAway
				moves = append(moves, m)
			}
			for _, m := range planSeats(nil, map[uint]seatState{id: a}, []uint{id}) {
				m.reason = model.CapacityTransferIn
				moves = append(moves, m)
			}
			continue
		}
		owner := a.owner
		if !has {
			owner = b.owner
		}
		if owner == 0 {
			continue // the owner's own customer: no ceiling
		}
		m := seatMove{client: id, owner: owner}
		var bSeats, aSeats int64
		if had && b.alive {
			bSeats = b.users
		}
		if has && a.alive {
			aSeats = a.users
		}
		m.delta = aSeats - bSeats
		switch {
		case !had && has:
			m.spend, m.reason = a.users, model.CapacityCreate
		case had && has:
			if a.users > b.users {
				m.spend, m.reason = a.users-b.users, model.CapacityGrow
			}
			if extended(b, a) {
				m.spend += a.users
				m.reason = model.CapacityExtend
			}
			// New credentials on a plan still running, in a change that
			// was not itself a sale of them: the plan handed to someone new.
			// Growing the plan sells the users added, not the ones it had,
			// so a grow in the same change does not let those go free.
			if (m.reason == "" || m.reason == model.CapacityGrow) && a.alive {
				m.rekey = rekeyUsers(b.creds, a.creds, a.users, max(a.users-b.users, 0))
			}
		}
		if m.reason == "" {
			switch {
			case m.delta < 0 && !has:
				m.reason = model.CapacityRelease
			case m.delta < 0 && a.users < b.users:
				m.reason = model.CapacityShrink
			case m.delta < 0:
				m.reason = model.CapacityEnded
			case m.delta > 0:
				m.reason = model.CapacityRevived
			}
		}
		if m.delta != 0 || m.spend != 0 || m.rekey != 0 {
			moves = append(moves, m)
		}
	}
	sort.SliceStable(moves, func(i, j int) bool { return moves[i].delta < moves[j].delta })
	return moves
}
