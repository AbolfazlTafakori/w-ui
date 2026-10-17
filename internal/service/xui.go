package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// 3x-ui panels as nodes.
//
// A 3x-ui panel keeps its inbounds; this panel reads them and holds each as
// one of its tunnels on that node, so a customer is put on one the way they
// are put on any other. What goes the other way is the customers, as that
// panel's clients, one per file. The inbound itself is edited on the 3x-ui
// panel, and the next read brings the change here.

// xuiSubnet is the address pool of a 3x-ui tunnel. Its clients have no
// address of their own, but every file here is held by an address on its
// tunnel; these are never routed or shown.
const xuiSubnet = "100.127.0.0/16"

// XUIInboundSeen is an inbound read from a 3x-ui node.
type XUIInboundSeen struct {
	model.XUIInbound
	Port int
}

// MirrorXUI brings this panel's tunnels on a 3x-ui node into line with the
// inbounds read from it: new ones added, changed ones updated, and ones gone
// from there removed -- or, while customers are still on them, switched off
// and marked gone, so nobody loses a file without the operator seeing why.
func (s *Interfaces) MirrorXUI(ctx context.Context, nodeID uint, endpointHost string, seen []XUIInboundSeen) error {
	db := s.db.WithContext(ctx)
	var have []model.Interface
	if err := db.Where("node_id = ? AND protocol = ?", nodeID, model.ProtocolXray).Find(&have).Error; err != nil {
		return fmt.Errorf("service: read 3x-ui tunnels: %w", err)
	}
	byRemote := make(map[int]*model.Interface, len(have))
	names := map[string]bool{}
	for i := range have {
		byRemote[have[i].XUI.V.RemoteID] = &have[i]
		names[strings.ToLower(have[i].Name)] = true
	}
	// Names taken by this node's other tunnels count too.
	var others []string
	if err := db.Model(&model.Interface{}).Where("node_id = ? AND protocol <> ?", nodeID, model.ProtocolXray).
		Pluck("name", &others).Error; err != nil {
		return fmt.Errorf("service: read tunnel names: %w", err)
	}
	for _, n := range others {
		names[strings.ToLower(n)] = true
	}

	present := map[int]bool{}
	for _, in := range seen {
		present[in.RemoteID] = true
		if cur, ok := byRemote[in.RemoteID]; ok {
			fields := map[string]any{}
			if cur.ListenPort != in.Port {
				fields["listen_port"] = in.Port
			}
			if cur.Enabled != in.Enabled {
				fields["enabled"] = in.Enabled
			}
			if cur.EndpointHost != endpointHost {
				fields["endpoint_host"] = endpointHost
			}
			if cur.XUI.V != in.XUIInbound {
				fields["xui"] = model.JSONField[model.XUIInbound]{V: in.XUIInbound}
			}
			if len(fields) > 0 {
				if err := db.Model(&model.Interface{}).Where("id = ?", cur.ID).Updates(fields).Error; err != nil {
					return fmt.Errorf("service: update 3x-ui tunnel %s: %w", cur.Name, err)
				}
			}
			continue
		}
		name := xuiName(in, names)
		names[strings.ToLower(name)] = true
		iface := model.Interface{
			NodeID:       nodeID,
			Name:         name,
			Protocol:     model.ProtocolXray,
			Enabled:      in.Enabled,
			ListenPort:   in.Port,
			Subnet:       xuiSubnet,
			EndpointHost: endpointHost,
			XUI:          model.JSONField[model.XUIInbound]{V: in.XUIInbound},
		}
		if err := db.Create(&iface).Error; err != nil {
			return fmt.Errorf("service: add 3x-ui tunnel %s: %w", name, err)
		}
		if s.pools != nil {
			if _, err := s.pools.Add(iface.ID, iface.Subnet); err != nil {
				return fmt.Errorf("service: address pool for %s: %w", name, err)
			}
		}
		s.log.Info("3x-ui inbound added as a tunnel", "node", nodeID, "tunnel", name, "inbound", in.RemoteID, "kind", in.Kind)
	}

	for _, cur := range byRemote {
		if present[cur.XUI.V.RemoteID] {
			continue
		}
		var held int64
		if err := db.Model(&model.Account{}).Where("interface_id = ?", cur.ID).Count(&held).Error; err != nil {
			return fmt.Errorf("service: count devices: %w", err)
		}
		if held == 0 {
			if err := db.Delete(&model.Interface{}, cur.ID).Error; err != nil {
				return fmt.Errorf("service: remove 3x-ui tunnel %s: %w", cur.Name, err)
			}
			s.log.Info("3x-ui inbound gone; its tunnel removed", "tunnel", cur.Name)
			continue
		}
		if cur.XUI.V.Gone && !cur.Enabled {
			continue
		}
		v := cur.XUI.V
		v.Gone = true
		if err := db.Model(&model.Interface{}).Where("id = ?", cur.ID).Updates(map[string]any{
			"enabled": false, "xui": model.JSONField[model.XUIInbound]{V: v},
		}).Error; err != nil {
			return fmt.Errorf("service: mark 3x-ui tunnel %s gone: %w", cur.Name, err)
		}
		s.log.Warn("3x-ui inbound removed there while customers are on it here", "tunnel", cur.Name, "devices", held)
	}
	return nil
}

