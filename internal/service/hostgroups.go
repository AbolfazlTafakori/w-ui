package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Host groups: the hosts page as the classic panel has it.
//
// What the operator enters is one thing -- a name, some addresses, the
// inbounds it applies to -- and what is stored is one row per address per
// inbound, all carrying the same group id. Reading groups them back; writing
// a group replaces its rows. A row from before groups existed has no id and
// is shown as a group of its own.

// HostGroup is one entry on the hosts page.
type HostGroup struct {
	GroupID      string   `json:"groupId"`
	InterfaceIDs []uint   `json:"interfaceIds"`
	Hosts        []string `json:"hosts"` // "address" or "address:port"

	SortOrder      int      `json:"sortOrder"`
	Remark         string   `json:"remark"`
	Description    string   `json:"description"`
	Enabled        bool     `json:"enabled"`
	Tags           []string `json:"tags"`
	Port           int      `json:"port"`
	ExcludeFormats []string `json:"excludeFormats"`
	Shuffle        bool     `json:"shuffle"`

	// Reachability, the worst of the group's rows, from the prober.
	Reachable   bool       `json:"reachable"`
	LastCheckAt *time.Time `json:"lastCheckAt"`
	LastError   string     `json:"lastError"`
	// RowIDs are the rows behind the group, for the per-row check.
	RowIDs []uint `json:"rowIds"`
}

// SubFormats are the names a host can be left out of.
var SubFormats = []string{"conf", "base64", "zip", "page"}

func groupKey(h model.Host) string {
	if h.GroupID != "" {
		return h.GroupID
	}
	return "fallback_" + strconv.FormatUint(uint64(h.ID), 10)
}

func formatHostAddr(addr string, port int) string {
	if port <= 0 {
		return addr
	}
	if strings.Contains(addr, ":") {
		return "[" + addr + "]:" + strconv.Itoa(port)
	}
	return addr + ":" + strconv.Itoa(port)
}

// parseHostAndPort splits "host", "host:port" or "[v6]:port"; a missing
// port is the default.
func parseHostAndPort(s string, defaultPort int) (string, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", defaultPort
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			return h, n
		}
		return h, defaultPort
	}
	return strings.Trim(s, "[]"), defaultPort
}

func newGroupID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ListGroups returns the hosts page.
func (s *Hosts) ListGroups(ctx context.Context) ([]HostGroup, error) {
	var rows []model.Host
	if err := s.db.WithContext(ctx).Order("priority, id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: list hosts: %w", err)
	}
	return groupHosts(rows), nil
}

func groupHosts(rows []model.Host) []HostGroup {
	byKey := map[string]*HostGroup{}
	var order []string
	for _, h := range rows {
		k := groupKey(h)
		g, ok := byKey[k]
		if !ok {
			g = &HostGroup{
				GroupID: k, InterfaceIDs: []uint{}, Hosts: []string{}, SortOrder: h.Priority,
				Remark: h.Name, Description: h.Description, Enabled: h.Enabled,
				Tags: splitList(h.Tags), Port: h.Port, ExcludeFormats: splitList(h.ExcludeFormats),
				Shuffle: h.Shuffle, Reachable: true, RowIDs: []uint{},
			}
			byKey[k] = g
			order = append(order, k)
		}
		if !containsUint(g.InterfaceIDs, h.InterfaceID) {
			g.InterfaceIDs = append(g.InterfaceIDs, h.InterfaceID)
		}
		// The group's port is the one every row shares; a row's own port is
		// written into its address when it differs.
		addr := h.Address
		if h.Port != 0 && h.Port != g.Port {
			addr = formatHostAddr(h.Address, h.Port)
		}
		if addr != "" && !containsStr(g.Hosts, addr) {
			g.Hosts = append(g.Hosts, addr)
		}
		if h.Priority < g.SortOrder {
			g.SortOrder = h.Priority
		}
		if !h.Reachable {
			g.Reachable = false
			if g.LastError == "" {
				g.LastError = h.LastError
			}
		}
		if h.LastCheckAt != nil && (g.LastCheckAt == nil || h.LastCheckAt.After(*g.LastCheckAt)) {
			t := *h.LastCheckAt
			g.LastCheckAt = &t
		}
		g.RowIDs = append(g.RowIDs, h.ID)
	}
	out := make([]HostGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Remark < out[j].Remark
	})
	return out
}

