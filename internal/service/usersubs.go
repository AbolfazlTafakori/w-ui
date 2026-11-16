package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Each user's own link.
//
// A plan for several is one customer whose files are numbered by user: user
// 2 is the file named for them on every tunnel the plan reaches. The
// customer's link carries them all, which is right for the person who
// bought the plan and wrong for the people they share it with -- each of
// them could take another's file, and none of them could be cut off alone.
//
// So each user has a link of their own beside it, carrying their files and
// nobody else's: in a browser their own page, in an app their own
// configurations. The customer's link is the plan's, and is exactly what it
// was. A user's link is tied to the user's file name and not to a position:
// a user removed takes their link with them, and the users after them keep
// theirs rather than inheriting a neighbour's.

// UserLink is one user's link, as the operator's table shows it.
type UserLink struct {
	// User is the user's number in the plan, as the customer's page counts
	// them; Device is the file name that number stands for.
	User   int    `json:"user"`
	Device string `json:"device"`
	Token  string `json:"token"`
	Link   string `json:"link"`
	// Tunnels are the names of the tunnels this user has a file on.
	Tunnels []string `json:"tunnels"`
	// UpBytes and DownBytes are what this user's files carried.
	UpBytes   uint64 `json:"upBytes"`
	DownBytes uint64 `json:"downBytes"`
	// LastOnline is the freshest handshake on any of this user's files.
	LastOnline *time.Time `json:"lastOnline"`
}

// userKey is how a user is told apart inside one customer: their file name,
// the way the panel compares those.
func userKey(device string) string { return strings.ToLower(strings.TrimSpace(device)) }

// userNumbers is each file's user number: the place of its name among the
// customer's file names, in the order the files were issued. The first name
// is user 1 whatever it is called, so the file a customer had as a plan of
// one stays user 1 when the plan grows.
func userNumbers(accounts []model.Account) map[uint]int {
	names := deviceNames(sortedByID(accounts))
	place := make(map[string]int, len(names))
	for i, n := range names {
		place[userKey(n)] = i + 1
	}
	out := make(map[uint]int, len(accounts))
	for _, a := range accounts {
		out[a.ID] = place[userKey(a.DeviceName)]
	}
	return out
}

// userFiles keeps the files of one user.
func userFiles(accounts []model.Account, device string) []model.Account {
	key := userKey(device)
	var out []model.Account
	for _, a := range accounts {
		if userKey(a.DeviceName) == key {
			out = append(out, a)
		}
	}
	return out
}

// resolve finds what a link names: a customer and all their files for the
// customer's own link, or a customer narrowed to one user's files for a
// user's link, with that user's number. user is 0 for the customer's link.
//
// The customer's link is looked up first and exactly as before, so nothing a
// customer was handed answers any differently.
func (s *Subscriptions) resolve(ctx context.Context, token string) (c *model.Client, user int, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, 0, fmt.Errorf("%w: no subscription token", ErrNotFound)
	}
	var whole model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").
		Where("sub_token = ?", token).Limit(1).Find(&whole).Error; err != nil {
		return nil, 0, fmt.Errorf("service: read subscription: %w", err)
	}
	if whole.ID != 0 {
		return &whole, 0, nil
	}

	var us model.UserSub
	if err := s.db.WithContext(ctx).Where("token = ?", token).Limit(1).Find(&us).Error; err != nil {
		return nil, 0, fmt.Errorf("service: read subscription: %w", err)
	}
	if us.ID == 0 {
		// Deliberately the same answer as a token that never existed.
		return nil, 0, fmt.Errorf("%w: no such subscription", ErrNotFound)
	}
	var owner model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").
		Where("id = ?", us.ClientID).Limit(1).Find(&owner).Error; err != nil {
		return nil, 0, fmt.Errorf("service: read subscription: %w", err)
	}
	if owner.ID == 0 {
		return nil, 0, fmt.Errorf("%w: no such subscription", ErrNotFound)
	}
	nums := userNumbers(owner.Accounts)
	owner.Accounts = userFiles(owner.Accounts, us.Device)
	if !linkHolds(us, owner.Accounts) {
		// The user was taken off the plan, or a new user was given the
		// name since: either way this link is not theirs. A link that does
		// not exist, until its row is gone.
		return nil, 0, fmt.Errorf("%w: no such subscription", ErrNotFound)
	}
	return &owner, nums[owner.Accounts[0].ID], nil
}

// linkHolds reports whether a user's link still belongs to the files that
// carry its name: one of them must be older than the link. A link is only
// ever issued to a user who has files, so a name whose every file is newer
// than the link was taken off the plan and given to someone else -- by a
// path that did not drop the link on the way -- and the link must not hand
// the newcomer's files to whoever held the old one.
func linkHolds(us model.UserSub, files []model.Account) bool {
	for _, a := range files {
		if !a.CreatedAt.After(us.CreatedAt) {
			return true
		}
	}
	return false
}

