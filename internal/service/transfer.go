package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/capacity"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/idset"
	"github.com/abolfazl/w-ui/internal/membership"
	"github.com/abolfazl/w-ui/internal/scope"
)

// Handing customers from one operator to another.
//
// Who may: the owner, any customer to any admin or reseller or back to
// themselves; an admin, between themselves and their own resellers. A
// reseller hands nothing on.
//
// What it costs: receiving a customer is a sale to the operator receiving
// it, whoever makes the move -- their users are taken (seats at once) or
// spent (consumable), and the move is refused if they have not got them.
// The customer must fit the receiver's rules of sale.
//
// Servers: a customer handed to an operator held to chosen servers ends up
// on exactly those -- kept where the two overlap, added where the receiver
// has a server the customer had not, and taken off the ones the receiver
// does not sell -- for the receiver to change from there. Handed to the
// owner, or to an admin let onto every tunnel, they keep what they have. The one giving it up gets seats back when counted at once,
// and nothing when consumable, as a customer removed gives nothing back.
//
// Traffic: what a customer uses is counted to whoever owns them at the
// moment it is used, so from the move on it is the receiver's. CarryUsage
// counts what they had used before the move to the receiver as well -- to
// the receiver, and to the admin above them, where that admin had not
// already counted it.
//
// The customer themselves is not touched: their link, credentials,
// configurations, plan and history are as they were, so nobody is cut off
// by a move. They leave the groups of the operator they came from and are
// filed in the receiver's hidden group, so the one they came from no
// longer sees them anywhere.

// TransferInput is customers to hand to an operator.
type TransferInput struct {
	IDs []uint `json:"ids"`
	// To is the operator receiving them; zero is the owner.
	To uint `json:"to"`
	// CarryUsage counts what the customers used before the move to the
	// receiver's traffic allowance too; otherwise only what they use from
	// the move on.
	CarryUsage bool `json:"carryUsage"`
	// DryRun says what would happen, and does nothing.
	DryRun bool `json:"dryRun"`
}

// TransferResult is what a move did, or would do.
type TransferResult struct {
	// Moved is how many changed hands; Unchanged, how many already were the
	// receiver's.
	Moved     int `json:"moved"`
	Unchanged int `json:"unchanged"`
	// Units is what the receiver's users moved by: seats taken, or units
	// spent.
	Units int64 `json:"units"`
	// CarriedBytes is the traffic counted to the receiver for use before
	// the move.
	CarriedBytes uint64 `json:"carriedBytes"`
	// ServersAdded and ServersRemoved are servers put on, and taken off,
	// the customers to match the receiver's, counted per customer.
	ServersAdded   int `json:"serversAdded"`
	ServersRemoved int `json:"serversRemoved"`
}

// HandTo is the operator a customer being made or changed is handed to, in
// the same change.
type HandTo struct {
	To         uint `json:"to"`
	CarryUsage bool `json:"carryUsage"`
}

// thenHand runs do -- a customer made or changed -- and hands that customer
// to to, in one transaction: refused, neither happens, and the addresses the
// first took are given back.
func (s *Clients) thenHand(ctx context.Context, to HandTo, do func(v *Clients) (uint, error)) error {
	j := s.ipj
	if j == nil {
		j = &ipJournal{}
	}
	mark := j.mark()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		v := s.seatsView(tx, j)
		id, err := do(v)
		if err != nil {
			return err
		}
		_, err = v.Transfer(ctx, TransferInput{IDs: []uint{id}, To: to.To, CarryUsage: to.CarryUsage})
		return err
	})
	if err != nil {
		j.undoFrom(mark, s.log)
	}
	return err
}

// maxTransfer bounds one move, as a bulk action is bounded.
const maxTransfer = 5000

// transferParty is an operator on either end of a move.
type transferParty struct {
	id, parent uint
	role       model.AdminRole
	label      string
	allTunnels bool
}