func containsUint(xs []uint, v uint) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// validateGroup tidies the input and refuses what cannot be stored.
func (s *Hosts) validateGroup(ctx context.Context, in *HostGroup) error {
	in.Remark = strings.TrimSpace(in.Remark)
	if in.Remark == "" {
		return invalidField("remark", "give the host a name; it is what the config is called")
	}
	if len(in.Remark) > 256 {
		return invalidField("remark", "the name is longer than 256 characters")
	}
	in.Description = strings.TrimSpace(in.Description)
	if len(in.Description) > 64 {
		return invalidField("description", "the description is longer than 64 characters")
	}
	if len(in.InterfaceIDs) == 0 {
		return invalidField("interfaceIds", "choose at least one inbound")
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&model.Interface{}).Where("id IN ?", in.InterfaceIDs).Count(&n).Error; err != nil {
		return fmt.Errorf("service: check interfaces: %w", err)
	}
	if int(n) != len(dedupe(in.InterfaceIDs)) {
		return invalidField("interfaceIds", "one of those inbounds does not exist")
	}
	if in.Port < 0 || in.Port > 65535 {
		return invalidField("port", "port %d is out of range", in.Port)
	}
	var hosts []string
	for _, h := range in.Hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		addr, _ := parseHostAndPort(h, 0)
		if addr == "" {
			return invalidField("hosts", "%q is not an address", h)
		}
		if _, err := netip.ParseAddr(addr); err != nil {
			if strings.ContainsAny(addr, " /\\?#") {
				return invalidField("hosts", "%q is not a host name or address", addr)
			}
		}
		if !containsStr(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	in.Hosts = hosts
	var tags []string
	for _, t := range in.Tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.ContainsAny(t, ", ") {
			return invalidField("tags", "a tag cannot contain a comma or a space")
		}
		if !containsStr(tags, t) {
			tags = append(tags, t)
		}
	}
	in.Tags = tags
	var ex []string
	for _, f := range in.ExcludeFormats {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if !containsStr(SubFormats, f) {
			return invalidField("excludeFormats", "%q is not a format; the formats are %s", f, strings.Join(SubFormats, ", "))
		}
		if !containsStr(ex, f) {
			ex = append(ex, f)
		}
	}
	in.ExcludeFormats = ex
	return nil
}

// rowsFor expands a group into the rows that store it.
func rowsFor(groupID string, in HostGroup) []model.Host {
	hosts := in.Hosts
	if len(hosts) == 0 {
		// An address-less host: the inbound's own address with this group's
		// name and port, which is what the classic panel's "inherits" means.
		hosts = []string{""}
	}
	var rows []model.Host
	for _, hs := range hosts {
		addr, port := parseHostAndPort(hs, in.Port)
		for _, ifaceID := range dedupe(in.InterfaceIDs) {
			rows = append(rows, model.Host{
				GroupID: groupID, InterfaceID: ifaceID, Name: in.Remark, Description: in.Description,
				Enabled: in.Enabled, Address: addr, Port: port, Priority: in.SortOrder,
				Tags: joinList(in.Tags), ExcludeFormats: joinList(in.ExcludeFormats), Shuffle: in.Shuffle,
				Reachable: true,
			})
		}
	}
	return rows
}

