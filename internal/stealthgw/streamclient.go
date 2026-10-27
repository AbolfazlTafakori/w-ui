package stealthgw

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-ws reference client.
//
// A local UDP port a stock WireGuard sends to as if it were the server, and
// the wui-ws side of it, written once here so that the gateway is tested
// against the client rules and an app has a client to follow. Like the
// wui-udp client it cannot make WireGuard send anything, so it opens a
// session with the next handshake initiation or keepalive WireGuard sends,
// and drops what comes while it has none.

// Stream client timers.
const (
	// streamKeepMin and streamKeepMax bound the quiet a client lets pass
	// before it sends a padding-only chunk: well inside the minute a front
	// gives a quiet connection.
	streamKeepMin = 20 * time.Second
	streamKeepMax = 40 * time.Second
	// dialFor bounds a connection and its upgrade.
	dialFor = 10 * time.Second
	// retryMin and retryMax bound the wait between two connections that
	// failed.
	retryMin = 250 * time.Millisecond
	retryMax = 8 * time.Second
	// streamLiveness is how long a client waits for a word on a session it
	// sent on before it takes the session for dead. Longer than wui-udp's:
	// on a one-way flow WireGuard's own keepalive comes after ten seconds,
	// and the gateway holds it a moment more.
	streamLiveness = 20 * time.Second
)

// StreamClientConfig is one device's wui-ws client for one tunnel.
type StreamClientConfig struct {
	// URL is the transport: ws:// or wss://, the front's name and the
	// transport's path.
	URL string
	// Dial is the address connected to, when it is not the URL's host: a
	// CDN's address, say. The URL's host is still the name TLS and the
	// request give.
	Dial string
	// TLS is the client's TLS for wss://; nil is the system's roots.
	TLS *tls.Config
	// Listen is the local address WireGuard sends to; ":0" on the loopback
	// is usual.
	Listen string
	Secret stealth.Secret
	// Anchor is the tunnel's WireGuard public key.
	Anchor stealth.Anchor
	// TransportKey is the server's public transport key, base64.
	TransportKey string
	// Offset is added to the clock for a proof's timestamp, when the client
	// knows how far its clock is off the server's.
	Offset       time.Duration
	Wide, Normal int
}

// StreamClient is a running wui-ws reference client.
type StreamClient struct {
	cfg    StreamClientConfig
	url    *url.URL
	proofs *stealth.ProofClient
	local  *net.UDPConn
	ctx    context.Context

	mu         sync.Mutex
	app        netip.AddrPort
	cur        *clientStream
	connecting bool
	failures   int
	nextTry    time.Time
	sessions   atomic.Uint64
}

// clientStream is one connection.
type clientStream struct {
	conn  net.Conn
	send  *stealth.StreamSender
	recv  *stealth.StreamReceiver
	out   chan []byte
	done  chan struct{}
	once  sync.Once
	heard atomic.Int64 // the last chunk that opened
	wait  atomic.Int64 // the first send since heard, zero when none
}

// NewStreamClient opens the client's local socket.
func NewStreamClient(cfg StreamClientConfig) (*StreamClient, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return nil, fmt.Errorf("stealthgw: %q is not a ws:// or wss:// address", cfg.URL)
	}
	proofs, err := stealth.NewProofClient(cfg.Secret, cfg.Anchor, stealth.WS, cfg.TransportKey)
	if err != nil {
		return nil, err
	}
	la, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("stealthgw: listen address: %w", err)
	}
	local, err := net.ListenUDP("udp", la)
	if err != nil {
		return nil, err
	}
	return &StreamClient{cfg: cfg, url: u, proofs: proofs, local: local}, nil
}

// LocalAddr is where WireGuard is to send.
func (c *StreamClient) LocalAddr() net.Addr { return c.local.LocalAddr() }

// Sessions is how many sessions the client has opened.
func (c *StreamClient) Sessions() uint64 { return c.sessions.Load() }

// Serve runs the client until ctx ends.
func (c *StreamClient) Serve(ctx context.Context) error {
	c.ctx = ctx
	go func() {
		<-ctx.Done()
		c.local.Close()
		c.mu.Lock()
		cur := c.cur
		c.mu.Unlock()
		if cur != nil {
			cur.end()
		}
	}()
	buf := make([]byte, 65536)
	for {
		n, from, err := c.local.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		c.fromApp(from, append([]byte(nil), buf[:n]...))
	}
}

// fromApp is a datagram WireGuard sent to the server.
func (c *StreamClient) fromApp(from netip.AddrPort, d []byte) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.app = from
	if c.cur != nil && c.cur.dead(now) {
		c.cur.end()
		c.cur = nil
	}
	if c.cur != nil {
		c.cur.queue(d)
		return
	}
	// A session is opened with an initiation or a keepalive, never with
	// user data, and not again before the wait after a failure is over.
	if c.connecting || !(isInitiation(d) || isKeepalive(d)) || now.Before(c.nextTry) {
		return
	}
	c.connecting = true
	go c.connect(d)
}

// connect opens a session and sends d on it first.
func (c *StreamClient) connect(first []byte) {
	s, err := c.open()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connecting = false
	if err != nil {
		c.failures++
		wait := min(retryMax, retryMin<<min(c.failures-1, 5))
		c.nextTry = time.Now().Add(wait)
		return
	}
	c.failures, c.nextTry = 0, time.Time{}
	c.cur = s
	c.sessions.Add(1)
	s.queue(first)
	go c.reader(s)
	go c.writer(s)
}

