package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
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
	p := iface.OpenVPN.V
	transport := p.Transport
	if transport == "" {
		transport = "udp"
	}

	var b strings.Builder
	b.WriteString("client\ndev tun\n")
	fmt.Fprintf(&b, "proto %s\n", transport)
	fmt.Fprintf(&b, "remote %s %d\n", iface.EndpointHost, iface.ListenPort)
	b.WriteString("resolv-retry infinite\nnobind\npersist-key\npersist-tun\n")
	b.WriteString("remote-cert-tls server\n")
	if p.CipherSuite != "" {
		fmt.Fprintf(&b, "cipher %s\n", p.CipherSuite)
	}
	if p.Auth != "" {
		fmt.Fprintf(&b, "auth %s\n", p.Auth)
	}
	// This is the line that makes the client prompt for a username and
	// password. It is the whole reason OpenVPN is offered alongside WireGuard,
	// which has no credential concept at all.
	b.WriteString("auth-user-pass\n")
	b.WriteString("verb 3\n")

	if p.CACert != "" {
		fmt.Fprintf(&b, "\n<ca>\n%s\n</ca>\n", strings.TrimSpace(p.CACert))
	}
	if p.TLSCryptKey != "" {
		fmt.Fprintf(&b, "\n<tls-crypt>\n%s\n</tls-crypt>\n", strings.TrimSpace(p.TLSCryptKey))
	}

	return &Profile{
		Filename: fmt.Sprintf("%s.ovpn", filenameFor(acc)),
		MIMEType: "application/x-openvpn-profile",
		Body:     b.String(),
		Username: acc.Username,
		Secret:   acc.Secret,
	}
}

func filenameFor(acc *model.Account) string {
	name := slug(acc.DeviceName)
	return fmt.Sprintf("%s-%d", name, acc.ID)
}
