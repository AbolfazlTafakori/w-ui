package egress

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/listfield"
)

// followRename moves everything that points at an outbound or a balancer by
// its tag from the old tag to the new one, inside the rename's transaction:
// the rules that send traffic to it, the balancers that have it as a member,
// a fallback or a pinned choice, and the split tunnel's exit. Whole names
// only -- "socks-ab" is not "socks-a". (The default exit is read from the
// outbounds' order, not from its stored copy, which is kept in step here
// all the same.)
//
// One place for it, used by both renames: before, an outbound renamed took
// its rules alone, and left balancers and the split tunnel's exit pointing
// at a name nothing had -- a balancer that then refused every change.
func followRename(tx *gorm.DB, oldTag, newTag string) error {
	if oldTag == newTag {
		return nil
	}
	if err := tx.Model(&model.RoutingRule{}).Where("outbound_tag = ?", oldTag).
		Update("outbound_tag", newTag).Error; err != nil {
		return fmt.Errorf("move the rules to %q: %w", newTag, err)
	}

	var bals []model.Balancer
	if err := tx.Find(&bals).Error; err != nil {
		return fmt.Errorf("read the balancers: %w", err)
	}
	for _, b := range bals {
		updates := map[string]any{}
		members := listfield.Split(b.Members)
		moved := false
		for i, m := range members {
			if m == oldTag {
				members[i], moved = newTag, true
			}
		}
		if moved {
			updates["members"] = listfield.Join(members)
		}
		if b.Fallback == oldTag {
			updates["fallback"] = newTag
		}
		if b.Override == oldTag {
			updates["override"] = newTag
		}
		if len(updates) == 0 {
			continue
		}
		if err := tx.Model(&model.Balancer{}).Where("id = ?", b.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("move balancer %q to %q: %w", b.Tag, newTag, err)
		}
	}

	if err := tx.Model(&model.Setting{}).
		Where("key IN ? AND value = ?", []string{keyDefaultOutbound, keySplitOutbound}, oldTag).
		Update("value", newTag).Error; err != nil {
		return fmt.Errorf("move the routing exits to %q: %w", newTag, err)
	}
	return nil
}