// open connects, proves the device and takes the upgrade.
func (c *StreamClient) open() (*clientStream, error) {
	host := c.url.Host
	if c.url.Port() == "" {
		port := "80"
		if c.url.Scheme == "wss" {
			port = "443"
		}
		host = net.JoinHostPort(c.url.Hostname(), port)
	}
	addr := host
	if c.cfg.Dial != "" {
		addr = c.cfg.Dial
	}
	ctx, cancel := context.WithTimeout(c.ctx, dialFor)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if c.url.Scheme == "wss" {
		cfg := &tls.Config{}
		if c.cfg.TLS != nil {
			cfg = c.cfg.TLS.Clone()
		}
		if cfg.ServerName == "" {
			cfg.ServerName = c.url.Hostname()
		}
		cfg.NextProtos = []string{"http/1.1"}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = tc
	}
	token, keys, err := c.proofs.Token(time.Now().Add(c.cfg.Offset))
	if err != nil {
		conn.Close()
		return nil, err
	}
	wsKey := stealth.NewWebSocketKey()
	path := c.url.EscapedPath()
	req := "GET " + path + "?" + token + " HTTP/1.1\r\n" +
		"Host: " + c.url.Host + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + wsKey + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	conn.SetDeadline(time.Now().Add(dialFor))
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, stealth.MaxSealed+14)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != stealth.WebSocketAccept(wsKey) {
		conn.Close()
		return nil, fmt.Errorf("stealthgw: the server did not take the session: %s", resp.Status)
	}
	conn.SetDeadline(time.Time{})
	send, err := stealth.NewStreamSender(keys.Up, stealth.NewPadder(c.cfg.Wide, c.cfg.Normal))
	if err != nil {
		conn.Close()
		return nil, err
	}
	recv, err := stealth.NewStreamReceiver(keys.Down)
	if err != nil {
		conn.Close()
		return nil, err
	}
	s := &clientStream{conn: &bufConn{Conn: conn, r: br}, send: send, recv: recv, out: make(chan []byte, streamQueue), done: make(chan struct{})}
	s.heard.Store(time.Now().UnixNano())
	return s, nil
}

// bufConn reads through the reader the upgrade was read with, which may
// hold the first frames already.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (s *clientStream) queue(d []byte) {
	select {
	case s.out <- d:
		s.wait.CompareAndSwap(0, time.Now().UnixNano())
	default:
	}
}

// dead says the session was sent on and has not answered for streamLiveness: the
// gateway echoes what says nothing, so a live session is never quiet that
// long.
func (s *clientStream) dead(now time.Time) bool {
	select {
	case <-s.done:
		return true
	default:
	}
	w := s.wait.Load()
	return w != 0 && now.Sub(time.Unix(0, w)) >= streamLiveness
}

func (s *clientStream) end() {
	s.once.Do(func() {
		close(s.done)
		s.conn.SetWriteDeadline(time.Now().Add(time.Second))
		s.conn.Write(stealth.AppendFrame(nil, stealth.OpClose, stealth.CloseNormal, true))
		s.conn.Close()
	})
}

func (c *StreamClient) drop(s *clientStream) {
	s.end()
	c.mu.Lock()
	if c.cur == s {
		c.cur = nil
	}
	c.mu.Unlock()
}

// reader takes the server's chunks to WireGuard.
func (c *StreamClient) reader(s *clientStream) {
	defer c.drop(s)
	buf := make([]byte, stealth.MaxSealed)
	for {
		op, payload, err := stealth.ReadFrame(s.conn, buf, false)
		if err != nil {
			return
		}
		switch op {
		case stealth.OpClose:
			return
		case stealth.OpPing:
			s.conn.Write(stealth.AppendFrame(nil, stealth.OpPong, payload, true))
			continue
		case stealth.OpPong:
			continue
		}
		c.mu.Lock()
		app := c.app
		c.mu.Unlock()
		err = s.recv.Open(payload, func(d []byte) {
			if app.IsValid() {
				c.local.WriteToUDPAddrPort(d, app)
			}
		})
		if err != nil {
			return
		}
		s.heard.Store(time.Now().UnixNano())
		s.wait.Store(0)
	}
}

// writer seals what WireGuard sends into chunks, and sends a padding-only
// chunk when the session has been quiet a while.
func (c *StreamClient) writer(s *clientStream) {
	defer c.drop(s)
	var (
		batch  [][]byte
		carry  []byte
		sealed = make([]byte, 0, stealth.MaxSealed)
		frame  = make([]byte, 0, stealth.MaxSealed+14)
	)
	keep := time.NewTimer(streamKeepMin + jitter(streamKeepMax-streamKeepMin))
	defer keep.Stop()
	for {
		batch = batch[:0]
		if carry != nil {
			batch, carry = append(batch, carry), nil
		} else {
			select {
			case <-s.done:
				return
			case <-keep.C:
				s.wait.CompareAndSwap(0, time.Now().UnixNano())
			case d := <-s.out:
				batch = append(batch, d)
			}
		}
	drain:
		for {
			select {
			case d := <-s.out:
				if stealth.StreamFits(append(batch, d)) <= len(batch) {
					carry = d
					break drain
				}
				batch = append(batch, d)
			default:
				break drain
			}
		}
		var err error
		if sealed, err = s.send.Seal(sealed[:0], batch...); err != nil {
			return
		}
		s.conn.SetWriteDeadline(time.Now().Add(writeFor))
		if _, err := s.conn.Write(stealth.AppendFrame(frame[:0], stealth.OpBinary, sealed, true)); err != nil {
			return
		}
		keep.Reset(streamKeepMin + jitter(streamKeepMax-streamKeepMin))
	}
}