// CreateGroup adds a host, placed after the last.
func (s *Hosts) CreateGroup(ctx context.Context, in HostGroup) (*HostGroup, error) {
	if err := s.validateGroup(ctx, &in); err != nil {
		return nil, err
	}
	var last struct{ Max int }
	_ = s.db.WithContext(ctx).Model(&model.Host{}).Select("COALESCE(MAX(priority), 0) AS max").Scan(&last).Error
	in.SortOrder = last.Max + 1
	id := newGroupID()
	rows := rowsFor(id, in)
	if err := s.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: create host: %w", err)
	}
	s.log.Info("host added", "name", in.Remark, "addresses", len(in.Hosts), "inbounds", len(in.InterfaceIDs))
	return s.group(ctx, id)
}

// UpdateGroup replaces a host's rows with what the form now says.
func (s *Hosts) UpdateGroup(ctx context.Context, groupID string, in HostGroup) (*HostGroup, error) {
	if err := s.validateGroup(ctx, &in); err != nil {
		return nil, err
	}
	old, err := s.rowsOf(ctx, groupID)
	if err != nil {
		return nil, err
	}
	in.SortOrder = old[0].Priority
	// A pre-group row keeps its own id as its group from now on.
	newID := groupID
	if strings.HasPrefix(groupID, "fallback_") {
		newID = newGroupID()
	}
	rows := rowsFor(newID, in)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids := make([]uint, 0, len(old))
		for _, r := range old {
			ids = append(ids, r.ID)
		}
		if err := tx.Where("id IN ?", ids).Delete(&model.Host{}).Error; err != nil {
			return err
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		return nil, fmt.Errorf("service: update host: %w", err)
	}
	return s.group(ctx, newID)
}

// DeleteGroup removes a host.
func (s *Hosts) DeleteGroup(ctx context.Context, groupID string) error {
	old, err := s.rowsOf(ctx, groupID)
	if err != nil {
		return err
	}
	ids := make([]uint, 0, len(old))
	for _, r := range old {
		ids = append(ids, r.ID)
	}
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.Host{}).Error; err != nil {
		return fmt.Errorf("service: delete host: %w", err)
	}
	return nil
}

