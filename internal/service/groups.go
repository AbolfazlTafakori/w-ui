package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// groupsOf is the list form of what was sent: the list, or the one label
// an older caller sends. Trimmed, emptied of blanks, and deduplicated
// without regard to case, the first spelling kept.
func groupsOf(list []string, one string) []string {
	src := list
	if len(src) == 0 && strings.TrimSpace(one) != "" {
		src = []string{one}
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range src {
		g = strings.TrimSpace(g)
		if g == "" || seen[strings.ToLower(g)] {
			continue
		}
		seen[strings.ToLower(g)] = true
		out = append(out, g)
	}
	return out
}

func firstGroup(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	return groups[0]
}

// setGroups makes the customer's groups exactly these.
func setGroups(db *gorm.DB, clientID uint, groups []string) error {
	for _, g := range groups {
		if len(g) > 64 {
			return invalidField("groups", "group names are limited to 64 characters")
		}
	}
	if err := db.Where("client_id = ?", clientID).Delete(&model.ClientGroup{}).Error; err != nil {
		return fmt.Errorf("service: clear groups: %w", err)
	}
	for _, g := range groups {
		if err := db.Create(&model.ClientGroup{ClientID: clientID, Name: g}).Error; err != nil {
			return fmt.Errorf("service: set groups: %w", err)
		}
	}
	return syncGroupLabel(db, []uint{clientID})
}

// syncGroupLabel keeps the one-label column on each customer equal to the
// first of their groups, for anything that still reads it.
func syncGroupLabel(db *gorm.DB, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return db.Exec(`UPDATE clients SET "group" = COALESCE((SELECT MIN(name) FROM client_groups g WHERE g.client_id = clients.id), '') WHERE id IN ?`, ids).Error
}

// fillGroups reads each customer's groups, in one query for the lot.
func fillGroups(db *gorm.DB, items []model.Client) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
		items[i].Groups = []string{}
	}
	var rows []model.ClientGroup
	if err := db.Where("client_id IN ?", ids).Order("name").Find(&rows).Error; err != nil {
		return fmt.Errorf("service: read groups: %w", err)
	}
	by := map[uint][]string{}
	for _, r := range rows {
		by[r.ClientID] = append(by[r.ClientID], r.Name)
	}
	for i := range items {
		if g := by[items[i].ID]; g != nil {
			items[i].Groups = g
		}
	}
	return nil
}

// GroupSummary is one row of the groups page.
//
// A group is the customers in it, aggregated from their membership rows,
// plus a row of its own for a note and for existing before anyone is in
// it. A customer can be in several, so the sums of the groups add up to
// more than the customers when they overlap.
type GroupSummary struct {
	Name      string `json:"name"`
	Clients   int64  `json:"clients"`
	Devices   int64  `json:"devices"`
	Active    int64  `json:"active"`
	UpBytes   uint64 `json:"upBytes"`
	DownBytes uint64 `json:"downBytes"`
	UsedBytes uint64 `json:"usedBytes"`
	Note      string `json:"note"`
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
		SELECT g.name AS name,
		       COUNT(*) AS clients,
		       SUM(CASE WHEN c.status = ? THEN 1 ELSE 0 END) AS active,
		       COALESCE(SUM(c.up_bytes), 0)   AS up_bytes,
		       COALESCE(SUM(c.down_bytes), 0) AS down_bytes,
		       COALESCE(SUM(c.used_bytes), 0) AS used_bytes
		FROM client_groups g
		JOIN clients c ON c.id = g.client_id
		GROUP BY g.name
		ORDER BY g.name ASC`, model.StatusActive).
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
		SELECT g.name AS name, COUNT(*) AS devices
		FROM accounts a
		JOIN client_groups g ON g.client_id = a.client_id
		GROUP BY g.name`).
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

	// Groups that exist but hold nobody. The aggregate above can only see a
	// group through its members, so without this a freshly created group is
	// invisible and creating one looks like it failed.
	var declared []model.Group
	if err := db.Order("name").Find(&declared).Error; err != nil {
		return nil, fmt.Errorf("service: read groups: %w", err)
	}
	seen := make(map[string]bool, len(out.Items))
	for _, item := range out.Items {
		seen[item.Name] = true
	}
	for _, g := range declared {
		if seen[g.Name] {
			continue
		}
		out.Items = append(out.Items, GroupSummary{Name: g.Name, Note: g.Note})
		out.Totals.Groups++
	}
	sort.Slice(out.Items, func(i, j int) bool {
		// Populated groups first, then alphabetical: an operator scanning this
		// page is looking for the busy ones.
		if (out.Items[i].Clients > 0) != (out.Items[j].Clients > 0) {
			return out.Items[i].Clients > 0
		}
		return strings.ToLower(out.Items[i].Name) < strings.ToLower(out.Items[j].Name)
	})

	// Notes come from the group row, so a group with members shows one too.
	notes := make(map[string]string, len(declared))
	for _, g := range declared {
		notes[g.Name] = g.Note
	}
	for i := range out.Items {
		if out.Items[i].Note == "" {
			out.Items[i].Note = notes[out.Items[i].Name]
		}
	}

	if err := db.Raw(
		`SELECT COUNT(*) FROM clients WHERE NOT EXISTS (SELECT 1 FROM client_groups g WHERE g.client_id = clients.id)`).
		Scan(&out.Totals.Ungrouped).Error; err != nil {
		return nil, fmt.Errorf("service: count ungrouped: %w", err)
	}
	return out, nil
}

