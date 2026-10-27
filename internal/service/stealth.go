package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
)

// newStealthSecret is a device's transport secret, as an account keeps it.
func newStealthSecret() (string, error) {
	s, err := stealth.NewSecret()
	if err != nil {
		return "", err
	}
	return s.String(), nil
}

// EnsureStealthSecrets gives every WireGuard device this panel holds the
// records of a transport secret, when it has none: the devices made before
// the stealth transports were. A device on a tunnel another panel manages
// is that panel's to give one, and its secret arrives with it.
//
// Every device gets one, whether its tunnel has a stealth transport or not,
// so turning one on later needs nothing more than the customer's file again.
func EnsureStealthSecrets(ctx context.Context, db *gorm.DB) error {
	var ids []uint
	if err := db.WithContext(ctx).Model(&model.Account{}).
		Joins("JOIN interfaces ON interfaces.id = accounts.interface_id").
		Where("interfaces.protocol = ? AND interfaces.managed = ? AND (accounts.stealth_secret = '' OR accounts.stealth_secret IS NULL)",
			model.ProtocolWireGuard, false).
		Pluck("accounts.id", &ids).Error; err != nil {
		return fmt.Errorf("service: find devices without a transport secret: %w", err)
	}
	for _, id := range ids {
		secret, err := newStealthSecret()
		if err != nil {
			return err
		}
		// Only where there is still none, so a pass that races another
		// never replaces a secret a customer was already handed. Its
		// updated_at is left: the files a customer has did not change, and
		// their date is read from it.
		if err := db.WithContext(ctx).Model(&model.Account{}).
			Where("id = ? AND (stealth_secret = '' OR stealth_secret IS NULL)", id).
			UpdateColumn("stealth_secret", secret).Error; err != nil {
			return fmt.Errorf("service: store a transport secret: %w", err)
		}
	}
	return nil
}

// checkWireGuardTransport refuses a WireGuard transport there is none of.
func checkWireGuardTransport(name string) error {
	switch name {
	case "", model.TransportUDP, model.TransportWuiUDP:
		return nil
	}
	return invalidField("transport", "a WireGuard tunnel's transport is udp or wui-udp, not %q", name)
}

// checkStealth checks a WireGuard tunnel's transport as it would be, for a
// tunnel of mode on listenPort. selfID is the tunnel when it exists, and
// held the port its gateway already holds, which is no clash with itself.
// local says the tunnel is on this machine, which can be asked whether the
// port is free. Plain UDP comes back as the zero value.
func (s *Interfaces) checkStealth(ctx context.Context, nodeID, selfID uint, mode model.InterfaceMode,
	listenPort int, p model.StealthParams, held int, local bool,
) (model.StealthParams, error) {
	if err := checkWireGuardTransport(p.Transport); err != nil {
		return p, err
	}
	if p.Transport == "" || p.Transport == model.TransportUDP {
		if p.Port != 0 {
			return p, invalidField("transportPort", "plain UDP is on the tunnel's own port and takes no other")
		}
		return model.StealthParams{}, nil
	}
	if mode == model.ModeAmnezia {
		return p, invalidField("transport", "only a standard WireGuard tunnel takes %s; AmneziaWG's packets are its own", p.Transport)
	}
	switch {
	case p.Port == 0:
		return p, invalidField("transportPort", "a port of its own is needed for %s, beside the tunnel's", p.Transport)
	case p.Port < 1 || p.Port > 65535:
		return p, invalidField("transportPort", "port %d is out of range", p.Port)
	case p.Port == listenPort:
		return p, invalidField("transportPort", "port %d is the tunnel's own; %s needs one beside it", p.Port, p.Transport)
	}
	network := model.TransportNetwork(p.Transport)
	if err := s.portClash(ctx, nodeID, selfID, p.Port, []string{network}, "transportPort"); err != nil {
		return p, err
	}
	if local && p.Port != held {
		if err := checkPortFree(p.Port, network); err != nil {
			return p, &FieldError{Field: "transportPort", Err: err}
		}
	}
	return p, nil
}

// stealthClash is the tunnel on a server, other than selfID, whose stealth
// transport takes port on one of transports, or nil.
func (s *Interfaces) stealthClash(ctx context.Context, nodeID, selfID uint, port int, transports []string) (*model.Interface, error) {
	var withStealth []model.Interface
	q := s.db.WithContext(ctx).Where("node_id = ? AND protocol = ? AND stealth IS NOT NULL AND stealth <> ''", nodeID, model.ProtocolWireGuard)
	if selfID != 0 {
		q = q.Where("id <> ?", selfID)
	}
	if err := q.Find(&withStealth).Error; err != nil {
		return nil, fmt.Errorf("service: check stealth ports: %w", err)
	}
	for i := range withStealth {
		name, p := withStealth[i].StealthTransport()
		if p == port && slices.Contains(transports, model.TransportNetwork(name)) {
			return &withStealth[i], nil
		}
	}
	return nil, nil
}

// updateStealth takes a WireGuard tunnel's transport and its port. A device
// needs its file again to use a new one, but nobody's tunnel is touched: the
// gateway stands beside WireGuard, which goes on as it was.
func (s *Interfaces) updateStealth(ctx context.Context, iface *model.Interface, in UpdateInterfaceInput, fields map[string]any) error {
	if iface.Protocol != model.ProtocolWireGuard {
		if in.TransportPort != nil && *in.TransportPort != 0 {
			return invalidField("transportPort", "only a WireGuard tunnel's stealth transport takes a port of its own")
		}
		return nil
	}
	if in.Transport == nil && in.TransportPort == nil {
		return nil
	}
	want := iface.Stealth.V
	if in.Transport != nil {
		want.Transport = strings.ToLower(strings.TrimSpace(*in.Transport))
		if want.Transport == model.TransportUDP || want.Transport == "" {
			want = model.StealthParams{}
		}
	}
	if in.TransportPort != nil && want.Transport != "" {
		want.Port = *in.TransportPort
	}
	if want == iface.Stealth.V {
		return nil
	}
	port := iface.ListenPort
	if v, ok := fields["listen_port"].(int); ok {
		port = v
	}
	mode := iface.Mode
	if v, ok := fields["mode"].(model.InterfaceMode); ok {
		mode = v
	}
	local, err := s.isLocal(ctx, iface.NodeID)
	if err != nil {
		return err
	}
	p, err := s.checkStealth(ctx, iface.NodeID, iface.ID, mode, port, want, iface.Stealth.V.Port, local)
	if err != nil {
		return err
	}
	fields["stealth"] = model.JSON(p)
	return nil
}
