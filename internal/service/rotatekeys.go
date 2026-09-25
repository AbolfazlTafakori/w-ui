package service

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// Issuing a customer fresh credentials.
//
// The subscription link can be rotated on its own, and that changes where
// the files are fetched from; it does not change the files. A key that
// leaked is still a key that works, whoever holds it. This issues new
// credentials for the files themselves -- a new WireGuard key pair and
// preshared key, a new OpenVPN password -- so everything handed out
// before stops working at the next reconcile, a few seconds later.
//
// What is deliberately kept: the customer, their plan, their usage, their
// addresses, their device names, and their OpenVPN usernames. The point is
// to invalidate what was copied, not to renumber a customer who has to be
// told their new files anyway; keeping the address also keeps every
// routing rule and IP lease pointing at the same place. The username is
// kept for the same reason the address is -- it is not the secret.

// RotateKeys issues fresh credentials for a customer's files. With
// accountIDs given only those files are rotated, and they must belong to
// this customer; empty means all of them.
func (s *Clients) RotateKeys(ctx context.Context, clientID uint, accountIDs []uint) (int, error) {
	client, err := s.Get(ctx, clientID)
	if err != nil {
		return 0, err
	}

	want := map[uint]bool{}
	for _, id := range accountIDs {
		want[id] = true
	}
	var chosen []model.Account
	for _, a := range client.Accounts {
		if len(want) == 0 || want[a.ID] {
			chosen = append(chosen, a)
		}
	}
	if len(want) > 0 && len(chosen) != len(want) {
		return 0, invalidField("accountIds", "one of those files does not belong to this customer")
	}
	if len(chosen) == 0 {
		return 0, invalidField("accountIds", "this customer has no files to rotate")
	}

	// The interfaces are read first: a WireGuard file and an OpenVPN file
	// are rotated differently, and the protocol is the tunnel's.
	ifaces := map[uint]*model.Interface{}
	for _, a := range chosen {
		if _, ok := ifaces[a.InterfaceID]; ok {
			continue
		}
		f, err := s.loadInterface(ctx, a.InterfaceID)
		if err != nil {
			return 0, err
		}
		ifaces[a.InterfaceID] = f
	}

	type fresh struct {
		id                              uint
		private, public, preshared, sec string
		wg                              bool
	}
	out := make([]fresh, 0, len(chosen))
	for _, a := range chosen {
		f := ifaces[a.InterfaceID]
		switch f.Protocol {
		case model.ProtocolWireGuard:
			pair, err := wgkey.NewPair()
			if err != nil {
				return 0, err
			}
			out = append(out, fresh{id: a.ID, wg: true,
				private: pair.Private.String(), public: pair.Public.String(), preshared: pair.Preshared.String()})
		case model.ProtocolOpenVPN:
			secret, err := ovpnconf.NewSecret(16)
			if err != nil {
				return 0, err
			}
			out = append(out, fresh{id: a.ID, sec: secret})
		}
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, n := range out {
			cols := map[string]any{}
			if n.wg {
				cols["private_key"] = n.private
				cols["public_key"] = n.public
				cols["preshared_key"] = n.preshared
			} else {
				cols["secret"] = n.sec
			}
			if err := tx.Model(&model.Account{}).Where("id = ?", n.id).Updates(cols).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("service: rotate keys: %w", err)
	}

	names := make([]string, 0, len(chosen))
	for _, a := range chosen {
		names = append(names, a.DeviceName)
	}
	s.log.Info("client credentials rotated",
		"client", client.Name, "files", len(out), "devices", strings.Join(names, ", "),
		"consequence", "every configuration handed out before this stops working")
	return len(out), nil
}