// xuiName is the tunnel's name here: the inbound's remark made to fit, and
// numbered when another tunnel on the node already has it.
func xuiName(in XUIInboundSeen, taken map[string]bool) string {
	base := strings.TrimSpace(in.Remark)
	if base == "" {
		base = in.Tag
	}
	if base == "" {
		base = fmt.Sprintf("%s-%d", in.Kind, in.Port)
	}
	var b strings.Builder
	for _, r := range base {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	base = strings.TrimSpace(b.String())
	if base == "" {
		base = "inbound"
	}
	fit := func(s string, room int) string {
		r := []rune(s)
		if len(r) > room {
			r = r[:room]
		}
		return string(r)
	}
	name := fit(base, 32)
	for n := 2; taken[strings.ToLower(name)]; n++ {
		suffix := fmt.Sprintf("-%d", n)
		name = fit(base, 32-len(suffix)) + suffix
	}
	return name
}

// XUIEmail is the client email a file is given on a 3x-ui panel: unique
// there, and recognisably this panel's, so the clients the 3x-ui panel's own
// operator made are never touched.
func XUIEmail(tag string, acc *model.Account) string {
	return fmt.Sprintf("wui-%s-%d", tag, acc.ID)
}

// XUIOwns reports whether a client email on a 3x-ui panel is one this panel
// made: its own tag, and a number. Another W-UI panel on the same 3x-ui has
// a tag of its own, and the two never take each other's clients for theirs.
func XUIOwns(tag, email string) bool {
	return ownedName(email, "wui-"+tag+"-")
}

// PGUsername is a file's user on a Pasarguard panel, and PGOwns says a
// username is one this panel made, as XUIEmail and XUIOwns.
func PGUsername(tag string, acc *model.Account) string {
	return fmt.Sprintf("wui_%s_%d", tag, acc.ID)
}

func PGOwns(tag, name string) bool {
	return ownedName(name, "wui_"+tag+"_")
}

func ownedName(name, prefix string) bool {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

const keyPanelTag = "nodes.panelTag"

// PanelTag is this panel's own mark on the clients and users it makes on
// other panels: six letters and digits, drawn once and kept.
func PanelTag(ctx context.Context, db *gorm.DB) (string, error) {
	var row model.Setting
	err := db.WithContext(ctx).Where("key = ?", keyPanelTag).Limit(1).Find(&row).Error
	if err != nil {
		return "", err
	}
	if row.Value != "" {
		return row.Value, nil
	}
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	row = model.Setting{Key: keyPanelTag, Value: hex.EncodeToString(b), UpdatedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		// Made by another round meanwhile: that one is the tag.
		var again model.Setting
		if db.WithContext(ctx).Where("key = ?", keyPanelTag).Limit(1).Find(&again).Error == nil && again.Value != "" {
			return again.Value, nil
		}
		return "", err
	}
	return row.Value, nil
}

// xuiSecret is a new client's credential for an inbound of this kind: an id
// for VLESS and VMess, a key of the cipher's size for Shadowsocks 2022, and a
// password for the rest.
func xuiSecret(in model.XUIInbound) (string, error) {
	switch in.Kind {
	case "vless", "vmess":
		return newUUID()
	case "tuic":
		id, err := newUUID()
		if err != nil {
			return "", err
		}
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return id + "|" + hex.EncodeToString(b), nil
	case "wireguard", "amneziawg":
		k, err := wgkey.GeneratePrivate()
		if err != nil {
			return "", err
		}
		return k.String(), nil
	case "mtproto":
		return "", nil
	case "shadowsocks":
		size := 0
		switch strings.ToLower(in.Method) {
		case "2022-blake3-aes-128-gcm":
			size = 16
		case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
			size = 32
		}
		if size > 0 {
			b := make([]byte, size)
			if _, err := rand.Read(b); err != nil {
				return "", err
			}
			return base64.StdEncoding.EncodeToString(b), nil
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// XUIUsage is what a 3x-ui node's counters say one file spent since they
// were last read, with the counters to remember.
type XUIUsage struct {
	AccountID uint
	ClientID  uint
	Up, Down  uint64
	SeenUp    uint64
	SeenDown  uint64
}

// SaveXUICounters remembers the counters a 3x-ui node last reported, and the
// links it makes, for each file.
func SaveXUICounters(ctx context.Context, db *gorm.DB, usage []XUIUsage, links map[uint]string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range usage {
			if err := tx.Model(&model.Account{}).Where("id = ?", u.AccountID).
				Updates(map[string]any{"xui_up": u.SeenUp, "xui_down": u.SeenDown}).Error; err != nil {
				return err
			}
		}
		for id, link := range links {
			if err := tx.Model(&model.Account{}).Where("id = ?", id).Update("xui_link", link).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// XrayInboundInput is an inbound the form makes on a 3x-ui or Pasarguard
// node, as that panel's own form makes it: the protocol's settings, the
// transport and security (streamSettings) and sniffing as xray takes them,
// and what each panel keeps beside them -- on 3x-ui the inbound's own
// traffic allowance, expiry and reset, on Pasarguard the host its users'
// links are written for. Keys are drawn by /api/xray/keys and arrive here
// filled in.
type XrayInboundInput struct {
	Remark string `json:"-"`
	Port   int    `json:"-"`

	Protocol string         `json:"protocol"`
	Listen   string         `json:"listen"`
	Settings map[string]any `json:"settings"`
	Stream   map[string]any `json:"streamSettings,omitempty"`
	Sniffing map[string]any `json:"sniffing,omitempty"`

	// 3x-ui: the inbound's own traffic allowance in bytes (0 none), its
	// expiry in milliseconds since the epoch (0 none), and how often its
	// traffic starts again.
	Total           int64  `json:"total"`
	ExpiryTime      int64  `json:"expiryTime"`
	TrafficReset    string `json:"trafficReset"`
	TrafficResetDay int    `json:"trafficResetDay"`

	// Pasarguard: the host its users' links are written for.
	Host *PGHostInput `json:"host,omitempty"`

	// Customers is set here from the catalog: whether the kind takes them.
	Customers bool `json:"-"`
}

// PGHostInput is a Pasarguard host: where an inbound's users are told to
// connect, which may differ from where the inbound listens.
type PGHostInput struct {
	Address     []string `json:"address"`
	Port        int      `json:"port"`
	SNI         []string `json:"sni"`
	Host        []string `json:"host"`
	Path        string   `json:"path"`
	Fingerprint string   `json:"fingerprint"`
	ALPN        []string `json:"alpn"`
}

// RemotePanels speaks to the 3x-ui and Pasarguard panels used as nodes: the
// nodes package does (nodes.Remote). A panel run without it -- a CLI -- can
// do neither, and says so.
type RemotePanels interface {
	// CreateXrayInbound makes an inbound on the node and says what it is.
	CreateXrayInbound(ctx context.Context, node model.Node, in XrayInboundInput) (model.XUIInbound, error)
	// ImportCandidates reads the node's own users as customers to bring in.
	ImportCandidates(ctx context.Context, node model.Node, tunnels []model.Interface) ([]ImportCandidate, error)
}

// SetRemote gives the tunnels' service the panels used as nodes.
func (s *Interfaces) SetRemote(r RemotePanels) { s.remote = r }

var trafficResets = map[string]bool{"": true, "never": true, "hourly": true, "daily": true, "weekly": true, "monthly": true}

// check refuses what the node cannot take, and what xray would refuse
// there, before anything is sent: a protocol that server's version does not
// have, a security that protocol cannot carry, a transport it does not take.
func (in *XrayInboundInput) check(node *model.Node) error {
	kind, ok := InboundKindFor(node.Kind, node.Version, in.Protocol)
	if !ok {
		return invalidField("protocol", "%s (%s) has no %s inbound", node.Name, node.Version, in.Protocol)
	}
	if in.Port < 1 || in.Port > 65535 {
		return invalidField("listenPort", "a port is from 1 to 65535")
	}
	if in.Settings == nil {
		in.Settings = map[string]any{}
	}
	// Clients are this panel's to add, one per file, after the inbound is made.
	if _, has := in.Settings["clients"]; has || kind.Customers {
		in.Settings["clients"] = []any{}
	}
	if !kind.Stream {
		// A kind with no transport takes, at most, a stream that says so:
		// 3x-ui writes {"security": "none"} for WireGuard.
		if len(in.Stream) != 1 || str(in.Stream["security"]) != "none" {
			in.Stream = nil
		}
	} else {
		if in.Stream == nil {
			in.Stream = map[string]any{}
		}
		network, _ := in.Stream["network"].(string)
		if network == "" {
			network = "tcp"
			in.Stream["network"] = network
		}
		if network == "raw" {
			network = "tcp"
		}
		if len(kind.Transports) > 0 && !inList(kind.Transports, network) {
			return invalidField("xray.network", "%s does not take the %s transport", in.Protocol, network)
		}
		security, _ := in.Stream["security"].(string)
		if security == "" {
			security = "none"
			in.Stream["security"] = security
		}
		if !inList(kind.Securities, security) {
			return invalidField("xray.security", "%s cannot have %s security", in.Protocol, security)
		}
		if security == "reality" {
			if network != "tcp" && network != "grpc" && network != "xhttp" {
				return invalidField("xray.security", "Reality is over TCP, gRPC or XHTTP")
			}
			rs, _ := in.Stream["realitySettings"].(map[string]any)
			if rs == nil || str(rs["privateKey"]) == "" {
				return invalidField("xray.reality.privateKey", "Reality needs a private key: draw one")
			}
			if names, _ := rs["serverNames"].([]any); len(names) == 0 {
				return invalidField("xray.reality.serverNames", "Reality needs at least one server name")
			}
			if str(rs["dest"]) == "" && str(rs["target"]) == "" {
				return invalidField("xray.reality.target", "Reality needs the site it borrows its handshake from")
			}
		}
		if security == "tls" {
			ts, _ := in.Stream["tlsSettings"].(map[string]any)
			certs, _ := ts["certificates"].([]any)
			ok := false
			for _, c := range certs {
				m, _ := c.(map[string]any)
				if str(m["certificateFile"]) != "" || len(asList(m["certificate"])) > 0 {
					ok = true
				}
			}
			if !ok {
				return invalidField("xray.tls.certificates", "TLS needs the server's certificate: a file on that server, or its text")
			}
		}
	}
	if !trafficResets[in.TrafficReset] {
		return invalidField("xray.trafficReset", "the traffic resets never, hourly, daily, weekly or monthly")
	}
	if in.TrafficResetDay < 0 || in.TrafficResetDay > 31 {
		return invalidField("xray.trafficResetDay", "the reset day is from 1 to 31")
	}
	if in.Total < 0 || in.ExpiryTime < 0 {
		return invalidField("xray.total", "a traffic allowance or expiry cannot be negative")
	}
	return nil
}

func inList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// createXray makes the inbound on the node and holds it here as a tunnel
// straight away, rather than waiting for the next read to bring it.
func (s *Interfaces) createXray(ctx context.Context, node *model.Node, in CreateInterfaceInput) (*model.Interface, error) {
	if in.Xray == nil {
		return nil, invalidField("protocol", "%s is a %s panel: choose one of its protocols", node.Name, panelName(node.Kind))
	}
	x := *in.Xray
	x.Remark = strings.TrimSpace(in.Name)
	x.Port = in.ListenPort
	if x.Remark == "" {
		return nil, invalidField("name", "an inbound needs a name")
	}
	// A remark on that panel, not a network device here: any text, short.
	if err := checkText("name", x.Remark, 32); err != nil {
		return nil, err
	}
	var clash int64
	if err := s.db.WithContext(ctx).Model(&model.Interface{}).
		Where("node_id = ? AND LOWER(name) = LOWER(?)", node.ID, x.Remark).Count(&clash).Error; err != nil {
		return nil, fmt.Errorf("service: check interface name: %w", err)
	}
	if clash > 0 {
		return nil, invalidField("name", "a tunnel called %q already exists on %s", x.Remark, node.Name)
	}
	if err := s.db.WithContext(ctx).Model(&model.Interface{}).
		Where("node_id = ? AND listen_port = ?", node.ID, x.Port).Count(&clash).Error; err != nil {
		return nil, fmt.Errorf("service: check port: %w", err)
	}
	if clash > 0 {
		return nil, invalidField("listenPort", "port %d is already an inbound's on %s", x.Port, node.Name)
	}
	if err := x.check(node); err != nil {
		return nil, err
	}
	if k, ok := InboundKindFor(node.Kind, node.Version, x.Protocol); ok {
		x.Customers = k.Customers
	}
	if s.remote == nil {
		return nil, fmt.Errorf("%w: this panel cannot make inbounds on %s", ErrInvalid, node.Name)
	}
	made, err := s.remote.CreateXrayInbound(ctx, *node, x)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if k, ok := InboundKindFor(node.Kind, node.Version, x.Protocol); ok {
		made.Customers = k.Customers
	}
	// The sync round may have read the new inbound from there before this
	// got back: it is that tunnel, brought up to date, not a second one.
	var already model.Interface
	if err := s.db.WithContext(ctx).Where("node_id = ? AND protocol = ? AND name = ?", node.ID, model.ProtocolXray, x.Remark).
		Limit(1).Find(&already).Error; err == nil && already.ID != 0 {
		if err := s.db.WithContext(ctx).Model(&already).Updates(map[string]any{
			"xui": model.JSONField[model.XUIInbound]{V: made}, "listen_port": x.Port, "enabled": true,
		}).Error; err != nil {
			return nil, fmt.Errorf("service: hold the new inbound: %w", err)
		}
		already.XUI = model.JSONField[model.XUIInbound]{V: made}
		return &already, nil
	}
	iface := model.Interface{
		NodeID: node.ID, Name: x.Remark, Protocol: model.ProtocolXray, Enabled: true,
		ListenPort: x.Port, Subnet: xuiSubnet, EndpointHost: hostOf(node.Address),
		XUI: model.JSONField[model.XUIInbound]{V: made},
	}
	if err := s.db.WithContext(ctx).Create(&iface).Error; err != nil {
		return nil, fmt.Errorf("service: hold the new inbound: %w", err)
	}
	if s.pools != nil {
		if _, err := s.pools.Add(iface.ID, iface.Subnet); err != nil {
			return nil, err
		}
	}
	s.log.Info("inbound made on a node", "node", node.Name, "tunnel", iface.Name, "protocol", x.Protocol, "port", x.Port)
	return &iface, nil
}
