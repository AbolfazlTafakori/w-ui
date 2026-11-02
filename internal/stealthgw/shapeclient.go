package stealthgw

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The mold reference client.
//
// It is relay's client dressed in the operator's shape: it opens a session
// with the proof in the one place a token may go for the shape, numbers the
// chunks of request n at n*4096, and either polls (one request in flight, its
// answer carrying what waits) or, for a stream shape, opens one long
// Server-Sent-Events response that carries all the downstream data while its
// upstream rides later requests the gateway answers with padding alone.
//
// The rules an app keeps are relay's, with the shape on top:
//   - The opening request carries the proof and is sent once. For a poll
//     shape its answer opens the session; for a stream shape the opening is
//     the stream request, and its first event carries the tunnel's response.
//   - Poll: one request in flight, the poll wait of relay. Stream: the
//     stream carries the downstream; upstream datagrams go in later requests,
//     each answered with a padding-only chunk.
//   - A stream that ends falls back to polling when the shape allows it;
//     otherwise the session is over and a fresh one is opened.

// ShapeClientConfig is one device's mold client for one tunnel.
type ShapeClientConfig struct {
	URL          string
	Dial         string
	TLS          *tls.Config
	Listen       string
	Secret       stealth.Secret
	Anchor       stealth.Anchor
	TransportKey string
	Offset       time.Duration
	Wide, Normal int
	// Shape is the transport's shape, as the profile carries it.
	Shape stealth.Shape
}

// ShapeClient is a running mold reference client.
type ShapeClient struct {
	cfg    ShapeClientConfig
	url    *url.URL
	path   string
	proofs *stealth.ProofClient
	local  *net.UDPConn
	ctx    context.Context

	mu         sync.Mutex
	app        netip.AddrPort
	cur        *shapeRun
	connecting bool
	failures   int
	nextTry    time.Time
	sessions   atomic.Uint64
	requests   atomic.Uint64
}

// shapeRun is one session: what WireGuard hands it, and its end.
type shapeRun struct {
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func (r *shapeRun) end() { r.once.Do(func() { close(r.done) }) }

func (r *shapeRun) queue(d []byte) {
	select {
	case r.out <- d:
	default:
	}
}

// NewShapeClient opens the client's local socket.
func NewShapeClient(cfg ShapeClientConfig) (*ShapeClient, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("stealthgw: %q is not an https:// address", cfg.URL)
	}
	proofs, err := stealth.NewProofClient(cfg.Secret, cfg.Anchor, stealth.Mold, cfg.TransportKey)
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
	return &ShapeClient{cfg: cfg, url: u, path: u.EscapedPath(), proofs: proofs, local: local}, nil
}

// LocalAddr is where WireGuard is to send.
func (c *ShapeClient) LocalAddr() net.Addr { return c.local.LocalAddr() }

// Sessions is how many sessions the client has opened.
func (c *ShapeClient) Sessions() uint64 { return c.sessions.Load() }

// Requests is how many requests were answered.
func (c *ShapeClient) Requests() uint64 { return c.requests.Load() }

// Serve runs the client until ctx ends.
func (c *ShapeClient) Serve(ctx context.Context) error {
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

func (c *ShapeClient) fromApp(from netip.AddrPort, d []byte) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.app = from
	if c.cur != nil {
		c.cur.queue(d)
		return
	}
	if c.connecting || !(isInitiation(d) || isKeepalive(d)) || now.Before(c.nextTry) {
		return
	}
	c.connecting = true
	go c.session(d)
}

// deliver hands a datagram to WireGuard.
func (c *ShapeClient) deliver(d []byte) {
	c.mu.Lock()
	app := c.app
	c.mu.Unlock()
	if app.IsValid() {
		c.local.WriteToUDPAddrPort(d, app)
	}
}

func (c *ShapeClient) failed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connecting = false
	c.failures++
	c.nextTry = time.Now().Add(min(retryMax, retryMin<<min(c.failures-1, 5)))
}

