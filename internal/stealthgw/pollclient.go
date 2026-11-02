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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The relay reference client.
//
// The client's rules, as an app is to keep them:
//
//   - One request in flight. Something to send goes at once when no
//     request is in flight; when there is nothing, the client polls after a
//     wait that starts at pollFirst, grows by half after every answer that
//     carried nothing up to pollMost, starts again after one that carried
//     something, is spread by a quarter either way, and is never more than
//     a minute.
//   - The opening request carries the proof and is sent once: a front's
//     repeat of it is a spent proof. Anything but a 200 with a body that
//     opens ends the attempt.
//   - A later request whose connection ends before its answer is whole is
//     sent again, the same bytes on a new connection, up to pollRepeats
//     times: the gateway answers a repeat from the answer it kept. With no
//     answer within dialFor, or an answer that is not a 200 with a body
//     that opens, the session is over.
//   - A connection is kept from one request to the next, unless the answer
//     says Connection: close, or the front sent anything unasked between
//     two answers.
//   - There is no idle keepalive: the polls are the activity, and their
//     answers the sign of life.

// relay client timers.
const (
	pollFirst   = 100 * time.Millisecond
	pollMost    = 5 * time.Second
	pollLeast   = 60 * time.Second
	pollRepeats = 3
)

// PollClientConfig is one device's relay client for one tunnel.
type PollClientConfig struct {
	// URL is the transport: https://, the front's name and the transport's
	// path; http:// is for a test without a front.
	URL string
	// Dial is the address connected to, when it is not the URL's host.
	Dial string
	// TLS is the client's TLS; nil is the system's roots.
	TLS *tls.Config
	// Listen is the local address WireGuard sends to.
	Listen       string
	Secret       stealth.Secret
	Anchor       stealth.Anchor
	TransportKey string
	Offset       time.Duration
	Wide, Normal int
}

// PollClient is a running relay reference client.
type PollClient struct {
	cfg    PollClientConfig
	url    *url.URL
	proofs *stealth.ProofClient
	local  *net.UDPConn
	ctx    context.Context

	mu         sync.Mutex
	app        netip.AddrPort
	cur        *pollRun
	connecting bool
	failures   int
	nextTry    time.Time
	sessions   atomic.Uint64
	requests   atomic.Uint64
}

// pollRun is one session: what WireGuard hands it, and its end.
type pollRun struct {
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func (r *pollRun) end() { r.once.Do(func() { close(r.done) }) }

func (r *pollRun) queue(d []byte) {
	select {
	case r.out <- d:
	default:
	}
}

// NewPollClient opens the client's local socket.
func NewPollClient(cfg PollClientConfig) (*PollClient, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("stealthgw: %q is not an https:// address", cfg.URL)
	}
	proofs, err := stealth.NewProofClient(cfg.Secret, cfg.Anchor, stealth.Relay, cfg.TransportKey)
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
	return &PollClient{cfg: cfg, url: u, proofs: proofs, local: local}, nil
}

// LocalAddr is where WireGuard is to send.
func (c *PollClient) LocalAddr() net.Addr { return c.local.LocalAddr() }

// Sessions is how many sessions the client has opened.
func (c *PollClient) Sessions() uint64 { return c.sessions.Load() }

// Requests is how many requests were answered.
func (c *PollClient) Requests() uint64 { return c.requests.Load() }

