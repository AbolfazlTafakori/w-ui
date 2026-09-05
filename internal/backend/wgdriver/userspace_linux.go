//go:build linux

package wgdriver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// AmneziaWG without a kernel module.
//
// The obfuscation lives in a fork of the WireGuard kernel module, and that fork
// only exists as a package for the distributions its project has caught up
// with. On anything newer — a fresh Ubuntu, a kernel a few months ahead of the
// module — `ip link add type amneziawg` answers "Unknown device type", and the
// panel would have nothing to offer exactly where obfuscation is most needed.
//
// amneziawg-go is the same protocol in userspace over a TUN device. It is
// slower than the module and that is the entire cost; it needs no headers, no
// DKMS, and it does not break when the machine takes a kernel upgrade and
// reboots — which the module does, silently, taking every customer with it.
//
// The kernel module is still preferred when it is there. This is what happens
// when it is not.

// userspaceTool is the daemon that provides an AmneziaWG device without one.
const userspaceTool = "amneziawg-go"

// deviceAppears bounds the wait for a device the daemon creates after it forks.
const (
	deviceAppears = 5 * time.Second
	devicePoll    = 100 * time.Millisecond
)

// startUserspace brings up an AmneziaWG device through amneziawg-go.
//
// The daemon forks and returns, leaving the device behind, so this waits for
// the device rather than for the process: a daemon that exited without creating
// anything is a failure, and one that is still starting is not.
func (d *Driver) startUserspace(ctx context.Context, iface *model.Interface, linkErr error) error {
	if iface.Mode != model.ModeAmnezia {
		return linkErr
	}
	if _, err := exec.LookPath(userspaceTool); err != nil {
		// Both reasons, in one message. Being told only "unknown device type"
		// sends an operator after a kernel module for a machine that has no
		// package for one, when installing amneziawg-go would have done.
		return fmt.Errorf("%w: %s needs AmneziaWG, and this kernel has no "+
			"amneziawg module (%v) and no %s to provide one in userspace; "+
			"install amneziawg-go, or the kernel module if this distribution "+
			"has a package for it", ErrNoTool, iface.Name, linkErr, userspaceTool)
	}

	// The daemon inherits this process's working directory and refuses to
	// daemonize if it cannot read it, so it is given one that always exists.
	cmd := exec.CommandContext(ctx, userspaceTool, iface.Name)
	cmd.Dir = "/"
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: starting %s for %s: %v: %s",
			ErrLinkFailed, userspaceTool, iface.Name, err, complaint(out))
	}

	if err := d.waitForDevice(ctx, iface.Name); err != nil {
		return err
	}

	d.log.Info("created interface in userspace",
		"interface", iface.Name, "by", userspaceTool,
		"why", "this kernel has no amneziawg module")
	return nil
}

// waitForDevice blocks until the link exists, or gives up saying so.
func (d *Driver) waitForDevice(ctx context.Context, name string) error {
	deadline := time.Now().Add(deviceAppears)
	for {
		if d.run(ctx, "ip", "link", "show", "dev", name) == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s started but %s did not appear within %s",
				ErrLinkFailed, userspaceTool, name, deviceAppears)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(devicePoll):
		}
	}
}

// complaint picks the line of a daemon's output that says what went wrong.
//
// amneziawg-go greets every start with a boxed notice about the kernel module,
// so the first line of its output is never the interesting one. The last line
// that is not part of that box is.
func complaint(b []byte) string {
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r := []rune(line)[0]; r > 0x2500 && r < 0x2580 {
			continue // a box-drawing character: still the banner
		}
		return line
	}
	return "no output"
}
