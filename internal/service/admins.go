package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Selling the panel on.
//
// An operator with more capacity than customers resells it. The people they
// resell to need somewhere to manage their own customers, and must not be
// handed the machine: no interfaces, no nodes, no routing, no settings, and
// no sight of anybody else's customers. That is what this file administers.
//
// The ceiling a reseller is held to -- how many customers, how much traffic
// between them, and until when -- lives on their row rather than in a table
// beside it, because there is no reseller without a login and a login with
// no ceiling is simply one that has none.

// Admins manages panel operators.
type Admins struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewAdmins builds the operator service.
func NewAdmins(db *gorm.DB, log *slog.Logger) *Admins {
	return &Admins{db: db, log: log}
}

// AdminInput is a new operator, or the changes to one.
//
// The pointers are what makes a partial update possible: nil is "leave it",
// which is different from a zero that means "no limit". Create reads the
// same shape and treats nil as the default.
type AdminInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Note     string `json:"note"`

	Role model.AdminRole `json:"role"`

	Enabled     *bool        `json:"enabled"`
	ClientLimit *int         `json:"clientLimit"`
	QuotaBytes  *uint64      `json:"quotaBytes"`
	ExpiresAt   OptionalTime `json:"expiresAt"`

	// InterfaceIDs is which tunnels this reseller may sell. Nil leaves the
	// set alone on an update; an empty list is a deliberate "none", which
	// is a real thing to want -- a reseller kept on the panel with nothing
	// left to sell.
	InterfaceIDs []uint `json:"interfaceIds"`

	// DurationDays puts the term on hold until the reseller first signs in.
	// Above zero it wins over ExpiresAt, which is left empty until then.
	// Nil leaves it alone.
	DurationDays *int `json:"durationDays"`

	// GroupName is the owner's label for this reseller's customers -- what
	// the customer list is filtered by to answer "whose are these". Empty
	// on creation takes the default, reseller:<username>. Nil leaves it
	// alone.
	GroupName *string `json:"groupName"`
}

// maxTermDays bounds a term on hold. Ten years is past any plan anybody
// sells, and low enough that the date it turns into is one a database of
// either kind stores without complaint.
const maxTermDays = 3650

// Create adds an operator.
func (s *Admins) Create(ctx context.Context, in AdminInput) (*model.Admin, error) {
	username, err := s.checkUsername(ctx, 0, in.Username)
	if err != nil {
		return nil, err
	}
	if len(in.Password) < 8 {
		return nil, invalidField("password", "a password of at least 8 characters")
	}
	if in.Role == "" {
		in.Role = model.RoleReseller
	}
	if !in.Role.Valid() {
		return nil, invalidField("role", "%q is not a role this panel has", in.Role)
	}
	// One owner, set up when the panel was installed. A second would be a
	// second person who can remove the first.
	if in.Role == model.RoleOwner {
		return nil, invalidField("role", "the panel has one owner and it cannot be handed over from here")
	}
	if err := s.checkInterfaces(ctx, 0, in.Role, in.InterfaceIDs); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("service: hash password: %w", err)
	}

	admin := model.Admin{
		Username:     username,
		PasswordHash: string(hash),
		Note:         strings.TrimSpace(in.Note),
		Role:         in.Role,
		Locale:       "en",
		Enabled:      in.Enabled == nil || *in.Enabled,
		SessionEpoch: 1,
	}
	if in.Role.Capped() {
		if in.ClientLimit != nil {
			admin.ClientLimit = max(*in.ClientLimit, 0)
		}
		if in.QuotaBytes != nil {
			admin.QuotaBytes = *in.QuotaBytes
		}
		if in.ExpiresAt.Set {
			admin.ExpiresAt = in.ExpiresAt.Value
		}
		if in.DurationDays != nil {
			if *in.DurationDays < 0 || *in.DurationDays > maxTermDays {
				return nil, invalidField("durationDays", "a term of 1 to %d days", maxTermDays)
			}
			if *in.DurationDays > 0 {
				admin.DurationDays = *in.DurationDays
				admin.ExpiresAt = nil
			}
		}
	}

	wantGroup := ""
	if in.Role.Capped() && in.GroupName != nil {
		wantGroup = strings.TrimSpace(*in.GroupName)
		if wantGroup != "" {
			if err := s.checkOwnerGroup(ctx, 0, wantGroup); err != nil {
				return nil, err
			}
		}
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&admin).Error; err != nil {
			return fmt.Errorf("create operator: %w", err)
		}
		if admin.Role.Capped() {
			var (
				name string
				err  error
			)
			if wantGroup != "" {
				name, err = ensureOwnerGroup(tx, wantGroup, admin.Username)
			} else {
				name, err = s.makeOwnerGroup(tx, &admin)
			}
			if err != nil {
				return err
			}
			if err := tx.Model(&admin).Update("group_name", name).Error; err != nil {
				return fmt.Errorf("record the operator's own group: %w", err)
			}
			admin.GroupName = name
		}
		return s.setInterfaces(tx, admin.ID, in.InterfaceIDs)
	})
	if err != nil {
		return nil, fmt.Errorf("service: %w", err)
	}

	admin.InterfaceIDs = dedupe(in.InterfaceIDs)
	s.log.Info("operator created", "username", admin.Username, "role", admin.Role)
	return &admin, nil
}

