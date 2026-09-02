package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// Clients manages the sellable unit and its devices.
type Clients struct {
	db    *gorm.DB
	pools *ipam.Pools
	log   *slog.Logger
}

// NewClients builds the client service.
func NewClients(db *gorm.DB, pools *ipam.Pools, log *slog.Logger) *Clients {
	return &Clients{db: db, pools: pools, log: log}
}

// CreateInput describes a new client.
type CreateInput struct {
	Name           string           `json:"name"`
	Note           string           `json:"note"`
	Group          string           `json:"group"`
	InterfaceID    uint             `json:"interfaceId"`
	QuotaBytes     uint64           `json:"quotaBytes"`
	ExpiresAt      *time.Time       `json:"expiresAt"`
	DeviceLimit    int              `json:"deviceLimit"`
	RateBitsPerSec uint64           `json:"rateBitsPerSec"`
	ResetCycle     model.ResetCycle `json:"resetCycle"`

	// StartOnFirstUse defers the clock until the customer connects, with
	// DurationDays standing in for a date until then.
	StartOnFirstUse bool `json:"startOnFirstUse"`
	DurationDays    int  `json:"durationDays"`

	// Historical marks a record being restored rather than a plan being sold.
	// It is not part of the API: an expiry in the past is a typo on the form
	// and a fact in an import, and only the importer may say which.
	Historical bool `json:"-"`

	// DeviceNames seeds the first devices. When empty one device is created,
	// because a client with no device has nothing to hand the customer.
	DeviceNames []string `json:"deviceNames"`
}

