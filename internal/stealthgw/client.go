package stealthgw

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The reference client.
//
// A local UDP port a stock WireGuard sends to as if it were the server, and
// the veil side of it: what an app does, written once here so that the
// gateway is tested against the client rules and an app has a client to
// follow. It cannot make WireGuard send anything, so it opens a session with
// the next handshake initiation or keepalive WireGuard sends; an app that
// embeds WireGuard asks it for a handshake instead.

// liveness is how long a client waits for a word on a session it sent on
// before it takes the session for dead and opens another.
const liveness = 10 * time.Second

// ClientConfig is one device's client for one tunnel.
type ClientConfig struct {
	// Server is the gateway's address.
	Server string
	// Listen is the local address WireGuard sends to; ":0" on the loopback
	// is usual.
	Listen string
	Secret stealth.Secret
	// Anchor is the tunnel's WireGuard public key.
	Anchor stealth.Anchor
	MTU    int
	// Offset is added to the clock for an opening's timestamp, when the
	// client knows how far its clock is off the server's.
	Offset       time.Duration
	Wide, Normal int
}

// Client is a running reference client.
type Client struct {
	cfg    ClientConfig
	udp    *stealth.UDPClient
	room   int
	local  *net.UDPConn
	remote *net.UDPConn

	mu      sync.Mutex
	app     netip.AddrPort // where WireGuard sends from
	cur     *clientSession
	next    *clientSession // opened, not answered yet
	attempt int
}

type clientSession struct {
	send   *stealth.UDPSender
	recv   *stealth.UDPReceiver
	heard  time.Time // the last packet that opened
	waited time.Time // the first send since heard, zero when none
}

// NewClient opens the client's sockets.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.MTU <= 0 {
		return nil, errors.New("stealthgw: the tunnel's MTU is not set")
	}
	la, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("stealthgw: listen address: %w", err)
	}
	ra, err := net.ResolveUDPAddr("udp", cfg.Server)
	if err != nil {
		return nil, fmt.Errorf("stealthgw: server address: %w", err)
	}
	local, err := net.ListenUDP("udp", la)
	if err != nil {
		return nil, err
	}
	remote, err := net.DialUDP("udp", nil, ra)
	if err != nil {
		local.Close()
		return nil, err
	}
	return &Client{
		cfg: cfg, udp: stealth.NewUDPClient(cfg.Secret, cfg.Anchor),
		room: cfg.MTU + stealth.UDPOverhead, local: local, remote: remote,
	}, nil
}

// LocalAddr is where WireGuard is to send.
func (c *Client) LocalAddr() net.Addr { return c.local.LocalAddr() }

// Serve runs the client until ctx ends.
func (c *Client) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		c.local.Close()
		c.remote.Close()
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65536)
		scratch := make([]byte, 0, 65536)
		for {
			n, err := c.remote.Read(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			c.fromServer(buf[:n], scratch)
		}
	}()
	buf := make([]byte, 65536)
	for {
		n, from, err := c.local.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			continue
		}
		c.fromApp(from, buf[:n])
	}
	<-done
	return nil
}

// worn says the session has carried enough to be replaced.
func (s *clientSession) worn() bool {
	return s.send.Sent() >= stealth.ClientRenew || s.recv.Received() >= stealth.ClientRenew
}

// dead says the session was sent on and has not answered for liveness: the
// gateway echoes a keepalive, so a live session is never quiet that long.
func (s *clientSession) dead(now time.Time) bool {
	return !s.waited.IsZero() && now.Sub(s.waited) >= liveness
}

// fromApp is a datagram WireGuard sent to the server.
func (c *Client) fromApp(from netip.AddrPort, d []byte) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.app = from
	cur := c.cur
	// A session is opened with an initiation, or a keepalive, when there is
	// none, or the one there wore out or went quiet. A worn one carries on
	// until the new one is answered; a quiet one is taken for dead. Another
	// initiation while an opening waits is WireGuard trying again, and so
	// is the opening.
	dead := cur == nil || cur.dead(now)
	if (isInitiation(d) || isKeepalive(d)) && (dead || cur.worn()) && (c.next == nil || isInitiation(d)) {
		c.openLocked(d, 0)
		return
	}
	if dead {
		return // nothing to carry it, and data cannot open a session
	}
	p, err := cur.send.Seal(nil, d, false, max(c.room, len(d)+2+stealth.DataMin))
	if err != nil {
		return
	}
	if cur.waited.IsZero() {
		cur.waited = now
	}
	c.remote.Write(p)
}

// openLocked sends an opening around d, and again after each retry delay
// while nothing has answered it: each time under a fresh salt, since the
// server takes a salt once.
func (c *Client) openLocked(d []byte, retry int) {
	pad := stealth.NewPadder(c.cfg.Wide, c.cfg.Normal)
	p, keys, err := c.udp.Open(nil, time.Now().Add(c.cfg.Offset), d, &pad, c.room)
	if err != nil {
		return
	}
	c.next = &clientSession{send: stealth.NewUDPSender(keys.Up, pad), recv: stealth.NewUDPReceiver(keys.Down)}
	if retry == 0 {
		c.attempt++
	}
	c.remote.Write(p)
	if retry == len(stealth.Retries) {
		return
	}
	attempt, held := c.attempt, append([]byte(nil), d...)
	time.AfterFunc(stealth.Retries[retry], func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.attempt == attempt && c.next != nil {
			c.openLocked(held, retry+1)
		}
	})
}

// fromServer is a packet from the gateway.
func (c *Client) fromServer(p, scratch []byte) {
	now := time.Now()
	c.mu.Lock()
	var d []byte
	var err error = stealth.ErrRefused
	if c.next != nil {
		if d, err = c.next.recv.Open(p, scratch); err == nil {
			c.cur, c.next = c.next, nil
			c.attempt++
		}
	}
	if err != nil && c.cur != nil {
		d, err = c.cur.recv.Open(p, scratch)
	}
	if err != nil {
		c.mu.Unlock()
		return
	}
	c.cur.heard, c.cur.waited = now, time.Time{}
	app := c.app
	c.mu.Unlock()
	if d != nil && app.IsValid() {
		c.local.WriteToUDPAddrPort(d, app)
	}
}
