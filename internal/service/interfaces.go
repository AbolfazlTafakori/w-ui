// Package service holds the panel's business logic: everything the API does
// beyond decoding a request.
//
// Nothing here talks to the kernel. Services write the desired state to the
// database; the reconciler is what carries that state down to the data plane.
// That split is why a failed kernel call can never leave the panel's records
// disagreeing with what a customer was sold.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/netip"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// Validation errors. The API maps these to 400 rather than 500.
var (
	ErrInvalid       = errors.New("service: invalid input")
	ErrNotFound      = errors.New("service: not found")
	ErrDeviceLimit   = errors.New("service: device limit reached")
	ErrPoolExhausted = errors.New("service: no addresses left on this interface")
)

// Interfaces manages server-side listening endpoints.
type Interfaces struct {
	db    *gorm.DB
	pools *ipam.Pools
	log   *slog.Logger
}

// NewInterfaces builds the interface service.
func NewInterfaces(db *gorm.DB, pools *ipam.Pools, log *slog.Logger) *Interfaces {
	return &Interfaces{db: db, pools: pools, log: log}
}

// CreateInterfaceInput describes a new interface.
type CreateInterfaceInput struct {
	Name         string              `json:"name"`
	Protocol     model.Protocol      `json:"protocol"`
	ListenPort   int                 `json:"listenPort"`
	Subnet       string              `json:"subnet"`
	EndpointHost string              `json:"endpointHost"`
	MTU          int                 `json:"mtu"`
	DNS          string              `json:"dns"`
	NATInterface string              `json:"natInterface"`
	Mode         model.InterfaceMode `json:"mode"`

	// Transport is udp or tcp, and only means anything for OpenVPN — WireGuard
	// has no other option.
	//
	// It is worth exposing because TCP on 443 is what gets through a network
	// that inspects traffic and drops what it does not recognise: to anything
	// watching, the connection is a machine talking HTTPS to a web server.
	// It costs throughput, which is why UDP stays the default, and it is the
	// answer only when UDP is being blocked.
	Transport string `json:"transport"`

	// Enabled lets a tunnel be created switched off, so it can be prepared
	// before anyone is put on it. Absent means enabled, which is what someone
	// filling in this form almost always wants.
	Enabled *bool `json:"enabled"`
}

// Create validates and stores a new interface, generating the server key pair
// for WireGuard and an obfuscation profile when the mode calls for one.
func (s *Interfaces) Create(ctx context.Context, in CreateInterfaceInput) (*model.Interface, error) {
	if err := s.validate(&in); err != nil {
		return nil, err
	}

	iface := model.Interface{
		Name:         in.Name,
		NodeID:       1,
		Protocol:     in.Protocol,
		ListenPort:   in.ListenPort,
		Subnet:       in.Subnet,
		EndpointHost: in.EndpointHost,
		MTU:          in.MTU,
		DNS:          in.DNS,
		NATInterface: in.NATInterface,
		Mode:         in.Mode,
		Enabled:      in.Enabled == nil || *in.Enabled,
	}

	if in.Protocol == model.ProtocolWireGuard {
		pair, err := wgkey.NewPair()
		if err != nil {
			return nil, err
		}
		iface.PrivateKey = pair.Private.String()
		iface.PublicKey = pair.Public.String()

		if in.Mode == model.ModeAmnezia {
			iface.AWG = model.JSON(NewAWGParams())
		}
	} else {
		// Every interface gets its own certificate authority, generated here
		// rather than by easy-rsa so that the interface row is the only place
		// this material is stored. Leaving duplicate-cn off is what makes one
		// credential mean one session: a second login evicts the first, so the
		// device limit is enforced by the protocol rather than policed after
		// the fact.
		params, err := ovpnconf.NewPKI(iface.Name)
		if err != nil {
			return nil, err
		}
		if in.Transport != "" {
			params.Transport = in.Transport
		}
		iface.OpenVPN = model.JSON(params)
	}

	if err := s.db.WithContext(ctx).Create(&iface).Error; err != nil {
		return nil, fmt.Errorf("service: create interface: %w", err)
	}
	if _, err := s.pools.Add(iface.ID, iface.Subnet); err != nil {
		return nil, err
	}

	s.log.Info("interface created",
		"name", iface.Name, "protocol", iface.Protocol,
		"port", iface.ListenPort, "subnet", iface.Subnet, "mode", iface.Mode)
	return &iface, nil
}

