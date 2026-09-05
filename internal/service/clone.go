package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Making another tunnel like an existing one.
//
// A second tunnel is rarely a new decision. It is the same protocol, the same
// MTU, the same DNS and the same obfuscation settings on another port, because
// the reason for it is a port being filtered rather than anything about how the
// tunnel should work. Retyping all of that is how the copy ends up subtly
// different from the original, and the difference is found later by a customer
// whose configuration does not connect.
//
// What is deliberately not copied is identity. A clone gets its own name, its
// own port, its own subnet and its own keys — two tunnels sharing a server key
// would mean two servers a customer cannot tell apart, and two sharing a subnet
// would hand the same address to two people.

// CloneInput is what the operator chooses about the copy.
type CloneInput struct {
	Name       string `json:"name"`
	ListenPort int    `json:"listenPort"`
	Subnet     string `json:"subnet"`

	// EndpointHost is optional: a clone made to sit beside the original on the
	// same machine is reached at the same address, so it defaults to it.
	EndpointHost string `json:"endpointHost"`
}

// Clone copies an interface's settings into a new one.
//
// Customers are not copied. A clone is an empty tunnel that behaves like the
// original, and giving it the original's customers would double every
// allocation without anybody asking.
func (s *Interfaces) Clone(ctx context.Context, id uint, in CloneInput) (*model.Interface, error) {
	var src model.Interface
	if err := s.db.WithContext(ctx).First(&src, id).Error; err != nil {
		return nil, fmt.Errorf("%w: no interface %d", ErrNotFound, id)
	}
	// A tunnel this panel does not own is one the central panel would overwrite
	// on its next sync, and a copy of it would be a tunnel nobody manages.
	if src.Managed {
		return nil, fmt.Errorf("%w: %s belongs to another panel and cannot be copied here",
			ErrInvalid, src.Name)
	}

	in.Name = strings.TrimSpace(in.Name)
	in.Subnet = strings.TrimSpace(in.Subnet)
	in.EndpointHost = strings.TrimSpace(in.EndpointHost)
	if in.EndpointHost == "" {
		in.EndpointHost = src.EndpointHost
	}

	create := CreateInterfaceInput{
		Name:         in.Name,
		Protocol:     src.Protocol,
		NodeID:       src.NodeID,
		ListenPort:   in.ListenPort,
		Subnet:       in.Subnet,
		EndpointHost: in.EndpointHost,

		// Everything about how the tunnel behaves, which is the whole reason
		// for copying rather than creating.
		MTU:          src.MTU,
		DNS:          src.DNS,
		NATInterface: src.NATInterface,
		Mode:         src.Mode,
	}

	if ovpn := src.OpenVPN.V; ovpn.Transport != "" {
		create.Transport = string(ovpn.Transport)
	}

	// The obfuscation profile is deliberately not copied.
	//
	// Copying it looks like the helpful thing and is the opposite: the reason
	// for a second tunnel is that the first one is being blocked, and two
	// tunnels with the same S1-S4 and H1-H4 look identical to whatever is doing
	// the blocking. A clone that shared them would be blocked by the same rule,
	// the same day, which is the one outcome it exists to avoid. Create()
	// generates a fresh profile for the copy.

	out, err := s.Create(ctx, create)
	if err != nil {
		return nil, err
	}

	s.log.Info("interface copied", "from", src.Name, "to", out.Name,
		"protocol", out.Protocol, "port", out.ListenPort)
	return out, nil
}
