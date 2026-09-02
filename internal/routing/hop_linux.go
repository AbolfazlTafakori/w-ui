//go:build linux

package routing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// HopSpec is an upstream WireGuard tunnel this server dials.
//
// It is the mirror image of an interface the panel serves: there, customers are
// peers of ours; here, we are a peer of somebody else's. The distinction
// matters because nothing about a hop is ours to allocate — the address, the
// keys and the endpoint were all issued by whoever runs the far end.
type HopSpec struct {
	// Device is the interface name, derived from the outbound's id.
	Device string
	// Mark is used twice: to steer customer traffic into this hop, and as the
	// hop's own fwmark so its encrypted packets are not caught by its own
	// routing rule and looped back into itself.
	Mark uint32

	PrivateKey   string
	PeerPubKey   string
	PresharedKey string
	Endpoint     string
	// Address is what the upstream issued this server, with a prefix.
	Address string
	MTU     int
}

// HopManager brings upstream tunnels up and takes them down.
type HopManager struct {
	log *slog.Logger

	mu sync.Mutex
	// up is what each device was last configured with, so an unchanged hop is
	// not torn down and rebuilt every tick — which would drop every customer
	// using it, every two seconds.
	up map[string]string
}

// NewHopManager builds a manager.
func NewHopManager(log *slog.Logger) *HopManager {
	return &HopManager{log: log, up: map[string]string{}}
}

// Sync makes the set of live hop devices match specs exactly.
func (m *HopManager) Sync(ctx context.Context, specs []HopSpec) error {
	want := make(map[string]HopSpec, len(specs))
	for _, s := range specs {
		want[s.Device] = s
	}

	m.mu.Lock()
	live := make(map[string]string, len(m.up))
	for k, v := range m.up {
		live[k] = v
	}
	m.mu.Unlock()

	// Devices that should no longer exist.
	for dev := range live {
		if _, keep := want[dev]; !keep {
			m.down(ctx, dev)
		}
	}

	var firstErr error
	for dev, spec := range want {
		fp := spec.fingerprint()
		if live[dev] == fp {
			continue // unchanged; leave the customers on it alone
		}
		if err := m.bring(ctx, spec); err != nil {
			m.log.Error("could not bring up an outbound hop",
				"device", dev, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.mu.Lock()
		m.up[dev] = fp
		m.mu.Unlock()
	}
	return firstErr
}

// bring creates and configures one hop device.
func (m *HopManager) bring(ctx context.Context, s HopSpec) error {
	if err := m.assertSafe(ctx, s.Device); err != nil {
		return err
	}

	if _, err := run(ctx, ipBinary, "", "link", "show", "dev", s.Device); err != nil {
		if _, err := run(ctx, ipBinary, "", "link", "add", "dev", s.Device,
			"type", "wireguard"); err != nil {
			return fmt.Errorf("routing: create %s: %w", s.Device, err)
		}
	}

	// Written through a config file rather than argument by argument: a key on
	// a command line is visible to every process on the machine for as long as
	// the command runs.
	conf := s.wgQuickConf()
	if _, err := run(ctx, "wg", conf, "setconf", s.Device, "/dev/stdin"); err != nil {
		return fmt.Errorf("routing: configure %s: %w", s.Device, err)
	}

	if s.Address != "" {
		if _, err := run(ctx, ipBinary, "", "addr", "replace", s.Address,
			"dev", s.Device); err != nil {
			return fmt.Errorf("routing: address %s: %w", s.Device, err)
		}
	}
	if s.MTU > 0 {
		if _, err := run(ctx, ipBinary, "", "link", "set", "mtu",
			fmt.Sprint(s.MTU), "dev", s.Device); err != nil {
			m.log.Warn("could not set the hop MTU", "device", s.Device, "error", err)
		}
	}
	if _, err := run(ctx, ipBinary, "", "link", "set", "up", "dev", s.Device); err != nil {
		return fmt.Errorf("routing: bring %s up: %w", s.Device, err)
	}

	m.log.Info("outbound hop is up", "device", s.Device, "endpoint", s.Endpoint)
	return nil
}

// down removes a hop device.
func (m *HopManager) down(ctx context.Context, dev string) {
	if !strings.HasPrefix(dev, "wuih") {
		// Refuses to delete anything that is not one of ours, however it came
		// to be in the map.
		return
	}
	_, _ = run(ctx, ipBinary, "", "link", "del", "dev", dev)

	m.mu.Lock()
	delete(m.up, dev)
	m.mu.Unlock()
	m.log.Info("outbound hop removed", "device", dev)
}

// assertSafe refuses to touch a device this panel did not create.
//
// Hop names are prefixed and numbered from the database, so a collision should
// be impossible — but "should be impossible" is how another deployment's tunnel
// gets its keys overwritten, so it is checked rather than assumed.
func (m *HopManager) assertSafe(ctx context.Context, dev string) error {
	if !strings.HasPrefix(dev, "wuih") {
		return fmt.Errorf("routing: %q is not a hop device name this panel generates", dev)
	}
	out, err := run(ctx, ipBinary, "", "-details", "link", "show", "dev", dev)
	if err != nil {
		return nil // does not exist yet, which is the normal case
	}
	if !strings.Contains(string(out), "wireguard") {
		return fmt.Errorf(
			"routing: %s already exists on this machine and is not a WireGuard device; "+
				"it belongs to something else and will not be touched", dev)
	}
	return nil
}

// Teardown removes every hop device this manager brought up.
func (m *HopManager) Teardown(ctx context.Context) {
	m.mu.Lock()
	devs := make([]string, 0, len(m.up))
	for d := range m.up {
		devs = append(devs, d)
	}
	m.mu.Unlock()

	for _, d := range devs {
		m.down(ctx, d)
	}
}

// wgQuickConf renders the tunnel configuration.
//
// AllowedIPs is 0.0.0.0/0 because a hop is an exit: everything sent into it
// should go. The routing rule decides what is sent, not this.
//
// FwMark is the hop's own mark. Without it the encrypted packets this interface
// emits would match the very routing rule that steers traffic into it, and the
// tunnel would try to carry itself.
func (s HopSpec) wgQuickConf() string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", s.PrivateKey)
	if s.Mark != 0 {
		fmt.Fprintf(&b, "FwMark = 0x%08x\n", s.Mark)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", s.PeerPubKey)
	if s.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", s.PresharedKey)
	}
	fmt.Fprintf(&b, "Endpoint = %s\n", s.Endpoint)
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	// A hop usually sits behind NAT at our end. Without a keepalive the far
	// side's mapping expires and inbound packets stop arriving, which looks
	// like the exit working for a minute and then dying.
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String()
}

func (s HopSpec) fingerprint() string {
	return strings.Join([]string{
		s.Device, s.PeerPubKey, s.Endpoint, s.Address,
		fmt.Sprint(s.MTU), fmt.Sprintf("%08x", s.Mark),
		// The private and preshared keys are hashed into the fingerprint by
		// length alone. Their value must not sit in memory in a second place,
		// and a length change is enough to notice a rotation.
		fmt.Sprint(len(s.PrivateKey), len(s.PresharedKey)),
	}, "|")
}
