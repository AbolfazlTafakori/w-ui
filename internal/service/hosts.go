package service

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Hosts manages the public addresses customers are handed.
//
// An interface listens once, but the address a customer dials is not always the
// one it listens on: a domain in front of it, a spare kept for when the first
// is blocked, a bare address for clients that cannot resolve names. Each is a
// host, and a customer's configuration is written with whichever one applies.
type Hosts struct {
	db  *gorm.DB
	log *slog.Logger
}

func NewHosts(db *gorm.DB, log *slog.Logger) *Hosts {
	return &Hosts{db: db, log: log}
}

// HostView is a host with the interface it belongs to named, so a list does not
// need a second request per row to be readable.
type HostView struct {
	model.Host
	InterfaceName string `json:"interfaceName"`
	Protocol      string `json:"protocol"`
	EffectivePort int    `json:"effectivePort"`
}

// List returns every host, grouped by interface.
func (s *Hosts) List(ctx context.Context) ([]HostView, error) {
	var hosts []model.Host
	if err := s.db.WithContext(ctx).
		Order("interface_id, priority DESC, id").Find(&hosts).Error; err != nil {
		return nil, fmt.Errorf("service: list hosts: %w", err)
	}

	var ifaces []model.Interface
	if err := s.db.WithContext(ctx).Find(&ifaces).Error; err != nil {
		return nil, fmt.Errorf("service: read interfaces: %w", err)
	}
	byID := make(map[uint]model.Interface, len(ifaces))
	for _, i := range ifaces {
		byID[i.ID] = i
	}

	out := make([]HostView, 0, len(hosts))
	for _, h := range hosts {
		v := HostView{Host: h}
		if i, ok := byID[h.InterfaceID]; ok {
			v.InterfaceName = i.Name
			v.Protocol = string(i.Protocol)
			v.EffectivePort = h.EffectivePort(i.ListenPort)
		}
		out = append(out, v)
	}
	return out, nil
}

// ForInterface returns the enabled hosts of one interface, best first.
//
// Used when a customer's configuration is written. The interface's own endpoint
// is returned when no host is configured, so a panel that has never touched
// this page behaves exactly as it did before hosts existed.
func (s *Hosts) ForInterface(ctx context.Context, iface *model.Interface) ([]model.Host, error) {
	var hosts []model.Host
	err := s.db.WithContext(ctx).
		Where("interface_id = ? AND enabled = ?", iface.ID, true).
		Order("priority DESC, id").Find(&hosts).Error
	if err != nil {
		return nil, fmt.Errorf("service: read hosts: %w", err)
	}
	if len(hosts) == 0 {
		return []model.Host{{
			InterfaceID: iface.ID,
			Name:        "default",
			Address:     iface.EndpointHost,
			Port:        iface.ListenPort,
			Enabled:     true,
			Reachable:   true,
		}}, nil
	}
	return hosts, nil
}

