package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// GroupSummary is one row of the groups page.
//
// A group is not stored anywhere: it is the distinct values of clients.group,
// aggregated. That is deliberate — an operator creates a group by typing a name
// on a client, and it stops existing when the last member leaves.
type GroupSummary struct {
	Name      string `json:"name"`
	Clients   int64  `json:"clients"`
	Devices   int64  `json:"devices"`
	Active    int64  `json:"active"`
	UpBytes   uint64 `json:"upBytes"`
	DownBytes uint64 `json:"downBytes"`
	UsedBytes uint64 `json:"usedBytes"`
}

// GroupTotals is the strip above the table.
type GroupTotals struct {
	Groups        int64  `json:"groups"`
	GroupedClient int64  `json:"groupedClients"`
	Ungrouped     int64  `json:"ungrouped"`
	UpBytes       uint64 `json:"upBytes"`
	DownBytes     uint64 `json:"downBytes"`
	UsedBytes     uint64 `json:"usedBytes"`
}

// GroupsResult is what the page needs, in one round trip.
type GroupsResult struct {
	Items  []GroupSummary `json:"items"`
	Totals GroupTotals    `json:"totals"`
}

// Groups aggregates clients by their group label.
func (s *Clients) Groups(ctx context.Context) (*GroupsResult, error) {
	db := s.db.WithContext(ctx)

	var rows []struct {
		Name      string
		Clients   int64
		Active    int64
		UpBytes   uint64
		DownBytes uint64
		UsedBytes uint64
	}
	// Written as raw SQL rather than through the builder: "group" is a reserved
	// word, and the builder quotes the identifier it is handed, turning an
	// already-quoted name into an invalid one. The SQL below is standard and
	// runs unchanged on both supported engines.
	err := db.Raw(`
		SELECT COALESCE("group", '') AS name,
		       COUNT(*)              AS clients,
		       SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS active,
		       COALESCE(SUM(up_bytes), 0)   AS up_bytes,
		       COALESCE(SUM(down_bytes), 0) AS down_bytes,
		       COALESCE(SUM(used_bytes), 0) AS used_bytes
		FROM clients
		WHERE COALESCE("group", '') <> ''
		GROUP BY COALESCE("group", '')
		ORDER BY COALESCE("group", '') ASC`, model.StatusActive).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("service: aggregate groups: %w", err)
	}

	// Device counts come from a second grouped query rather than a join, which
	// would multiply the traffic sums by the number of devices per client.
	devices := map[string]int64{}
	var devRows []struct {
		Name    string
		Devices int64
	}
	err = db.Raw(`
		SELECT COALESCE(c."group", '') AS name, COUNT(*) AS devices
		FROM accounts a
		JOIN clients c ON c.id = a.client_id
		WHERE COALESCE(c."group", '') <> ''
		GROUP BY COALESCE(c."group", '')`).
		Scan(&devRows).Error
	if err != nil {
		return nil, fmt.Errorf("service: count group devices: %w", err)
	}
	for _, d := range devRows {
		devices[d.Name] = d.Devices
	}

	out := &GroupsResult{Items: make([]GroupSummary, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, GroupSummary{
			Name:      r.Name,
			Clients:   r.Clients,
			Devices:   devices[r.Name],
			Active:    r.Active,
			UpBytes:   r.UpBytes,
			DownBytes: r.DownBytes,
			UsedBytes: r.UsedBytes,
		})
		out.Totals.Groups++
		out.Totals.GroupedClient += r.Clients
		out.Totals.UpBytes += r.UpBytes
		out.Totals.DownBytes += r.DownBytes
		out.Totals.UsedBytes += r.UsedBytes
	}

	if err := db.Raw(
		`SELECT COUNT(*) FROM clients WHERE COALESCE("group", '') = ''`).
		Scan(&out.Totals.Ungrouped).Error; err != nil {
		return nil, fmt.Errorf("service: count ungrouped: %w", err)
	}
	return out, nil
}

