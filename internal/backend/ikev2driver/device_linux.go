package ikev2driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/backend/l2tpdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
)

// IfID is the interface id of a tunnel's device, which its sessions' SAs
// carry: one of its own, in a range no other program here is likely to
// use.
func IfID(iface *model.Interface) uint32 { return 0x57550000 | uint32(iface.ID&0xffff) }

// ensureDevice gives the tunnel its device: an xfrm interface its sessions
// are tied to, holding the gateway and with the customers' subnet routed
// into it. What is sent to a device then leaves through it, as through a
// WireGuard tunnel's, and is shaped there; what a device sends arrives on
// it.
//
// It returns the interface id the sessions are to carry. On a kernel
// without xfrm interfaces it falls back to the placeholder L2TP uses and
// returns zero: the tunnel works, on policies alone, but cannot be shaped.
func ensureDevice(ctx context.Context, iface *model.Interface) (uint32, error) {
	id := IfID(iface)
	switch kind, have := linkInfo(ctx, iface.Name); {
	case kind == "xfrm" && have == id:
	case kind == "xfrm" || kind == "dummy":
		// The placeholder of an earlier version, or a device of another
		// id: replaced by the tunnel's own.
		if err := run(ctx, "ip", "link", "del", "dev", iface.Name); err != nil {
			return 0, fmt.Errorf("ikev2driver: replace the %s device: %w", iface.Name, err)
		}
		fallthrough
	case kind == "":
		if err := createXfrm(ctx, iface, id); err != nil {
			if err := l2tpdriver.EnsureDevice(ctx, iface); err != nil {
				return 0, err
			}
			return 0, nil
		}
	default:
		return 0, fmt.Errorf("ikev2driver: a %s device called %s already exists", kind, iface.Name)
	}
	gw, err := l2tpdriver.Gateway(iface.Subnet)
	if err != nil {
		return 0, err
	}
	steps := [][]string{
		{"ip", "addr", "replace", gw.String() + "/32", "dev", iface.Name},
		{"ip", "link", "set", "dev", iface.Name, "mtu", strconv.Itoa(mtu(iface)), "up"},
		{"ip", "route", "replace", iface.Subnet, "dev", iface.Name},
	}
	for _, s := range steps {
		if err := run(ctx, s[0], s[1:]...); err != nil {
			return 0, fmt.Errorf("ikev2driver: set up the %s device: %w", iface.Name, err)
		}
	}
	return id, nil
}

// createXfrm makes the device. Without a lower device first -- the kernel
// then routes the encrypted packets as it would anything else -- and tied to
// the egress interface where an older ip or kernel insists on one.
func createXfrm(ctx context.Context, iface *model.Interface, id uint32) error {
	err := run(ctx, "ip", "link", "add", iface.Name, "type", "xfrm", "if_id", strconv.FormatUint(uint64(id), 10))
	if err != nil && iface.NATInterface != "" {
		err = run(ctx, "ip", "link", "add", iface.Name, "type", "xfrm", "dev", iface.NATInterface,
			"if_id", strconv.FormatUint(uint64(id), 10))
	}
	return err
}

// mtu leaves room for ESP in UDP inside a 1500-byte link.
func mtu(iface *model.Interface) int {
	if iface.MTU >= 1280 && iface.MTU <= 1500 {
		return iface.MTU
	}
	return 1400
}

func removeDevice(ctx context.Context, iface *model.Interface) error {
	if kind, _ := linkInfo(ctx, iface.Name); kind == "xfrm" {
		return run(ctx, "ip", "link", "del", "dev", iface.Name)
	}
	return l2tpdriver.RemoveDevice(ctx, iface)
}

// linkInfo is a device's kind and, for an xfrm interface, its id; an empty
// kind is no such device.
func linkInfo(ctx context.Context, name string) (kind string, ifID uint32) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "-j", "-d", "link", "show", "dev", name).Output()
	if err != nil {
		return "", 0
	}
	return parseLinkInfo(out)
}

func parseLinkInfo(ipJSON []byte) (string, uint32) {
	var links []struct {
		LinkInfo struct {
			Kind string `json:"info_kind"`
			Data struct {
				// A number in some versions of ip, a hex string
				// ("0x57550007") in others.
				IfID any `json:"if_id"`
			} `json:"info_data"`
		} `json:"linkinfo"`
	}
	if json.Unmarshal(ipJSON, &links) != nil || len(links) == 0 {
		return "", 0
	}
	k := links[0].LinkInfo.Kind
	if k == "" {
		k = "other"
	}
	return k, ifIDOf(links[0].LinkInfo.Data.IfID)
}

func ifIDOf(v any) uint32 {
	switch t := v.(type) {
	case float64:
		return uint32(t)
	case string:
		n, err := strconv.ParseUint(t, 0, 32)
		if err == nil {
			return uint32(n)
		}
	}
	return 0
}

func run(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