// started marks the session live and clears the backoff.
func (c *ShapeClient) started(run *shapeRun) {
	c.mu.Lock()
	c.connecting, c.failures, c.nextTry, c.cur = false, 0, time.Time{}, run
	c.mu.Unlock()
	c.sessions.Add(1)
}

func (c *ShapeClient) ended(run *shapeRun) {
	run.end()
	c.mu.Lock()
	if c.cur == run {
		c.cur = nil
	}
	c.mu.Unlock()
}

// session opens a session with first and carries it until it ends.
func (c *ShapeClient) session(first []byte) {
	token, keys, err := c.proofs.Token(time.Now().Add(c.cfg.Offset))
	if err != nil {
		c.failed()
		return
	}
	send, err1 := stealth.NewShapeSender(keys.Up, stealth.NewPadder(c.cfg.Wide, c.cfg.Normal))
	recv, err2 := stealth.NewShapeReceiver(keys.Down)
	if err1 != nil || err2 != nil {
		c.failed()
		return
	}
	list, err := send.AppendChunk(nil, 0, 0, true, stealth.ShapeDataMax, first)
	if err != nil {
		c.failed()
		return
	}
	parts, ok := stealth.BuildParts(c.cfg.Shape, c.path, token, list)
	if !ok {
		c.failed()
		return
	}
	if c.cfg.Shape.Streamed() {
		c.runStream(keys, send, recv, parts)
		return
	}
	c.runPoll(keys, send, recv, parts)
}

// runPoll carries a poll-shape session: one request in flight, the answer
// carrying what waits for the device.
func (c *ShapeClient) runPoll(keys stealth.StreamKeys, send *stealth.ShapeSender, recv *stealth.ShapeReceiver, opening stealth.ShapeParts) {
	var conn *pollConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	answer, err := c.exchange(&conn, c.request(opening, false), time.Now().Add(dialFor), 0)
	if err != nil {
		c.failed()
		return
	}
	chunks, err := recv.OpenList(0, answer)
	if err != nil {
		c.failed()
		return
	}
	run := &shapeRun{out: make(chan []byte, streamQueue), done: make(chan struct{})}
	c.started(run)
	c.requests.Add(1)
	defer c.ended(run)
	for _, chunk := range chunks {
		for _, d := range chunk {
			c.deliver(d)
		}
	}

	var (
		number  uint32
		wait    = pollFirst
		due     = time.Now().Add(spread(wait))
		pending [][]byte
	)
	for {
		if len(pending) == 0 {
			t := time.NewTimer(time.Until(due))
			select {
			case <-run.done:
				t.Stop()
				return
			case d := <-run.out:
				pending = append(pending, d)
			case <-t.C:
			}
			t.Stop()
		}
		size := 0
	drain:
		for size < stealth.ShapeDataMax {
			select {
			case d := <-run.out:
				pending, size = append(pending, d), size+2+len(d)
			default:
				break drain
			}
		}
		if number == stealth.ShapeRequestMax {
			return
		}
		number++
		body, sent := c.list(send, number, pending)
		if body == nil {
			return
		}
		parts, ok := stealth.BuildParts(c.cfg.Shape, c.path, stealth.ShapeQuery(keys.ID, number), body)
		if !ok {
			return
		}
		answer, err := c.exchange(&conn, c.request(parts, false), time.Now().Add(dialFor), pollRepeats)
		if err != nil {
			return
		}
		chunks, err := recv.OpenList(number, answer)
		if err != nil {
			return
		}
		c.requests.Add(1)
		got := 0
		for _, chunk := range chunks {
			for _, d := range chunk {
				c.deliver(d)
				got++
			}
		}
		pending = pending[sent:]
		wait = slower(wait, got == 0 && sent == 0)
		due = time.Now().Add(spread(wait))
	}
}

