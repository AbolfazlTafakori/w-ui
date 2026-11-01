package reconciler

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
	"github.com/abolfazl/w-ui/internal/stealth"
	"github.com/abolfazl/w-ui/internal/stealthgw"
)

// The stealth gateways.
//
// A WireGuard tunnel with a stealth transport on has a gateway beside it, on
// a port of its own, that hands what it unwraps to the tunnel's own port on
// the loopback: wui-udp's on UDP, wui-tls's on TCP as the server itself,
// wui-ws's, wui-poll's and wui-plain's on TCP behind the operator's web
// server or CDN.
// The gateway is the panel's, like the tunnel's peers: started when the
// tunnel wants one, given the devices the tunnel carries on every pass,
// restarted when what it was started with changes, and stopped when the
// tunnel stops wanting it. wui-tls's certificate is the exception: a renewed
// one is handed to the running gateway, so no session ends for it.
// WireGuard goes on as it was through all of it.

// stealthDir is where the gateways keep their replay limits, in the data
// directory.
const stealthDir = "stealth"

// gatewayStop bounds the wait for a stopped gateway to let go of its port.
const gatewayStop = 5 * time.Second

type gateways struct {
	mu      sync.Mutex
	running map[uint]*runningGateway // by tunnel
}

// gateway is a running gateway of either transport.
type gateway interface {
	Serve(ctx context.Context) error
	SetDevices([]stealthgw.Device)
	Source(endpoint string) (string, bool)
}

type runningGateway struct {
	key    string // what it was started with
	cert   string // the certificate a wui-tls gateway serves, PEM
	gw     gateway
	cancel context.CancelFunc
	done   chan struct{}
	failed string // the last start that failed, so it is said once
}

// gatewayKey is what a gateway was started with: a change to any of it
// starts it again.
func gatewayKey(i model.Interface) string {
	name, port := i.StealthTransport()
	p := i.Stealth.V
	if p.Proven() {
		key := fmt.Sprintf("%s|%d|%d|%s|%s|%s|%s|%s|%s|%s|%s", name, port, i.ListenPort, i.PrivateKey,
			p.Path, p.Origin, p.Proxies, p.ClientHeader, p.Cookie, p.Key, p.PreviousKey)
		if name == model.TransportWuiTLS {
			// Not the certificate: keepStealth hands a new one over.
			key += fmt.Sprintf("|%s|%t", p.Host, p.ProxyProtocol)
		}
		return key
	}
	if name == model.TransportWuiFakeTLS {
		return fmt.Sprintf("%s|%d|%d|%s|%s|%s", name, port, i.ListenPort, i.PrivateKey, p.Site, p.NodeKey)
	}
	return fmt.Sprintf("%s|%d|%d|%d|%s", name, port, i.ListenPort, i.MTU, i.PrivateKey)
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
		if name, _ := iface.StealthTransport(); name == "" {
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
			r.keepCertificate(iface, rg)
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
	name, port := iface.StealthTransport()
	fail := func(err error) *runningGateway {
		if msg := err.Error(); msg != rg.failed {
			r.log.Error("a stealth transport could not start on this tunnel; its devices reach it over plain WireGuard meanwhile",
				"interface", iface.Name, "transport", name, "port", port, "error", err)
			rg.failed = msg
		}
		return rg
	}
	priv, err := base64.StdEncoding.DecodeString(iface.PrivateKey)
	if err != nil || len(priv) != 32 {
		return fail(fmt.Errorf("the tunnel's private key is not a WireGuard key"))
	}
	var gw interface {
		gateway
		Listen() error
	}
	switch name {
	case model.TransportWuiTLS:
		var tg *stealthgw.TLSGateway
		if tg, err = r.newTLSGateway(iface, priv); err == nil {
			gw, rg.cert = tg, servedCert(iface)
		}
	case model.TransportWuiWS:
		gw, err = r.newWSGateway(iface, priv)
	case model.TransportWuiPoll:
		gw, err = r.newPollGateway(iface, priv)
	case model.TransportWuiShape:
		gw, err = r.newShapeGateway(iface, priv)
	case model.TransportWuiPlain:
		gw, err = r.newPlainGateway(iface, priv)
	case model.TransportWuiFakeTLS:
		gw, err = r.newFakeTLSGateway(iface, priv)
	default:
		gw, err = r.newUDPGateway(iface, priv)
	}
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
			r.log.Error("a stealth transport stopped", "interface", iface.Name, "transport", name, "error", err)
		}
	}()
	r.log.Info("a stealth transport is listening", "interface", iface.Name, "transport", name, "port", port)
	return rg
}

