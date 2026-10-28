package stealthgw

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-tls gateway.
//
// wui-tls is wui-ws without a front: the gateway is the server, on a port of
// its own, and ends TLS itself with a certificate devices know by its pin.
// With nothing in front of it, it must tell a device from everyone else by
// the bytes alone, and must give everyone else exactly what the operator's
// own site would have given them. So:
//
//  1. TLS is ended, within a deadline.
//  2. A connection to the decoy -- the operator's own web server -- is opened
//     at once, before a byte is read, so the decoy's own timers run from the
//     moment they would for a visitor that reached it directly.
//  3. The request line is read as it arrives (package stealth, request.go).
//     The first byte that cannot belong to a device's line, a line too long
//     or too slow, the peer closing, or the decoy saying anything or closing
//     first, decides that the peer is not a device.
//  4. A complete line is a device's only when its path is the transport's,
//     its query is a proof that holds, and TLS 1.3 was spoken, which every
//     client insists on. The token is opened whatever the path, so the work
//     a line costs does not say whether its path was right.
//  5. A device's head is read, the decoy's connection closed unused, and the
//     session runs as wui-ws's does.
//  6. Anyone else's bytes, every one read so far, go to the decoy unchanged,
//     and from then on the gateway relays both ways and reads nothing:
//     half-closes and resets pass through as they come. The gateway never
//     writes a byte of its own to such a peer -- no status, no error page.
//
// What it cannot hide: the TLS handshake is the gateway's own, not the
// decoy's, and a relayed answer is a hop later than the decoy's would be.

// TLSConfig is one tunnel's wui-tls gateway.
type TLSConfig struct {
	// Listen is the TCP address devices reach the gateway at.
	Listen string
	// Path is the transport's path.
	Path string
	// Origin is the decoy, the operator's own web server, http:// and its
	// address: whoever is not a device is handed to it byte for byte.
	Origin string
	// ProxyProtocol starts every connection to the origin with a PROXY
	// protocol header, version 2, naming the visitor, for an origin that
	// is set to expect one.
	ProxyProtocol bool
	// Host is the name the gateway's own request to the origin gives, to
	// learn how it answers; the origin's own address when empty.
	Host string
	// Certificate is what TLS is ended with, until SetCertificate gives
	// another.
	Certificate *tls.Certificate
	// CertFile and KeyFile are an issued certificate's files instead,
	// read again whenever they change.
	CertFile, KeyFile string

	WireGuard     string
	PrivateKey    [32]byte
	TransportKeys []stealth.TransportKey
	ReplayFile    string
	Sessions      int
	Wide, Normal  int
	Log           *slog.Logger
}

// wui-tls limits and timers.
const (
	// handshakeFor bounds TLS, from the accept.
	handshakeFor = 10 * time.Second
	// lineFor bounds the request line.
	lineFor = 10 * time.Second
	// originDialFor bounds a connection to the decoy.
	originDialFor = 5 * time.Second
	// undecidedMax is how many connections may be undecided at once, and
	// undecidedPerSource how many of them one address may hold. Over
	// either, the oldest is decided as a stranger's at once: a stranger
	// only ever displaces a stranger.
	undecidedMax       = 512
	undecidedPerSource = 64
	// relayedMax is how many strangers may be relayed at once. Over it, the
	// one idle longest goes, if it has been idle relayIdle, else the oldest.
	relayedMax = 1024
	relayIdle  = 60 * time.Second
	// connSpare is room for connections beyond the pools: ones closing.
	connSpare = 128
	// relayBuf is what each direction of a relay reads into.
	relayBuf = 16 << 10
	// learnFor bounds the gateway's own request to the decoy, learnAgain
	// is how soon it is asked again after it failed, and learnEvery how
	// soon after it answered.
	learnFor   = 10 * time.Second
	learnAgain = time.Minute
	learnEvery = time.Hour
	// sayAgain is how often a decoy that stays down is said to be.
	sayAgain = time.Minute
	// filesEvery is how often an issued certificate's files are looked at.
	filesEvery = time.Minute
)

