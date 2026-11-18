package operators

import (
	"context"
	"fmt"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

// LedgerLine is one movement of capacity, as the capacity page lists it:
// whose account moved, who moved it, and for which customer or token.
type LedgerLine struct {
	model.CapacityEntry
	// Operator is whose account moved; Actor who did it ("" for the panel
	// itself: an expiry the reconciler noticed). Client is the customer the
	// units were for, "" once they are deleted.
	Operator string `json:"operator"`
	Actor    string `json:"actor"`
	Client   string `json:"client,omitempty"`
}

// Ledger is the capacity movements this operator may see, newest first: the
// owner's every account's, an admin's their own and their resellers', a
// reseller's their own. kind narrows to users or tokens ("" both); before,
// when not 0, pages back from that entry. limit is at most 500.
//
// Narrowed here and only here, by whose account moved: an operator can never
// ask for another tree's line, whatever the request names.
func (s *Admins) Ledger(ctx context.Context, me *model.Admin, kind string, before uint, limit int) ([]LedgerLine, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	holders, err := s.tokenHolders(ctx, me)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Model(&model.CapacityEntry{}).Order("id DESC").Limit(limit)
	if holders != nil {
		q = q.Where("admin_id IN ?", holders)
	}
	switch kind {
	case "":
	case model.CapacityUsers, model.CapacityTokens:
		q = q.Where("kind = ?", kind)
	default:
		return nil, fmt.Errorf("%w: kind is users or tokens", fault.ErrInvalid)
	}
	if before != 0 {
		q = q.Where("id < ?", before)
	}
	var rows []model.CapacityEntry
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: capacity ledger: %w", err)
	}

	// Names in two reads for the page, never one per line.
	ops, clients := map[uint]bool{}, map[uint]bool{}
	for _, r := range rows {
		ops[r.AdminID] = true
		if r.ActorID != 0 {
			ops[r.ActorID] = true
		}
		if r.ClientID != 0 {
			clients[r.ClientID] = true
		}
	}
	opNames, err := s.namesOf(ctx, &model.Admin{}, "username", ops)
	if err != nil {
		return nil, err
	}
	clientNames, err := s.namesOf(ctx, &model.Client{}, "name", clients)
	if err != nil {
		return nil, err
	}
	out := make([]LedgerLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, LedgerLine{
			CapacityEntry: r,
			Operator:      opNames[r.AdminID],
			Actor:         opNames[r.ActorID],
			Client:        clientNames[r.ClientID],
		})
	}
	return out, nil
}

// namesOf reads one name column for a set of ids.
//
// The customers' table narrows itself to the caller (scope), so a customer
// is named only to someone who may see them.
func (s *Admins) namesOf(ctx context.Context, m any, col string, ids map[uint]bool) (map[uint]string, error) {
	out := map[uint]string{}
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]uint, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var rows []struct {
		ID   uint
		Name string
	}
	if err := s.db.WithContext(ctx).Model(m).Select("id, "+col+" AS name").Where("id IN ?", list).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: names: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}