// replayFile is where a tunnel's gateway keeps its replay limit: one file
// per transport, since each judges its own openings or proofs.
func replayFile(dataDir string, iface model.Interface) string {
	name, _ := iface.StealthTransport()
	if name == model.TransportWuiUDP {
		return filepath.Join(dataDir, stealthDir, fmt.Sprintf("%d.replay", iface.ID)) // as wui-udp has always kept it
	}
	return filepath.Join(dataDir, stealthDir, fmt.Sprintf("%d.%s.replay", iface.ID, name))
}

func (r *Reconciler) newUDPGateway(iface model.Interface, priv []byte) (*stealthgw.Gateway, error) {
	mtu := iface.MTU
	if mtu <= 0 {
		mtu = 1420
	}
	cfg := stealthgw.Config{
		Listen:     net.JoinHostPort("", strconv.Itoa(iface.StealthUDP())),
		WireGuard:  net.JoinHostPort("127.0.0.1", strconv.Itoa(iface.ListenPort)),
		MTU:        mtu,
		ReplayFile: replayFile(r.dataDir, iface),
		Log:        r.log.With("interface", iface.Name),
	}
	copy(cfg.PrivateKey[:], priv)
	return stealthgw.New(cfg)
}

// newTLSGateway is wui-tls's gateway: the server itself, on every address,
// ending TLS with the certificate the tunnel serves and handing whoever is
// not a device to the decoy.
func (r *Reconciler) newTLSGateway(iface model.Interface, priv []byte) (*stealthgw.TLSGateway, error) {
	cfg, err := r.streamConfig(iface, priv)
	if err != nil {
		return nil, err
	}
	p := iface.Stealth.V
	cert, err := stealth.TLSCertificate(p.ServedCert())
	if err != nil {
		return nil, err
	}
	_, port := iface.StealthTransport()
	// The name the gateway's own requests give the decoy, as a visitor's
	// browser would: the tunnel's name, with the port when it is not 443.
	host := p.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // an IPv6 address
	}
	if port != 443 {
		host += ":" + strconv.Itoa(port)
	}
	return stealthgw.NewTLS(stealthgw.TLSConfig{
		Listen: net.JoinHostPort("", strconv.Itoa(port)), Path: cfg.Path, Origin: cfg.Origin,
		ProxyProtocol: p.ProxyProtocol, Host: host, Certificate: cert,
		WireGuard: cfg.WireGuard, PrivateKey: cfg.PrivateKey, TransportKeys: cfg.TransportKeys,
		ReplayFile: cfg.ReplayFile, Log: cfg.Log,
	})
}

// servedCert is the certificate a tunnel's wui-tls gateway serves, PEM.
func servedCert(iface model.Interface) string {
	cert, _ := iface.Stealth.V.ServedCert()
	return cert
}

// keepCertificate hands a running wui-tls gateway the tunnel's certificate
// when it changed: renewed, or the panel's taken up or let go. Connections
// there are keep the one they began with. One that does not read is said,
// and the gateway keeps the one it has.
func (r *Reconciler) keepCertificate(iface model.Interface, rg *runningGateway) {
	tg, ok := rg.gw.(*stealthgw.TLSGateway)
	if !ok {
		return
	}
	served := servedCert(iface)
	if served == rg.cert {
		return
	}
	cert, err := stealth.TLSCertificate(iface.Stealth.V.ServedCert())
	if err != nil {
		r.log.Error("wui-tls's new certificate does not read; the gateway keeps the one it has", "interface", iface.Name, "error", err)
		return
	}
	tg.SetCertificate(cert)
	rg.cert = served
	r.log.Info("wui-tls serves a new certificate", "interface", iface.Name, "source", iface.Stealth.V.CertSource)
}

// newWSGateway is wui-ws's gateway.
func (r *Reconciler) newWSGateway(iface model.Interface, priv []byte) (*stealthgw.StreamGateway, error) {
	cfg, err := r.streamConfig(iface, priv)
	if err != nil {
		return nil, err
	}
	return stealthgw.NewStream(cfg)
}