// learnedFields are the header fields the answer to a proof takes from the
// decoy, so it reads as the decoy's own; the date is written fresh.
var learnedFields = []string{"Server"}

// The suites TLS 1.2 may end on: a key agreement and an AEAD, nothing else.
// TLS 1.3's are all of that.
var tls12Suites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// TLSGateway is wui-tls in front of one WireGuard tunnel.
type TLSGateway struct {
	*StreamGateway
	tcfg    TLSConfig
	origin  string // host:port
	tlsConf *tls.Config
	cert    atomic.Pointer[tls.Certificate]
	files   [2]time.Time // the files' times when last read
	learned atomic.Pointer[[]string]
	room    chan struct{}
	done    chan struct{} // closed when the gateway stops

	pmu       sync.Mutex
	nextID    uint64
	undecided map[uint64]*undecided
	perSource map[netip.Addr]int
	relayed   map[uint64]*relaying
	downAt    time.Time // when a decoy that is down was last said to be
	closed    bool

	tstats struct {
		accepted, tlsFailed, displaced, strangers, relayed, evicted, decoyDown,
		headRefused, learned, learnFailed atomic.Uint64
		why [whyCount]atomic.Uint64
	}
}

// What decided a connection was not a device's.
const (
	whyNone = iota
	whyDisplaced
	whyOrigin
	whySyntax
	whySlow
	whyClosed
	whyProof
	whyCount
)

// whyDecided marks a connection whose line was decided by its own bytes, so
// nothing from outside changes it after.
const whyDecided = whyCount

// NewTLS prepares a gateway. Nothing listens until Serve.
func NewTLS(cfg TLSConfig) (*TLSGateway, error) {
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Scheme != "http" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("stealthgw: the decoy %q is not an http:// address", cfg.Origin)
	}
	origin := u.Host
	if u.Port() == "" {
		origin = net.JoinHostPort(u.Hostname(), "80")
	}
	sg, err := newStreamGateway(StreamConfig{
		Listen: cfg.Listen, Path: cfg.Path, WireGuard: cfg.WireGuard, PrivateKey: cfg.PrivateKey,
		TransportKeys: cfg.TransportKeys, ReplayFile: cfg.ReplayFile, Sessions: cfg.Sessions,
		Wide: cfg.Wide, Normal: cfg.Normal, Log: cfg.Log,
	}, stealth.TLS)
	if err != nil {
		return nil, err
	}
	if cfg.Host == "" {
		cfg.Host = u.Host
	}
	g := &TLSGateway{
		StreamGateway: sg, tcfg: cfg, origin: origin,
		room:      make(chan struct{}, undecidedMax+relayedMax+sg.cfg.Sessions+connSpare),
		done:      make(chan struct{}),
		undecided: map[uint64]*undecided{}, perSource: map[netip.Addr]int{}, relayed: map[uint64]*relaying{},
	}
	switch {
	case cfg.CertFile != "" || cfg.KeyFile != "":
		if err := g.readFiles(); err != nil {
			return nil, err
		}
	case cfg.Certificate != nil:
		g.cert.Store(cfg.Certificate)
	default:
		return nil, errors.New("stealthgw: wui-tls needs a certificate")
	}
	g.tlsConf = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		CipherSuites: tls12Suites,
		NextProtos:   []string{"http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return g.cert.Load(), nil
		},
	}
	none := []string{}
	g.learned.Store(&none)
	return g, nil
}

// SetCertificate has the next connection's TLS ended with c. The connections
// there are keep the one they began with.
func (g *TLSGateway) SetCertificate(c *tls.Certificate) {
	if c != nil {
		g.cert.Store(c)
	}
}