func (s *Interfaces) validate(in *CreateInterfaceInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.EndpointHost = strings.TrimSpace(in.EndpointHost)

	if in.Name == "" {
		return invalidField("name", "name is required")
	}
	if !in.Protocol.Valid() {
		return invalidField("protocol", "unknown protocol %q", in.Protocol)
	}
	if !backend.Supports(in.Protocol) {
		return invalidField("protocol", "no driver available for %q on this server", in.Protocol)
	}
	if in.ListenPort < 1 || in.ListenPort > 65535 {
		return invalidField("listenPort", "listen port %d is out of range", in.ListenPort)
	}
	// Transport is OpenVPN's alone. WireGuard is UDP and nothing else, so
	// accepting the field there would be accepting a setting that does nothing.
	switch in.Transport {
	case "", "udp", "tcp":
	default:
		return invalidField("transport", "transport must be udp or tcp, not %q", in.Transport)
	}
	if in.Transport != "" && in.Protocol != model.ProtocolOpenVPN {
		return invalidField("transport",
			"only OpenVPN has a choice of transport; WireGuard is always UDP")
	}

	// Checked here rather than discovered later. A tunnel on a port something
	// else already holds is created, reported as configured, and simply never
	// reachable — the kind of failure that is only ever found by a customer.
	//
	// Checked against the transport this interface will actually use: UDP 443
	// being free says nothing about TCP 443, which is the port most worth
	// asking about because a web server is so often already on it.
	proto := "udp"
	if in.Transport == "tcp" {
		proto = "tcp"
	}
	if err := checkPortFree(in.ListenPort, proto); err != nil {
		return &FieldError{Field: "listenPort", Err: err}
	}
	if in.EndpointHost == "" {
		return invalidField("endpointHost", "endpoint host is required; it is what clients dial")
	}
	if _, err := netip.ParsePrefix(in.Subnet); err != nil {
		return invalidField("subnet", "subnet %q: %v", in.Subnet, err)
	}
	if in.MTU == 0 {
		in.MTU = 1420
	}
	if in.MTU < 576 || in.MTU > 9000 {
		return invalidField("mtu", "MTU %d is out of range (576-9000)", in.MTU)
	}
	if in.DNS == "" {
		in.DNS = "1.1.1.1"
	}
	if in.NATInterface == "" {
		in.NATInterface = "eth0"
	}
	switch in.Mode {
	case "":
		in.Mode = model.ModeStandard
	case model.ModeStandard, model.ModeAmnezia:
	default:
		return invalidField("mode", "unknown mode %q", in.Mode)
	}
	if in.Mode == model.ModeAmnezia && in.Protocol != model.ProtocolWireGuard {
		return invalidField("mode", "AmneziaWG mode applies to WireGuard only")
	}
	return nil
}

// List returns every interface.
func (s *Interfaces) List(ctx context.Context) ([]model.Interface, error) {
	var out []model.Interface
	if err := s.db.WithContext(ctx).Order("id").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("service: list interfaces: %w", err)
	}
	return out, nil
}