// ListGroupNames returns the distinct group labels, for the picker on the
// client form.
func (s *Clients) ListGroupNames(ctx context.Context) ([]string, error) {
	var names []string
	err := s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT COALESCE("group", '') AS g
		FROM clients
		WHERE COALESCE("group", '') <> ''
		ORDER BY g ASC`).
		Scan(&names).Error
	if err != nil {
		return nil, fmt.Errorf("service: list group names: %w", err)
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// RenameGroup moves every member of one group to another name.
//
// Renaming onto an existing name merges the two, which is the only sensible
// reading of the request and is what an operator tidying up labels wants.
func (s *Clients) RenameGroup(ctx context.Context, from, to string) (int64, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		return 0, fmt.Errorf("%w: no group named", ErrInvalid)
	}
	if to == "" {
		return 0, fmt.Errorf("%w: the new name cannot be empty", ErrInvalid)
	}
	if len(to) > 64 {
		return 0, fmt.Errorf("%w: group names are limited to 64 characters", ErrInvalid)
	}

	res := s.db.WithContext(ctx).Model(&model.Client{}).
		Where(`"group" = ?`, from).Update("group", to)
	if res.Error != nil {
		return 0, fmt.Errorf("service: rename group: %w", res.Error)
	}
	s.log.Info("group renamed", "from", from, "to", to, "clients", res.RowsAffected)
	return res.RowsAffected, nil
}

// AssignGroup puts the given clients into a group. An empty name takes them out
// of whatever group they were in.
func (s *Clients) AssignGroup(ctx context.Context, name string, ids []uint) (int64, error) {
	name = strings.TrimSpace(name)
	if len(name) > 64 {
		return 0, fmt.Errorf("%w: group names are limited to 64 characters", ErrInvalid)
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: no clients selected", ErrInvalid)
	}

	res := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("id IN ?", ids).Update("group", name)
	if res.Error != nil {
		return 0, fmt.Errorf("service: assign group: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// GroupAction is an operation applied to every member of a group.
type GroupAction string

const (
	GroupReset         GroupAction = "reset"         // zero usage, revive the exhausted
	GroupEnable        GroupAction = "enable"        //
	GroupDisable       GroupAction = "disable"       //
	GroupExtend        GroupAction = "extend"        // push expiry out by Days
	GroupSetQuota      GroupAction = "quota"         // set the allowance
	GroupClear         GroupAction = "clear"         // dissolve the group, keep clients
	GroupDeleteClients GroupAction = "deleteClients" // delete every member
)

// GroupOp describes a bulk action over a group.
type GroupOp struct {
	Action     GroupAction `json:"action"`
	Group      string      `json:"group"`
	Days       int         `json:"days"`
	QuotaBytes uint64      `json:"quotaBytes"`
}

// ApplyToGroup runs an action across every client carrying the label.
func (s *Clients) ApplyToGroup(ctx context.Context, op GroupOp) (int64, error) {
	group := strings.TrimSpace(op.Group)
	if group == "" {
		return 0, fmt.Errorf("%w: no group named", ErrInvalid)
	}

	db := s.db.WithContext(ctx)
	scope := func() *gorm.DB { return db.Model(&model.Client{}).Where(`"group" = ?`, group) }
	now := time.Now().UTC()

	switch op.Action {
	case GroupEnable:
		res := scope().Update("status", model.StatusActive)
		return res.RowsAffected, wrapBulk(res.Error)

	case GroupDisable:
		res := scope().Update("status", model.StatusDisabled)
		return res.RowsAffected, wrapBulk(res.Error)

	case GroupReset:
		res := scope().Updates(map[string]any{
			"used_bytes":    0,
			"up_bytes":      0,
			"down_bytes":    0,
			"last_reset_at": now,
			"status": gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END",
				model.StatusExhausted, model.StatusActive),
		})
		return res.RowsAffected, wrapBulk(res.Error)

	case GroupExtend:
		if op.Days == 0 {
			return 0, fmt.Errorf("%w: days must not be zero", ErrInvalid)
		}
		// The date arithmetic is done in Go, one row at a time, rather than in
		// SQL: the interval syntax differs between SQLite and PostgreSQL, and a
		// dialect-specific expression here would work in development and fail
		// silently on a Postgres deployment.
		//
		// Extension is measured from each client's own expiry, so a customer
		// with three weeks left keeps them. Anyone already expired, or with no
		// expiry at all, is measured from now instead.
		delta := time.Duration(op.Days) * 24 * time.Hour

		var members []model.Client
		if err := scope().Find(&members).Error; err != nil {
			return 0, fmt.Errorf("service: load group members: %w", err)
		}

		var n int64
		err := db.Transaction(func(tx *gorm.DB) error {
			for _, m := range members {
				base := now
				if m.ExpiresAt != nil && m.ExpiresAt.After(now) {
					base = *m.ExpiresAt
				}
				next := base.Add(delta)

				fields := map[string]any{"expires_at": next}
				if m.Status == model.StatusExpired && next.After(now) {
					fields["status"] = model.StatusActive
				}
				if err := tx.Model(&model.Client{}).
					Where("id = ?", m.ID).Updates(fields).Error; err != nil {
					return err
				}
				n++
			}
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("service: extend group: %w", err)
		}
		return n, nil

	case GroupSetQuota:
		res := scope().Updates(map[string]any{
			"quota_bytes": op.QuotaBytes,
			"status": gorm.Expr(
				"CASE WHEN status = ? AND (? = 0 OR used_bytes < ?) THEN ? ELSE status END",
				model.StatusExhausted, op.QuotaBytes, op.QuotaBytes, model.StatusActive),
		})
		return res.RowsAffected, wrapBulk(res.Error)

	case GroupClear:
		res := scope().Update("group", "")
		return res.RowsAffected, wrapBulk(res.Error)

	case GroupDeleteClients:
		var ids []uint
		if err := scope().Pluck("id", &ids).Error; err != nil {
			return 0, fmt.Errorf("service: list group members: %w", err)
		}
		return s.Bulk(ctx, BulkDelete, ids)

	default:
		return 0, fmt.Errorf("%w: unknown action %q", ErrInvalid, op.Action)
	}
}
