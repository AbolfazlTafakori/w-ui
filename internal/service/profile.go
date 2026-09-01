package service

import (
	"context"
	"fmt"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
	"github.com/abolfazl/w-ui/internal/wgconf"
)

// Profile is a rendered client configuration.
type Profile struct {
	Filename string `json:"filename"`
	MIMEType string `json:"mimeType"`
	Body     string `json:"body"`

	// Username and Secret are set for OpenVPN, where the credential is entered
	// in the client rather than carried in the file.
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

// Profile renders the configuration for one device.
func (s *Clients) Profile(ctx context.Context, accountID uint) (*Profile, error) {
	var acc model.Account
	if err := s.db.WithContext(ctx).First(&acc, accountID).Error; err != nil {
		return nil, fmt.Errorf("%w: device %d", ErrNotFound, accountID)
	}
	iface, err := s.loadInterface(ctx, acc.InterfaceID)
	if err != nil {
		return nil, err
	}

	switch iface.Protocol {
	case model.ProtocolWireGuard:
		return renderWireGuard(&acc, iface), nil
	case model.ProtocolOpenVPN:
		return renderOpenVPN(&acc, iface), nil
	default:
		return nil, fmt.Errorf("%w: protocol %q", ErrInvalid, iface.Protocol)
	}
}

func renderWireGuard(acc *model.Account, iface *model.Interface) *Profile {
	// Rendered by the shared generator, the same one the driver writes the
	// server side with. Two copies would eventually disagree and hand a
	// customer a file the server no longer matches.
	return &Profile{
		Filename: fmt.Sprintf("%s.conf", filenameFor(acc)),
		MIMEType: "text/plain; charset=utf-8",
		Body:     wgconf.RenderClient(acc, iface),
	}
}

func renderOpenVPN(acc *model.Account, iface *model.Interface) *Profile {
	// Rendered by the shared generator, the same one the driver writes the
	// server side with. Two copies would eventually disagree about a cipher or
	// a port and produce a failure that names neither.
	return &Profile{
		Filename: fmt.Sprintf("%s.ovpn", filenameFor(acc)),
		MIMEType: "application/x-openvpn-profile",
		Body:     ovpnconf.RenderClient(acc, iface),
		Username: acc.Username,
		Secret:   acc.Secret,
	}
}

func filenameFor(acc *model.Account) string {
	name := slug(acc.DeviceName)
	return fmt.Sprintf("%s-%d", name, acc.ID)
}
