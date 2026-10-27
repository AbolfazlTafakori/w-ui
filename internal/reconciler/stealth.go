package reconciler

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/stealth"
	"github.com/abolfazl/w-ui/internal/stealthgw"
)

// The stealth gateways.
//
// A WireGuard tunnel with wui-udp on has a gateway beside it, on a port of
// its own, that hands what it unwraps to the tunnel's own port on the
// loopback. The gateway is the panel's, like the tunnel's peers: started when
// the tunnel wants one, given the devices the tunnel carries on every pass,
// restarted when what it was started with changes, and stopped when the
// tunnel stops wanting it. WireGuard goes on as it was through all of it.

// stealthDir is where the gateways keep their replay limits, in the data
// directory.
const stealthDir = "stealth"

// gatewayStop bounds the wait for a stopped gateway to let go of its port.
const gatewayStop = 5 * time.Second

type gateways struct {
	mu      sync.Mutex
	running map[uint]*runningGateway // by tunnel
}

type runningGateway struct {
	key    string // what it was started with
	gw     *stealthgw.Gateway
	cancel context.CancelFunc
	done   chan struct{}
	failed string // the last start that failed, so it is said once
}

// gatewayKey is what a gateway was started with: a change to any of it
// starts it again.
func gatewayKey(i model.Interface) string {
	return fmt.Sprintf("%d|%d|%d|%s", i.StealthUDP(), i.ListenPort, i.MTU, i.PrivateKey)
}

// keepStealth brings the gateways in line with the tunnels.
func (r *Reconciler) keepStealth(d *desired) {
	if r.dataDir == "" {
		return // a reconciler with nowhere to keep a replay limit runs none
	}
	g := &r.gws
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running == nil {
		g.running = map[uint]*runningGateway{}
	}
	want := map[uint]bool{}
	for _, iface := range d.interfaces {
		if iface.StealthUDP() == 0 {
			continue
		}
		want[iface.ID] = true
		key := gatewayKey(iface)
		rg := g.running[iface.ID]
		if rg != nil && rg.key != key {
			r.stopGateway(iface.ID, rg)
			rg = nil
		}
		if rg == nil || rg.gw == nil {
			rg = r.startGateway(iface, key, rg)
			g.running[iface.ID] = rg
		}
		if rg.gw != nil {
			rg.gw.SetDevices(stealthDevices(d.perIface[iface.ID]))
		}
	}
	for id, rg := range g.running {
		if !want[id] {
			r.stopGateway(id, rg)
			delete(g.running, id)
		}
	}
}

// startGateway starts a tunnel's gateway. A start that fails -- the port is
// taken, the key cannot be read -- is said once and tried again next pass.
func (r *Reconciler) startGateway(iface model.Interface, key string, before *runningGateway) *runningGateway {
	rg := &runningGateway{key: key}
	if before != nil {
		rg.failed = before.failed
	}
	fail := func(err error) *runningGateway {
		if msg := err.Error(); msg != rg.failed {
			r.log.Error("wui-udp could not start on this tunnel; its devices reach it over plain WireGuard meanwhile",
				"interface", iface.Name, "port", iface.StealthUDP(), "error", err)
			rg.failed = msg
		}
		return rg
	}
	priv, err := base64.StdEncoding.DecodeString(iface.PrivateKey)
	if err != nil || len(priv) != 32 {
		return fail(fmt.Errorf("the tunnel's private key is not a WireGuard key"))
	}
	mtu := iface.MTU
	if mtu <= 0 {
		mtu = 1420
	}
	cfg := stealthgw.Config{
		Listen:     net.JoinHostPort("", strconv.Itoa(iface.StealthUDP())),
		WireGuard:  net.JoinHostPort("127.0.0.1", strconv.Itoa(iface.ListenPort)),
		MTU:        mtu,
		ReplayFile: filepath.Join(r.dataDir, stealthDir, fmt.Sprintf("%d.replay", iface.ID)),
		Log:        r.log.With("interface", iface.Name),
	}
	copy(cfg.PrivateKey[:], priv)
	gw, err := stealthgw.New(cfg)
	if err != nil {
		return fail(err)
	}
	if err := gw.Listen(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rg.gw, rg.cancel, rg.done, rg.failed = gw, cancel, make(chan struct{}), ""
	go func() {
		defer close(rg.done)
		if err := gw.Serve(ctx); err != nil {
			r.log.Error("wui-udp stopped", "interface", iface.Name, "error", err)
		}
	}()
	r.log.Info("wui-udp is listening", "interface", iface.Name, "port", iface.StealthUDP())
	return rg
}

// stopGateway stops a gateway and waits, a while, for its port to be free.
func (r *Reconciler) stopGateway(id uint, rg *runningGateway) {
	if rg == nil || rg.gw == nil {
		return
	}
	rg.cancel()
	select {
	case <-rg.done:
	case <-time.After(gatewayStop):
		r.log.Warn("a wui-udp gateway did not stop in time", "interface", id)
	}
}

// StopGateways stops every gateway, for the panel's shutdown.
func (r *Reconciler) StopGateways() {
	r.gws.mu.Lock()
	defer r.gws.mu.Unlock()
	for id, rg := range r.gws.running {
		r.stopGateway(id, rg)
		delete(r.gws.running, id)
	}
}

// stealthDevices is the devices a tunnel carries that hold a transport
// secret, as its gateway takes them. A device left out of what the tunnel
// carries -- out of allowance, switched off -- is left out here too, and
// loses its sessions with its peer.
func stealthDevices(accounts []backend.DesiredAccount) []stealthgw.Device {
	out := make([]stealthgw.Device, 0, len(accounts))
	for _, a := range accounts {
		if a.StealthSecret == "" {
			continue
		}
		secret, err := stealth.ParseSecret(a.StealthSecret)
		if err != nil {
			continue
		}
		pub, err := base64.StdEncoding.DecodeString(a.PublicKey)
		if err != nil || len(pub) != 32 {
			continue
		}
		d := stealthgw.Device{Secret: secret}
		copy(d.PublicKey[:], pub)
		out = append(out, d)
	}
	return out
}

// realEndpoint is where a device on a tunnel really is, for the address its
// WireGuard peer shows: a gateway's socket on the loopback stands for the
// device's own address behind it.
func (r *Reconciler) realEndpoint(ifaceID uint, endpoint string) string {
	r.gws.mu.Lock()
	rg := r.gws.running[ifaceID]
	r.gws.mu.Unlock()
	if rg == nil || rg.gw == nil {
		return endpoint
	}
	if src, ok := rg.gw.Source(endpoint); ok {
		return src
	}
	return endpoint
}