// readFiles reads an issued certificate's files when they changed since they
// were last read. A pair that does not read leaves the one there is.
func (g *TLSGateway) readFiles() error {
	var at [2]time.Time
	for i, f := range []string{g.tcfg.CertFile, g.tcfg.KeyFile} {
		st, err := os.Stat(f)
		if err != nil {
			return fmt.Errorf("stealthgw: the certificate's files: %w", err)
		}
		at[i] = st.ModTime()
	}
	if at == g.files && g.cert.Load() != nil {
		return nil
	}
	c, err := tls.LoadX509KeyPair(g.tcfg.CertFile, g.tcfg.KeyFile)
	if err != nil {
		return fmt.Errorf("stealthgw: the certificate's files: %w", err)
	}
	g.cert.Store(&c)
	g.files = at
	return nil
}

// Serve runs the gateway until ctx ends.
func (g *TLSGateway) Serve(ctx context.Context) error {
	if err := g.Listen(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g.running.Add(1)
	go func() {
		defer g.running.Done()
		g.keep(ctx)
	}()
	return g.serve(ctx, g.accept, g.stop)
}

// accept takes connections while there is room for them. When there is none
// it stops taking them and leaves them to the system's queue, as a busy
// server does: no connection is closed at its accept for a count.
func (g *TLSGateway) accept() error {
	for {
		select {
		case g.room <- struct{}{}:
		case <-g.done:
			return nil
		}
		c, err := g.ln.Accept()
		if err != nil {
			<-g.room
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		g.tstats.accepted.Add(1)
		go func() {
			defer func() { <-g.room }()
			g.handle(c)
		}()
	}
}

// stop closes the port, and every connection that is not a session's: the
// sessions are ended by serve.
func (g *TLSGateway) stop() {
	g.ln.Close()
	g.pmu.Lock()
	if !g.closed {
		close(g.done)
	}
	g.closed = true
	for _, u := range g.undecided {
		u.stop(whyDisplaced)
	}
	for _, r := range g.relayed {
		r.kill()
	}
	g.pmu.Unlock()
}

// keep learns how the decoy answers, and reads an issued certificate's files
// again when they change.
func (g *TLSGateway) keep(ctx context.Context) {
	learn := time.NewTimer(0)
	defer learn.Stop()
	files := time.NewTicker(filesEvery)
	defer files.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-learn.C:
			ok := g.learn(ctx)
			switch {
			case ok && failing:
				g.log.Info("the decoy answers the gateway's own request again")
			case !ok && !failing:
				g.log.Warn("the decoy did not answer the gateway's own request, so a session's answer carries none of its fields; asking again every minute")
			}
			failing = !ok
			if ok {
				learn.Reset(learnEvery)
			} else {
				learn.Reset(learnAgain)
			}
		case <-files.C:
			if g.tcfg.CertFile == "" {
				continue
			}
			if err := g.readFiles(); err != nil {
				g.log.Error("the certificate's files do not read; the gateway keeps the certificate it has", "error", err)
			}
		}
	}
}

// learn asks the decoy for its front page, as a visitor would, and keeps the
// header fields a session's answer takes from it.
func (g *TLSGateway) learn(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, learnFor)
	defer cancel()
	c, err := g.dialOrigin(ctx, netip.AddrPort{}, netip.AddrPort{})
	if err != nil {
		g.tstats.learnFailed.Add(1)
		return false
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: "+g.tcfg.Host+"\r\nConnection: close\r\n\r\n"); err != nil {
		g.tstats.learnFailed.Add(1)
		return false
	}
	head := make([]byte, 0, stealth.HeadMax)
	buf := make([]byte, 4096)
	end := -1
	for end < 0 {
		n, err := c.Read(buf)
		head = append(head, buf[:n]...)
		end = bytes.Index(head, []byte("\r\n\r\n"))
		if end < 0 && (err != nil || len(head) >= stealth.HeadMax) {
			g.tstats.learnFailed.Add(1)
			return false
		}
	}
	lines := strings.Split(string(head[:end]), "\r\n")
	if !strings.HasPrefix(lines[0], "HTTP/1.") {
		g.tstats.learnFailed.Add(1)
		return false
	}
	got := []string{}
	for _, want := range learnedFields {
		for _, l := range lines[1:] {
			name, value, ok := strings.Cut(l, ":")
			value = strings.TrimSpace(value)
			if ok && strings.EqualFold(name, want) && fieldName(name) && fieldValue(value) {
				got = append(got, name+": "+value) // spelled as the decoy spells it; its first value
				break
			}
		}
	}
	g.learned.Store(&got)
	g.tstats.learned.Add(1)
	return true
}