// HostInput is what the form collects.
type HostInput struct {
	InterfaceID uint   `json:"interfaceId"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Priority    int    `json:"priority"`
	Enabled     *bool  `json:"enabled"`
	Note        string `json:"note"`
}

// Create adds a host.
func (s *Hosts) Create(ctx context.Context, in HostInput) (*model.Host, error) {
	if err := s.validate(ctx, &in, 0); err != nil {
		return nil, err
	}
	h := model.Host{
		InterfaceID: in.InterfaceID,
		Name:        in.Name,
		Address:     in.Address,
		Port:        in.Port,
		Priority:    in.Priority,
		Enabled:     in.Enabled == nil || *in.Enabled,
		Note:        strings.TrimSpace(in.Note),
		Reachable:   true,
	}
	if err := s.db.WithContext(ctx).Create(&h).Error; err != nil {
		return nil, fmt.Errorf("service: create host: %w", err)
	}
	s.log.Info("host added", "name", h.Name, "address", h.Address)
	return &h, nil
}

// Update changes one.
func (s *Hosts) Update(ctx context.Context, id uint, in HostInput) (*model.Host, error) {
	db := s.db.WithContext(ctx)

	var h model.Host
	if err := db.First(&h, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no host %d", ErrNotFound, id)
	}
	if in.InterfaceID == 0 {
		in.InterfaceID = h.InterfaceID
	}
	if err := s.validate(ctx, &in, id); err != nil {
		return nil, err
	}

	updates := map[string]any{
		"interface_id": in.InterfaceID,
		"name":         in.Name,
		"address":      in.Address,
		"port":         in.Port,
		"priority":     in.Priority,
		"note":         strings.TrimSpace(in.Note),
		"updated_at":   time.Now().UTC(),
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if err := db.Model(&h).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("service: update host: %w", err)
	}
	if err := db.First(&h, id).Error; err != nil {
		return nil, fmt.Errorf("service: reload host: %w", err)
	}
	return &h, nil
}

// Delete removes one.
func (s *Hosts) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&model.Host{}, id)
	if res.Error != nil {
		return fmt.Errorf("service: delete host: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: no host %d", ErrNotFound, id)
	}
	return nil
}

// Check tries to reach a host the way a customer would.
func (s *Hosts) Check(ctx context.Context, id uint) (*CheckResult, error) {
	db := s.db.WithContext(ctx)

	var h model.Host
	if err := db.First(&h, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no host %d", ErrNotFound, id)
	}
	var iface model.Interface
	if err := db.First(&iface, h.InterfaceID).Error; err != nil {
		return nil, fmt.Errorf("%w: host %d has no interface", ErrNotFound, id)
	}

	port := h.EffectivePort(iface.ListenPort)
	res := &CheckResult{Tag: h.Name}

	// A WireGuard endpoint is UDP and will not answer a TCP connect, so what
	// this proves is that the name resolves and the host is routable — not that
	// the tunnel is up. The message says so rather than letting a green tick be
	// read as more than it is.
	addr := net.JoinHostPort(h.Address, fmt.Sprint(port))
	d, err := dialLatency(ctx, addr, "tcp")

	now := time.Now().UTC()
	if err != nil {
		res.OK = false
		res.Error = friendlyDialError(err)
		db.Model(&h).Updates(map[string]any{
			"reachable": false, "last_check_at": now, "last_error": res.Error,
		})
		return res, nil
	}
	res.OK = true
	res.LatencyMS = int(d.Milliseconds())
	if iface.IsWireGuard() {
		res.Error = "the address answers; a WireGuard tunnel cannot be proved from here"
	}
	db.Model(&h).Updates(map[string]any{
		"reachable": true, "last_check_at": now, "last_error": "",
	})
	return res, nil
}

func (s *Hosts) validate(ctx context.Context, in *HostInput, selfID uint) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)

	if in.Name == "" {
		return invalidField("name", "give the host a name so the list can be read later")
	}
	if in.InterfaceID == 0 {
		return invalidField("interfaceId", "a host has to belong to an interface")
	}

	var iface model.Interface
	if err := s.db.WithContext(ctx).Limit(1).
		Find(&iface, in.InterfaceID).Error; err != nil {
		return fmt.Errorf("service: check interface: %w", err)
	}
	if iface.ID == 0 {
		return invalidField("interfaceId", "there is no interface %d", in.InterfaceID)
	}

	if in.Address == "" {
		return invalidField("address", "a host needs the address customers will dial")
	}
	if strings.ContainsAny(in.Address, " /\\:") {
		return invalidField("address",
			"%q should be just a hostname or address; the port goes in its own field", in.Address)
	}
	if in.Port != 0 && (in.Port < 1 || in.Port > 65535) {
		return invalidField("port", "%d is not a port number", in.Port)
	}

	var clash int64
	q := s.db.WithContext(ctx).Model(&model.Host{}).
		Where("interface_id = ? AND LOWER(name) = LOWER(?)", in.InterfaceID, in.Name)
	if selfID != 0 {
		q = q.Where("id != ?", selfID)
	}
	if err := q.Count(&clash).Error; err != nil {
		return fmt.Errorf("service: check host name: %w", err)
	}
	if clash > 0 {
		return invalidField("name", "%s already has a host called %q", iface.Name, in.Name)
	}
	return nil
}