// makeOwnerGroup creates the label every customer this reseller makes is
// put in.
//
// It is the owner's group, not the reseller's: it is never shown to them,
// never listed among theirs, and cannot be renamed, deleted or taken off a
// customer from their side. What it buys the owner is the one question a
// shared panel always raises -- whose customer is this -- answered by a
// filter on the customer list rather than by asking.
func (s *Admins) makeOwnerGroup(tx *gorm.DB, admin *model.Admin) (string, error) {
	base := "reseller:" + admin.Username
	name := base
	for n := 2; ; n++ {
		var clash int64
		err := tx.Model(&model.Group{}).
			Where("owner_id = 0 AND LOWER(name) = LOWER(?)", name).
			Count(&clash).Error
		if err != nil {
			return "", fmt.Errorf("check the operator's own group: %w", err)
		}
		if clash == 0 {
			break
		}
		name = fmt.Sprintf("%s-%d", base, n)
		if n > 50 {
			return "", fmt.Errorf("could not find a free name for the operator's own group")
		}
	}

	// Owned by the owner (zero), which is what keeps it off the reseller's
	// groups page while the customers in it are theirs.
	g := model.Group{
		OwnerID: 0,
		Name:    name,
		Note:    "Customers of " + admin.Username,
	}
	if err := tx.Create(&g).Error; err != nil {
		return "", fmt.Errorf("create the operator's own group: %w", err)
	}
	return name, nil
}

// checkOwnerGroup refuses a label that would mislead or hide something.
//
// Two resellers under one label would make "whose customers are these" a
// question the list can no longer answer. And a label that is one of this
// reseller's own group names would be hidden from them along with the
// owner's -- the group they made would vanish from their own page.
func (s *Admins) checkOwnerGroup(ctx context.Context, self uint, name string) error {
	if len(name) > 64 {
		return invalidField("groupName", "group names are limited to 64 characters")
	}
	db := s.db.WithContext(ctx)
	var clash int64
	if err := db.Model(&model.Admin{}).
		Where("id <> ? AND LOWER(group_name) = LOWER(?)", self, name).
		Count(&clash).Error; err != nil {
		return fmt.Errorf("service: check the label: %w", err)
	}
	if clash > 0 {
		return invalidField("groupName", "another reseller's customers are already filed under %q", name)
	}
	if self != 0 {
		if err := db.Model(&model.Group{}).
			Where("owner_id = ? AND LOWER(name) = LOWER(?)", self, name).
			Count(&clash).Error; err != nil {
			return fmt.Errorf("service: check the label: %w", err)
		}
		if clash > 0 {
			return invalidField("groupName", "%q is one of this reseller's own groups", name)
		}
	}
	return nil
}

