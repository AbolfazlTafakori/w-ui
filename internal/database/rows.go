package database

import "gorm.io/gorm"

// CreateKeepingSwitch inserts a row whose "enabled" column the schema
// defaults to true, and makes it say what was asked.
//
// An insert leaves a false out as a zero value and the column takes its
// default, so anything created switched off -- a reseller, a server, a node --
// came up switched on. The two statements run in one transaction, so nothing
// reading the table can see it on in between.
func CreateKeepingSwitch(db *gorm.DB, row any, enabled bool) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if enabled {
			return nil
		}
		return tx.Model(row).Update("enabled", false).Error
	})
}
