package stealthgw

import (
	"bufio"
	"context"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-faketls reference client.
//
// A local UDP port a stock WireGuard sends to as if it were the server, and
// the faketls side of it, written once here so the gateway is tested against
// the client rules and an app has a client to follow. Like the other stream
// clients it cannot make WireGuard send anything, so it opens a session with
// the next handshake initiation or keepalive WireGuard sends and drops what
// comes while it has none. A session is a device's ClientHello, sealed with the
// proof, then the gateway's hand-written flight, then the record carry.

// FakeTLSClientConfig is one device's wui-faketls client for one tunnel.
type FakeTLSClientConfig struct {
	// Dial is the gateway's address, host:port.
	Dial string
	// Name is the SNI the hello asks for: one of the gateway's borrowed-site
	// names.
	Name string
	// NodeKey is the gateway's public transport key, base64 of 32 bytes.
	NodeKey string
	// Listen is the local address WireGuard sends to; ":0" on the loopback.
	Listen string
	Secret stealth.Secret
	// Anchor is the tunnel's WireGuard public key, which is also the inbound
	// the proof is bound to.
	Anchor stealth.Anchor
	// Offset is added to the clock for a proof's timestamp.
	Offset       time.Duration
	Wide, Normal int
}

// FakeTLSClient is a running wui-faketls reference client.
type FakeTLSClient struct {
	cfg    FakeTLSClientConfig
	client *stealth.FakeClient
	local  *net.UDPConn
	ctx    context.Context

	mu         sync.Mutex
	app        netip.AddrPort
	cur        *fakeClientConn
	connecting bool
	failures   int
	nextTry    time.Time
	sessions   atomic.Uint64
}

// fakeClientConn is one connection's record layer.
type fakeClientConn struct {
	conn   net.Conn
	r      *bufio.Reader
	conn13 *stealth.FakeConn
	out    chan []byte
	done   chan struct{}
	once   sync.Once
	wait   atomic.Int64 // the first send since the last word heard, zero when none
}

// NewFakeTLSClient opens the client's local socket.
func NewFakeTLSClient(cfg FakeTLSClientConfig) (*FakeTLSClient, error) {
	if cfg.Name == "" {
		return nil, errors.New("stealthgw: the client needs a site name")
	}
	node, err := base64.StdEncoding.DecodeString(cfg.NodeKey)
	if err != nil || len(node) != 32 {
		return nil, fmt.Errorf("stealthgw: the node key is not 32 base64 bytes")
	}
	keys := stealth.Derive(cfg.Secret, cfg.Anchor)
	client := stealth.NewFakeClient(node, keys, cfg.Anchor[:])
	la, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("stealthgw: listen address: %w", err)
	}
	local, err := net.ListenUDP("udp", la)
	if err != nil {
		return nil, err
	}
	return &FakeTLSClient{cfg: cfg, client: client, local: local}, nil
}

// LocalAddr is where WireGuard is to send.
func (c *FakeTLSClient) LocalAddr() net.Addr { return c.local.LocalAddr() }

// Sessions is how many sessions the client has opened.
func (c *FakeTLSClient) Sessions() uint64 { return c.sessions.Load() }

// Serve runs the client until ctx ends.
func (c *FakeTLSClient) Serve(ctx context.Context) error {
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
func (c *FakeTLSClient) fromApp(from netip.AddrPort, d []byte) {
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
	if c.connecting || !(isInitiation(d) || isKeepalive(d)) || now.Before(c.nextTry) {
		return
	}
	c.connecting = true
	go c.connect(d)
}

// connect opens a session and sends d on it first.
func (c *FakeTLSClient) connect(first []byte) {
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

// open connects, seals a hello, finishes the handshake, and returns the
// session's record layer.
func (c *FakeTLSClient) open() (*fakeClientConn, error) {
	ctx, cancel := context.WithTimeout(c.ctx, dialFor)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.cfg.Dial)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	conn.SetDeadline(time.Now().Add(dialFor))

	x := make([]byte, 32)
	if _, err := rand.Read(x); err != nil {
		conn.Close()
		return nil, err
	}
	// The hybrid X25519MLKEM768 group, as a modern client offers: the proof is
	// keyed to the X25519 half, the ML-KEM half makes the session's keys
	// quantum-resistant.
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		conn.Close()
		return nil, err
	}
	hello, ok := stealth.FakeHelloHybrid(c.cfg.Name, x, dk.EncapsulationKey().Bytes())
	if !ok {
		conn.Close()
		return nil, errors.New("stealthgw: the hello did not build")
	}
	if !c.client.Carry(hello, x, time.Now().Add(c.cfg.Offset).Unix()) {
		conn.Close()
		return nil, errors.New("stealthgw: the hello did not seal")
	}
	rec := append([]byte{22, 3, 3, byte(len(hello) >> 8), byte(len(hello))}, hello...)
	if _, err := conn.Write(rec); err != nil {
		conn.Close()
		return nil, err
	}

	r := bufio.NewReaderSize(conn, fakeRecordMax+stealth.TLSHeader)
	flight, err := readFlight(r)
	if err != nil {
		conn.Close()
		return nil, err
	}
	expected := c.client.Leaf(x, hello)
	clientFin, conn13, err := stealth.FakeClientFlight(hello, x, dk.Bytes(), expected, flight)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write(clientFin); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	s := &fakeClientConn{
		conn: conn, r: r, conn13: conn13,
		out: make(chan []byte, streamQueue), done: make(chan struct{}),
	}
	return s, nil
}