// ensureOwnerGroup makes sure the owner has a group of that name, creating
// it when there is none, and returns the name as the group spells it. An
// existing one is used as it is -- the owner may file a reseller's
// customers beside customers of their own on purpose -- and in its own
// spelling, so "vip" typed against an existing "VIP" does not leave the
// customers under one name and the group under another.
func ensureOwnerGroup(tx *gorm.DB, name, username string) (string, error) {
	var existing []string
	if err := tx.Model(&model.Group{}).
		Where("owner_id = 0 AND LOWER(name) = LOWER(?)", name).
		Limit(1).Pluck("name", &existing).Error; err != nil {
		return "", fmt.Errorf("check the operator's own group: %w", err)
	}
	if len(existing) > 0 {
		return existing[0], nil
	}
	// A group can exist only as the customers filed under it, never having
	// been made on the groups page. Its spelling is theirs.
	if err := tx.Model(&model.ClientGroup{}).
		Where("LOWER(name) = LOWER(?)", name).
		Limit(1).Pluck("name", &existing).Error; err != nil {
		return "", fmt.Errorf("check the operator's own group: %w", err)
	}
	if len(existing) > 0 {
		name = existing[0]
	}
	g := model.Group{OwnerID: 0, Name: name, Note: "Customers of " + username}
	if err := tx.Create(&g).Error; err != nil {
		return "", fmt.Errorf("create the operator's own group: %w", err)
	}
	return name, nil
}

// dropOwnerGroupIfUnused removes the owner's group of that name once nothing
// is filed under it and no reseller is labelled with it. A label shared with
// the owner's own customers stays where it is.
func dropOwnerGroupIfUnused(tx *gorm.DB, name string, except uint) error {
	var members, labels int64
	if err := tx.Model(&model.ClientGroup{}).Where("name = ?", name).Count(&members).Error; err != nil {
		return fmt.Errorf("check the old label: %w", err)
	}
	if err := tx.Model(&model.Admin{}).
		Where("id <> ? AND group_name = ?", except, name).Count(&labels).Error; err != nil {
		return fmt.Errorf("check the old label: %w", err)
	}
	if members > 0 || labels > 0 {
		return nil
	}
	if err := tx.Where("owner_id = 0 AND name = ?", name).Delete(&model.Group{}).Error; err != nil {
		return fmt.Errorf("remove the old label: %w", err)
	}
	return nil
}

// refileCustomers moves one reseller's customers from one label to another.
func refileCustomers(tx *gorm.DB, ownerID uint, from, to string) error {
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
	return syncGroupLabel(tx, ids)
}