func fieldName(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return s != ""
}

func fieldValue(s string) bool {
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// dialOrigin connects to the decoy. With the PROXY protocol on, the
// connection opens with a header naming from and to; the zero value of
// either names the gateway itself, as on its own requests.
func (g *TLSGateway) dialOrigin(ctx context.Context, from, to netip.AddrPort) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", g.origin)
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	if !g.tcfg.ProxyProtocol {
		return c, nil
	}
	if !from.IsValid() || !to.IsValid() {
		from = addrPortOf(c.LocalAddr())
		to = addrPortOf(c.RemoteAddr())
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetWriteDeadline(dl)
	}
	if _, err := c.Write(proxyHeader(from, to)); err != nil {
		c.Close()
		return nil, err
	}
	c.SetWriteDeadline(time.Time{})
	return c, nil
}

func addrPortOf(a net.Addr) netip.AddrPort {
	if t, ok := a.(*net.TCPAddr); ok {
		ap := t.AddrPort()
		return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	}
	return netip.AddrPort{}
}

// proxyHeader is a PROXY protocol header, version 2, for a TCP connection
// from from to to. Both are written in one family: an IPv4 address beside
// an IPv6 one is written mapped.
func proxyHeader(from, to netip.AddrPort) []byte {
	h := []byte("\r\n\r\n\x00\r\nQUIT\n")
	h = append(h, 0x21) // version 2, PROXY
	f, t := from.Addr().Unmap(), to.Addr().Unmap()
	if f.Is4() && t.Is4() {
		h = append(h, 0x11) // TCP over IPv4
		h = binary.BigEndian.AppendUint16(h, 12)
		h = append(h, f.AsSlice()...)
		h = append(h, t.AsSlice()...)
	} else {
		h = append(h, 0x21) // TCP over IPv6
		h = binary.BigEndian.AppendUint16(h, 36)
		f16, t16 := f.As16(), t.As16()
		h = append(h, f16[:]...)
		h = append(h, t16[:]...)
	}
	h = binary.BigEndian.AppendUint16(h, from.Port())
	return binary.BigEndian.AppendUint16(h, to.Port())
}

// undecided is a connection that has proven nothing yet.
type undecided struct {
	id  uint64
	src netip.Addr
	raw net.Conn
	why atomic.Int32
}

// stop decides the connection from outside: it was displaced, or the decoy
// spoke first. A connection already decided by its own bytes is not changed.
func (u *undecided) stop(why int32) {
	if u.why.CompareAndSwap(whyNone, why) {
		u.raw.SetReadDeadline(time.Unix(1, 0))
	}
}

// enter puts a new connection among the undecided, deciding the oldest of
// its address, or the oldest of all, as a stranger's when there is no room.
func (g *TLSGateway) enter(raw net.Conn, src netip.Addr) *undecided {
	g.pmu.Lock()
	defer g.pmu.Unlock()
	g.nextID++
	u := &undecided{id: g.nextID, src: src, raw: raw}
	if g.closed {
		u.stop(whyDisplaced)
		return u
	}
	oldest := func(of func(*undecided) bool) *undecided {
		var o *undecided
		for _, v := range g.undecided {
			if of(v) && (o == nil || v.id < o.id) {
				o = v
			}
		}
		return o
	}
	if g.perSource[src] >= undecidedPerSource {
		g.displace(oldest(func(v *undecided) bool { return v.src == src }))
	}
	for len(g.undecided) >= undecidedMax {
		g.displace(oldest(func(*undecided) bool { return true }))
	}
	g.undecided[u.id] = u
	g.perSource[src]++
	return u
}

