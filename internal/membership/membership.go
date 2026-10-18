// Package membership is who a customer belongs to: the reseller who owns
// them, and the groups they are filed in, with the one-label column kept
// equal to the first of those groups.
//
// The customers' service and the operators' both change it -- a customer
// joins a group; a reseller is relabelled or deleted and their customers go
// with them -- and each does so inside a transaction of its own that covers
// more than this. So every function here takes the caller's transaction
// rather than opening one: a reseller deleted halfway must not leave their
// customers half handed over.
package membership

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/scope"
)

// SyncLabel keeps the one-label column on each customer equal to the first
// of their groups, for anything that still reads it.
//
// Vouched for (scope.Vouch): every caller's ids are already the operator's
// -- a customer read through the narrowed Get or Create, members read
// through callerMembers, or the owner acting on a reseller.
func SyncLabel(db *gorm.DB, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(scope.Vouch(ctx)).Exec(`UPDATE clients SET "group" = COALESCE((SELECT MIN(name) FROM client_groups g WHERE g.client_id = clients.id), '') WHERE id IN ?`, ids).Error
}

// Refile moves one reseller's customers from one label to another.
func Refile(tx *gorm.DB, ownerID uint, from, to string) error {
	var ids []uint
	if err := tx.Model(&model.Client{}).Where("owner_id = ?", ownerID).Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("list the reseller's customers: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	// A customer already in the new group keeps that one row rather than
	// being given a second: the pair is the key.
	if err := tx.Where("name = ? AND client_id IN ? AND client_id IN (SELECT client_id FROM client_groups WHERE name = ?)",
		to, ids, from).Delete(&model.ClientGroup{}).Error; err != nil {
		return fmt.Errorf("refile the reseller's customers: %w", err)
	}
	if err := tx.Model(&model.ClientGroup{}).
		Where("name = ? AND client_id IN ?", from, ids).
		Update("name", to).Error; err != nil {
		return fmt.Errorf("refile the reseller's customers: %w", err)
	}
	return SyncLabel(tx, ids)
}

// HandOver gives a departing operator's customers to the owner: the owner
// administers them from now on, the operator's own groups go, and their
// customers leave the operator's label -- theirs only: the owner may have
// filed customers of their own under the same name, and those are not the
// departing operator's to take. label is the operator's label, or "" when
// they had none.
func HandOver(tx *gorm.DB, ownerID uint, label string) error {
	// Read before they are handed over, when they can still be told apart
	// from the owner's own.
	var kept []uint
	if err := tx.Model(&model.Client{}).Where("owner_id = ?", ownerID).
		Pluck("id", &kept).Error; err != nil {
		return fmt.Errorf("list the operator's customers: %w", err)
	}
	if err := tx.Model(&model.Client{}).Where("owner_id = ?", ownerID).
		Update("owner_id", 0).Error; err != nil {
		return fmt.Errorf("hand over the operator's customers: %w", err)
	}
	if err := tx.Where("owner_id = ?", ownerID).Delete(&model.Group{}).Error; err != nil {
		return fmt.Errorf("delete the operator's groups: %w", err)
	}
	if label == "" || len(kept) == 0 {
		return nil
	}
	if err := tx.Where("name = ? AND client_id IN ?", label, kept).
		Delete(&model.ClientGroup{}).Error; err != nil {
		return fmt.Errorf("clear the operator's own group: %w", err)
	}
	if err := SyncLabel(tx, kept); err != nil {
		return fmt.Errorf("clear the operator's own group: %w", err)
	}
	return nil
}
