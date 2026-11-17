package operators

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// What admins and resellers changed, for the admin over them.
//
// Written beside the panel's log line for each change (internal/api's
// logAction) rather than read back from the log: the log keeps a thousand
// lines in memory, goes on a restart, and names an operator by a username
// that can change. The owner's own changes and a machine token's are not
// here -- the owner has the log, and nobody is above them.

// actionRetention is how long a change is kept: long enough for a month's
// questions about who did what, short enough that the table stays small.
const actionRetention = 90 * 24 * time.Hour

// pruneActionsEvery bounds how often the old rows are swept.
const pruneActionsEvery = time.Hour

var actionsPruned struct {
	sync.Mutex
	at time.Time
}

// RecordAction writes down one change an admin or a reseller made. Anyone
// else is not recorded here. The path is the request's as routed -- ids at
// most -- and nothing of the body or the answer is taken.
func (s *Admins) RecordAction(ctx context.Context, admin *model.Admin, method, path string, status int, viaToken bool) {
	if admin == nil || (admin.Role != model.RoleAdmin && admin.Role != model.RoleReseller) {
		return
	}
	if len(path) > 255 {
		path = path[:255]
	}
	now := time.Now().UTC()
	row := model.OperatorAction{AdminID: admin.ID, Method: method, Path: path, Status: status, ViaToken: viaToken, At: now}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		s.log.Warn("could not record an operator's action", "operator", admin.Username, "error", err)
		return
	}
	actionsPruned.Lock()
	due := now.Sub(actionsPruned.at) > pruneActionsEvery
	if due {
		actionsPruned.at = now
	}
	actionsPruned.Unlock()
	if due {
		if err := s.db.WithContext(ctx).Where("at < ?", now.Add(-actionRetention)).Delete(&model.OperatorAction{}).Error; err != nil {
			s.log.Warn("could not prune operators' actions", "error", err)
		}
	}
}

// Activity is the changes this operator may see, newest first: the owner
// every operator's, an admin their own and their resellers', a reseller
// their own. limit is at most 1000.
func (s *Admins) Activity(ctx context.Context, me *model.Admin, limit int) ([]model.OperatorAction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	holders, err := s.tokenHolders(ctx, me)
	if err != nil {
		return nil, err
	}
	q := s.db.WithContext(ctx).Order("at DESC, id DESC").Limit(limit)
	if holders != nil {
		q = q.Where("admin_id IN ?", holders)
	}
	out := []model.OperatorAction{}
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("service: operators' actions: %w", err)
	}
	names, err := s.usernames(ctx, out)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Username = names[out[i].AdminID]
	}
	return out, nil
}

// usernames is the name of each operator these rows name.
func (s *Admins) usernames(ctx context.Context, rows []model.OperatorAction) (map[uint]string, error) {
	ids := map[uint]bool{}
	for _, r := range rows {
		ids[r.AdminID] = true
	}
	out := map[uint]string{}
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]uint, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var names []struct {
		ID       uint
		Username string
	}
	if err := s.db.WithContext(ctx).Model(&model.Admin{}).Where("id IN ?", list).Select("id, username").Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("service: name operators' actions: %w", err)
	}
	for _, n := range names {
		out[n.ID] = n.Username
	}
	return out, nil
}