// Create builds a client and its initial devices.
//
// The protocol is taken from the interface rather than asked for separately: an
// account only exists on an interface, and an interface serves exactly one
// protocol, so accepting both would allow them to disagree.
func (s *Clients) Create(ctx context.Context, in CreateInput) (*model.Client, error) {
	iface, err := s.loadInterface(ctx, in.InterfaceID)
	if err != nil {
		return nil, err
	}
	names, err := s.validateCreate(&in)
	if err != nil {
		return nil, err
	}

	client := model.Client{
		Name:           in.Name,
		Note:           in.Note,
		Group:          strings.TrimSpace(in.Group),
		Protocol:       iface.Protocol,
		QuotaBytes:     in.QuotaBytes,
		ExpiresAt:      in.ExpiresAt,
		DeviceLimit:    in.DeviceLimit,
		RateBitsPerSec: in.RateBitsPerSec,
		ResetCycle:     in.ResetCycle,
		Status:         model.StatusActive,

		StartOnFirstUse: in.StartOnFirstUse,
		DurationDays:    in.DurationDays,
	}

	// A deferred plan carries a duration instead of a date. Keeping both would
	// leave two answers to "when does this end", and the wrong one would be
	// enforced the moment the customer connected.
	if client.StartOnFirstUse && client.DurationDays > 0 {
		client.ExpiresAt = nil
	} else {
		client.StartOnFirstUse = false
		client.DurationDays = 0
	}

	// Addresses are reserved before the transaction opens and released if it
	// fails, so a rolled-back create cannot leak addresses out of the pool.
	addrs, release, err := s.reserve(iface.ID, len(names))
	if err != nil {
		return nil, err
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&client).Error; err != nil {
			return fmt.Errorf("create client: %w", err)
		}
		for i, name := range names {
			acc, err := s.buildAccount(&client, iface, name, addrs[i])
			if err != nil {
				return err
			}
			if err := tx.Create(acc).Error; err != nil {
				return fmt.Errorf("create device %q: %w", name, err)
			}
			if err := tx.Create(&model.IPLease{
				AccountID: acc.ID,
				ClientID:  client.ID,
				IP:        acc.IP,
				FromTS:    time.Now().UTC(),
			}).Error; err != nil {
				return fmt.Errorf("record address lease: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		release()
		return nil, fmt.Errorf("service: %w", err)
	}

	s.log.Info("client created",
		"id", client.ID, "name", client.Name, "protocol", client.Protocol,
		"devices", len(names), "quotaBytes", client.QuotaBytes)

	return s.Get(ctx, client.ID)
}

// buildAccount generates the credentials for one device.
func (s *Clients) buildAccount(client *model.Client, iface *model.Interface, name string, addr netip.Addr) (*model.Account, error) {
	acc := &model.Account{
		ClientID:    client.ID,
		InterfaceID: iface.ID,
		NodeID:      iface.NodeID,
		DeviceName:  name,
		IP:          addr.String(),
		Enabled:     true,
	}

	switch iface.Protocol {
	case model.ProtocolWireGuard:
		pair, err := wgkey.NewPair()
		if err != nil {
			return nil, err
		}
		acc.PrivateKey = pair.Private.String()
		acc.PublicKey = pair.Public.String()
		acc.PresharedKey = pair.Preshared.String()

	case model.ProtocolOpenVPN:
		secret, err := ovpnconf.NewSecret(16)
		if err != nil {
			return nil, err
		}
		// The username carries the client so the management interface,
		// which identifies sessions by common name, maps a live session back to
		// a customer without a lookup.
		acc.Username = fmt.Sprintf("s%d-%s", client.ID, slug(name))
		acc.Secret = secret
	}
	return acc, nil
}

// reserve allocates n addresses and returns a function that gives them back.
func (s *Clients) reserve(interfaceID uint, n int) ([]netip.Addr, func(), error) {
	alloc, err := s.pools.Get(interfaceID)
	if err != nil {
		return nil, nil, err
	}

	addrs := make([]netip.Addr, 0, n)
	release := func() {
		for _, a := range addrs {
			if err := alloc.Release(a); err != nil {
				s.log.Error("could not return address to pool", "address", a, "error", err)
			}
		}
	}

	for i := 0; i < n; i++ {
		addr, err := alloc.Allocate()
		if err != nil {
			release()
			if errors.Is(err, ipam.ErrPoolFull) {
				return nil, nil, fmt.Errorf("%w: interface %d", ErrPoolExhausted, interfaceID)
			}
			return nil, nil, err
		}
		addrs = append(addrs, addr)
	}
	return addrs, release, nil
}

func (s *Clients) validateCreate(in *CreateInput) ([]string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, invalidField("name", "name is required")
	}
	if in.DeviceLimit < 1 {
		in.DeviceLimit = 1
	}
	if in.DeviceLimit > 50 {
		return nil, invalidField("deviceLimit", "device limit must be between 1 and 64")
	}
	switch in.ResetCycle {
	case "":
		in.ResetCycle = model.ResetNone
	case model.ResetNone, model.ResetDaily, model.ResetWeekly, model.ResetMonthly:
	default:
		return nil, invalidField("resetCycle", "unknown reset cycle %q", in.ResetCycle)
	}
	// A date in the past is a mistake when someone is selling a plan, and a
	// fact when a record is being restored. Import says so; the form does not.
	if !in.Historical && in.ExpiresAt != nil && in.ExpiresAt.Before(time.Now()) {
		return nil, invalidField("expiresAt", "expiry is in the past")
	}

	names := make([]string, 0, len(in.DeviceNames))
	for _, n := range in.DeviceNames {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		names = []string{"device-1"}
	}
	if len(names) > in.DeviceLimit {
		return nil, fmt.Errorf("%w: %d devices requested but the limit is %d",
			ErrInvalid, len(names), in.DeviceLimit)
	}
	return names, nil
}

// AddDevice adds one device to an existing client.
func (s *Clients) AddDevice(ctx context.Context, subID uint, name string) (*model.Account, error) {
	client, err := s.Get(ctx, subID)
	if err != nil {
		return nil, err
	}
	if len(client.Accounts) >= client.DeviceLimit {
		return nil, fmt.Errorf("%w: %d of %d in use", ErrDeviceLimit, len(client.Accounts), client.DeviceLimit)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("device-%d", len(client.Accounts)+1)
	}

	iface, err := s.loadInterface(ctx, client.Accounts[0].InterfaceID)
	if err != nil {
		return nil, err
	}

	addrs, release, err := s.reserve(iface.ID, 1)
	if err != nil {
		return nil, err
	}

	acc, err := s.buildAccount(client, iface, name, addrs[0])
	if err != nil {
		release()
		return nil, err
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(acc).Error; err != nil {
			return err
		}
		return tx.Create(&model.IPLease{
			AccountID: acc.ID,
			ClientID:  client.ID,
			IP:        acc.IP,
			FromTS:    time.Now().UTC(),
		}).Error
	})
	if err != nil {
		release()
		return nil, fmt.Errorf("service: add device: %w", err)
	}
	return acc, nil
}

