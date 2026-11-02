package stealthgw

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
	"golang.zx2c4.com/wireguard/tun/tuntest"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Load tests: many devices on one gateway at once, each a real wireguard-go
// client whose encrypted traffic rides the transport to a real wireguard-go
// server and back. Gated so an ordinary `go test` never runs them.
//
//	WUI_LOADTEST=1 WUI_LOADN=60 go test -run TestLoad -timeout 20m ./internal/stealthgw
//
// A device passes when a ping it sends reaches the server through the tunnel
// and a ping the server sends reaches it back -- the whole data path, not a
// handshake alone.

func loadN(t *testing.T) int {
	n := 40
	if v := os.Getenv("WUI_LOADN"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 {
			n = x
		}
	}
	if n > 250 {
		n = 250 // the client IP is 10.0.0.(2+i)
	}
	t.Logf("%d devices", n)
	return n
}

// loadDevices draws n WireGuard keys, n transport secrets, and the device list
// the gateway is given.
func loadDevices(t *testing.T, n int) ([]wgtypes.Key, []stealth.Secret, []Device) {
	keys := make([]wgtypes.Key, n)
	secrets := make([]stealth.Secret, n)
	devs := make([]Device, n)
	for i := range keys {
		keys[i] = key(t)
		secrets[i] = secret(t)
		devs[i] = Device{PublicKey: keys[i].PublicKey(), Secret: secrets[i]}
	}
	return keys, secrets, devs
}