// SetGroupsEnabled switches several hosts at once.
func (s *Hosts) SetGroupsEnabled(ctx context.Context, groupIDs []string, on bool) (int64, error) {
	ids, err := s.rowIDs(ctx, groupIDs)
	if err != nil {
		return 0, err
	}
	res := s.db.WithContext(ctx).Model(&model.Host{}).Where("id IN ?", ids).
		Updates(map[string]any{"enabled": on, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return 0, fmt.Errorf("service: switch hosts: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// DeleteGroups removes several hosts at once.
func (s *Hosts) DeleteGroups(ctx context.Context, groupIDs []string) (int64, error) {
	ids, err := s.rowIDs(ctx, groupIDs)
	if err != nil {
		return 0, err
	}
	res := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.Host{})
	if res.Error != nil {
		return 0, fmt.Errorf("service: delete hosts: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ReorderGroups writes the order the page shows: first is first.
func (s *Hosts) ReorderGroups(ctx context.Context, groupIDs []string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, gid := range groupIDs {
			q := tx.Model(&model.Host{})
			if strings.HasPrefix(gid, "fallback_") {
				id, _ := strconv.ParseUint(strings.TrimPrefix(gid, "fallback_"), 10, 64)
				q = q.Where("id = ?", id)
			} else {
				q = q.Where("group_id = ?", gid)
			}
			if err := q.Updates(map[string]any{"priority": i + 1, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("service: reorder hosts: %w", err)
	}
	return nil
}

// AllTags is every tag in use, for the form's suggestions.
func (s *Hosts) AllTags(ctx context.Context) ([]string, error) {
	var raws []string
	if err := s.db.WithContext(ctx).Model(&model.Host{}).Where("tags <> ''").Pluck("tags", &raws).Error; err != nil {
		return nil, fmt.Errorf("service: read tags: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range raws {
		for _, t := range splitList(r) {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func (s *Hosts) group(ctx context.Context, groupID string) (*HostGroup, error) {
	rows, err := s.rowsOf(ctx, groupID)
	if err != nil {
		return nil, err
	}
	gs := groupHosts(rows)
	if len(gs) == 0 {
		return nil, fmt.Errorf("%w: no host %s", ErrNotFound, groupID)
	}
	return &gs[0], nil
}

func (s *Hosts) rowsOf(ctx context.Context, groupID string) ([]model.Host, error) {
	var rows []model.Host
	q := s.db.WithContext(ctx).Order("priority, id")
	if strings.HasPrefix(groupID, "fallback_") {
		id, err := strconv.ParseUint(strings.TrimPrefix(groupID, "fallback_"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: no host %s", ErrNotFound, groupID)
		}
		q = q.Where("id = ?", id)
	} else {
		q = q.Where("group_id = ?", groupID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("service: read host: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no host %s", ErrNotFound, groupID)
	}
	return rows, nil
}

func (s *Hosts) rowIDs(ctx context.Context, groupIDs []string) ([]uint, error) {
	var ids []uint
	for _, gid := range groupIDs {
		rows, err := s.rowsOf(ctx, gid)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no hosts selected", ErrInvalid)
	}
	return ids, nil
}

// HostVariant is one endpoint a customer's configuration is written for.
type HostVariant struct {
	Host     *model.Host // nil for the interface's own endpoint
	Endpoint string
	Port     int
}

// Label is the name the variant's config is called by.
func (v HostVariant) Label() string {
	if v.Host == nil {
		return ""
	}
	return v.Host.Name
}

// variantsFor is where a WireGuard customer connects to on an interface:
// the enabled hosts in order, or the interface's own endpoint when it has
// none. A host that leaves its address blank inherits the interface's, as
// the classic panel's does. format leaves out the hosts excluded from it. Hosts win:
// once one exists the interface's own address is not handed out on its
// own, which is exactly how the classic panel's externalProxy fan-out behaves.
func variantsFor(iface *model.Interface, format string) []HostVariant {
	own := HostVariant{Endpoint: iface.EndpointHost, Port: iface.ListenPort}
	if !iface.IsWireGuard() {
		return []HostVariant{own}
	}
	hosts := append([]model.Host(nil), iface.Hosts...)
	sort.SliceStable(hosts, func(i, j int) bool {
		if hosts[i].Priority != hosts[j].Priority {
			return hosts[i].Priority < hosts[j].Priority
		}
		return hosts[i].ID < hosts[j].ID
	})
	var out []HostVariant
	shuffle := false
	for i := range hosts {
		h := &hosts[i]
		if !h.Enabled || (format != "" && containsStr(splitList(h.ExcludeFormats), format)) {
			continue
		}
		v := HostVariant{Host: h, Endpoint: h.Address, Port: h.EffectivePort(iface.ListenPort)}
		if v.Endpoint == "" {
			v.Endpoint = iface.EndpointHost
		}
		out = append(out, v)
		if h.Shuffle {
			shuffle = true
		}
	}
	if len(out) == 0 {
		return []HostVariant{own}
	}
	if shuffle {
		for i := len(out) - 1; i > 0; i-- {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
			j := int(n.Int64())
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// withEndpoint is the interface as seen from one variant, for the renderer.
func withEndpoint(iface model.Interface, v HostVariant) model.Interface {
	iface.EndpointHost = v.Endpoint
	iface.ListenPort = v.Port
	return iface
}

// variantFilename names a per-host file after the device and the host.
func variantFilename(base string, v HostVariant) string {
	if v.Host == nil {
		return base
	}
	ext := ""
	if i := strings.LastIndex(base, "."); i > 0 {
		base, ext = base[:i], base[i:]
	}
	return base + "-" + safeFilename(v.Host.Name) + ext
}