// RemoveDevice deletes a device and returns its address to the pool.
func (s *Clients) RemoveDevice(ctx context.Context, accountID uint) error {
	var acc model.Account
	err := s.db.WithContext(ctx).First(&acc, accountID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: device %d", ErrNotFound, accountID)
	}
	if err != nil {
		return fmt.Errorf("service: load device: %w", err)
	}

	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Close the lease rather than deleting it: the record of who held the
		// address is exactly what an abuse report needs, and the customer being
		// gone is no reason to lose it.
		if err := tx.Model(&model.IPLease{}).
			Where("account_id = ? AND to_ts IS NULL", acc.ID).
			Update("to_ts", now).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Account{}, acc.ID).Error
	})
	if err != nil {
		return fmt.Errorf("service: remove device: %w", err)
	}

	if addr, perr := netip.ParseAddr(acc.IP); perr == nil {
		if alloc, aerr := s.pools.Get(acc.InterfaceID); aerr == nil {
			if rerr := alloc.Release(addr); rerr != nil {
				s.log.Error("could not return address to pool", "address", addr, "error", rerr)
			}
		}
	}
	return nil
}

// ListFilter narrows a client listing.
type ListFilter struct {
	Search   string
	Status   model.ClientStatus
	Protocol model.Protocol
	Group    string
	Sort     string
	Page     int
	PerPage  int
}

// orderBy maps a sort key to a SQL clause. Unknown keys fall back to newest
// first rather than being interpolated, so the parameter cannot reach the query.
func (f ListFilter) orderBy() string {
	switch f.Sort {
	case "oldest":
		return "id ASC"
	case "name":
		return "name ASC"
	case "name_desc":
		return "name DESC"
	case "traffic":
		return "used_bytes DESC"
	case "expiry":
		// Clients with no expiry sort last: an operator scanning by expiry is
		// looking for what runs out soonest, not for what never will.
		return "expires_at IS NULL ASC, expires_at ASC"
	default:
		return "id DESC"
	}
}

// Page is one page of clients.
type Page struct {
	Items   []model.Client `json:"items"`
	Total   int64          `json:"total"`
	Page    int            `json:"page"`
	PerPage int            `json:"perPage"`
}