// ListGroupNames returns the distinct group labels, for the picker on the
// client form.
//
// A group exists two ways: as a label on a customer, and as a row made on
// the groups page before anyone is in it. The picker offers both, so a
// group made empty there is there to choose here.
func (s *Clients) ListGroupNames(ctx context.Context) ([]string, error) {
	var names []string
	err := s.db.WithContext(ctx).Raw(`
		SELECT g FROM (
			SELECT DISTINCT name AS g FROM client_groups
			UNION
			SELECT name AS g FROM groups
		) AS all_groups
		WHERE COALESCE(g, '') <> ''
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

	var moved int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A member of both keeps one row, the new name's; otherwise the
		// rename would give them the same row twice.
		if err := tx.Exec(`DELETE FROM client_groups WHERE name = ? AND client_id IN (SELECT client_id FROM client_groups WHERE name = ?)`, from, to).Error; err != nil {
			return fmt.Errorf("merge members: %w", err)
		}
		res := tx.Model(&model.ClientGroup{}).Where("name = ?", from).Update("name", to)
		if res.Error != nil {
			return fmt.Errorf("rename members: %w", res.Error)
		}
		moved = res.RowsAffected
		if err := tx.Exec(`UPDATE clients SET "group" = ? WHERE "group" = ?`, to, from).Error; err != nil {
			return fmt.Errorf("rename label: %w", err)
		}

		// The row moves too. Leaving it behind would keep the old name alive as
		// an empty group and lose the new one's note.
		if err := tx.Model(&model.Group{}).Where("name = ?", from).
			Update("name", to).Error; err != nil {
			return fmt.Errorf("rename group: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("service: %w", err)
	}
	s.log.Info("group renamed", "from", from, "to", to, "clients", moved)
	return moved, nil
}

// AssignGroup adds the given clients to a group, or with remove takes them
// out of it; their other groups are untouched. An empty name takes them
// out of every group, which is what an older caller meant by it.
func (s *Clients) AssignGroup(ctx context.Context, name string, ids []uint, remove bool) (int64, error) {
	name = strings.TrimSpace(name)
	if len(name) > 64 {
		return 0, fmt.Errorf("%w: group names are limited to 64 characters", ErrInvalid)
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: no clients selected", ErrInvalid)
	}
	var n int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		switch {
		case name == "":
			res := tx.Where("client_id IN ?", ids).Delete(&model.ClientGroup{})
			n = res.RowsAffected
			if res.Error != nil {
				return res.Error
			}
		case remove:
			res := tx.Where("client_id IN ? AND name = ?", ids, name).Delete(&model.ClientGroup{})
			n = res.RowsAffected
			if res.Error != nil {
				return res.Error
			}
		default:
			for _, id := range ids {
				res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ClientGroup{ClientID: id, Name: name})
				if res.Error != nil {
					return res.Error
				}
				n += res.RowsAffected
			}
		}
		return syncGroupLabel(tx, ids)
	})
	if err != nil {
		return 0, fmt.Errorf("service: assign group: %w", err)
	}
	return n, nil
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
	scope := func() *gorm.DB {
		return db.Model(&model.Client{}).Where("id IN (SELECT client_id FROM client_groups WHERE name = ?)", group)
	}
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
		var ids []uint
		if err := scope().Pluck("id", &ids).Error; err != nil {
			return 0, fmt.Errorf("service: list group members: %w", err)
		}
		if len(ids) == 0 {
			return 0, nil
		}
		return s.AssignGroup(ctx, group, ids, true)

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

// CreateGroup makes an empty group.
//
// An empty group is the point: an operator sets one up before the customers
// exist, then assigns them. Without a row of its own a group could only be
// brought into being by typing its name onto a customer, which is not a thing
// anybody would guess.
func (s *Clients) CreateGroup(ctx context.Context, name, note string) (*model.Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalidField("name", "a group needs a name")
	}
	if len(name) > 64 {
		return nil, invalidField("name", "that name is too long")
	}

	// Checked case-insensitively. Two groups differing only in capitalisation
	// are two groups nobody can tell apart in a list.
	var clash int64
	if err := s.db.WithContext(ctx).Model(&model.Group{}).
		Where("LOWER(name) = LOWER(?)", name).Count(&clash).Error; err != nil {
		return nil, fmt.Errorf("service: check group name: %w", err)
	}
	if clash > 0 {
		return nil, invalidField("name", "a group called %q already exists", name)
	}

	g := model.Group{Name: name, Note: strings.TrimSpace(note)}
	if err := s.db.WithContext(ctx).Create(&g).Error; err != nil {
		return nil, fmt.Errorf("service: create group: %w", err)
	}
	s.log.Info("group created", "group", name)
	return &g, nil
}

// DeleteGroup removes a group. Its members are ungrouped, not deleted.
//
// The two are separated deliberately: deleting a group is tidying, deleting the
// customers in it is losing money, and a single button that did both would
// eventually do the second when someone meant the first.
func (s *Clients) DeleteGroup(ctx context.Context, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("%w: which group?", ErrInvalid)
	}

	var ungrouped int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&model.ClientGroup{}).Where("name = ?", name).Pluck("client_id", &ids).Error; err != nil {
			return fmt.Errorf("list members: %w", err)
		}
		res := tx.Where("name = ?", name).Delete(&model.ClientGroup{})
		if res.Error != nil {
			return fmt.Errorf("ungroup members: %w", res.Error)
		}
		ungrouped = res.RowsAffected
		if len(ids) > 0 {
			if err := syncGroupLabel(tx, ids); err != nil {
				return err
			}
		}

		if err := tx.Where("name = ?", name).Delete(&model.Group{}).Error; err != nil {
			return fmt.Errorf("delete group: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("service: %w", err)
	}
	s.log.Info("group deleted", "group", name, "ungrouped", ungrouped)
	return ungrouped, nil
}