// stagger spreads the clients' first handshakes out, so a small box is not
// asked to run every userspace-WireGuard handshake (curve25519, CPU-heavy) at
// the same instant. It changes the timing, not what is tested: every tunnel
// must still carry data both ways. Overridable with WUI_LOADSTAGGER (ms).
func staggerFor() time.Duration {
	ms := 200
	if v := os.Getenv("WUI_LOADSTAGGER"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x >= 0 {
			ms = x
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// runTunnels pings the server from every client and the clients from the
// server and fails the test if any tunnel carries nothing. statsf, logged at
// the end, is the gateway's own figures -- the drop counters say whether a
// tunnel that did not come up was rate-limited or just slow.
func runTunnels(t *testing.T, name string, server *wgEnd, clients []*wgEnd, statsf func() string) {
	var failUp, failDown atomic.Int64
	up := make([]atomic.Bool, len(clients))
	var wg sync.WaitGroup
	stagger := staggerFor()
	start := time.Now()
	for i, cl := range clients {
		wg.Add(1)
		go func(i int, cl *wgEnd) {
			defer wg.Done()
			time.Sleep(time.Duration(i) * stagger)
			ip := netip.AddrFrom4([4]byte{10, 0, 0, byte(2 + i)})
			if !through(cl, server, tuntest.Ping(serverIP, ip), 120*time.Second) {
				failUp.Add(1)
				return
			}
			if !through(server, cl, tuntest.Ping(ip, serverIP), 60*time.Second) {
				failDown.Add(1)
				return
			}
			up[i].Store(true)
		}(i, cl)
	}
	wg.Wait()
	ok := len(clients) - int(failUp.Load()) - int(failDown.Load())
	t.Logf("%s: %d/%d tunnels carried data both ways in %s (up-fail %d, down-fail %d)",
		name, ok, len(clients), time.Since(start).Round(time.Millisecond), failUp.Load(), failDown.Load())
	if statsf != nil {
		t.Logf("%s gateway: %s", name, statsf())
	}
	if failUp.Load() != 0 || failDown.Load() != 0 {
		t.Errorf("%s: %d of %d tunnels did not carry data both ways", name, len(clients)-ok, len(clients))
	}
	// With WUI_LOADSECS set, blast real traffic through every tunnel that came
	// up and see it stay alive under load.
	if throughputSecs() > 0 {
		var live []int
		for i := range clients {
			if up[i].Load() {
				live = append(live, i)
			}
		}
		blastTraffic(t, name, server, clients, live)
	}
}

// throughputSecs is how long the traffic phase blasts, from WUI_LOADSECS; zero
// (the default) turns the traffic phase off.
func throughputSecs() time.Duration {
	if v := os.Getenv("WUI_LOADSECS"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 {
			return time.Duration(x) * time.Second
		}
	}
	return 0
}

// loadPayload is the UDP payload a traffic packet carries, so each one is a
// full-size tunnel packet rather than a bare ping.
const loadPayload = 1200

// ipPacket builds an IPv4/UDP packet from src to dst with a payload of n zero
// bytes. WireGuard routes it by dst on the way out and accepts it by src on the
// way in, so src must be the sending device's address and dst the server's.
func ipPacket(src, dst netip.Addr, n int) []byte {
	total := 20 + 8 + n
	p := make([]byte, total)
	p[0] = 0x45 // IPv4, header length 5 words
	binary.BigEndian.PutUint16(p[2:], uint16(total))
	p[8] = 64 // TTL
	p[9] = 17 // UDP
	copy(p[12:16], src.AsSlice())
	copy(p[16:20], dst.AsSlice())
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(p[10:], ^uint16(sum))
	// A minimal UDP header so the length adds up; the body stays zero.
	binary.BigEndian.PutUint16(p[20:], 40000)
	binary.BigEndian.PutUint16(p[22:], 40001)
	binary.BigEndian.PutUint16(p[24:], uint16(8+n))
	return p
}

// blastTraffic drives real download traffic through the live tunnels for the
// window -- the server sends each client full-size packets, as a customer
// pulling traffic down -- and reports how much got through and whether any
// tunnel stopped carrying it. A drainer per client counts what it received, so
// a tunnel that dies under load shows up as one that delivered nothing.
func blastTraffic(t *testing.T, name string, server *wgEnd, clients []*wgEnd, live []int) {
	if len(live) == 0 {
		return
	}
	dur := throughputSecs()
	recv := make([]atomic.Int64, len(clients))
	var recvBytes, sent atomic.Int64
	stop := make(chan struct{})
	// One drainer per client counts what the server's traffic delivers to it,
	// so a tunnel that stops carrying it shows up as one that got nothing.
	var drain sync.WaitGroup
	for _, idx := range live {
		i, cl := idx, clients[idx]
		drain.Add(1)
		go func() {
			defer drain.Done()
			for {
				select {
				case <-stop:
					for {
						select {
						case p := <-cl.tun.Inbound:
							recv[i].Add(1)
							recvBytes.Add(int64(len(p)))
						default:
							return
						}
					}
				case p := <-cl.tun.Inbound:
					recv[i].Add(1)
					recvBytes.Add(int64(len(p)))
				}
			}
		}()
	}

	deadline := time.Now().Add(dur)
	start := time.Now()
	var wg sync.WaitGroup
	for _, idx := range live {
		i := idx
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip := netip.AddrFrom4([4]byte{10, 0, 0, byte(2 + i)})
			tick := time.NewTicker(2 * time.Millisecond) // ~500 full-size packets/s/tunnel
			defer tick.Stop()
			for time.Now().Before(deadline) {
				<-tick.C
				select {
				case server.tun.Outbound <- ipPacket(serverIP, ip, loadPayload):
					sent.Add(1)
				default:
				}
			}
		}()
	}
	wg.Wait()
	time.Sleep(500 * time.Millisecond) // let the last packets arrive
	close(stop)
	drain.Wait()

	el := time.Since(start)
	var total, dead int64
	minPer := int64(1) << 62
	for _, idx := range live {
		c := recv[idx].Load()
		total += c
		if c == 0 {
			dead++
		}
		if c < minPer {
			minPer = c
		}
	}
	mb := float64(recvBytes.Load()) / (1 << 20)
	t.Logf("%s traffic (download): %d sent, %d delivered over %d tunnels, %.1f MB in %s = %.1f MB/s; min/tunnel %d, dead %d",
		name, sent.Load(), total, len(live), mb, el.Round(time.Millisecond), mb/el.Seconds(), minPer, dead)
	if dead > 0 {
		t.Errorf("%s: %d of %d tunnels stopped carrying traffic under load", name, dead, len(live))
	}
}

func TestLoadTransports(t *testing.T) {
	if os.Getenv("WUI_LOADTEST") == "" {
		t.Skip("set WUI_LOADTEST=1 to run the load tests")
	}
	n := loadN(t)

	t.Run("veil", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		f := newFixture(t, keys...)
		f.gw.SetDevices(devs)
		clients := make([]*wgEnd, n)
		for i := range keys {
			clients[i] = f.client(t, secrets[i], keys[i])
		}
		runTunnels(t, "veil", f.server, clients, func() string { return fmt.Sprintf("%+v", f.gw.Stats()) })
	})

	t.Run("drift", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		f := newStreamFixture(t, nil, keys...)
		f.gw.SetDevices(devs)
		clients := make([]*wgEnd, n)
		for i := range keys {
			clients[i], _ = f.client(t, secrets[i], keys[i])
		}
		runTunnels(t, "drift", f.server, clients, func() string { return fmt.Sprintf("%+v", f.gw.Stats()) })
	})

	t.Run("anchor", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		f := newTLSFixture(t, nil, nil, keys...)
		f.gw.SetDevices(devs)
		clients := make([]*wgEnd, n)
		for i := range keys {
			clients[i], _ = f.client(t, secrets[i], keys[i])
		}
		runTunnels(t, "anchor", f.server, clients, func() string { return fmt.Sprintf("%+v", f.gw.Stats()) })
	})

	t.Run("relay", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		f := newPollFixture(t, keys...)
		f.gw.SetDevices(devs)
		clients := make([]*wgEnd, n)
		for i := range keys {
			clients[i], _ = f.client(t, secrets[i], keys[i], nil)
		}
		runTunnels(t, "relay", f.server, clients, func() string { return fmt.Sprintf("%+v", f.gw.Stats()) })
	})

	t.Run("mold", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		shape, _ := stealth.Preset("get-poll")
		f := newShapeFixture(t, shape, keys...)
		f.gw.SetDevices(devs)
		clients := make([]*wgEnd, n)
		for i := range keys {
			clients[i], _ = f.client(t, secrets[i], keys[i])
		}
		runTunnels(t, "mold", f.server, clients, func() string { return fmt.Sprintf("%+v", f.gw.Stats()) })
	})

	t.Run("bare", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		f := newPlainFixture(t, keys...)
		f.gw.SetDevices(devs)
		clients := make([]*wgEnd, n)
		for i := range keys {
			clients[i], _ = f.client(t, secrets[i], keys[i])
		}
		runTunnels(t, "bare", f.server, clients, func() string { return fmt.Sprintf("%+v", f.gw.Stats()) })
	})

	t.Run("mirror", func(t *testing.T) {
		keys, secrets, devs := loadDevices(t, n)
		runMirror(t, keys, secrets, devs)
	})
}