// List returns a page of clients.
//
// Paging is not optional: an operator with ten thousand customers must never
// receive them all in one response, and the UI is built to page rather than to
// filter in the browser.
func (s *Clients) List(ctx context.Context, f ListFilter) (*Page, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 200 {
		f.PerPage = 25
	}

	q := s.db.WithContext(ctx).Model(&model.Client{})
	if f.Search != "" {
		like := "%" + strings.ToLower(f.Search) + "%"
		q = q.Where("LOWER(name) LIKE ? OR LOWER(note) LIKE ?", like, like)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Protocol != "" {
		q = q.Where("protocol = ?", f.Protocol)
	}
	if f.Group != "" {
		q = q.Where(`"group" = ?`, f.Group)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("service: count clients: %w", err)
	}

	var items []model.Client
	err := q.Preload("Accounts").
		Order(f.orderBy()).
		Offset((f.Page - 1) * f.PerPage).
		Limit(f.PerPage).
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("service: list clients: %w", err)
	}

	return &Page{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}

// Get loads one client with its devices.
func (s *Clients) Get(ctx context.Context, id uint) (*model.Client, error) {
	var client model.Client
	err := s.db.WithContext(ctx).Preload("Accounts").First(&client, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: client %d", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("service: load client: %w", err)
	}
	return &client, nil
}

// UpdateInput carries the fields an operator may change.
type UpdateInput struct {
	Name           *string             `json:"name"`
	Note           *string             `json:"note"`
	Group          *string             `json:"group"`
	QuotaBytes     *uint64             `json:"quotaBytes"`
	ExpiresAt      **time.Time         `json:"expiresAt"`
	DeviceLimit    *int                `json:"deviceLimit"`
	RateBitsPerSec *uint64             `json:"rateBitsPerSec"`
	ResetCycle     *model.ResetCycle   `json:"resetCycle"`
	Status         *model.ClientStatus `json:"status"`
}

// Update applies changes to a client.
func (s *Clients) Update(ctx context.Context, id uint, in UpdateInput) (*model.Client, error) {
	client, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	fields := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalid)
		}
		fields["name"] = name
	}
	if in.Note != nil {
		fields["note"] = *in.Note
	}
	if in.Group != nil {
		fields["group"] = strings.TrimSpace(*in.Group)
	}
	if in.QuotaBytes != nil {
		fields["quota_bytes"] = *in.QuotaBytes
	}
	if in.ExpiresAt != nil {
		fields["expires_at"] = *in.ExpiresAt
	}
	if in.DeviceLimit != nil {
		if *in.DeviceLimit < len(client.Accounts) {
			return nil, fmt.Errorf("%w: limit %d is below the %d devices already issued",
				ErrInvalid, *in.DeviceLimit, len(client.Accounts))
		}
		fields["device_limit"] = *in.DeviceLimit
	}
	if in.RateBitsPerSec != nil {
		fields["rate_bits_per_sec"] = *in.RateBitsPerSec
	}
	if in.ResetCycle != nil {
		fields["reset_cycle"] = *in.ResetCycle
	}
	if in.Status != nil {
		fields["status"] = *in.Status
	}

	// Raising the quota or pushing back the expiry on a client that was
	// cut off should put the customer back online without a second action.
	if s.revives(client, in) {
		fields["status"] = model.StatusActive
	}

	if len(fields) == 0 {
		return client, nil
	}
	if err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("id = ?", id).Updates(fields).Error; err != nil {
		return nil, fmt.Errorf("service: update client: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Clients) revives(client *model.Client, in UpdateInput) bool {
	if in.Status != nil {
		return false // an explicit status wins
	}
	if client.Status == model.StatusExhausted && in.QuotaBytes != nil && *in.QuotaBytes > client.UsedBytes {
		return true
	}
	if client.Status == model.StatusExpired && in.ExpiresAt != nil {
		exp := *in.ExpiresAt
		return exp == nil || exp.After(time.Now())
	}
	return false
}

// ResetTraffic zeroes usage and reactivates a client that had run out.
func (s *Clients) ResetTraffic(ctx context.Context, id uint) (*model.Client, error) {
	now := time.Now().UTC()
	fields := map[string]any{"used_bytes": 0, "last_reset_at": now}

	client, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if client.Status == model.StatusExhausted {
		fields["status"] = model.StatusActive
	}

	if err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("id = ?", id).Updates(fields).Error; err != nil {
		return nil, fmt.Errorf("service: reset traffic: %w", err)
	}
	return s.Get(ctx, id)
}

// Delete removes a client and returns its addresses to the pool.
func (s *Clients) Delete(ctx context.Context, id uint) error {
	client, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.IPLease{}).
			Where("client_id = ? AND to_ts IS NULL", id).
			Update("to_ts", now).Error; err != nil {
			return err
		}
		if err := tx.Where("client_id = ?", id).Delete(&model.Account{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Client{}, id).Error
	})
	if err != nil {
		return fmt.Errorf("service: delete client: %w", err)
	}

	for _, acc := range client.Accounts {
		addr, perr := netip.ParseAddr(acc.IP)
		if perr != nil {
			continue
		}
		if alloc, aerr := s.pools.Get(acc.InterfaceID); aerr == nil {
			if rerr := alloc.Release(addr); rerr != nil {
				s.log.Error("could not return address to pool", "address", addr, "error", rerr)
			}
		}
	}
	s.log.Info("client deleted", "id", id, "name", client.Name)
	return nil
}