// Get loads one interface.
func (s *Interfaces) Get(ctx context.Context, id uint) (*model.Interface, error) {
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

// Profile is the one OpenVPN file every customer on this tunnel uses.
//
// An OpenVPN profile carries nothing about who is connecting: the certificate
// authority, the addresses and the cipher belong to the tunnel, and the
// customer is settled at connect time by the username and password they type.
// So there is one file per tunnel, handed out once, and selling somebody access
// is creating them credentials rather than generating them a file.
//
// That is also what makes taking access away work. Deleting a customer removes
// their credentials; the file they still have on their phone connects to
// nothing, and nobody else's file has to change.
//
// WireGuard has no equivalent — its profile carries the device's own private
// key — so this is refused there rather than returning something misleading.
func (s *Interfaces) Profile(ctx context.Context, id uint) (name, body string, err error) {
	iface, err := s.Get(ctx, id)
	if err != nil {
		return "", "", err
	}
	if iface.Protocol != model.ProtocolOpenVPN {
		return "", "", fmt.Errorf(
			"%w: a WireGuard profile is per device, because it carries that device's own key",
			ErrInvalid)
	}

	var hosts []model.Host
	if err := s.db.WithContext(ctx).
		Where("interface_id = ? AND enabled = ?", iface.ID, true).
		Order("priority, id").Find(&hosts).Error; err != nil {
		return "", "", fmt.Errorf("service: read hosts: %w", err)
	}
	iface.Hosts = hosts

	return iface.Name + ".ovpn", ovpnconf.RenderProfile(iface), nil
}

// Load is what an interface is carrying: how many customers sit on it, how many
// devices they hold, and how much they have used between them.
type Load struct {
	Clients   int64  `json:"clients"`
	Devices   int64  `json:"devices"`
	UsedBytes uint64 `json:"usedBytes"`
}

// Loads returns per-interface totals, keyed by interface id.
//
// This is two grouped queries rather than a query per interface: a panel with
// several interfaces should not pay a round trip for each one every time the
// list is drawn.
func (s *Interfaces) Loads(ctx context.Context) (map[uint]Load, error) {
	out := map[uint]Load{}

	var counts []struct {
		InterfaceID uint
		Clients     int64
		Devices     int64
	}
	err := s.db.WithContext(ctx).Model(&model.Account{}).
		Select("interface_id, COUNT(DISTINCT client_id) AS clients, COUNT(*) AS devices").
		Group("interface_id").
		Scan(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("service: count interface load: %w", err)
	}
	for _, c := range counts {
		out[c.InterfaceID] = Load{Clients: c.Clients, Devices: c.Devices}
	}

	// A client's devices all live on one interface, so summing each client once
	// per interface gives that interface's traffic without double counting.
	var usage []struct {
		InterfaceID uint
		Used        uint64
	}
	err = s.db.WithContext(ctx).
		Table("(?) AS a", s.db.Model(&model.Account{}).
			Select("DISTINCT client_id, interface_id")).
		Joins("JOIN clients c ON c.id = a.client_id").
		Select("a.interface_id AS interface_id, COALESCE(SUM(c.used_bytes), 0) AS used").
		Group("a.interface_id").
		Scan(&usage).Error
	if err != nil {
		return nil, fmt.Errorf("service: sum interface traffic: %w", err)
	}
	for _, u := range usage {
		l := out[u.InterfaceID]
		l.UsedBytes = u.Used
		out[u.InterfaceID] = l
	}
	return out, nil
}

// UpdateInterfaceInput carries the fields an operator may change after
// creation. Subnet and protocol are absent on purpose: changing either would
// invalidate every config already handed out.
type UpdateInterfaceInput struct {
	Enabled      *bool   `json:"enabled"`
	EndpointHost *string `json:"endpointHost"`
	MTU          *int    `json:"mtu"`
	DNS          *string `json:"dns"`
	NATInterface *string `json:"natInterface"`

	// Transport switches an OpenVPN tunnel between udp and tcp. Every customer
	// on it needs their configuration again afterwards, so it is not a setting
	// to change idly.
	Transport *string `json:"transport"`
}

// Update applies changes to an interface.
func (s *Interfaces) Update(ctx context.Context, id uint, in UpdateInterfaceInput) (*model.Interface, error) {
	iface, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	fields := map[string]any{}
	if in.Enabled != nil {
		fields["enabled"] = *in.Enabled
	}
	if in.EndpointHost != nil {
		host := strings.TrimSpace(*in.EndpointHost)
		if host == "" {
			return nil, invalidField("endpointHost", "endpoint host is required")
		}
		fields["endpoint_host"] = host
	}
	if in.MTU != nil {
		if *in.MTU < 576 || *in.MTU > 9000 {
			return nil, invalidField("mtu", "MTU %d is out of range (576-9000)", *in.MTU)
		}
		fields["mtu"] = *in.MTU
	}
	if in.DNS != nil {
		fields["dns"] = strings.TrimSpace(*in.DNS)
	}
	if in.NATInterface != nil {
		fields["nat_interface"] = strings.TrimSpace(*in.NATInterface)
	}

	// Switching an OpenVPN tunnel between UDP and TCP.
	//
	// It rewrites both halves of the configuration — the server's proto line
	// and every customer's — so a customer whose file still says udp cannot
	// connect afterwards. Said plainly in the log rather than left for the
	// operator to work out from support messages.
	if in.Transport != nil {
		want := strings.ToLower(strings.TrimSpace(*in.Transport))
		switch want {
		case "udp", "tcp":
		default:
			return nil, invalidField("transport", "transport must be udp or tcp, not %q", want)
		}
		if iface.Protocol != model.ProtocolOpenVPN {
			return nil, invalidField("transport",
				"only OpenVPN has a choice of transport; WireGuard is always UDP")
		}
		params := iface.OpenVPN.V
		if params.Transport != want {
			// The port has to be free on the transport being moved to. UDP 443
			// being ours says nothing about TCP 443, where a web server usually
			// already is.
			if err := checkPortFree(iface.ListenPort, want); err != nil {
				return nil, &FieldError{Field: "transport", Err: err}
			}
			params.Transport = want
			fields["open_vpn"] = model.JSON(params)
			s.log.Warn("openvpn transport changed; every customer needs their configuration again",
				"interface", iface.Name, "from", iface.OpenVPN.V.Transport, "to", want)
		}
	}

	if len(fields) == 0 {
		return iface, nil
	}
	if err := s.db.WithContext(ctx).Model(&model.Interface{}).
		Where("id = ?", id).Updates(fields).Error; err != nil {
		return nil, fmt.Errorf("service: update interface: %w", err)
	}
	s.log.Info("interface updated", "id", id, "name", iface.Name, "fields", len(fields))
	return s.Get(ctx, id)
}

// Delete removes an interface.
//
// It refuses while accounts still sit on it. Cascading would silently destroy
// working customer configs, and an operator who means that should remove the
// clients first and see how many there were.
func (s *Interfaces) Delete(ctx context.Context, id uint) error {
	iface, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	var accounts int64
	if err := s.db.WithContext(ctx).Model(&model.Account{}).
		Where("interface_id = ?", id).Count(&accounts).Error; err != nil {
		return fmt.Errorf("service: count accounts: %w", err)
	}
	if accounts > 0 {
		return fmt.Errorf("%w: %d device(s) still use %q; remove those clients first",
			ErrInvalid, accounts, iface.Name)
	}

	if err := s.db.WithContext(ctx).Delete(&model.Interface{}, id).Error; err != nil {
		return fmt.Errorf("service: delete interface: %w", err)
	}
	s.log.Info("interface deleted", "id", id, "name", iface.Name)
	return nil
}

// Usage reports how full an interface's address pool is.
type Usage struct {
	InterfaceID uint `json:"interfaceId"`
	Allocated   int  `json:"allocated"`
	Capacity    int  `json:"capacity"`
}

// PoolUsage returns allocation counts for one interface.
func (s *Interfaces) PoolUsage(id uint) (Usage, error) {
	alloc, err := s.pools.Get(id)
	if err != nil {
		return Usage{}, err
	}
	return Usage{InterfaceID: id, Allocated: alloc.InUse(), Capacity: alloc.Capacity()}, nil
}

// NewAWGParams generates an AmneziaWG obfuscation profile.
//
// S1-S4 and H1-H4 are interface-wide: every client of this interface must carry
// the same values, which is why they are generated once here rather than per
// customer. Jc/Jmin/Jmax only affect what the sender pads with and are ignored
// by the far side, so their exact values matter less.
//
// The H1-H4 ranges are kept far apart so the four message types stay
// distinguishable to a peer that validates them.
func NewAWGParams() model.AWGParams {
	between := func(lo, hi int) int { return lo + rand.Intn(hi-lo+1) }

	jmin := between(40, 89)
	return model.AWGParams{
		Jc:   between(3, 6),
		Jmin: jmin,
		Jmax: between(jmin+50, 250),

		// Kept at or above 12 bytes so AmneziaWG 3.x header protection, which
		// draws its nonce from the first 12 bytes of this padding, can be
		// switched on later without regenerating every client config.
		S1: between(15, 150),
		S2: between(15, 150),
		S3: between(12, 55),
		S4: between(12, 27),

		H1: uint32(between(100000, 500000)),
		H2: uint32(between(1000000, 5000000)),
		H3: uint32(between(10000000, 50000000)),
		H4: uint32(between(100000000, 500000000)),
	}
}
