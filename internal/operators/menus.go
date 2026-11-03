package operators

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

// An admin's menu grants: which of the grantable pages the owner has let
// them reach. Held in AdminMenuPermission rows, read as a set, and refused
// in the backend for any page not among them (internal/api gating). Only an
// admin has any; the owner reaches everything and a reseller's menu is fixed.

// cleanMenus validates a grant list and drops duplicates, keeping the
// canonical order. An unknown page is refused rather than silently ignored,
// so a typo in the form is a visible error, not a page quietly left off.
func cleanMenus(in []string) ([]string, error) {
	seen := map[string]bool{}
	for _, m := range in {
		if !model.ValidMenu(m) {
			return nil, fault.Field("menuIds", "%q is not a page that can be granted", m)
		}
		seen[m] = true
	}
	out := make([]string, 0, len(seen))
	for _, m := range model.GrantableMenus() {
		if seen[m] {
			out = append(out, m)
		}
	}
	return out, nil
}

// setMenus replaces an admin's grants with exactly the given set, inside the
// caller's transaction.
func setMenus(tx *gorm.DB, adminID uint, menus []string) error {
	if err := tx.Where("admin_id = ?", adminID).Delete(&model.AdminMenuPermission{}).Error; err != nil {
		return fmt.Errorf("clear the admin's pages: %w", err)
	}
	if len(menus) == 0 {
		return nil
	}
	rows := make([]model.AdminMenuPermission, 0, len(menus))
	for _, m := range menus {
		rows = append(rows, model.AdminMenuPermission{AdminID: adminID, Menu: m})
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("record the admin's pages: %w", err)
	}
	return nil
}

// HasMenu reports whether an admin has been granted a page. Used by the
// backend gate so a page the owner left unchecked is refused, not only
// hidden.
func (s *Admins) HasMenu(ctx context.Context, adminID uint, menu string) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&model.AdminMenuPermission{}).
		Where("admin_id = ? AND menu = ?", adminID, menu).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("service: check the admin's page: %w", err)
	}
	return n > 0, nil
}

// MenuIDs is the pages an admin may reach, in menu order. Empty for anyone
// but an admin.
func (s *Admins) MenuIDs(ctx context.Context, adminID uint) ([]string, error) {
	var have []string
	if err := s.db.WithContext(ctx).Model(&model.AdminMenuPermission{}).
		Where("admin_id = ?", adminID).Pluck("menu", &have).Error; err != nil {
		return nil, fmt.Errorf("service: load the admin's pages: %w", err)
	}
	set := map[string]bool{}
	for _, m := range have {
		set[m] = true
	}
	out := make([]string, 0, len(have))
	for _, m := range model.GrantableMenus() {
		if set[m] {
			out = append(out, m)
		}
	}
	return out, nil
}