// Transfer hands the customers in.IDs to in.To.
func (s *Clients) Transfer(ctx context.Context, in TransferInput) (*TransferResult, error) {
	ids := idset.Unique(in.IDs)
	if len(ids) == 0 {
		return nil, invalidField("ids", "no customers selected")
	}
	if len(ids) > maxTransfer {
		return nil, invalidField("ids", "%d customers selected; at most %d at once", len(ids), maxTransfer)
	}
	// Read unnarrowed: who may be moved is decided here (withinReach), and
	// an admin's resellers' customers are among them.
	open := ScopeOf(ctx)
	open.Restricted = false
	db := s.db.WithContext(scope.Vouch(WithScope(ctx, open)))
	caller, err := transferCaller(ctx, db)
	if err != nil {
		return nil, err
	}
	to, err := transferTarget(db, caller, in.To)
	if err != nil {
		return nil, err
	}
	var clients []model.Client
	if err := db.Select("id, name, owner_id, admin_id, used_bytes").Where("id IN ?", ids).Find(&clients).Error; err != nil {
		return nil, fmt.Errorf("service: read the customers to move: %w", err)
	}
	if err := withinReach(db, caller, ids, clients); err != nil {
		return nil, err
	}
	servers, err := receiverServers(db, to)
	if err != nil {
		return nil, err
	}

	out := &TransferResult{}
	var moving []model.Client
	for _, c := range clients {
		if c.OwnerID == to.id {
			out.Unchanged++
			continue
		}
		moving = append(moving, c)
	}
	if len(moving) == 0 {
		return out, nil
	}
	moveIDs := make([]uint, len(moving))
	for i, c := range moving {
		moveIDs[i] = c.ID
	}

	// Every customer here was checked to be the caller's to move, so the
	// rest runs unnarrowed: the settling must see each of them before and
	// after, whichever operator holds them. The caller is still who acts.
	sc := ScopeOf(ctx)
	sc.Restricted = false
	vctx := scope.Vouch(WithScope(ctx, sc))
	// Addresses taken and given back on the way are written down, and given
	// back or taken again if the move is refused or only previewed.
	j := s.ipj
	if j == nil {
		j = &ipJournal{}
	}
	mark := j.mark()
	err = s.db.WithContext(vctx).Transaction(func(tx *gorm.DB) error {
		before := receiverUsed(tx, to.id)
		err := SettleChange(vctx, tx, moveIDs, ByTransfer, func(tx *gorm.DB) ([]uint, error) {
			if servers != nil {
				added, removed, err := s.seatsView(tx, j).matchServers(vctx, moveIDs, servers)
				if err != nil {
					return nil, err
				}
				out.ServersAdded, out.ServersRemoved = added, removed
			}
			carried, err := handOver(tx, to, moving, in.CarryUsage)
			out.CarriedBytes = carried
			return nil, err
		})
		if err != nil {
			return err
		}
		out.Moved = len(moving)
		out.Units = receiverUsed(tx, to.id) - before
		if in.DryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil {
		j.undoFrom(mark, s.log)
	}
	if errors.Is(err, errDryRun) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	s.log.Info("customers handed to another operator", "customers", out.Moved, "to", to.id,
		"units", out.Units, "carriedBytes", out.CarriedBytes,
		"serversAdded", out.ServersAdded, "serversRemoved", out.ServersRemoved)
	return out, nil
}

// transferCaller is who is moving customers: nil for the owner, else the
// admin doing it. A reseller hands nothing on.
func transferCaller(ctx context.Context, db *gorm.DB) (*model.Admin, error) {
	sc := ScopeOf(ctx)
	if !sc.Capped {
		return nil, nil
	}
	var me model.Admin
	if err := db.First(&me, sc.AdminID).Error; err != nil {
		return nil, fmt.Errorf("service: read who is moving customers: %w", err)
	}
	if me.Role != model.RoleAdmin {
		return nil, fmt.Errorf("%w: only the owner and an admin hand customers to another operator", ErrInvalid)
	}
	return &me, nil
}

// transferTarget is the operator receiving the customers, within the
// caller's reach: anyone for the owner; for an admin, themselves or one of
// their own resellers -- any other is not found, telling them nothing.
func transferTarget(db *gorm.DB, caller *model.Admin, id uint) (transferParty, error) {
	if id == 0 {
		if caller != nil {
			return transferParty{}, invalidField("to", "an admin hands customers to themselves or their own resellers")
		}
		return transferParty{role: model.RoleOwner, allTunnels: true}, nil
	}
	var a model.Admin
	if err := db.First(&a, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return transferParty{}, fmt.Errorf("%w: no operator %d", ErrNotFound, id)
		}
		return transferParty{}, fmt.Errorf("service: read the operator receiving them: %w", err)
	}
	if a.Role != model.RoleAdmin && a.Role != model.RoleReseller {
		return transferParty{}, invalidField("to", "customers go to an admin or a reseller")
	}
	if caller != nil && a.ID != caller.ID && (a.Role != model.RoleReseller || a.ParentAdminID != caller.ID) {
		return transferParty{}, fmt.Errorf("%w: no operator %d", ErrNotFound, id)
	}
	return transferParty{id: a.ID, parent: a.ParentAdminID, role: a.Role, label: a.GroupName,
		allTunnels: a.Role == model.RoleAdmin && a.CanViewAllInterfaces}, nil
}

// withinReach refuses customers that are missing, or that are not the
// caller's to move: an admin moves their own customers and their
// resellers', even one the owner lets see every customer.
func withinReach(db *gorm.DB, caller *model.Admin, ids []uint, clients []model.Client) error {
	if len(clients) != len(ids) {
		return fmt.Errorf("%w: %d of the customers selected", ErrNotFound, len(ids)-len(clients))
	}
	if caller == nil {
		return nil
	}
	var mine []uint
	if err := db.Model(&model.Admin{}).Where("parent_admin_id = ?", caller.ID).Pluck("id", &mine).Error; err != nil {
		return fmt.Errorf("service: read the admin's resellers: %w", err)
	}
	tree := map[uint]bool{caller.ID: true}
	for _, id := range mine {
		tree[id] = true
	}
	for _, c := range clients {
		if !tree[c.OwnerID] {
			return fmt.Errorf("%w: customer %d", ErrNotFound, c.ID)
		}
	}
	return nil
}

// receiverServers is the servers a customer handed to the receiver ends up
// on: theirs, or nil -- keep what they have -- for the owner and an admin
// let onto every tunnel. A receiver with none can be handed nobody, since a
// customer has to be on at least one.
func receiverServers(db *gorm.DB, to transferParty) ([]uint, error) {
	if to.allTunnels {
		return nil, nil
	}
	var ids []uint
	if err := db.Model(&model.AdminInterface{}).Where("admin_id = ?", to.id).Order("interface_id").Pluck("interface_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("service: read the receiver's servers: %w", err)
	}
	if len(ids) == 0 {
		return nil, invalidField("to", "the operator receiving them has no servers to sell; give them one first")
	}
	return ids, nil
}

// matchServers puts each customer on exactly the servers want, the way
// the customer form does (setInterfaces): files made on the ones added for
// every device they hold, and taken off, with their addresses, from the
// rest. It says how many servers were added and removed, per customer.
func (s *Clients) matchServers(ctx context.Context, ids []uint, want []uint) (added, removed int, err error) {
	wanted := map[uint]bool{}
	for _, id := range want {
		wanted[id] = true
	}
	for _, id := range ids {
		client, err := s.Get(ctx, id)
		if err != nil {
			return 0, 0, err
		}
		have := map[uint]bool{}
		for _, f := range clientInterfaces(client.Accounts) {
			have[f] = true
		}
		a, r := 0, 0
		for f := range wanted {
			if !have[f] {
				a++
			}
		}
		for f := range have {
			if !wanted[f] {
				r++
			}
		}
		if a == 0 && r == 0 {
			continue
		}
		if err := s.setInterfaces(ctx, client, want); err != nil {
			return 0, 0, fmt.Errorf("%s: %w", client.Name, err)
		}
		added += a
		removed += r
	}
	return added, removed, nil
}

// receiverUsed is the receiver's users in use, or zero with no account.
func receiverUsed(tx *gorm.DB, id uint) int64 {
	if id == 0 {
		return 0
	}
	acc, err := capacity.Account(tx, id, model.CapacityUsers)
	if err != nil || acc == nil {
		return 0
	}
	return acc.Used
}

// handOver writes the move: whose each customer is, the groups they are in,
// and, asked for, their past traffic on the receiver's allowance. It gives
// back the traffic carried.
func handOver(tx *gorm.DB, to transferParty, moving []model.Client, carry bool) (uint64, error) {
	stamp := uint(0)
	switch to.role {
	case model.RoleAdmin:
		stamp = to.id
	case model.RoleReseller:
		stamp = to.parent
	}
	ids := make([]uint, len(moving))
	for i, c := range moving {
		ids[i] = c.ID
	}
	if err := tx.Model(&model.Client{}).Where("id IN ?", ids).
		Updates(map[string]any{"owner_id": to.id, "admin_id": stamp}).Error; err != nil {
		return 0, fmt.Errorf("service: hand the customers over: %w", err)
	}
	if err := regroup(tx, to, moving); err != nil {
		return 0, err
	}
	if !carry {
		return 0, nil
	}
	// Each customer's past use, to the receiver and the admin above them,
	// leaving out whoever counted it already: an admin handing their own
	// customer to their reseller had it on their own allowance all along.
	add := map[uint]uint64{}
	var carried uint64
	for _, c := range moving {
		if c.UsedBytes == 0 {
			continue
		}
		carried += c.UsedBytes
		// The receiver and the admin above them, each once: an admin
		// receiving is both.
		for who := range map[uint]bool{to.id: true, stamp: true} {
			if who != 0 && who != c.OwnerID && who != c.AdminID {
				add[who] += c.UsedBytes
			}
		}
	}
	for who, n := range add {
		if err := tx.Model(&model.Admin{}).Where("id = ?", who).
			UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", n)).Error; err != nil {
			return 0, fmt.Errorf("service: count the customers' past traffic: %w", err)
		}
	}
	return carried, nil
}

