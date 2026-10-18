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