// UserLinks is every user's own link for one customer, issuing the ones
// not issued yet: a user added to the plan has a link the first time the
// operator looks, without anyone having to ask for it.
//
// The customer is read under the caller's scope, so a reseller reaches
// only their own customers' users; a customer they cannot see is not found.
func (s *Subscriptions) UserLinks(ctx context.Context, clientID uint, requestHost string) ([]UserLink, error) {
	var c model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").
		Where("id = ?", clientID).Limit(1).Find(&c).Error; err != nil {
		return nil, fmt.Errorf("service: read client: %w", err)
	}
	if c.ID == 0 {
		return nil, fmt.Errorf("%w: no client %d", ErrNotFound, clientID)
	}
	names := deviceNames(sortedByID(c.Accounts))
	tokens, err := s.ensureUserTokens(ctx, c.ID, c.Accounts)
	if err != nil {
		return nil, err
	}
	cfg, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}

	var ifaces []model.Interface
	if err := s.db.WithContext(ctx).Select("id", "name").Find(&ifaces).Error; err != nil {
		return nil, fmt.Errorf("service: read interfaces: %w", err)
	}
	tunnel := make(map[uint]string, len(ifaces))
	for _, f := range ifaces {
		tunnel[f.ID] = f.Name
	}

	out := make([]UserLink, 0, len(names))
	for i, name := range names {
		u := UserLink{User: i + 1, Device: name, Token: tokens[userKey(name)], Tunnels: []string{}}
		u.Link = linkWith(cfg, u.Token, requestHost)
		seen := map[uint]bool{}
		for _, a := range sortedByID(userFiles(c.Accounts, name)) {
			u.UpBytes += a.UpBytes
			u.DownBytes += a.DownBytes
			if hs := a.LastHandshake; hs != nil && (u.LastOnline == nil || hs.After(*u.LastOnline)) {
				t := *hs
				u.LastOnline = &t
			}
			if !seen[a.InterfaceID] {
				seen[a.InterfaceID] = true
				if n := tunnel[a.InterfaceID]; n != "" {
					u.Tunnels = append(u.Tunnels, n)
				}
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// ensureUserTokens gives every user of the customer a token, keeping the
// ones they have, and returns them by userKey.
//
// Read first and written only when something is missing, so a page opened
// a thousand times a minute costs one indexed read each, not a write. Two
// first openings at once both try to issue a user's link; the second insert
// is dropped by the unique index and both read back the one that won, so
// nobody is shown a link that does not exist.
//
// A row whose user has no files left, or whose files all came after it, is
// a link to someone who is no longer there (linkHolds): it is removed, and
// the user who has the name now gets a link of their own.
func (s *Subscriptions) ensureUserTokens(ctx context.Context, clientID uint, accounts []model.Account) (map[string]string, error) {
	db := s.db.WithContext(ctx)
	read := func() (map[string]model.UserSub, error) {
		var have []model.UserSub
		if err := db.Where("client_id = ?", clientID).Find(&have).Error; err != nil {
			return nil, err
		}
		out := make(map[string]model.UserSub, len(have))
		for _, h := range have {
			out[h.Device] = h
		}
		return out, nil
	}
	have, err := read()
	if err != nil {
		return nil, fmt.Errorf("service: read users' links: %w", err)
	}
	names := deviceNames(sortedByID(accounts))
	var stale []uint
	for k, h := range have {
		if !linkHolds(h, userFiles(accounts, k)) {
			stale = append(stale, h.ID)
			delete(have, k)
		}
	}
	var missing []model.UserSub
	for _, n := range names {
		k := userKey(n)
		if _, ok := have[k]; ok {
			continue
		}
		token, err := newSubToken()
		if err != nil {
			return nil, err
		}
		missing = append(missing, model.UserSub{ClientID: clientID, Device: k, Token: token})
	}
	if len(stale) > 0 || len(missing) > 0 {
		err := db.Transaction(func(tx *gorm.DB) error {
			if len(stale) > 0 {
				if err := tx.Where("id IN ?", stale).Delete(&model.UserSub{}).Error; err != nil {
					return err
				}
			}
			if len(missing) > 0 {
				// A user issued meanwhile by another request keeps theirs.
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&missing).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("service: issue users' links: %w", err)
		}
		if have, err = read(); err != nil {
			return nil, fmt.Errorf("service: read users' links: %w", err)
		}
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		k := userKey(n)
		h, ok := have[k]
		if !ok || h.Token == "" {
			return nil, fmt.Errorf("service: no link could be issued for a user of client %d", clientID)
		}
		out[k] = h.Token
	}
	return out, nil
}

// RotateUserLink gives one user a new link; the old one stops working at
// once. The customer's own link and the other users' are not touched.
func (s *Subscriptions) RotateUserLink(ctx context.Context, clientID uint, user int, requestHost string) (*UserLink, error) {
	links, err := s.UserLinks(ctx, clientID, requestHost)
	if err != nil {
		return nil, err
	}
	if user < 1 || user > len(links) {
		return nil, invalidField("user", "this customer has no user %d", user)
	}
	u := links[user-1]
	token, err := newSubToken()
	if err != nil {
		return nil, err
	}
	res := s.db.WithContext(ctx).Model(&model.UserSub{}).
		Where("client_id = ? AND device = ?", clientID, userKey(u.Device)).
		Updates(map[string]any{"token": token, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return nil, fmt.Errorf("service: rotate a user's link: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("%w: user %d", ErrNotFound, user)
	}
	u.Token = token
	if u.Link, err = s.LinkFor(ctx, token, requestHost); err != nil {
		return nil, err
	}
	s.log.Info("a user's subscription link rotated", "client", clientID, "user", user)
	return &u, nil
}

// dropUserLink removes a user's link once the last of their files is gone,
// so a user added later under the same name is not handed a link someone
// else was given. Called inside the transaction that removed the file.
func dropUserLink(tx *gorm.DB, clientID uint, device string) error {
	var left int64
	if err := tx.Model(&model.Account{}).
		Where("client_id = ? AND LOWER(TRIM(device_name)) = ?", clientID, userKey(device)).
		Count(&left).Error; err != nil {
		return err
	}
	if left > 0 {
		return nil
	}
	err := tx.Where("client_id = ? AND device = ?", clientID, userKey(device)).Delete(&model.UserSub{}).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return nil
}

// userTokenTaken reports whether a token is some user's link, so a
// customer's own link is never set to one.
func userTokenTaken(db *gorm.DB, token string) (bool, error) {
	var n int64
	if err := db.Model(&model.UserSub{}).Where("token = ?", token).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}