// regroup takes the customers out of every group the receiver does not
// keep -- the operator they came from has their own groups and hidden
// label, none of which the receiver may be shown -- and files them in the
// receiver's hidden group.
func regroup(tx *gorm.DB, to transferParty, moving []model.Client) error {
	ids := make([]uint, len(moving))
	for i, c := range moving {
		ids[i] = c.ID
	}
	// The groups of each operator a customer came from go, whatever the
	// receiver keeps: a group is known here by its name alone, so the
	// giver's "vip" would otherwise stay on as the receiver's "vip", filed
	// there by nobody.
	byGiver := map[uint][]uint{}
	for _, c := range moving {
		if c.OwnerID != to.id {
			byGiver[c.OwnerID] = append(byGiver[c.OwnerID], c.ID)
		}
	}
	for giver, theirs := range byGiver {
		var names []string
		if err := tx.Model(&model.Group{}).Where("owner_id = ?", giver).Pluck("name", &names).Error; err != nil {
			return fmt.Errorf("service: read the groups they leave: %w", err)
		}
		if len(names) == 0 {
			continue
		}
		if err := tx.Where("client_id IN ? AND name IN ?", theirs, names).Delete(&model.ClientGroup{}).Error; err != nil {
			return fmt.Errorf("service: take the customers out of the groups they leave: %w", err)
		}
	}
	var keep []string
	if err := tx.Model(&model.Group{}).Where("owner_id = ?", to.id).Pluck("name", &keep).Error; err != nil {
		return fmt.Errorf("service: read the receiver's groups: %w", err)
	}
	if to.id == 0 {
		// The owner's own groups, less every operator's hidden one.
		var labels []string
		if err := tx.Model(&model.Admin{}).Where("group_name <> ''").Pluck("group_name", &labels).Error; err != nil {
			return fmt.Errorf("service: read the operators' hidden groups: %w", err)
		}
		hidden := map[string]bool{}
		for _, l := range labels {
			hidden[l] = true
		}
		kept := keep[:0]
		for _, n := range keep {
			if !hidden[n] {
				kept = append(kept, n)
			}
		}
		keep = kept
	} else if to.label != "" {
		keep = append(keep, to.label)
	}
	q := tx.Where("client_id IN ?", ids)
	if len(keep) > 0 {
		q = q.Where("name NOT IN ?", keep)
	}
	if err := q.Delete(&model.ClientGroup{}).Error; err != nil {
		return fmt.Errorf("service: take the customers out of the groups they leave: %w", err)
	}
	if to.label != "" {
		for _, id := range ids {
			if err := tx.Where(model.ClientGroup{ClientID: id, Name: to.label}).
				FirstOrCreate(&model.ClientGroup{ClientID: id, Name: to.label}).Error; err != nil {
				return fmt.Errorf("service: file the customers under the receiver: %w", err)
			}
		}
	}
	return membership.SyncLabel(tx, ids)
}