// depletingPercent is the share of the allowance at which a client is flagged
// as running low.
const depletingPercent = 80

// BulkAction is an operation applied to a selected set of clients.
type BulkAction string

const (
	BulkEnable  BulkAction = "enable"
	BulkDisable BulkAction = "disable"
	BulkReset   BulkAction = "reset"
	BulkDelete  BulkAction = "delete"
)

// Bulk applies an action to many clients at once and reports how many changed.
//
// Enable and reset are written as single statements rather than a loop: an
// operator renewing a few hundred customers at the start of a month should not
// pay for one round trip each.
func (s *Clients) Bulk(ctx context.Context, action BulkAction, ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("%w: no clients selected", ErrInvalid)
	}
	if len(ids) > 1000 {
		return 0, fmt.Errorf("%w: %d clients selected, limit is 1000", ErrInvalid, len(ids))
	}

	db := s.db.WithContext(ctx)
	now := time.Now().UTC()

	switch action {
	case BulkEnable:
		res := db.Model(&model.Client{}).Where("id IN ?", ids).
			Update("status", model.StatusActive)
		return res.RowsAffected, wrapBulk(res.Error)

	case BulkDisable:
		res := db.Model(&model.Client{}).Where("id IN ?", ids).
			Update("status", model.StatusDisabled)
		return res.RowsAffected, wrapBulk(res.Error)

	case BulkReset:
		// Resetting usage also revives anyone who had been cut off for running
		// out, which is what "renew" means to the person doing it.
		res := db.Model(&model.Client{}).Where("id IN ?", ids).
			Updates(map[string]any{
				"used_bytes":    0,
				"last_reset_at": now,
				"status": gorm.Expr(
					"CASE WHEN status = ? THEN ? ELSE status END",
					model.StatusExhausted, model.StatusActive),
			})
		return res.RowsAffected, wrapBulk(res.Error)

	case BulkDelete:
		// Deletion goes one at a time because each client's addresses have to
		// be returned to the pool and its leases closed.
		var n int64
		for _, id := range ids {
			if err := s.Delete(ctx, id); err != nil {
				if errors.Is(err, ErrNotFound) {
					continue
				}
				return n, err
			}
			n++
		}
		return n, nil

	default:
		return 0, fmt.Errorf("%w: unknown action %q", ErrInvalid, action)
	}
}

// AdjustInput changes expiry, allowance or renewal across a selection.
//
// Every field is optional and nil means "leave alone", so one dialog can offer
// all three without forcing the operator to restate the two they do not want to
// touch.
type AdjustInput struct {
	IDs        []uint            `json:"ids"`
	AddDays    *int              `json:"addDays"`
	QuotaBytes *uint64           `json:"quotaBytes"`
	ResetCycle *model.ResetCycle `json:"resetCycle"`
}

