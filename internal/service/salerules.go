package service

import (
	"crypto/sha256"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The rules of a sale, held at the same door as the users a change costs
// (SettleChange), so no path that makes or extends a customer escapes them.
//
// An operator may be held to the longest term and the most traffic one
// change gives a customer (model.Admin.SaleMaxDays, SaleMaxBytes). With a
// rule set, every customer they make or extend must have an end or a limit,
// and one the rule covers: a plan with no end on one unit, run by hand as
// many customers, is what it closes. A reseller under an admin is held to
// both their own rules and the admin's, whichever is tighter.
//
// Giving a customer new credentials -- their link, one user's link, their
// keys, their passwords -- hands the plan to whoever gets them. On
// consumable users that is a new sale, and it spends the users whose
// credentials changed (rekeyUsers), unless the operator, and the admin above
// a reseller, were both let off it (model.Admin.RekeyFree). A customer whose
// plan has ended changes hands for free: the extension that serves them
// again is what pays. Only what an operator asks for is held to either; the
// owner and the panel are not.

// saleSlack is how far past the rule a term may end: a plan sold to the
// end of a day chosen on a calendar is that many days, not one more.
const saleSlack = 24 * time.Hour

// credSet is what a customer signs in with, for telling a change of it.
type credSet struct {
	sub   string
	files map[uint]fileCred // by account
	links map[string]string // a user's own link, by user
}

type fileCred struct {
	user string
	sum  [32]byte
}

// loadCreds reads the credentials of the customers ids.
func loadCreds(tx *gorm.DB, ids []uint) (map[uint]*credSet, error) {
	out := make(map[uint]*credSet, len(ids))
	for start := 0; start < len(ids); start += 900 {
		part := ids[start:min(start+900, len(ids))]
		var subs []struct {
			ID       uint
			SubToken string
		}
		if err := tx.Model(&model.Client{}).Select("id, sub_token").Where("id IN ?", part).Scan(&subs).Error; err != nil {
			return nil, fmt.Errorf("service: read customers' links: %w", err)
		}
		for _, r := range subs {
			out[r.ID] = &credSet{sub: r.SubToken, files: map[uint]fileCred{}, links: map[string]string{}}
		}
		var files []model.Account
		if err := tx.Select("id, client_id, device_name, public_key, preshared_key, secret, username, stealth_secret").
			Where("client_id IN ?", part).Find(&files).Error; err != nil {
			return nil, fmt.Errorf("service: read customers' credentials: %w", err)
		}
		for _, f := range files {
			if c := out[f.ClientID]; c != nil {
				c.files[f.ID] = fileCred{user: userKey(f.DeviceName),
					sum: sha256.Sum256([]byte(f.PublicKey + "\x00" + f.PresharedKey + "\x00" + f.Secret + "\x00" + f.Username + "\x00" + f.StealthSecret))}
			}
		}
		var links []model.UserSub
		if err := tx.Select("client_id, device, token").Where("client_id IN ?", part).Find(&links).Error; err != nil {
			return nil, fmt.Errorf("service: read users' links: %w", err)
		}
		for _, l := range links {
			if c := out[l.ClientID]; c != nil {
				c.links[userKey(l.Device)] = l.Token
			}
		}
	}
	return out, nil
}

// rekeyUsers is how many of a customer's users were given new credentials
// between b and a: all of them for a new link of the customer's own, else
// each user one of whose files or whose own link changed. Files and links
// made or removed are not counted -- a user added is paid for as one.
func rekeyUsers(b, a *credSet, users int64) int64 {
	if b == nil || a == nil || users <= 0 {
		return 0
	}
	if b.sub != "" && a.sub != "" && b.sub != a.sub {
		return users
	}
	changed := map[string]bool{}
	for id, was := range b.files {
		if now, ok := a.files[id]; ok && now.sum != was.sum {
			changed[now.user] = true
		}
	}
	for user, was := range b.links {
		if now, ok := a.links[user]; ok && was != "" && now != was {
			changed[user] = true
		}
	}
	return min(int64(len(changed)), users)
}

// saleRule is the rules an operator's customers are sold under: their own
// and their admin's, the tighter of each.
type saleRule struct {
	days      int
	bytes     uint64
	rekeyFree bool
}

// saleRules reads the rules of the operators owners, each with the admin
// above them.
func saleRules(tx *gorm.DB, owners map[uint]bool) (map[uint]saleRule, error) {
	out := map[uint]saleRule{}
	if len(owners) == 0 {
		return out, nil
	}
	ids := make([]uint, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	type row struct {
		ID            uint
		ParentAdminID uint
		SaleMaxDays   int
		SaleMaxBytes  uint64
		RekeyFree     bool
	}
	cols := "id, parent_admin_id, sale_max_days, sale_max_bytes, rekey_free"
	var rows []row
	if err := tx.Model(&model.Admin{}).Select(cols).Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: read operators' sale rules: %w", err)
	}
	parents := map[uint]row{}
	var want []uint
	for _, r := range rows {
		if r.ParentAdminID != 0 {
			want = append(want, r.ParentAdminID)
		}
	}
	if len(want) > 0 {
		var ps []row
		if err := tx.Model(&model.Admin{}).Select(cols).Where("id IN ?", want).Scan(&ps).Error; err != nil {
			return nil, fmt.Errorf("service: read admins' sale rules: %w", err)
		}
		for _, p := range ps {
			parents[p.ID] = p
		}
	}
	tighter := func(a, b int) int {
		if a == 0 || (b != 0 && b < a) {
			return b
		}
		return a
	}
	for _, r := range rows {
		rule := saleRule{days: r.SaleMaxDays, bytes: r.SaleMaxBytes, rekeyFree: r.RekeyFree}
		if p, ok := parents[r.ParentAdminID]; ok {
			rule.days = tighter(rule.days, p.SaleMaxDays)
			if rule.bytes == 0 || (p.SaleMaxBytes != 0 && p.SaleMaxBytes < rule.bytes) {
				rule.bytes = p.SaleMaxBytes
			}
			rule.rekeyFree = rule.rekeyFree && p.RekeyFree
		}
		out[r.ID] = rule
	}
	return out, nil
}

// checkSales refuses a change that gives a customer more than their
// operator's rules allow: a plan made or extended past the longest term or
// the most traffic one sale may give, or with no end or no limit at all
// where a rule asks for one.
func checkSales(rules map[uint]saleRule, before, after map[uint]seatState, ids []uint, now time.Time) error {
	for _, id := range ids {
		a, has := after[id]
		if !has || a.owner == 0 {
			continue
		}
		b, had := before[id]
		if had && b.owner != a.owner {
			had = false // come to a new owner: a sale of theirs from nothing
		}
		if had && !extended(b, a) {
			continue
		}
		rule, ok := rules[a.owner]
		if !ok {
			continue
		}
		if err := checkTerm(rule.days, b, a, had, now); err != nil {
			return err
		}
		if err := checkTraffic(rule.bytes, b, a, had); err != nil {
			return err
		}
	}
	return nil
}

func checkTerm(days int, b, a seatState, had bool, now time.Time) error {
	if days <= 0 {
		return nil
	}
	if a.expires == nil && a.duration == 0 {
		return invalidField("expiresAt", "each sale of yours is held to %d days, so a customer needs an end date", days)
	}
	var added time.Duration
	if a.expires != nil {
		base := now
		if had && b.expires != nil && b.expires.After(now) {
			base = *b.expires
		}
		added = a.expires.Sub(base)
	} else {
		prev := 0
		if had && b.expires == nil {
			prev = b.duration
		}
		added = time.Duration(a.duration-prev) * time.Hour
	}
	if added > time.Duration(days)*24*time.Hour+saleSlack {
		return invalidField("expiresAt", "each sale of yours is held to %d days; this gives %d", days, int((added+time.Hour-1)/(24*time.Hour)))
	}
	return nil
}

func checkTraffic(limit uint64, b, a seatState, had bool) error {
	if limit == 0 {
		return nil
	}
	if a.quota == 0 {
		return invalidField("quotaBytes", "each sale of yours is held to %s of traffic, so a customer needs a traffic limit", gbWord(limit))
	}
	added := int64(a.quota)
	if had {
		added -= int64(b.quota)
		if b.used > 0 && a.used == 0 {
			added += int64(b.used)
		}
	}
	if added > int64(limit) {
		return invalidField("quotaBytes", "each sale of yours is held to %s of traffic; this gives %s", gbWord(limit), gbWord(uint64(added)))
	}
	return nil
}

// gbWord is bytes as a sentence says them, in gigabytes.
func gbWord(n uint64) string {
	gb := float64(n) / (1 << 30)
	if gb == float64(int64(gb)) {
		return fmt.Sprintf("%d GB", int64(gb))
	}
	return fmt.Sprintf("%.1f GB", gb)
}