func (g *TLSGateway) displace(u *undecided) {
	if u == nil {
		return
	}
	g.forget(u)
	g.tstats.displaced.Add(1)
	u.stop(whyDisplaced)
}

// leave takes a connection out of the undecided; again is nothing.
func (g *TLSGateway) leave(u *undecided) {
	g.pmu.Lock()
	g.forget(u)
	g.pmu.Unlock()
}

func (g *TLSGateway) forget(u *undecided) {
	if g.undecided[u.id] != u {
		return
	}
	delete(g.undecided, u.id)
	if g.perSource[u.src]--; g.perSource[u.src] <= 0 {
		delete(g.perSource, u.src)
	}
}

// originDial is the connection to the decoy a peer's line is read beside:
// the connection once it is open, and the first thing the decoy did on it.
type originDial struct {
	cancel context.CancelFunc
	conn   chan dialed
	first  chan firstRead
	early  []byte
}

type dialed struct {
	c   net.Conn
	err error
}

type firstRead struct {
	n   int
	err error
}

// openOrigin starts the connection to the decoy for u, and watches it: a
// decoy that says anything, or closes, before the line is complete has
// decided the peer as it decides a visitor that reached it directly.
func (g *TLSGateway) openOrigin(u *undecided, from, to netip.AddrPort) *originDial {
	ctx, cancel := context.WithTimeout(context.Background(), originDialFor)
	o := &originDial{cancel: cancel, conn: make(chan dialed, 1), first: make(chan firstRead, 1), early: make([]byte, relayBuf)}
	go func() {
		c, err := g.dialOrigin(ctx, from, to)
		o.conn <- dialed{c, err}
		if err != nil {
			return
		}
		n, err := c.Read(o.early)
		o.first <- firstRead{n, err}
		u.stop(whyOrigin)
	}()
	return o
}

// abandon closes the decoy's connection unused, whenever it opens.
func (o *originDial) abandon() {
	o.cancel()
	go func() {
		if d := <-o.conn; d.c != nil {
			d.c.Close()
		}
	}()
}

// handle is one connection, from its accept on.
func (g *TLSGateway) handle(raw net.Conn) {
	peer, local := addrPortOf(raw.RemoteAddr()), addrPortOf(raw.LocalAddr())
	if tc, ok := raw.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	u := g.enter(raw, peer.Addr())
	defer g.leave(u)

	// 1. TLS. A handshake that fails, is too slow, or whose connection is
	// displaced meanwhile is closed as the system closes it.
	raw.SetDeadline(time.Now().Add(handshakeFor))
	tc := tls.Server(raw, g.tlsConf)
	if err := tc.Handshake(); err != nil {
		if u.why.Load() == whyDisplaced {
			g.tstats.why[whyDisplaced].Add(1)
		} else {
			g.tstats.tlsFailed.Add(1)
		}
		raw.Close()
		return
	}
	raw.SetDeadline(time.Time{})
	modern := tc.ConnectionState().Version == tls.VersionTLS13

	// 2. The decoy, now.
	o := g.openOrigin(u, peer, local)

	// 3. The line, as it arrives.
	buf := make([]byte, stealth.HeadMax)
	var (
		sc       stealth.LineScanner
		filled   int
		scan     = stealth.LineMore
		why      int32
		deadline = time.Now().Add(lineFor)
	)
	for scan == stealth.LineMore {
		if why = u.why.Load(); why != whyNone {
			break
		}
		raw.SetReadDeadline(deadline)
		if why = u.why.Load(); why != whyNone {
			break
		}
		n, err := tc.Read(buf[filled:stealth.LineMax])
		if n > 0 {
			scan = sc.Feed(buf[filled : filled+n])
			filled += n
		}
		if err != nil && scan == stealth.LineMore {
			var ne net.Error
			switch why = u.why.Load(); {
			case why != whyNone:
			case errors.As(err, &ne) && ne.Timeout():
				why = whySlow
			case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
				why = whyClosed
			default:
				// A connection that broke under the gateway has nobody
				// left to answer.
				o.abandon()
				raw.Close()
				return
			}
			break
		}
	}
	if scan == stealth.LineStranger {
		why = whySyntax
	}
	// From here what was read decides, and nothing from outside.
	u.why.CompareAndSwap(whyNone, whyDecided)
	if u.why.Load() == whyDisplaced {
		why = whyDisplaced
	}

	// 4. A complete line is weighed, whatever the decoy did meanwhile: a
	// device's line that arrived is never turned away for that.
	if scan == stealth.LineDone && why != whyDisplaced {
		line, _ := stealth.ParseLine(buf[:sc.Len()])
		if dev, keys, ok := g.check(line, modern, peer.Addr()); ok {
			g.leave(u)
			o.abandon()
			g.device(tc, raw, peer.Addr(), dev, keys, buf, filled, sc.Len())
			return
		}
		why = whyProof
	}
	g.tstats.why[why].Add(1)
	g.leave(u)
	g.stranger(tc, raw, o, buf[:filled])
}