// Adjust applies an expiry/quota/renewal change to the selected clients.
func (s *Clients) Adjust(ctx context.Context, in AdjustInput) (int64, error) {
	if len(in.IDs) == 0 {
		return 0, fmt.Errorf("%w: no clients selected", ErrInvalid)
	}
	if in.AddDays == nil && in.QuotaBytes == nil && in.ResetCycle == nil {
		return 0, fmt.Errorf("%w: nothing to change", ErrInvalid)
	}

	db := s.db.WithContext(ctx)
	now := time.Now().UTC()

	var members []model.Client
	if err := db.Where("id IN ?", in.IDs).Find(&members).Error; err != nil {
		return 0, fmt.Errorf("service: load selection: %w", err)
	}

	var n int64
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, m := range members {
			fields := map[string]any{}

			if in.AddDays != nil {
				// Measured from each client's own expiry, so time already paid
				// for is not lost. Anyone expired starts from today.
				base := now
				if m.ExpiresAt != nil && m.ExpiresAt.After(now) {
					base = *m.ExpiresAt
				}
				next := base.Add(time.Duration(*in.AddDays) * 24 * time.Hour)
				fields["expires_at"] = next
				if m.Status == model.StatusExpired && next.After(now) {
					fields["status"] = model.StatusActive
				}
			}
			if in.QuotaBytes != nil {
				fields["quota_bytes"] = *in.QuotaBytes
				if m.Status == model.StatusExhausted &&
					(*in.QuotaBytes == 0 || m.UsedBytes < *in.QuotaBytes) {
					fields["status"] = model.StatusActive
				}
			}
			if in.ResetCycle != nil {
				fields["reset_cycle"] = *in.ResetCycle
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
		return 0, fmt.Errorf("service: adjust clients: %w", err)
	}
	return n, nil
}

// ResetAllTraffic zeroes usage for every client and revives the exhausted.
func (s *Clients) ResetAllTraffic(ctx context.Context) (int64, error) {
	res := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("1 = 1").
		Updates(map[string]any{
			"used_bytes":    0,
			"up_bytes":      0,
			"down_bytes":    0,
			"last_reset_at": time.Now().UTC(),
			"status": gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END",
				model.StatusExhausted, model.StatusActive),
		})
	if res.Error != nil {
		return 0, fmt.Errorf("service: reset all traffic: %w", res.Error)
	}
	s.log.Warn("traffic reset for every client", "clients", res.RowsAffected)
	return res.RowsAffected, nil
}

