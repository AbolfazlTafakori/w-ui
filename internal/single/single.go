// Package single makes sure only one panel is running where it matters.
//
// Two panels on one machine do not coexist; they fight, and they do it
// silently. Each one owns `table inet wui` outright and rewrites it whole on
// every apply, so the second one to start deletes the first one's quotas and
// counters — and the first does not notice, because it compares what it means
// to write against what it last wrote, not against the kernel. The same goes
// for `table inet wui_policy`, for the tc hierarchy at handle 1: on every
// device, for WireGuard interfaces claimed by name and for OpenVPN ports.
//
// The result is a panel that looks healthy while enforcing nothing: customers
// who have used their whole allowance keep going, and nobody finds out until
// the bill. This is not a hypothetical. It happened on a live server, for about
// thirty-five minutes, because a second panel was started there to test
// something and left running.
//
// So it is refused at startup rather than warned about.
package single

import (
	"errors"
	"fmt"
)

// ErrAlreadyRunning is returned when another panel holds this machine.
var ErrAlreadyRunning = errors.New("another W-UI panel is already running here")

// Holder is what a successful claim gives back. Releasing is optional — the
// claim dies with the process either way — but a test that starts and stops
// several panels needs it.
type Holder interface {
	Release()
}

// Claim takes the machine, or reports who already has it.
//
// The identity is written into the claim so the panel that is refused can say
// which one is in the way, rather than leaving an operator to work out why a
// service will not start.
func Claim(identity string) (Holder, error) {
	return claim(identity)
}

// Describe turns a refusal into something worth reading.
func Describe(err error, holder string) error {
	if holder == "" {
		return fmt.Errorf("%w. Two panels on one machine overwrite each other's "+
			"firewall rules, and neither notices: stop the other one, or run this "+
			"one on a separate machine", ErrAlreadyRunning)
	}
	return fmt.Errorf("%w: %s. Two panels on one machine overwrite each other's "+
		"firewall rules, and neither notices, so this one will not start",
		ErrAlreadyRunning, holder)
}