// runStream carries a stream-shape session: the opening is the stream
// request, and its events carry the downstream; upstream rides later
// requests on a second connection.
func (c *ShapeClient) runStream(keys stealth.StreamKeys, send *stealth.ShapeSender, recv *stealth.ShapeReceiver, opening stealth.ShapeParts) {
	conn, err := c.dial(time.Now().Add(dialFor))
	if err != nil {
		c.failed()
		return
	}
	conn.SetDeadline(time.Time{})
	if _, err := conn.Write(c.request(opening, true)); err != nil {
		conn.Close()
		c.failed()
		return
	}
	if err := readStreamHead(conn.br); err != nil {
		conn.Close()
		c.failed()
		return
	}
	run := &shapeRun{out: make(chan []byte, streamQueue), done: make(chan struct{})}
	c.started(run)
	c.requests.Add(1)
	defer c.ended(run)
	defer conn.Close()

	// Upstream: later requests on a connection of their own.
	var upMu sync.Mutex
	go c.streamUpstream(keys, send, run, &upMu)

	reader := stealth.NewSseReader()
	buf := make([]byte, 16<<10)
	events := 0
	for {
		select {
		case <-run.done:
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(sseReadIdle))
		n, rerr := conn.br.Read(buf)
		if n > 0 {
			var out [][]byte
			done, ferr := reader.Feed(buf[:n], &out)
			if ferr != nil {
				c.streamDone(run)
				return
			}
			for _, sealed := range out {
				number, _ := stealth.ShapeNumber(0, events)
				events++
				if err := recv.OpenEvent(number, sealed, c.deliver); err != nil {
					c.streamDone(run)
					return
				}
			}
			if done {
				c.streamDone(run)
				return
			}
		}
		if rerr != nil {
			var ne net.Error
			if errors.As(rerr, &ne) && ne.Timeout() {
				continue
			}
			c.streamDone(run)
			return
		}
	}
}

// streamUpstream sends the device's datagrams as later requests while the
// stream is open; their answers are padding and are let go.
func (c *ShapeClient) streamUpstream(keys stealth.StreamKeys, send *stealth.ShapeSender, run *shapeRun, mu *sync.Mutex) {
	var conn *pollConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	var number uint32
	for {
		var pending [][]byte
		select {
		case <-run.done:
			return
		case d := <-run.out:
			pending = append(pending, d)
		}
		size := 2 + len(pending[0])
	drain:
		for size < stealth.ShapeDataMax {
			select {
			case d := <-run.out:
				pending, size = append(pending, d), size+2+len(d)
			default:
				break drain
			}
		}
		if number == stealth.ShapeRequestMax {
			run.end()
			return
		}
		number++
		mu.Lock()
		body, _ := c.list(send, number, pending)
		mu.Unlock()
		if body == nil {
			run.end()
			return
		}
		parts, ok := stealth.BuildParts(c.cfg.Shape, c.path, stealth.ShapeQuery(keys.ID, number), body)
		if !ok {
			run.end()
			return
		}
		if _, err := c.exchange(&conn, c.request(parts, false), time.Now().Add(dialFor), pollRepeats); err != nil {
			run.end()
			return
		}
		c.requests.Add(1)
	}
}

// streamDone ends a stream. A later pass opens a fresh session; falling back
// to polling within the same session is left to the app, which the gateway
// treats alike.
func (c *ShapeClient) streamDone(run *shapeRun) { run.end() }

// list seals a request's body: pending's datagrams in chunks at request n,
// as many as fit, and returns how many were taken. A body is never empty.
func (c *ShapeClient) list(send *stealth.ShapeSender, n uint32, pending [][]byte) (body []byte, sent int) {
	index := 0
	for sent < len(pending) && index < stealth.ShapeChunks {
		take := max(stealth.StreamFits(pending[sent:]), 1)
		next, err := send.AppendChunk(body, n, index, false, stealth.ShapeDataMax, pending[sent:sent+take]...)
		if errors.Is(err, stealth.ErrBodyFull) {
			break
		}
		if err != nil {
			return nil, 0
		}
		body, sent, index = next, sent+take, index+1
	}
	if len(body) == 0 {
		next, err := send.AppendChunk(nil, n, 0, false, stealth.ShapeDataMax)
		if err != nil {
			return nil, 0
		}
		body = next
	}
	return body, sent
}