// DeleteByStatus removes every client in a given state, which is how an
// operator clears out the customers who never renewed.
func (s *Clients) DeleteByStatus(ctx context.Context, status model.ClientStatus) (int64, error) {
	switch status {
	case model.StatusExhausted, model.StatusExpired, model.StatusDisabled:
	default:
		return 0, fmt.Errorf("%w: refusing to bulk delete %q clients", ErrInvalid, status)
	}

	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.Client{}).
		Where("status = ?", status).Pluck("id", &ids).Error; err != nil {
		return 0, fmt.Errorf("service: list %s clients: %w", status, err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return s.Bulk(ctx, BulkDelete, ids)
}

// BatchInput creates several clients in one go, numbering them from a prefix.
type BatchInput struct {
	CreateInput
	Prefix string `json:"prefix"`
	Count  int    `json:"count"`
	Start  int    `json:"start"`
}

// CreateBatch issues a run of clients that share a plan.
//
// Each is created through the normal path rather than by bulk insert, so every
// one gets its own keys and addresses and a partial failure leaves the
// successful ones intact rather than rolling back a whole batch.
func (s *Clients) CreateBatch(ctx context.Context, in BatchInput) ([]model.Client, error) {
	if in.Count < 1 || in.Count > 200 {
		return nil, fmt.Errorf("%w: count must be between 1 and 200", ErrInvalid)
	}
	prefix := strings.TrimSpace(in.Prefix)
	if prefix == "" {
		return nil, fmt.Errorf("%w: a name prefix is required", ErrInvalid)
	}
	start := in.Start
	if start < 1 {
		start = 1
	}

	out := make([]model.Client, 0, in.Count)
	for i := 0; i < in.Count; i++ {
		spec := in.CreateInput
		spec.Name = fmt.Sprintf("%s-%d", prefix, start+i)
		created, err := s.Create(ctx, spec)
		if err != nil {
			if len(out) > 0 {
				return out, fmt.Errorf("service: created %d of %d before failing: %w",
					len(out), in.Count, err)
			}
			return nil, err
		}
		out = append(out, *created)
	}
	s.log.Info("client batch created", "prefix", prefix, "count", len(out))
	return out, nil
}

func wrapBulk(err error) error {
	if err != nil {
		return fmt.Errorf("service: bulk update: %w", err)
	}
	return nil
}

// Overview is the dashboard summary.
type Overview struct {
	Clients    int64  `json:"clients"`
	Active     int64  `json:"active"`
	Exhausted  int64  `json:"exhausted"`
	Expired    int64  `json:"expired"`
	Disabled   int64  `json:"disabled"`
	Depleting  int64  `json:"depleting"`
	Devices    int64  `json:"devices"`
	Online     int64  `json:"online"`
	Interfaces int64  `json:"interfaces"`
	TotalUsed  uint64 `json:"totalUsedBytes"`
	// TotalQuota sums only the clients that have a ceiling. Unlimited
	// ones are excluded rather than counted as zero, so the ratio of sold to
	// consumed traffic stays meaningful when both kinds are on the same server.
	TotalQuota  uint64 `json:"totalQuotaBytes"`
	DeviceSlots int64  `json:"deviceSlots"`
}

// Overview returns the dashboard counters.
func (s *Clients) Overview(ctx context.Context) (*Overview, error) {
	db := s.db.WithContext(ctx)
	var o Overview

	count := func(dst *int64, m any, where ...any) error {
		q := db.Model(m)
		if len(where) > 0 {
			q = q.Where(where[0], where[1:]...)
		}
		return q.Count(dst).Error
	}

	if err := count(&o.Clients, &model.Client{}); err != nil {
		return nil, err
	}
	if err := count(&o.Active, &model.Client{}, "status = ?", model.StatusActive); err != nil {
		return nil, err
	}
	if err := count(&o.Exhausted, &model.Client{}, "status = ?", model.StatusExhausted); err != nil {
		return nil, err
	}
	if err := count(&o.Expired, &model.Client{}, "status = ?", model.StatusExpired); err != nil {
		return nil, err
	}
	if err := count(&o.Disabled, &model.Client{}, "status = ?", model.StatusDisabled); err != nil {
		return nil, err
	}
	// Depleting is the early warning: still working, but far enough through the
	// allowance that the operator can offer a renewal before service stops
	// rather than after the customer complains.
	if err := count(&o.Depleting, &model.Client{},
		"status = ? AND quota_bytes > 0 AND used_bytes * 100 >= quota_bytes * ?",
		model.StatusActive, depletingPercent); err != nil {
		return nil, err
	}
	if err := count(&o.Devices, &model.Account{}); err != nil {
		return nil, err
	}
	if err := count(&o.Interfaces, &model.Interface{}); err != nil {
		return nil, err
	}

	// A peer is considered present if it handshook within the window after
	// which WireGuard itself treats a session as stale.
	cutoff := time.Now().UTC().Add(-3 * time.Minute)
	if err := count(&o.Online, &model.Account{}, "last_handshake > ?", cutoff); err != nil {
		return nil, err
	}

	var sums struct {
		Used  *uint64
		Quota *uint64
		Slots *int64
	}
	if err := db.Model(&model.Client{}).
		Select("SUM(used_bytes) AS used, " +
			"SUM(CASE WHEN quota_bytes > 0 THEN quota_bytes ELSE 0 END) AS quota, " +
			"SUM(device_limit) AS slots").
		Scan(&sums).Error; err != nil {
		return nil, fmt.Errorf("service: sum usage: %w", err)
	}
	if sums.Used != nil {
		o.TotalUsed = *sums.Used
	}
	if sums.Quota != nil {
		o.TotalQuota = *sums.Quota
	}
	if sums.Slots != nil {
		o.DeviceSlots = *sums.Slots
	}
	return &o, nil
}

func (s *Clients) loadInterface(ctx context.Context, id uint) (*model.Interface, error) {
	var iface model.Interface
	err := s.db.WithContext(ctx).First(&iface, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: interface %d", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("service: load interface: %w", err)
	}
	return &iface, nil
}

// slug reduces a device name to something safe for an OpenVPN common name.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "device"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