// check weighs a complete line. The token is opened whatever the path and
// whatever TLS was spoken, so the work does not say which of them was wrong.
func (g *TLSGateway) check(line stealth.RequestLine, modern bool, peer netip.Addr) (*streamDevice, stealth.StreamKeys, bool) {
	path := line.PathIs(g.cfg.Path)
	dev, p, keys, ok := g.open(string(line.Query))
	if !ok || !path || !modern {
		return nil, stealth.StreamKeys{}, false
	}
	return g.admit(dev, &p, keys, peer, time.Now())
}

// device reads a device's head and runs its session. A head that is too
// long, too slow, or no WebSocket upgrade is closed with no answer: TLS's
// close, then the connection's.
func (g *TLSGateway) device(tc *tls.Conn, raw net.Conn, peer netip.Addr, dev *streamDevice, keys stealth.StreamKeys, buf []byte, filled, line int) {
	now := time.Now()
	raw.SetReadDeadline(now.Add(headerFor))
	end := -1
	from := line - 2
	for {
		if i := bytes.Index(buf[from:filled], []byte("\r\n\r\n")); i >= 0 {
			end = from + i + 4
			break
		}
		from = max(filled-3, line-2)
		if filled == len(buf) {
			break
		}
		n, err := tc.Read(buf[filled:])
		filled += n
		if err != nil && n == 0 {
			break
		}
	}
	var key string
	ok := end >= 0
	if ok {
		key, ok = stealth.WebSocketHeadKey(buf[line:end])
	}
	if !ok {
		g.release(dev)
		g.tstats.headRefused.Add(1)
		raw.SetDeadline(time.Now().Add(closeWait))
		tc.CloseWrite()
		raw.Close()
		return
	}
	raw.SetReadDeadline(time.Time{})
	rest := append([]byte(nil), buf[end:filled]...)
	r := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(rest), tc), stealth.MaxSealed+14)
	g.run(dev, peer, tc, r, g.upgrade(key, *g.learned.Load()), keys, now)
}

// relaying is one stranger's connection being relayed to the decoy.
type relaying struct {
	id   uint64
	last atomic.Int64 // when a byte last crossed, Unix nanoseconds

	mu     sync.Mutex
	conns  []net.Conn
	killed bool
}

// hold has the relay close c when it is killed.
func (r *relaying) hold(c net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.killed {
		c.Close()
		return
	}
	r.conns = append(r.conns, c)
}