// --- mirror (faketls) needs a fixture of its own: the others' udpEcho is
// replaced by a real wireguard-go server, so a real tunnel runs through it. ---

func runMirror(t *testing.T, keys []wgtypes.Key, secrets []stealth.Secret, devs []Device) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	port := freePort(t)
	lines := []string{fmt.Sprintf("listen_port=%d", port)}
	for i, p := range keys {
		lines = append(lines, peer(p.PublicKey(), netip.AddrFrom4([4]byte{10, 0, 0, byte(2 + i)})))
	}
	srv := wireguard(t, key(t), lines...)

	var node [32]byte
	rand.Read(node[:])
	site, host := loadFakeSite(t)
	replayPath := filepath.Join(t.TempDir(), "replay")
	if err := writeLimit(replayPath, uint64(time.Now().Unix())-100); err != nil {
		t.Fatal(err)
	}
	g, err := NewFakeTLS(FakeTLSConfig{
		Listen: "127.0.0.1:0", Site: site, NodeKey: node,
		WireGuard: fmt.Sprintf("127.0.0.1:%d", port), PrivateKey: srv.key, ReplayFile: replayPath,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Listen(); err != nil {
		t.Fatal(err)
	}
	go g.Serve(ctx)
	g.SetDevices(devs)

	npriv, _ := ecdh.X25519().NewPrivateKey(node[:])
	nodePub := base64.StdEncoding.EncodeToString(npriv.PublicKey().Bytes())
	anchor := stealth.Anchor(srv.key.PublicKey())

	clients := make([]*wgEnd, len(keys))
	for i := range keys {
		c, err := NewFakeTLSClient(FakeTLSClientConfig{
			Dial: g.Addr().String(), Name: host, NodeKey: nodePub,
			Listen: "127.0.0.1:0", Secret: secrets[i], Anchor: anchor,
		})
		if err != nil {
			t.Fatal(err)
		}
		go c.Serve(ctx)
		clients[i] = wireguard(t, keys[i], peer(srv.key.PublicKey(), serverIP, "endpoint="+c.LocalAddr().String()))
	}
	runTunnels(t, "mirror", srv, clients, func() string { return fmt.Sprintf("%+v", g.Stats()) })
}

// loadFakeSite is a bare TCP site for the gateway to borrow: it answers every
// relayed stranger and is never reached by a device.
func loadFakeSite(t *testing.T) (addr, host string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c)
			}(c)
		}
	}()
	h, _, _ := net.SplitHostPort(ln.Addr().String())
	return ln.Addr().String(), h
}
