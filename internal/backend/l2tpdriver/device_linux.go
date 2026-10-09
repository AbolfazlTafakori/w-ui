//go:build linux

package l2tpdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

const cmdTimeout = 10 * time.Second

// ensureDevice gives the tunnel a device of its own name holding its gateway
// address.
//
// Every PPP session has the gateway as its local end, but a session's device
// exists only while that session does. The panel's resolver answers on each
// tunnel's gateway, and a tunnel is a named device to the rest of the panel;
// a dummy device holding the address is both, from the moment the tunnel is
// opened until it is removed. Linux lets the PPP devices share the address.
func ensureDevice(ctx context.Context, iface *model.Interface) error {
	gw, err := gateway(iface.Subnet)
	if err != nil {
		return err
	}
	if run(ctx, "ip", "link", "show", "dev", iface.Name) != nil {
		if err := run(ctx, "ip", "link", "add", "dev", iface.Name, "type", "dummy"); err != nil {
			return fmt.Errorf("l2tpdriver: create the %s device: %w", iface.Name, err)
		}
	} else if kind := linkKind(ctx, iface.Name); kind != "dummy" {
		// A device of that name that is something else -- another tunnel
		// a previous protocol left -- is not taken over.
		return fmt.Errorf("l2tpdriver: a %s device called %s already exists", kind, iface.Name)
	}
	if err := run(ctx, "ip", "addr", "replace", gw.String()+"/32", "dev", iface.Name); err != nil {
		return fmt.Errorf("l2tpdriver: address the %s device: %w", iface.Name, err)
	}
	if err := run(ctx, "ip", "link", "set", "up", "dev", iface.Name); err != nil {
		return fmt.Errorf("l2tpdriver: bring up the %s device: %w", iface.Name, err)
	}
	return nil
}

func removeDevice(ctx context.Context, iface *model.Interface) error {
	if run(ctx, "ip", "link", "show", "dev", iface.Name) != nil {
		return nil
	}
	if linkKind(ctx, iface.Name) != "dummy" {
		return nil
	}
	return run(ctx, "ip", "link", "del", "dev", iface.Name)
}

// linkKind is the device's type as the kernel names it: dummy, wireguard,
// tun, and so on.
func linkKind(ctx context.Context, name string) string {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "-j", "-d", "link", "show", "dev", name).Output()
	if err != nil {
		return ""
	}
	return kindFrom(out)
}

// kindFrom reads the kind out of `ip -j -d link` for one device.
func kindFrom(ipJSON []byte) string {
	var links []struct {
		LinkInfo struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if json.Unmarshal(ipJSON, &links) != nil || len(links) != 1 {
		return ""
	}
	if links[0].LinkInfo.Kind == "" {
		return "a physical"
	}
	return links[0].LinkInfo.Kind
}

func run(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errBuf.String()); msg != "" {
			return fmt.Errorf("%s: %s", strings.Join(append([]string{name}, args...), " "), msg)
		}
		return err
	}
	return nil
}