// readFlight reads the server's handshake flight: a plaintext ServerHello
// record, an optional change_cipher_spec, and the encrypted handshake record.
func readFlight(r *bufio.Reader) ([]byte, error) {
	outer, first, err := readRecord(r)
	if err != nil {
		return nil, err
	}
	if outer != 22 {
		return nil, errors.New("stealthgw: the flight does not begin with a ServerHello")
	}
	flight := append([]byte(nil), first...)
	for i := 0; i < 3; i++ {
		outer, rec, err := readRecord(r)
		if err != nil {
			return nil, err
		}
		flight = append(flight, rec...)
		if outer == 23 { // the encrypted handshake record
			return flight, nil
		}
	}
	return nil, errors.New("stealthgw: no encrypted handshake record")
}

func (s *fakeClientConn) queue(d []byte) {
	select {
	case s.out <- d:
		s.wait.CompareAndSwap(0, time.Now().UnixNano())
	default:
	}
}

// dead says the session was sent on and has not answered for streamLiveness.
func (s *fakeClientConn) dead(now time.Time) bool {
	select {
	case <-s.done:
		return true
	default:
	}
	w := s.wait.Load()
	return w != 0 && now.Sub(time.Unix(0, w)) >= streamLiveness
}

func (s *fakeClientConn) end() {
	s.once.Do(func() {
		close(s.done)
		if sealed, ok := s.conn13.Send.Seal(21, []byte{1, 0}, 0, nil); ok {
			s.conn.SetWriteDeadline(time.Now().Add(time.Second))
			s.conn.Write(sealed)
		}
		s.conn.Close()
	})
}

func (c *FakeTLSClient) drop(s *fakeClientConn) {
	s.end()
	c.mu.Lock()
	if c.cur == s {
		c.cur = nil
	}
	c.mu.Unlock()
}

// reader takes the server's records to WireGuard.
func (c *FakeTLSClient) reader(s *fakeClientConn) {
	defer c.drop(s)
	var carry []byte
	for {
		outer, record, err := readRecord(s.r)
		if err != nil {
			return
		}
		if outer == 20 {
			continue
		}
		kind, content, ok := openRecord13(s.conn13.Recv, record)
		if !ok || kind == 21 {
			return
		}
		if kind != 23 {
			continue
		}
		c.mu.Lock()
		app := c.app
		c.mu.Unlock()
		plain := content
		if carry != nil {
			plain = append(carry, content...)
			carry = nil
		}
		consumed, err := stealth.ReadFakeFrames(plain, func(d []byte) {
			if app.IsValid() {
				c.local.WriteToUDPAddrPort(d, app)
			}
		})
		if err != nil {
			return
		}
		if consumed < len(plain) {
			carry = append([]byte(nil), plain[consumed:]...)
		}
		s.wait.Store(0)
	}
}

// writer seals what WireGuard sends into records, and sends a padding-only
// record when the session has been quiet a while.
func (c *FakeTLSClient) writer(s *fakeClientConn) {
	defer c.drop(s)
	var (
		batch  [][]byte
		carry  []byte
		record = make([]byte, 0, fakeRecordInner+512)
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
				if fakeFits(batch, d) {
					batch = append(batch, d)
				} else {
					carry = d
					break drain
				}
			default:
				break drain
			}
		}
		frame, ok := stealth.AppendFakeFrame(nil, 0, batch...)
		if !ok {
			return
		}
		sealed, ok := s.conn13.Send.Seal(23, frame, 0, record[:0])
		if !ok {
			return
		}
		s.conn.SetWriteDeadline(time.Now().Add(writeFor))
		if _, err := s.conn.Write(sealed); err != nil {
			return
		}
		keep.Reset(streamKeepMin + jitter(streamKeepMax-streamKeepMin))
	}
}