// Update changes an operator.
func (s *Admins) Update(ctx context.Context, id uint, in AdminInput) (*model.Admin, error) {
	var admin model.Admin
	if err := s.db.WithContext(ctx).First(&admin, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("service: load operator: %w", err)
	}

	fields := map[string]any{}

	if strings.TrimSpace(in.Username) != "" && !strings.EqualFold(in.Username, admin.Username) {
		username, err := s.checkUsername(ctx, admin.ID, in.Username)
		if err != nil {
			return nil, err
		}
		fields["username"] = username
	}
	if in.Note != "" || strings.TrimSpace(in.Note) != admin.Note {
		fields["note"] = strings.TrimSpace(in.Note)
	}

	role := admin.Role
	if in.Role != "" && in.Role != admin.Role {
		if !in.Role.Valid() {
			return nil, invalidField("role", "%q is not a role this panel has", in.Role)
		}
		if admin.Role == model.RoleOwner {
			return nil, invalidField("role", "the owner's role cannot be changed")
		}
		if in.Role == model.RoleOwner {
			return nil, invalidField("role", "the panel has one owner and it cannot be handed over from here")
		}
		role = in.Role
		fields["role"] = role
	}
	if admin.Role == model.RoleOwner && in.Enabled != nil && !*in.Enabled {
		return nil, invalidField("enabled", "the owner cannot be switched off")
	}

	// A password change ends whatever sessions this operator has open. That
	// is the point of changing it from here: an owner shutting a reseller
	// out should not have to wait for a token to expire.
	if in.Password != "" {
		if len(in.Password) < 8 {
			return nil, invalidField("password", "a password of at least 8 characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("service: hash password: %w", err)
		}
		fields["password_hash"] = string(hash)
		fields["session_epoch"] = admin.SessionEpoch + 1
	}

	if in.Enabled != nil && *in.Enabled != admin.Enabled {
		fields["enabled"] = *in.Enabled
		// Switched off means out, not "out once the token expires". Their
		// customers stop either way -- that is computed, not stored -- but
		// leaving the operator signed in to watch it happen is not what an
		// owner who clicked this meant.
		if !*in.Enabled {
			fields["session_epoch"] = admin.SessionEpoch + 1
		}
	}

	// The ceiling only means anything for a reseller. Promoting one to
	// administrator clears it rather than leaving numbers on the row that
	// nothing reads and the next owner has to guess at.
	switch {
	case role.Capped():
		if in.ClientLimit != nil {
			fields["client_limit"] = max(*in.ClientLimit, 0)
		}
		if in.QuotaBytes != nil {
			fields["quota_bytes"] = *in.QuotaBytes
		}
		if in.ExpiresAt.Set {
			fields["expires_at"] = in.ExpiresAt.Value
			if in.ExpiresAt.Value != nil {
				fields["duration_days"] = 0
			}
		}
		if in.DurationDays != nil {
			d := *in.DurationDays
			if d < 0 || d > maxTermDays {
				return nil, invalidField("durationDays", "a term of 1 to %d days", maxTermDays)
			}
			fields["duration_days"] = d
			if d > 0 {
				fields["expires_at"] = nil
			}
		}
	case admin.Role.Capped():
		fields["client_limit"] = 0
		fields["quota_bytes"] = uint64(0)
		fields["expires_at"] = nil
		fields["duration_days"] = 0
	}

	// The label moves only for a reseller, and only when it changes.
	newGroup := ""
	if role.Capped() && admin.Role.Capped() && in.GroupName != nil {
		want := strings.TrimSpace(*in.GroupName)
		if want == "" {
			return nil, invalidField("groupName", "a reseller's customers have to be filed under something")
		}
		if want != admin.GroupName {
			if err := s.checkOwnerGroup(ctx, admin.ID, want); err != nil {
				return nil, err
			}
			newGroup = want
		}
	}

	if err := s.checkInterfaces(ctx, admin.ID, role, in.InterfaceIDs); err != nil {
		return nil, err
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if newGroup != "" {
			canonical, err := ensureOwnerGroup(tx, newGroup, admin.Username)
			if err != nil {
				return err
			}
			newGroup = canonical
			if admin.GroupName != "" {
				if err := refileCustomers(tx, admin.ID, admin.GroupName, newGroup); err != nil {
					return err
				}
			}
			fields["group_name"] = newGroup
		}
		if len(fields) > 0 {
			if err := tx.Model(&model.Admin{}).Where("id = ?", admin.ID).Updates(fields).Error; err != nil {
				return fmt.Errorf("update operator: %w", err)
			}
		}
		if newGroup != "" && admin.GroupName != "" {
			if err := dropOwnerGroupIfUnused(tx, admin.GroupName, admin.ID); err != nil {
				return err
			}
		}
		if in.InterfaceIDs != nil {
			return s.setInterfaces(tx, admin.ID, in.InterfaceIDs)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("service: %w", err)
	}

	s.log.Info("operator changed", "username", admin.Username, "fields", len(fields))
	return s.Get(ctx, admin.ID)
}

// DeleteMode says what becomes of a departing operator's customers.
type DeleteMode string

const (
	// DeleteKeepClients hands them to the owner. The plans stay live and
	// the configurations people are holding keep working; only who
	// administers them changes.
	DeleteKeepClients DeleteMode = "keep"

	// DeleteWithClients removes them. What the reseller sold stops.
	DeleteWithClients DeleteMode = "delete"
)

// Delete removes an operator.
//
// There is no default for what happens to their customers, and this refuses
// rather than choosing: one answer quietly stops people paying for a
// service and the other quietly leaves an owner administering customers
// they did not know they had. The caller says which.
func (s *Admins) Delete(ctx context.Context, id uint, mode DeleteMode, clients *Clients) error {
	var admin model.Admin
	if err := s.db.WithContext(ctx).First(&admin, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("service: load operator: %w", err)
	}
	if admin.Role == model.RoleOwner {
		return fmt.Errorf("%w: the panel's owner cannot be deleted", ErrInvalid)
	}

	var held int64
	err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("owner_id = ?", admin.ID).Count(&held).Error
	if err != nil {
		return fmt.Errorf("service: count the operator's customers: %w", err)
	}
	if held > 0 && mode != DeleteKeepClients && mode != DeleteWithClients {
		return invalidField("clients",
			"%s has %d customers: say whether to keep them under your own account or delete them with the operator",
			admin.Username, held)
	}

	if held > 0 && mode == DeleteWithClients {
		// Through the customer service rather than a DELETE, so addresses
		// go back to their pools and the tunnels are told.
		var ids []uint
		if err := s.db.WithContext(ctx).Model(&model.Client{}).
			Where("owner_id = ?", admin.ID).Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("service: list the operator's customers: %w", err)
		}
		if _, err := clients.Bulk(ctx, BulkDelete, ids); err != nil {
			return err
		}
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Read before they are handed over, when they can still be told
		// apart from the owner's own.
		var kept []uint
		if err := tx.Model(&model.Client{}).Where("owner_id = ?", admin.ID).
			Pluck("id", &kept).Error; err != nil {
			return fmt.Errorf("list the operator's customers: %w", err)
		}
		// Anything left is kept, and the owner administers it.
		if err := tx.Model(&model.Client{}).Where("owner_id = ?", admin.ID).
			Update("owner_id", 0).Error; err != nil {
			return fmt.Errorf("hand over the operator's customers: %w", err)
		}
		if err := tx.Where("owner_id = ?", admin.ID).Delete(&model.Group{}).Error; err != nil {
			return fmt.Errorf("delete the operator's groups: %w", err)
		}
		if admin.GroupName != "" {
			// This reseller's customers leave the label, and nobody else's:
			// the owner may have filed customers of their own under the same
			// name, and those are not the departing reseller's to take.
			if len(kept) > 0 {
				if err := tx.Where("name = ? AND client_id IN ?", admin.GroupName, kept).
					Delete(&model.ClientGroup{}).Error; err != nil {
					return fmt.Errorf("clear the operator's own group: %w", err)
				}
				if err := syncGroupLabel(tx, kept); err != nil {
					return fmt.Errorf("clear the operator's own group: %w", err)
				}
			}
			if err := dropOwnerGroupIfUnused(tx, admin.GroupName, admin.ID); err != nil {
				return err
			}
		}
		if err := tx.Where("admin_id = ?", admin.ID).Delete(&model.AdminInterface{}).Error; err != nil {
			return fmt.Errorf("clear the operator's tunnels: %w", err)
		}
		// A node bought for one reseller goes back to the pool rather than
		// being deleted: the machine is still there and still costs money.
		if err := tx.Model(&model.Node{}).Where("owner_id = ?", admin.ID).
			Update("owner_id", 0).Error; err != nil {
			return fmt.Errorf("release the operator's nodes: %w", err)
		}
		if err := tx.Delete(&model.Admin{}, admin.ID).Error; err != nil {
			return fmt.Errorf("delete operator: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}

	s.log.Warn("operator deleted", "username", admin.Username, "customers", held, "mode", mode)
	return nil
}

// Get returns one operator with their tunnels and customer count filled in.
func (s *Admins) Get(ctx context.Context, id uint) (*model.Admin, error) {
	var admin model.Admin
	if err := s.db.WithContext(ctx).First(&admin, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("service: load operator: %w", err)
	}
	if err := s.fill(ctx, []*model.Admin{&admin}); err != nil {
		return nil, err
	}
	return &admin, nil
}

// List returns every operator, the owner first and the rest by name.
func (s *Admins) List(ctx context.Context) ([]model.Admin, error) {
	var admins []model.Admin
	err := s.db.WithContext(ctx).
		Order("CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, username ASC").
		Find(&admins).Error
	if err != nil {
		return nil, fmt.Errorf("service: list operators: %w", err)
	}
	ptrs := make([]*model.Admin, len(admins))
	for i := range admins {
		ptrs[i] = &admins[i]
	}
	if err := s.fill(ctx, ptrs); err != nil {
		return nil, err
	}
	return admins, nil
}

// fill adds what is not on the row: the tunnels each may sell, and how many
// customers they hold. Two queries for the whole list rather than two per
// operator.
func (s *Admins) fill(ctx context.Context, admins []*model.Admin) error {
	if len(admins) == 0 {
		return nil
	}
	ids := make([]uint, len(admins))
	byID := make(map[uint]*model.Admin, len(admins))
	for i, a := range admins {
		ids[i] = a.ID
		byID[a.ID] = a
		a.InterfaceIDs = []uint{}
	}

	var links []model.AdminInterface
	if err := s.db.WithContext(ctx).Where("admin_id IN ?", ids).Find(&links).Error; err != nil {
		return fmt.Errorf("service: load operator tunnels: %w", err)
	}
	for _, l := range links {
		if a := byID[l.AdminID]; a != nil {
			a.InterfaceIDs = append(a.InterfaceIDs, l.InterfaceID)
		}
	}

	type row struct {
		OwnerID uint
		N       int64
	}
	var counts []row
	err := s.db.WithContext(ctx).Model(&model.Client{}).
		Select("owner_id, COUNT(*) AS n").
		Where("owner_id IN ?", ids).
		Group("owner_id").Scan(&counts).Error
	if err != nil {
		return fmt.Errorf("service: count operators' customers: %w", err)
	}
	for _, c := range counts {
		if a := byID[c.OwnerID]; a != nil {
			a.Clients = c.N
		}
	}
	return nil
}

// checkUsername validates a name and refuses one already taken.
func (s *Admins) checkUsername(ctx context.Context, self uint, name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) < 3 {
		return "", invalidField("username", "a username of at least 3 characters")
	}
	if len(name) > 64 {
		return "", invalidField("username", "that username is too long")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return "", invalidField("username",
				"a username uses letters, digits, and - _ . only")
		}
	}
	// Case-insensitively: two operators whose names differ only in
	// capitalisation are two nobody can tell apart on a list, and sign-in
	// would be a guess at which one they meant.
	var clash int64
	err := s.db.WithContext(ctx).Model(&model.Admin{}).
		Where("LOWER(username) = LOWER(?) AND id <> ?", name, self).
		Count(&clash).Error
	if err != nil {
		return "", fmt.Errorf("service: check username: %w", err)
	}
	if clash > 0 {
		return "", invalidField("username", "%q is taken", name)
	}
	return name, nil
}

// checkInterfaces refuses tunnels that do not exist.
func (s *Admins) checkInterfaces(ctx context.Context, adminID uint, role model.AdminRole, ids []uint) error {
	if !role.Capped() || len(ids) == 0 {
		return nil
	}
	ids = dedupe(ids)
	var found int64
	if err := s.db.WithContext(ctx).Model(&model.Interface{}).
		Where("id IN ?", ids).Count(&found).Error; err != nil {
		return fmt.Errorf("service: check tunnels: %w", err)
	}
	if int(found) != len(ids) {
		return invalidField("interfaceIds", "one of those servers no longer exists")
	}

	// A machine reserved for one reseller carries nobody else. Allowing a
	// tunnel on it to a second would put that reseller's customers on
	// hardware somebody else is paying for.
	var taken int64
	err := s.db.WithContext(ctx).Model(&model.Interface{}).
		Where("id IN ? AND node_id IN (SELECT id FROM nodes WHERE owner_id <> 0 AND owner_id <> ?)",
			ids, adminID).
		Count(&taken).Error
	if err != nil {
		return fmt.Errorf("service: check reserved machines: %w", err)
	}
	if taken > 0 {
		return invalidField("interfaceIds",
			"one of those servers is on a machine reserved for another operator")
	}
	return nil
}

// setInterfaces replaces which tunnels an operator may sell.
func (s *Admins) setInterfaces(tx *gorm.DB, adminID uint, ids []uint) error {
	if err := tx.Where("admin_id = ?", adminID).Delete(&model.AdminInterface{}).Error; err != nil {
		return fmt.Errorf("clear the operator's tunnels: %w", err)
	}
	ids = dedupe(ids)
	if len(ids) == 0 {
		return nil
	}
	rows := make([]model.AdminInterface, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, model.AdminInterface{AdminID: adminID, InterfaceID: id})
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("record the operator's tunnels: %w", err)
	}
	return nil
}

// AllowedInterfaces is the set of tunnels an operator may put a customer on,
// or nil when they are not held to a set.
func (s *Admins) AllowedInterfaces(ctx context.Context, admin *model.Admin) (map[uint]bool, error) {
	if admin == nil || !admin.Role.Capped() {
		return nil, nil
	}
	var ids []uint
	err := s.db.WithContext(ctx).Model(&model.AdminInterface{}).
		Where("admin_id = ?", admin.ID).Pluck("interface_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("service: load the operator's tunnels: %w", err)
	}
	// Non-nil even when empty: an empty set means none allowed, and a nil
	// one means no restriction. The difference decides whether a reseller
	// with no tunnels can sell every tunnel.
	out := make(map[uint]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// RecordUsage adds to what a reseller's customers have carried between them.
//
// Called with the same deltas that move a customer's own counter, so the two
// can never drift by more than one tick. Zero is the owner, who has no
// ceiling and no row to update.
func (s *Admins) RecordUsage(ctx context.Context, byOwner map[uint]uint64) error {
	for ownerID, delta := range byOwner {
		if ownerID == 0 || delta == 0 {
			continue
		}
		err := s.db.WithContext(ctx).Model(&model.Admin{}).
			Where("id = ?", ownerID).
			UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", delta)).Error
		if err != nil {
			return fmt.Errorf("service: record operator usage: %w", err)
		}
	}
	return nil
}

// RecomputeUsage sets every reseller's counter to the sum of their
// customers' usage.
//
// The counter is incremental so that the page and the sweep do not each run
// an aggregate over every customer. Incremental counters drift -- a customer
// deleted, a customer's usage reset, a restore from a backup taken
// mid-tick -- so this puts it back, and is cheap enough to run at boot and
// whenever a reseller's customers change hands.
func (s *Admins) RecomputeUsage(ctx context.Context) error {
	err := s.db.WithContext(ctx).Exec(`
		UPDATE admins SET used_bytes = COALESCE(
			(SELECT SUM(c.used_bytes) FROM clients c WHERE c.owner_id = admins.id), 0)
		WHERE role <> 'owner'`).Error
	if err != nil {
		return fmt.Errorf("service: recompute operator usage: %w", err)
	}
	return nil
}

// ResetUsage puts one reseller's counter back to zero -- the owner topping
// them up for another month -- and clears the same counters on the
// customers underneath, since the allowance they were spending is the one
// being renewed.
func (s *Admins) ResetUsage(ctx context.Context, id uint) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Admin{}).Where("id = ?", id).
			Update("used_bytes", 0).Error; err != nil {
			return fmt.Errorf("reset operator usage: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}
	s.log.Info("operator allowance reset", "admin", id)
	return nil
}

// CheckCanCreate reports whether a reseller has room for another customer.
//
// Read before a customer is created rather than after, so the refusal names
// the ceiling instead of the operator discovering it from a row that will
// not save.
func (s *Admins) CheckCanCreate(ctx context.Context, admin *model.Admin, adding int) error {
	if admin == nil || !admin.Role.Capped() {
		return nil
	}
	now := time.Now().UTC()
	switch {
	case !admin.Enabled:
		return fmt.Errorf("%w: your account is switched off", ErrInvalid)
	case admin.Expired(now):
		return fmt.Errorf("%w: your account's term has ended", ErrInvalid)
	case admin.QuotaExceeded():
		return fmt.Errorf("%w: your data allowance is used up", ErrInvalid)
	}
	if admin.ClientLimit <= 0 {
		return nil
	}
	var held int64
	err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("owner_id = ?", admin.ID).Count(&held).Error
	if err != nil {
		return fmt.Errorf("service: count your customers: %w", err)
	}
	if held+int64(adding) > int64(admin.ClientLimit) {
		return invalidField("name",
			"you may have %d customers and you have %d", admin.ClientLimit, held)
	}
	return nil
}