// newPollGateway is wui-poll's gateway: wui-ws's settings, the tunnel's
// datagrams in POST requests.
func (r *Reconciler) newPollGateway(iface model.Interface, priv []byte) (*stealthgw.PollGateway, error) {
	cfg, err := r.streamConfig(iface, priv)
	if err != nil {
		return nil, err
	}
	return stealthgw.NewPoll(stealthgw.PollConfig{StreamConfig: cfg})
}

// newShapeGateway is wui-shape's gateway: wui-poll's, with the operator's
// request shape.
func (r *Reconciler) newShapeGateway(iface model.Interface, priv []byte) (*stealthgw.ShapeGateway, error) {
	cfg, err := r.streamConfig(iface, priv)
	if err != nil {
		return nil, err
	}
	shape, err := stealth.ParseShape([]byte(iface.Stealth.V.Shape))
	if err != nil {
		return nil, fmt.Errorf("the request shape is not valid: %w", err)
	}
	return stealthgw.NewShape(stealthgw.ShapeConfig{StreamConfig: cfg, Shape: shape})
}

// newPlainGateway is wui-plain's gateway: wui-ws's, with its cookie.
func (r *Reconciler) newPlainGateway(iface model.Interface, priv []byte) (*stealthgw.PlainGateway, error) {
	cfg, err := r.streamConfig(iface, priv)
	if err != nil {
		return nil, err
	}
	return stealthgw.NewPlain(stealthgw.PlainConfig{StreamConfig: cfg, Cookie: iface.Stealth.V.Cookie})
}

// newFakeTLSGateway is wui-faketls's gateway: the server itself, on its own
// port, that borrows a real site. A device's ClientHello opens a session; any
// other is relayed to the site byte for byte.
func (r *Reconciler) newFakeTLSGateway(iface model.Interface, priv []byte) (*stealthgw.FakeTLSGateway, error) {
	p := iface.Stealth.V
	node, err := base64.StdEncoding.DecodeString(p.NodeKey)
	if err != nil || len(node) != 32 {
		return nil, fmt.Errorf("the node key is not an X25519 key")
	}
	_, port := iface.StealthTransport()
	cfg := stealthgw.FakeTLSConfig{
		Listen:     net.JoinHostPort("", strconv.Itoa(port)),
		Site:       p.Site,
		WireGuard:  net.JoinHostPort("127.0.0.1", strconv.Itoa(iface.ListenPort)),
		ReplayFile: replayFile(r.dataDir, iface),
		Log:        r.log.With("interface", iface.Name),
	}
	copy(cfg.PrivateKey[:], priv)
	copy(cfg.NodeKey[:], node)
	return stealthgw.NewFakeTLS(cfg)
}

// streamConfig is what a gateway behind the operator's front is started
// with. With no front trusted but the loopback it listens on the loopback
// alone, for a web server on this machine; a front elsewhere has it listen
// on every address, and the firewall lets the port through.
func (r *Reconciler) streamConfig(iface model.Interface, priv []byte) (stealthgw.StreamConfig, error) {
	p := iface.Stealth.V
	_, port := iface.StealthTransport()
	proxies, err := service.ParseWSProxies(p.Proxies)
	if err != nil {
		return stealthgw.StreamConfig{}, err
	}
	host := "127.0.0.1"
	if len(proxies) > 0 {
		host = ""
		proxies = append(proxies, netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128"))
	}
	var keys []stealth.TransportKey
	for _, k := range []string{p.Key, p.PreviousKey} {
		if k == "" {
			continue
		}
		tk, err := stealth.ParseTransportKey(k)
		if err != nil {
			return stealthgw.StreamConfig{}, err
		}
		keys = append(keys, tk)
	}
	cfg := stealthgw.StreamConfig{
		Listen:        net.JoinHostPort(host, strconv.Itoa(port)),
		Path:          p.Path,
		Origin:        p.Origin,
		Proxies:       proxies,
		ClientHeader:  p.ClientHeader,
		WireGuard:     net.JoinHostPort("127.0.0.1", strconv.Itoa(iface.ListenPort)),
		TransportKeys: keys,
		ReplayFile:    replayFile(r.dataDir, iface),
		Log:           r.log.With("interface", iface.Name),
	}
	copy(cfg.PrivateKey[:], priv)
	return cfg, nil
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
		r.log.Warn("a stealth gateway did not stop in time", "interface", id)
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
