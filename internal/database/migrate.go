package database

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The schema follows the models: every start, the tables gain what the
// models have and they do not (AutoMigrate), and nothing is ever taken away
// (additive_test.go). What that cannot do -- an index to drop before the
// tables are brought up to date, data to carry from an old shape into a new
// one -- is a migration: numbered, run once, in order, and recorded in
// schema_migrations, so a database says which of them it has been through.
//
// A migration is written so that running it again changes nothing. A
// restore from a portable backup loads rows from an older panel after the
// migrations have run, and those rows are then put through the data ones
// again (Replay).

// schemaMigration is one migration a database has been through.
type schemaMigration struct {
	ID        int       `gorm:"primaryKey;autoIncrement:false"`
	Name      string    `gorm:"size:200;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

func (schemaMigration) TableName() string { return "schema_migrations" }

// migration is one step. before runs ahead of bringing the tables up to
// date; the rest after.
type migration struct {
	id     int
	name   string
	before bool
	run    func(tx *gorm.DB) error
}

// migrations are every step, in the order they run. A new one goes at the
// end with the next number; a number is never reused or renumbered.
var migrations = []migration{
	{1, "group names are unique per operator, not across the panel", true, dropPanelWideGroupIndex},
	{2, "whoever administered the panel before roles owns it", false, existingAdminsOwn},
	{3, "a customer's group label becomes their first group", false, carryGroupLabels},
	{4, "each file's usage is counted by the panel from now on", false, startExactFileUsage},
	{5, "files already on a 3x-ui or Pasarguard node were placed there by this panel", false, markPlacedOnXrayNodes},
	{6, "the sharing record says whose customer each entry was", false, ownSharingRecord},
	{7, "an operator's user ceiling becomes seats held at once, kept as running figures", false, openUserCapacity},
}

// Migrate brings the schema up to date.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		return fmt.Errorf("database: record of migrations: %w", err)
	}
	var done []int
	if err := db.Model(&schemaMigration{}).Pluck("id", &done).Error; err != nil {
		return fmt.Errorf("database: read the migrations done: %w", err)
	}
	applied := map[int]bool{}
	for _, id := range done {
		applied[id] = true
	}
	if err := runMigrations(db, applied, true); err != nil {
		return err
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("database: migrate: %w", err)
	}
	return runMigrations(db, applied, false)
}

func runMigrations(db *gorm.DB, applied map[int]bool, before bool) error {
	for _, m := range migrations {
		if m.before != before || applied[m.id] {
			continue
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.run(tx); err != nil {
				return err
			}
			return tx.Create(&schemaMigration{ID: m.id, Name: m.name, AppliedAt: time.Now().UTC()}).Error
		})
		if err != nil {
			return fmt.Errorf("database: migration %d (%s): %w", m.id, m.name, err)
		}
		applied[m.id] = true
	}
	return nil
}

// Replay runs the data migrations again, for rows that came in after they
// ran: a portable backup from an older panel. Each is written to change
// nothing the second time, so this only carries what the backup brought.
func Replay(db *gorm.DB) error {
	for _, m := range migrations {
		if m.before {
			continue
		}
		if err := db.Transaction(m.run); err != nil {
			return fmt.Errorf("database: migration %d (%s) on the restored rows: %w", m.id, m.name, err)
		}
	}
	return nil
}

// dropPanelWideGroupIndex: a group's name used to be unique across the
// panel; it is now unique per operator, so two resellers can each have a
// "trial". AutoMigrate adds the new index but will not drop the old one,
// and leaving it would refuse the second reseller their own name. Dropped by
// every name the two supported drivers give it, IF EXISTS on all of them.
func dropPanelWideGroupIndex(tx *gorm.DB) error {
	for _, idx := range []string{"idx_groups_name", "uni_groups_name", "groups_name_key"} {
		if err := tx.Exec(`DROP INDEX IF EXISTS ` + idx).Error; err != nil {
			return fmt.Errorf("drop old group name index: %w", err)
		}
	}
	return nil
}

// existingAdminsOwn: whoever was already administering this panel owns it.
// The column defaults to owner, which covers the single account every
// existing install has; this names it explicitly for any row that predates
// the default and would otherwise come up with an empty role.
func existingAdminsOwn(tx *gorm.DB) error {
	if err := tx.Exec(`UPDATE admins SET role = 'owner' WHERE COALESCE(role, '') = ''`).Error; err != nil {
		return fmt.Errorf("set administrator roles: %w", err)
	}
	return nil
}

// carryGroupLabels: a customer used to carry one group as a column; they now
// carry any number as rows. The column's label becomes the first row for
// every customer who has the label and no rows yet.
func carryGroupLabels(tx *gorm.DB) error {
	if err := tx.Exec(`
		INSERT INTO client_groups (client_id, name)
		SELECT id, "group" FROM clients
		WHERE COALESCE("group", '') <> ''
		  AND NOT EXISTS (SELECT 1 FROM client_groups g WHERE g.client_id = clients.id)`).Error; err != nil {
		return fmt.Errorf("carry groups over: %w", err)
	}
	return nil
}

// markPlacedOnXrayNodes: a file on a 3x-ui or Pasarguard node from before
// placement was recorded was placed there by this panel, or is about to be;
// none can be another's under its number, which only a restore hands out
// again. Marked so the first round after the update takes their clients for
// theirs, as it always did, instead of making every one of them again. Run
// again on rows a restore brings from an older panel, it marks those the
// same way: they were that panel's.
func markPlacedOnXrayNodes(tx *gorm.DB) error {
	if err := tx.Exec(`
		UPDATE accounts SET xui_placed = TRUE
		WHERE xui_placed = FALSE AND COALESCE(adopted, '') = ''
		  AND node_id IN (SELECT id FROM nodes WHERE kind IN (?, ?))`,
		model.KindXUI, model.KindPG).Error; err != nil {
		return fmt.Errorf("mark files placed: %w", err)
	}
	return nil
}

// ownSharingRecord: the sharing record now keeps whose customer each entry
// was, so a reseller or an admin can be shown their own. An entry whose
// customer still exists takes that customer's owner; one whose customer is
// already gone has nobody left to ask, and stays the owner's alone -- zero,
// which no operator but the owner is shown. Only entries still at zero are
// touched, so running it again changes nothing.
func ownSharingRecord(tx *gorm.DB) error {
	if err := tx.Exec(`
		UPDATE sharing_events
		SET owner_id = (SELECT clients.owner_id FROM clients WHERE clients.id = sharing_events.client_id)
		WHERE owner_id = 0 AND client_id <> 0
		  AND EXISTS (SELECT 1 FROM clients WHERE clients.id = sharing_events.client_id)`).Error; err != nil {
		return fmt.Errorf("own the sharing record: %w", err)
	}
	return nil
}

// openUserCapacity: an operator's user ceiling (client_limit) becomes an
// account of seats held at once -- capacity's "open" mode, the nearest to
// what the ceiling did -- with its figures written once and their ledger
// beside them, so every later change is a move of a figure rather than a
// count. Seats are held by the customers still being served: one whose plan
// ended (expired, or out of traffic) gives its seats back, which the old
// count did not. An admin's handed figure is what their resellers were
// given. Only operators without an account yet are touched, so a second run
// changes nothing.
func openUserCapacity(tx *gorm.DB) error {
	type op struct {
		ID            uint
		Role          string
		ClientLimit   int64
		ParentAdminID uint
	}
	var ops []op
	if err := tx.Raw(`SELECT id, role, client_limit, parent_admin_id FROM admins
		WHERE role IN ('admin', 'reseller') AND client_limit > 0
		  AND id NOT IN (SELECT admin_id FROM capacity_accounts WHERE kind = 'users')`).Scan(&ops).Error; err != nil {
		return fmt.Errorf("read operators' ceilings: %w", err)
	}
	limit := map[uint]int64{}
	for _, o := range ops {
		limit[o.ID] = o.ClientLimit
	}
	now := time.Now().UTC()
	for _, o := range ops {
		var held int64
		if err := tx.Raw(`SELECT COALESCE(SUM(device_limit), 0) FROM clients
			WHERE owner_id = ? AND status NOT IN ('expired', 'exhausted')`, o.ID).Scan(&held).Error; err != nil {
			return fmt.Errorf("count operator %d's seats: %w", o.ID, err)
		}
		var handed int64
		if o.Role == "admin" {
			for _, c := range ops {
				if c.ParentAdminID == o.ID {
					handed += c.ClientLimit
				}
			}
		}
		acc := model.CapacityAccount{AdminID: o.ID, Kind: model.CapacityUsers, Mode: model.CapacityOpen,
			Granted: o.ClientLimit, Used: held, Handed: handed, UpdatedAt: now}
		if err := tx.Create(&acc).Error; err != nil {
			return fmt.Errorf("open operator %d's account: %w", o.ID, err)
		}
		for field, delta := range map[string]int64{"granted": acc.Granted, "used": acc.Used, "handed": acc.Handed} {
			if delta == 0 {
				continue
			}
			e := model.CapacityEntry{AccountID: acc.ID, AdminID: o.ID, Kind: model.CapacityUsers, Field: field,
				Delta: delta, Reason: model.CapacityMigrate, At: now}
			if err := tx.Create(&e).Error; err != nil {
				return fmt.Errorf("write operator %d's ledger: %w", o.ID, err)
			}
		}
	}
	return nil
}
