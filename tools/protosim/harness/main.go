//go:build linux

// Command harness is the server end of the protocol simulation: the panel's
// own driver for one protocol, opened on a tunnel built the way the panel
// builds one, given devices, and asked for each device's file -- exactly what
// a customer downloads. It then lets customers out the way the panel does
// (routing.BuildNAT) and waits, so clients in other containers can dial in
// with those files.
//
// Writing ctl/drop takes the last device away (a Sync without it), as the
// panel does when a customer is disabled; ctl/dropped says it is done.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/backend/ovpndriver"
	"github.com/abolfazl/w-ui/internal/backend/wgdriver"
	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ovpnconf"
	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/wgkey"
)

// devices is how many customers the tunnel carries; the last is the one
// taken away mid-run.
const devices = 3

func main() {
	protocol := flag.String("protocol", "", "wireguard or openvpn")
	transport := flag.String("transport", "udp", "openvpn: udp or tcp")
	endpoint := flag.String("endpoint", "", "the address clients dial")
	out := flag.String("out", "/out", "where device files are written")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	iface := &model.Interface{
		ID: 1, NodeID: 1, Enabled: true,
		Protocol:     model.Protocol(*protocol),
		Subnet:       "10.66.0.0/24",
		EndpointHost: *endpoint,
		DNS:          "1.1.1.1",
		NATInterface: "eth1",
		Mode:         model.ModeStandard,
	}

	var drv backend.Backend
	switch iface.Protocol {
	case model.ProtocolWireGuard:
		pair, err := wgkey.NewPair()
		must(err)
		iface.Name, iface.ListenPort, iface.MTU = "wg0", 51820, 1420
		iface.PrivateKey, iface.PublicKey = pair.Private.String(), pair.Public.String()
		d := wgdriver.New()
		d.SetLogger(log)
		drv = d
	case model.ProtocolOpenVPN:
		params, err := ovpnconf.NewPKI("ovpn0")
		must(err)
		params.Transport = *transport
		iface.Name, iface.ListenPort, iface.MTU = "ovpn0", 1194, 1420
		iface.OpenVPN = model.JSON(params)
		ovpndriver.DataRoot = "/var/lib/wui"
		d := ovpndriver.New()
		d.SetLogger(log)
		drv = d
	default:
		fail("unknown protocol %q", *protocol)
	}

	must(drv.Open(ctx, iface))

	// Every device as the panel makes one: an address of its own, and the
	// protocol's credentials.
	var accounts []model.Account
	for i := 1; i <= devices; i++ {
		a := model.Account{
			ID: uint(i), InterfaceID: iface.ID, NodeID: 1, Enabled: true,
			DeviceName: fmt.Sprintf("dev%02d", i),
			IP:         fmt.Sprintf("10.66.0.%d", i+1),
		}
		if iface.Protocol == model.ProtocolWireGuard {
			pair, err := wgkey.NewPair()
			must(err)
			psk, err := wgkey.GeneratePresharedKey()
			must(err)
			a.PrivateKey, a.PublicKey, a.PresharedKey = pair.Private.String(), pair.Public.String(), psk.String()
		} else {
			secret, err := ovpnconf.NewSecret(20)
			must(err)
			a.Username, a.Secret = fmt.Sprintf("s1-dev%02d", i), secret
		}
		accounts = append(accounts, a)
	}
	sync := func(n int) {
		var want []backend.DesiredAccount
		for _, a := range accounts[:n] {
			want = append(want, backend.DesiredAccount{
				ID: a.ID, DeviceName: a.DeviceName, IP: netip.MustParseAddr(a.IP),
				PublicKey: a.PublicKey, PresharedKey: a.PresharedKey,
				Username: a.Username, Secret: a.Secret,
			})
		}
		rep, err := drv.Sync(ctx, want)
		must(err)
		log.Info("synced", "devices", n, "report", fmt.Sprintf("%+v", rep))
	}
	sync(devices)

	// Each device's file, and what the client should find once it is in.
	must(os.MkdirAll(*out, 0o755))
	for _, a := range accounts {
		p, err := drv.Render(ctx, &a, iface)
		must(err)
		must(os.WriteFile(filepath.Join(*out, a.DeviceName+".profile"), p.Body, 0o644))
		creds := a.Username + "\n" + a.Secret + "\n"
		must(os.WriteFile(filepath.Join(*out, a.DeviceName+".creds"), []byte(creds), 0o600))
		must(os.WriteFile(filepath.Join(*out, a.DeviceName+".ip"), []byte(a.IP), 0o644))
	}
	// A device the server never heard of: a WireGuard key it does not
	// know, an OpenVPN password that is not the device's.
	stranger := accounts[0]
	if iface.Protocol == model.ProtocolWireGuard {
		pair, err := wgkey.NewPair()
		must(err)
		stranger.PrivateKey, stranger.PublicKey = pair.Private.String(), pair.Public.String()
		stranger.DeviceName = "stranger"
	} else {
		stranger.DeviceName = "stranger"
		stranger.Secret = "NotThePassword123"
	}
	p, err := drv.Render(ctx, &stranger, iface)
	must(err)
	must(os.WriteFile(filepath.Join(*out, "stranger.profile"), p.Body, 0o644))
	must(os.WriteFile(filepath.Join(*out, "stranger.creds"), []byte(stranger.Username+"\n"+stranger.Secret+"\n"), 0o600))
	must(os.WriteFile(filepath.Join(*out, "stranger.ip"), []byte(stranger.IP), 0o644))

	// Customers out to the internet, as the panel's routing does it.
	nat := routing.BuildNAT([]netip.Prefix{netip.MustParsePrefix(iface.Subnet)}, []string{iface.Name})
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(nat)
	if b, err := cmd.CombinedOutput(); err != nil {
		fail("nft: %v: %s", err, b)
	}

	must(drv.Health(ctx))
	fmt.Println("server ready")

	ctl := filepath.Join(*out, "ctl")
	must(os.MkdirAll(ctl, 0o777))
	for {
		if _, err := os.Stat(filepath.Join(ctl, "drop")); err == nil {
			sync(devices - 1)
			// Nothing more: the Sync alone has to end it -- OpenVPN's
			// driver disconnects a removed sign-in, a WireGuard peer is gone.
			must(os.Remove(filepath.Join(ctl, "drop")))
			must(os.WriteFile(filepath.Join(ctl, "dropped"), nil, 0o644))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "harness: "+format+"\n", a...)
	os.Exit(1)
}