func (r *relaying) kill() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.killed = true
	for _, c := range r.conns {
		c.Close()
	}
}

func (r *relaying) isKilled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.killed
}

// startRelay takes a place among the relayed, closing the one idle longest
// -- or, if none has been idle long, the oldest -- when there is no room.
func (g *TLSGateway) startRelay() *relaying {
	g.pmu.Lock()
	defer g.pmu.Unlock()
	g.nextID++
	r := &relaying{id: g.nextID}
	r.last.Store(time.Now().UnixNano())
	if g.closed {
		r.killed = true
		return r
	}
	if len(g.relayed) >= relayedMax {
		var idlest, oldest *relaying
		for _, v := range g.relayed {
			if idlest == nil || v.last.Load() < idlest.last.Load() {
				idlest = v
			}
			if oldest == nil || v.id < oldest.id {
				oldest = v
			}
		}
		out := oldest
		if time.Since(time.Unix(0, idlest.last.Load())) >= relayIdle {
			out = idlest
		}
		delete(g.relayed, out.id)
		out.kill()
		g.tstats.evicted.Add(1)
	}
	g.relayed[r.id] = r
	return r
}

func (g *TLSGateway) endRelay(r *relaying) {
	g.pmu.Lock()
	delete(g.relayed, r.id)
	g.pmu.Unlock()
}

// decoyDown says, at most once a minute, that the decoy cannot be reached:
// while it lasts a stranger is closed with no answer, which shows the
// gateway. Devices are not touched by it.
func (g *TLSGateway) decoyDown() {
	g.tstats.decoyDown.Add(1)
	g.pmu.Lock()
	say := time.Since(g.downAt) >= sayAgain
	if say {
		g.downAt = time.Now()
	}
	g.pmu.Unlock()
	if say {
		g.log.Warn("the decoy cannot be reached, so whoever is not a device is closed with no answer, which shows the gateway; devices are not affected",
			"origin", g.origin, "since start", g.tstats.decoyDown.Load())
	}
}

func (g *TLSGateway) decoyUp() {
	g.pmu.Lock()
	was := !g.downAt.IsZero()
	g.downAt = time.Time{}
	g.pmu.Unlock()
	if was {
		g.log.Info("the decoy is reached again", "origin", g.origin)
	}
}

