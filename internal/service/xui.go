package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
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
func XUIEmail(acc *model.Account) string {
	return fmt.Sprintf("wui-%d", acc.ID)
}

// XUIOwns reports whether a client email on a 3x-ui panel is one this panel
// made.
func XUIOwns(email string) bool {
	rest, ok := strings.CutPrefix(email, "wui-")
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

// xuiSecret is a new client's credential for an inbound of this kind: an id
// for VLESS and VMess, a key of the cipher's size for Shadowsocks 2022, and a
// password for the rest.
func xuiSecret(in model.XUIInbound) (string, error) {
	switch in.Kind {
	case "vless", "vmess":
		return newUUID()
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
// node: what an operator decides, the keys being drawn where it is made.
type XrayInboundInput struct {
	Remark string `json:"-"`
	Port   int    `json:"-"`
	// Kind is vless, vmess, trojan or shadowsocks.
	Kind string `json:"kind"`
	// Transport is tcp, ws or grpc; Path is the WebSocket path or the gRPC
	// service name.
	Transport string `json:"transport"`
	Path      string `json:"path"`
	// Security is none or reality; Reality is for VLESS over TCP, and SNI is
	// the site it borrows its handshake from.
	Security string `json:"security"`
	SNI      string `json:"sni"`
	// Method is a Shadowsocks inbound's cipher.
	Method string `json:"method"`
}

// XrayProtocols are the kinds of inbound this panel makes on a 3x-ui or
// Pasarguard node.
var XrayProtocols = []string{"vless", "vmess", "trojan", "shadowsocks"}

// CreateXrayInbound makes an inbound on a node of another panel; set by the
// nodes package, which speaks to them.
var CreateXrayInbound func(ctx context.Context, node model.Node, in XrayInboundInput) (model.XUIInbound, error)

func (in *XrayInboundInput) check() error {
	known := false
	for _, k := range XrayProtocols {
		known = known || in.Kind == k
	}
	if !known {
		return invalidField("protocol", "an inbound here is VLESS, VMess, Trojan or Shadowsocks")
	}
	if in.Port < 1 || in.Port > 65535 {
		return invalidField("listenPort", "a port is from 1 to 65535")
	}
	if in.Transport == "" {
		in.Transport = "tcp"
	}
	switch in.Transport {
	case "tcp":
		in.Path = ""
	case "ws":
		if in.Path == "" {
			in.Path = "/"
		}
		if !strings.HasPrefix(in.Path, "/") {
			in.Path = "/" + in.Path
		}
	case "grpc":
		if in.Path == "" {
			in.Path = "grpc"
		}
	default:
		return invalidField("xray.transport", "the transport is TCP, WebSocket or gRPC")
	}
	if in.Kind == "shadowsocks" {
		in.Transport, in.Path = "tcp", ""
	}
	if in.Security == "" {
		in.Security = "none"
	}
	switch in.Security {
	case "none":
		in.SNI = ""
	case "reality":
		if in.Kind != "vless" || in.Transport != "tcp" {
			return invalidField("xray.security", "Reality is for VLESS over TCP")
		}
		in.SNI = strings.TrimSpace(in.SNI)
		if in.SNI == "" {
			in.SNI = "www.speedtest.net"
		}
		if !validHostName(in.SNI) {
			return invalidField("xray.sni", "the Reality site is a host name, such as www.speedtest.net")
		}
	default:
		return invalidField("xray.security", "the security is none or Reality")
	}
	if in.Kind == "shadowsocks" {
		switch in.Method {
		case "":
			in.Method = "2022-blake3-aes-128-gcm"
		case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "chacha20-ietf-poly1305", "aes-256-gcm", "aes-128-gcm":
		default:
			return invalidField("xray.method", "that Shadowsocks cipher is not one xray takes")
		}
	} else {
		in.Method = ""
	}
	return nil
}

func validHostName(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

// createXray makes the inbound on the node and holds it here as a tunnel
// straight away, rather than waiting for the next read to bring it.
func (s *Interfaces) createXray(ctx context.Context, node *model.Node, in CreateInterfaceInput) (*model.Interface, error) {
	if in.Xray == nil {
		return nil, invalidField("protocol", "%s is a %s panel: choose VLESS, VMess, Trojan or Shadowsocks", node.Name, panelName(node.Kind))
	}
	x := *in.Xray
	x.Remark = strings.TrimSpace(in.Name)
	x.Port = in.ListenPort
	// A remark on that panel, not a network device here: any text, short.
	if x.Remark == "" {
		return nil, invalidField("name", "an inbound needs a name")
	}
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
	if err := x.check(); err != nil {
		return nil, err
	}
	if CreateXrayInbound == nil {
		return nil, fmt.Errorf("%w: this panel cannot make inbounds on %s", ErrInvalid, node.Name)
	}
	made, err := CreateXrayInbound(ctx, *node, x)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
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
	s.log.Info("inbound made on a node", "node", node.Name, "tunnel", iface.Name, "kind", x.Kind, "port", x.Port)
	return &iface, nil
}
