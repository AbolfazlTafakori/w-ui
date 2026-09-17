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
	// The host this file was written for, when it was written for one.
	HostID          uint   `json:"hostId,omitempty"`
	HostName        string `json:"hostName,omitempty"`
	HostDescription string `json:"hostDescription,omitempty"`
}

// Profile renders the configuration for one device: the first of its host
// variants, which is the interface's own endpoint when it has no hosts.
func (s *Clients) Profile(ctx context.Context, accountID uint) (*Profile, error) {
	all, err := s.Profiles(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return &all[0], nil
}

// Profiles renders one configuration per host the device can reach, as
// the subscription hands them out.
func (s *Clients) Profiles(ctx context.Context, accountID uint) ([]Profile, error) {
	var acc model.Account
	if err := s.db.WithContext(ctx).First(&acc, accountID).Error; err != nil {
		return nil, fmt.Errorf("%w: device %d", ErrNotFound, accountID)
	}
	iface, err := s.loadInterface(ctx, acc.InterfaceID)
	if err != nil {
		return nil, err
	}
	// The file is named after the customer, so what lands in their app is
	// the name the operator gave them rather than "device-1".
	var owner model.Client
	if err := s.db.WithContext(ctx).Preload("Accounts").First(&owner, acc.ClientID).Error; err != nil {
		return nil, fmt.Errorf("%w: customer %d", ErrNotFound, acc.ClientID)
	}
	single := len(deviceNames(owner.Accounts)) <= 1
	var hosts []model.Host
	if err := s.db.WithContext(ctx).Where("interface_id = ?", iface.ID).Order("priority, id").Find(&hosts).Error; err != nil {
		return nil, fmt.Errorf("service: read hosts: %w", err)
	}
	iface.Hosts = hosts

	var out []Profile
	for _, v := range variantsFor(iface, "") {
		at := withEndpoint(*iface, v)
		var p *Profile
		switch iface.Protocol {
		case model.ProtocolWireGuard:
			p = renderWireGuard(&acc, &at)
		case model.ProtocolOpenVPN:
			p = renderOpenVPN(&acc, &at)
		default:
			return nil, fmt.Errorf("%w: protocol %q", ErrInvalid, iface.Protocol)
		}
		p.Filename = variantFilename(clientFilename(owner.Name, acc.DeviceName, p.Filename, single), v)
		if v.Host != nil {
			p.HostID = v.Host.ID
			p.HostName = v.Host.Name
			p.HostDescription = v.Host.Description
		}
		out = append(out, *p)
	}
	return out, nil
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