// readStreamHead reads the response head of a stream answer and checks it is
// a 200 of an event stream.
func readStreamHead(br *bufio.Reader) error {
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, " 200 ") {
		return errAnswer
	}
	eventStream := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-type:") && strings.Contains(strings.ToLower(line), "text/event-stream") {
			eventStream = true
		}
	}
	if !eventStream {
		return errAnswer
	}
	return nil
}

// exchange writes one request and reads its answer's body, retrying a
// connection that ends before the answer is whole, as relay's does.
func (c *ShapeClient) exchange(conn **pollConn, req []byte, deadline time.Time, repeats int) ([]byte, error) {
	if *conn != nil && (*conn).stale() {
		(*conn).Close()
		*conn = nil
	}
	for attempt := 0; ; attempt++ {
		if *conn == nil {
			nc, err := c.dial(deadline)
			if err != nil {
				return nil, err
			}
			*conn = nc
		}
		p := *conn
		p.SetDeadline(deadline)
		body, closing, err := roundTrip(p, req)
		if err == nil {
			p.SetDeadline(time.Time{})
			if closing {
				p.Close()
				*conn = nil
			}
			return body, nil
		}
		p.Close()
		*conn = nil
		if errors.Is(err, errAnswer) || attempt >= repeats || time.Now().After(deadline) {
			return nil, err
		}
	}
}

// dial connects to the front, with TLS for https://.
func (c *ShapeClient) dial(deadline time.Time) (*pollConn, error) {
	host := c.url.Host
	if c.url.Port() == "" {
		port := "443"
		if c.url.Scheme == "http" {
			port = "80"
		}
		host = net.JoinHostPort(c.url.Hostname(), port)
	}
	addr := host
	if c.cfg.Dial != "" {
		addr = c.cfg.Dial
	}
	ctx, cancel := context.WithDeadline(c.ctx, deadline)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if c.url.Scheme == "https" {
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
	return &pollConn{Conn: conn, br: bufio.NewReaderSize(conn, 32<<10)}, nil
}

// request is one request of the shape: the method and target of parts, the
// fields of a browser's fetch from a page of the same site, the header field
// the shape names, and the body.
func (c *ShapeClient) request(parts stealth.ShapeParts, accept bool) []byte {
	host := c.url.Host
	if (c.url.Scheme == "https" && c.url.Port() == "443") || (c.url.Scheme == "http" && c.url.Port() == "80") {
		host = c.url.Hostname()
	}
	origin := c.url.Scheme + "://" + host
	var b strings.Builder
	b.WriteString(c.cfg.Shape.Method + " " + parts.Target + " HTTP/1.1\r\n")
	b.WriteString("Host: " + host + "\r\n")
	b.WriteString("Connection: keep-alive\r\n")
	if c.cfg.Shape.Bodied() {
		b.WriteString("Content-Length: " + strconv.Itoa(len(parts.Body)) + "\r\n")
		b.WriteString("Content-Type: application/octet-stream\r\n")
	}
	if parts.Header != "" {
		b.WriteString(c.cfg.Shape.Name + ": " + parts.Header + "\r\n")
	}
	b.WriteString("User-Agent: " + browserAgent + "\r\n")
	if accept {
		b.WriteString("Accept: text/event-stream\r\n")
	} else {
		b.WriteString("Accept: */*\r\n")
	}
	b.WriteString("Origin: " + origin + "\r\n")
	b.WriteString("Sec-Fetch-Site: same-origin\r\nSec-Fetch-Mode: cors\r\nSec-Fetch-Dest: empty\r\n")
	b.WriteString("Referer: " + origin + "/\r\n")
	b.WriteString("Accept-Language: en-US,en;q=0.9\r\n\r\n")
	return append([]byte(b.String()), parts.Body...)
}

// sseReadIdle bounds one read of a stream, so the client notices its end.
const sseReadIdle = 2 * time.Second