// Serve runs the client until ctx ends.
func (c *PollClient) Serve(ctx context.Context) error {
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

func (c *PollClient) fromApp(from netip.AddrPort, d []byte) {
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
func (c *PollClient) deliver(d []byte) {
	c.mu.Lock()
	app := c.app
	c.mu.Unlock()
	if app.IsValid() {
		c.local.WriteToUDPAddrPort(d, app)
	}
}

// failed is an opening that did not open: the next waits a while.
func (c *PollClient) failed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connecting = false
	c.failures++
	c.nextTry = time.Now().Add(min(retryMax, retryMin<<min(c.failures-1, 5)))
}

// session opens a session with first and carries it until it ends.
func (c *PollClient) session(first []byte) {
	token, keys, err := c.proofs.Token(time.Now().Add(c.cfg.Offset))
	if err != nil {
		c.failed()
		return
	}
	send, err1 := stealth.NewStreamSender(keys.Up, stealth.NewPadder(c.cfg.Wide, c.cfg.Normal))
	recv, err2 := stealth.NewPollReceiver(keys.Down)
	if err1 != nil || err2 != nil {
		c.failed()
		return
	}
	body, err := send.AppendBody(nil, first)
	if err != nil {
		c.failed()
		return
	}
	var conn *pollConn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	// The opening request, sent once.
	answer, err := c.exchange(&conn, c.post(token, body), time.Now().Add(dialFor), 0)
	if err != nil {
		c.failed()
		return
	}
	pb, err := recv.Open(answer)
	if err != nil {
		c.failed()
		return
	}
	pb.Commit()
	run := &pollRun{out: make(chan []byte, streamQueue), done: make(chan struct{})}
	c.mu.Lock()
	c.connecting, c.failures, c.nextTry, c.cur = false, 0, time.Time{}, run
	c.mu.Unlock()
	c.sessions.Add(1)
	c.requests.Add(1)
	defer func() {
		run.end()
		c.mu.Lock()
		if c.cur == run {
			c.cur = nil
		}
		c.mu.Unlock()
	}()
	for _, d := range pb.Datagrams() {
		c.deliver(d)
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
		for size < stealth.PollBodyMax {
			select {
			case d := <-run.out:
				pending, size = append(pending, d), size+2+len(d)
			default:
				break drain
			}
		}
		// A new session is opened before a number would pass its limit.
		if number == stealth.PollRequestMax {
			return
		}
		number++
		body, sent := body[:0], 0
		for sent < len(pending) {
			n := max(stealth.StreamFits(pending[sent:]), 1)
			next, err := send.AppendBody(body, pending[sent:sent+n]...)
			if errors.Is(err, stealth.ErrBodyFull) {
				break
			}
			if err != nil {
				return
			}
			body, sent = next, sent+n
		}
		if len(body) == 0 {
			if body, err = send.AppendBody(body); err != nil {
				return
			}
		}
		answer, err := c.exchange(&conn, c.post(stealth.PollQuery(keys.ID, number), body), time.Now().Add(dialFor), pollRepeats)
		if err != nil {
			return
		}
		pb, err := recv.Open(answer)
		if err != nil {
			return
		}
		pb.Commit()
		c.requests.Add(1)
		got := pb.Datagrams()
		for _, d := range got {
			c.deliver(d)
		}
		pending = pending[sent:]
		wait = slower(wait, len(got) == 0 && sent == 0)
		due = time.Now().Add(spread(wait))
	}
}

// slower is the wait before the next poll: the first wait after an exchange
// that carried something, and half as long again, up to pollMost, after one
// that carried nothing.
func slower(wait time.Duration, empty bool) time.Duration {
	if !empty {
		return pollFirst
	}
	return min(wait*3/2, pollMost)
}

// spread is a wait drawn from three quarters of it to five quarters, and
// never more than pollLeast.
func spread(wait time.Duration) time.Duration {
	return min(wait*3/4+jitter(wait/2), pollLeast)
}

// pollConn is a kept connection to the front.
type pollConn struct {
	net.Conn
	br *bufio.Reader
}

// stale says the front sent something nobody asked for, or ended the
// connection, since the last answer: nothing stands between two answers,
// so such a connection is given up before a request is written on it.
func (p *pollConn) stale() bool {
	if p.br.Buffered() > 0 {
		return true
	}
	p.SetReadDeadline(time.Now())
	_, err := p.br.Peek(1)
	p.SetReadDeadline(time.Time{})
	var ne net.Error
	return !(errors.As(err, &ne) && ne.Timeout())
}

// errAnswer is an answer that is not a session's: refused, or not
// readable as one.
var errAnswer = errors.New("stealthgw: the answer is not the session's")

// exchange writes one request and reads its answer's body. A connection
// that ends before the answer is whole has the same bytes sent again on a
// new one, repeats times at most, all before deadline.
func (c *PollClient) exchange(conn **pollConn, req []byte, deadline time.Time, repeats int) ([]byte, error) {
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

// roundTrip is one request and its answer on p: the body of a 200, and
// whether the answer ends the connection. errAnswer is an answer that came
// whole and is no session's; any other error is a connection that ended.
func roundTrip(p *pollConn, req []byte) ([]byte, bool, error) {
	if _, err := p.Write(req); err != nil {
		return nil, false, err
	}
	resp, err := http.ReadResponse(p.br, &http.Request{Method: http.MethodPost})
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" ||
		resp.ContentLength < stealth.PollBodyMin || resp.ContentLength > stealth.PollBodyMax {
		return nil, false, errAnswer
	}
	body := make([]byte, resp.ContentLength)
	if _, err := io.ReadFull(resp.Body, body); err != nil {
		return nil, false, err
	}
	return body, resp.Close, nil
}

// dial connects to the front, with TLS for https://.
func (c *PollClient) dial(deadline time.Time) (*pollConn, error) {
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
	return &pollConn{Conn: conn, br: bufio.NewReaderSize(conn, 16<<10)}, nil
}

// post is one request: the fields of a browser's fetch from a page of the
// same site, in a browser's order, with the body. An app sends its own
// platform's browser's fields.
func (c *PollClient) post(query string, body []byte) []byte {
	host := c.url.Host
	if (c.url.Scheme == "https" && c.url.Port() == "443") || (c.url.Scheme == "http" && c.url.Port() == "80") {
		host = c.url.Hostname()
	}
	origin := c.url.Scheme + "://" + host
	head := "POST " + c.url.EscapedPath() + "?" + query + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Connection: keep-alive\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"User-Agent: " + browserAgent + "\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Accept: */*\r\n" +
		"Origin: " + origin + "\r\n" +
		"Sec-Fetch-Site: same-origin\r\n" +
		"Sec-Fetch-Mode: cors\r\n" +
		"Sec-Fetch-Dest: empty\r\n" +
		"Referer: " + origin + "/\r\n" +
		"Accept-Encoding: gzip, deflate, br, zstd\r\n" +
		"Accept-Language: en-US,en;q=0.9\r\n\r\n"
	return append([]byte(head), body...)
}