// stranger hands a peer that is not a device to the decoy: every byte it
// sent, as it sent them, and then both ways until either side ends.
func (g *TLSGateway) stranger(tc *tls.Conn, raw net.Conn, o *originDial, sent []byte) {
	g.tstats.strangers.Add(1)
	r := g.startRelay()
	defer g.endRelay(r)
	defer raw.Close()
	r.hold(raw)
	raw.SetDeadline(time.Time{})

	var d dialed
	select {
	case d = <-o.conn:
	case <-time.After(originDialFor + time.Second):
		d.err = context.DeadlineExceeded
	}
	o.cancel()
	if d.err != nil {
		// Nothing was written to the peer, so its close waits on nothing
		// but its own few bytes.
		g.decoyDown()
		raw.SetDeadline(time.Now().Add(closeWait))
		tc.CloseWrite()
		return
	}
	origin := d.c
	defer origin.Close()
	r.hold(origin)
	if r.isKilled() {
		return
	}
	g.decoyUp()
	g.tstats.relayed.Add(1)

	// What the decoy did before the peer's line was complete, if it did
	// anything yet: it may have spoken, closed or reset.
	var first firstRead
	known := false
	select {
	case first = <-o.first:
		known = true
	default:
	}
	touch := func() { r.last.Store(time.Now().UnixNano()) }

	// Up: the peer to the decoy. A decoy that has closed already is sent
	// nothing. It ends saying whether the decoy's answer is still to be
	// waited for: it is, unless the peer's connection broke.
	upDone := make(chan bool, 1)
	go func() {
		if known && (first.n == 0 || first.err != nil) {
			upDone <- true
			return
		}
		if _, err := origin.Write(sent); err != nil {
			upDone <- true
			return
		}
		buf := make([]byte, relayBuf)
		for {
			n, err := tc.Read(buf)
			if n > 0 {
				touch()
				if _, werr := origin.Write(buf[:n]); werr != nil {
					upDone <- true
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					// The peer closed its side, with TLS's close or
					// without it. The decoy is told, and may still answer.
					if c, ok := origin.(*net.TCPConn); ok {
						c.CloseWrite()
					}
					upDone <- true
					return
				}
				upDone <- false
				return
			}
		}
	}()

	// Down: the decoy to the peer. It ends saying whether the decoy reset
	// its connection.
	downDone := make(chan bool, 1)
	go func() {
		if !known {
			first = <-o.first
		}
		if first.n > 0 {
			if _, err := tc.Write(o.early[:first.n]); err != nil {
				downDone <- false
				return
			}
			touch()
		}
		if first.err != nil || first.n == 0 {
			downDone <- isReset(first.err)
			return
		}
		buf := make([]byte, relayBuf)
		for {
			n, err := origin.Read(buf)
			if n > 0 {
				if _, werr := tc.Write(buf[:n]); werr != nil {
					downDone <- false
					return
				}
				touch()
			}
			if err != nil {
				downDone <- isReset(err)
				return
			}
		}
	}()

	var reset bool
	upEnded := false
	select {
	case reset = <-downDone:
	case wait := <-upDone:
		if !wait {
			return
		}
		upEnded = true
		reset = <-downDone
	}
	if r.isKilled() {
		return
	}
	if reset {
		// The decoy reset, so the peer is reset.
		if c, ok := raw.(*net.TCPConn); ok {
			c.SetLinger(0)
		}
		return
	}
	raw.SetWriteDeadline(time.Now().Add(closeWait))
	tc.CloseWrite()
	// The decoy has closed, but the peer may still be sending. A web server
	// lingers over such a close, reading for a while, so its peer is not
	// reset before it has read the answer: so does the gateway, passing
	// what comes on to the decoy as before.
	if !upEnded {
		select {
		case <-upDone:
		case <-time.After(closeWait):
		}
	}
}

func isReset(err error) bool {
	return err != nil && errors.Is(err, connReset)
}

// TLSStats is what a wui-tls gateway has done since it started, and holds
// now: its sessions' counters, and its own.
type TLSStats struct {
	StreamStats
	Accepted, TLSFailed, Displaced uint64
	Strangers, Relayed, Evicted    uint64
	DecoyDown, HeadRefused         uint64
	Learned, LearnFailed           uint64
	Syntax, Slow, Closed, Origin   uint64
	Proof                          uint64
	Undecided, Relaying            int
}

// Stats reads the counters.
func (g *TLSGateway) Stats() TLSStats {
	st := TLSStats{StreamStats: g.StreamGateway.Stats()}
	st.Decoyed = g.tstats.strangers.Load()
	st.Accepted, st.TLSFailed = g.tstats.accepted.Load(), g.tstats.tlsFailed.Load()
	st.Displaced = g.tstats.displaced.Load()
	st.Strangers, st.Relayed, st.Evicted = g.tstats.strangers.Load(), g.tstats.relayed.Load(), g.tstats.evicted.Load()
	st.DecoyDown, st.HeadRefused = g.tstats.decoyDown.Load(), g.tstats.headRefused.Load()
	st.Learned, st.LearnFailed = g.tstats.learned.Load(), g.tstats.learnFailed.Load()
	w := func(i int) uint64 { return g.tstats.why[i].Load() }
	st.Syntax, st.Slow, st.Closed, st.Origin, st.Proof = w(whySyntax), w(whySlow), w(whyClosed), w(whyOrigin), w(whyProof)
	g.pmu.Lock()
	st.Undecided, st.Relaying = len(g.undecided), len(g.relayed)
	g.pmu.Unlock()
	return st
}
