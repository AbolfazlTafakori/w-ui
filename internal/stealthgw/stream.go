package stealthgw

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-ws gateway.
//
// wui-ws is WebSocket behind the operator's own web server or a CDN, which
// ends TLS and hands the gateway plain HTTP. Every request the gateway is
// given is either a device's proof or somebody else's, and somebody else's
// goes to the decoy, the operator's own site, as it came: the gateway makes
// no answer of its own to anyone who cannot prove a device's secret. A proof
// is a request for the transport's path whose query is a token (package
// stealth), with a WebSocket upgrade; a request is only taken for one when
// it came from a front the gateway trusts, since only a front says who the
// visitor really is.
//
// A proof that holds opens a session: the upgrade is answered, and from then
// on every binary message either way is one sealed chunk. The session has a
// socket of its own to the tunnel, so WireGuard sees it at an endpoint of its
// own and roams the device to it on the first packet that authenticates, as
// it would on plain UDP. Nothing is sent on a session until WireGuard has
// spoken on it.

// StreamConfig is one tunnel's wui-ws gateway.
type StreamConfig struct {
	// Listen is the TCP address the front reaches the gateway at.
	Listen string
	// Path is the transport's path, as the front passes it on.
	Path string
	// Origin is the decoy, an http:// or https:// URL that every request
	// that is not a proof goes to as it came. Empty, such a request is
	// answered 404, and the front dresses the answer as it dresses its
	// own.
	Origin string
	// Proxies are the fronts the gateway trusts to say who a visitor is;
	// none means the loopback alone. A request from anywhere else is never
	// a proof.
	Proxies []netip.Prefix
	// ClientHeader is the header a front names the visitor in;
	// X-Forwarded-For when empty.
	ClientHeader string
	// WireGuard is the tunnel's own listen address, as the gateway reaches
	// it: the loopback and the tunnel's port.
	WireGuard string
	// PrivateKey is the tunnel's WireGuard private key. The gateway needs it
	// to tell whose a handshake initiation is, and for nothing else.
	PrivateKey [32]byte
	// TransportKeys are the transport's keys, the current first, and the one
	// before while profiles still name it.
	TransportKeys []stealth.TransportKey
	// ReplayFile is where the replay limit is kept, one file per gateway.
	ReplayFile string
	// Sessions bounds the sessions held at once; zero is the default.
	Sessions int
	// Wide and Normal are the padding ceilings; zero is the largest.
	Wide, Normal int
	Log          *slog.Logger
}

// Stream limits and timers.
const (
	// MinPath is the shortest path a stream transport takes: the path is
	// what keeps the transport apart from the rest of a site.
	MinPath = 12
	// deviceStreams is how many sessions one device may hold at once.
	deviceStreams = 8
	// streamIdle ends a session nothing crossed for this long.
	streamIdle = 300 * time.Second
	// closeWait is how long a closed session waits for its client's close
	// before the connection is let go.
	closeWait = 5 * time.Second
	// writeFor bounds one write to a client.
	writeFor = 30 * time.Second
	// limitWait is how long a proof above the replay limit waits for the
	// raise it asked for: a stream that is refused costs its client a new
	// connection, where a UDP opening costs a datagram.
	limitWait = 2 * time.Second
	// streamQueue is how many datagrams wait to go to one client.
	streamQueue = 512
	// headerMax bounds a request's head.
	headerMax = 16 << 10
	// headerFor bounds the time a request's head may take.
	headerFor = 10 * time.Second
)

// StreamGateway is wui-ws in front of one WireGuard tunnel.
type StreamGateway struct {
	cfg     StreamConfig
	log     *slog.Logger
	proofs  *stealth.ProofServer
	resp    *responder
	replay  *replay
	wg      *net.UDPAddr
	decoy   http.Handler
	path    [32]byte // the hash of the path, compared in constant time
	cookie  string
	proxies []netip.Prefix
	header  string
	ln      net.Listener
	// perDevice is how many sessions one device may hold at once.
	perDevice int

	tags atomic.Pointer[map[stealth.Tag]*streamDevice]

	mu       sync.Mutex
	devices  map[[32]byte]*streamDevice
	streams  map[*stream]struct{}
	fakes    map[*fakeStream]struct{} // wui-faketls's sessions
	held     int                      // places for sessions, the running and the reserved
	sources  map[netip.Addr]*bucket
	behind   map[string]netip.Addr               // a session socket's address -> its visitor
	polls    map[[stealth.PollIDLen]byte]*stream // wui-poll's sessions by their id
	shapes   map[[stealth.PollIDLen]byte]*stream // wui-shape's sessions by their id
	askLimit uint64

	stats    counters
	decoyed  atomic.Uint64
	polled   atomic.Uint64
	repeated atomic.Uint64
	wake     chan struct{}
	running  sync.WaitGroup
}

// streamDevice is what the gateway knows of one device.
type streamDevice struct {
	pub     [32]byte
	secret  stealth.Secret
	keys    stealth.DeviceKeys
	ss      [32]byte
	usable  bool
	proofs  bucket
	spent   bucket
	pending int
	streams int
	indexes map[uint32]time.Time
	gone    bool
}

// NewStream prepares a gateway. Nothing listens until Serve.
func NewStream(cfg StreamConfig) (*StreamGateway, error) {
	decoy, err := newDecoy(cfg.Origin)
	if err != nil {
		return nil, err
	}
	g, err := newStreamGateway(cfg, stealth.WS)
	if err != nil {
		return nil, err
	}
	g.decoy = decoy
	return g, nil
}

// newStreamGateway is the part of a gateway every stream transport has: the
// proofs of transport, the devices, the sessions and their limits.
func newStreamGateway(cfg StreamConfig, transport string) (*StreamGateway, error) {
	// wui-faketls opens its sessions from a ClientHello's carrier, not from a
	// token on a path, so it needs neither a path nor a transport key and
	// stands up no proof server; everything else a stream gateway is -- the
	// devices, the sessions, their limits and the replay rule -- it keeps.
	token := transport != stealth.FakeTLS
	if token {
		if len(cfg.Path) < MinPath || cfg.Path[0] != '/' {
			return nil, fmt.Errorf("stealthgw: a path of at least %d characters, from /, is needed", MinPath)
		}
		if len(cfg.TransportKeys) == 0 {
			return nil, errors.New("stealthgw: the transport has no key")
		}
	}
	if cfg.Sessions <= 0 {
		cfg.Sessions = defaultSlots
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	resp, err := newResponder(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("stealthgw: tunnel private key: %w", err)
	}
	wg, err := net.ResolveUDPAddr("udp", cfg.WireGuard)
	if err != nil {
		return nil, fmt.Errorf("stealthgw: tunnel address: %w", err)
	}
	var proofs *stealth.ProofServer
	cookie := ""
	if token {
		var anchor stealth.Anchor
		copy(anchor[:], resp.priv.PublicKey().Bytes())
		proofs, err = stealth.NewProofServer(anchor, transport, cfg.TransportKeys...)
		if err != nil {
			return nil, err
		}
		cookie = cookieName(cfg.TransportKeys[0])
	}
	g := &StreamGateway{
		cfg: cfg, log: cfg.Log.With("transport", transport, "listen", cfg.Listen),
		proofs: proofs, resp: resp, wg: wg,
		path:    sha256.Sum256([]byte(cfg.Path)),
		cookie:  cookie,
		proxies: cfg.Proxies, header: cfg.ClientHeader,
		devices: map[[32]byte]*streamDevice{}, streams: map[*stream]struct{}{},
		fakes:   map[*fakeStream]struct{}{},
		sources: map[netip.Addr]*bucket{}, behind: map[string]netip.Addr{},
		polls: map[[stealth.PollIDLen]byte]*stream{}, shapes: map[[stealth.PollIDLen]byte]*stream{}, perDevice: deviceStreams,
		wake: make(chan struct{}, 1),
	}
	if len(g.proxies) == 0 {
		g.proxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	}
	if g.header == "" {
		g.header = "X-Forwarded-For"
	}
	empty := map[stealth.Tag]*streamDevice{}
	g.tags.Store(&empty)
	return g, nil
}

// newDecoy is what answers every request that is not a proof: the origin,
// as the request came, or a plain 404 without one.
func newDecoy(origin string) (http.Handler, error) {
	if origin == "" {
		return http.NotFoundHandler(), nil
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("stealthgw: the decoy %q is not an http:// or https:// address", origin)
	}
	return &httputil.ReverseProxy{
		// The request goes on as it came, to its own path and with its own
		// Host: the origin is the site the front serves.
		Director: func(out *http.Request) {
			out.URL.Scheme, out.URL.Host = u.Scheme, u.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.WriteHeader(http.StatusBadGateway)
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}, nil
}

// cookieName is the name of the cookie an upgrade sets, to vary the
// answer's length: a name of the transport key's own, so it stays one name
// as a site's cookie does.
func cookieName(k stealth.TransportKey) string {
	h := sha256.Sum256([]byte("w-ui stealth v1 cookie " + k.Public()))
	b := make([]byte, 0, 8)
	for _, c := range h[:8] {
		b = append(b, 'a'+c%26)
	}
	return string(b)
}

// SetDevices puts the devices that may use the gateway in force. A device
// that is no longer among them loses its sessions at once.
func (g *StreamGateway) SetDevices(list []Device) {
	var anchor stealth.Anchor
	copy(anchor[:], g.resp.priv.PublicKey().Bytes())
	now := time.Now()
	g.mu.Lock()
	keep := make(map[[32]byte]*streamDevice, len(list))
	for _, d := range list {
		if old, ok := g.devices[d.PublicKey]; ok && old.secret == d.Secret {
			keep[d.PublicKey] = old
			continue
		}
		dev := &streamDevice{pub: d.PublicKey, secret: d.Secret, keys: stealth.Derive(d.Secret, anchor), indexes: map[uint32]time.Time{}}
		dev.ss, dev.usable = g.resp.static(d.PublicKey)
		dev.proofs, dev.spent = newBucket(deviceProofs, now), newBucket(deviceSpent, now)
		keep[d.PublicKey] = dev
	}
	var ending []*stream
	for pub, dev := range g.devices {
		if keep[pub] != dev {
			dev.gone = true
		}
	}
	for s := range g.streams {
		if s.dev.gone {
			ending = append(ending, s)
		}
	}
	var endingFakes []*fakeStream
	for s := range g.fakes {
		if s.dev.gone {
			endingFakes = append(endingFakes, s)
		}
	}
	g.devices = keep
	tags := make(map[stealth.Tag]*streamDevice, len(keep))
	collisions := 0
	for _, d := range keep {
		if other, ok := tags[d.keys.Tag]; ok && other != d {
			delete(tags, d.keys.Tag)
			collisions++
			continue
		}
		tags[d.keys.Tag] = d
	}
	g.mu.Unlock()
	g.tags.Store(&tags)
	if collisions > 0 {
		g.log.Error("two devices share a tag, so neither can open a session; their transport secrets are copies and must be replaced", "tags", collisions)
	}
	for _, s := range ending {
		s.finish()
	}
	for _, s := range endingFakes {
		s.finish()
	}
}

// Listen opens the gateway's port and its replay limit. Serve calls it when
// it has not been called.
func (g *StreamGateway) Listen() error {
	if g.ln != nil {
		return nil
	}
	now := time.Now()
	r, err := openReplay(g.cfg.ReplayFile, uint64(now.Unix()))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		g.log.Info("no replay limit is kept yet; the gateway takes proofs from a minute from now")
	case err != nil:
		g.log.Error("the replay limit could not be read; the gateway takes proofs from a minute from now", "error", err)
	}
	g.replay = r
	ln, err := net.Listen("tcp", g.cfg.Listen)
	if err != nil {
		return fmt.Errorf("stealthgw: listen: %w", err)
	}
	g.ln = ln
	g.writeDue(uint64(now.Unix()))
	return nil
}

// Addr is the address the gateway listens at, once it listens.
func (g *StreamGateway) Addr() net.Addr {
	if g.ln == nil {
		return nil
	}
	return g.ln.Addr()
}

// Serve runs the gateway until ctx ends.
func (g *StreamGateway) Serve(ctx context.Context) error {
	if err := g.Listen(); err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           g,
		ReadHeaderTimeout: headerFor,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    headerMax,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	err := g.serve(ctx, func() error { return srv.Serve(g.ln) }, func() { srv.Close() })
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// serve runs the gateway's timers beside accept, which takes connections
// until stop is called when ctx ends, and ends every session after.
func (g *StreamGateway) serve(ctx context.Context, accept func() error, stop func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g.running.Add(1)
	go func() {
		defer g.running.Done()
		g.tick(ctx)
	}()
	go func() {
		<-ctx.Done()
		stop()
	}()
	err := accept()
	cancel()
	g.mu.Lock()
	all := make([]*stream, 0, len(g.streams))
	for s := range g.streams {
		all = append(all, s)
	}
	allFakes := make([]*fakeStream, 0, len(g.fakes))
	for s := range g.fakes {
		allFakes = append(allFakes, s)
	}
	g.mu.Unlock()
	for _, s := range all {
		s.finish()
	}
	for _, s := range allFakes {
		s.finish()
	}
	g.running.Wait()
	return err
}

func (g *StreamGateway) tick(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.wake:
		case <-t.C:
		}
		now := time.Now()
		g.writeDue(uint64(now.Unix()))
		g.replay.expire(uint64(now.Unix()))
		g.sweep(now)
	}
}

func (g *StreamGateway) writeDue(now uint64) {
	g.mu.Lock()
	asked := g.askLimit
	g.askLimit = 0
	g.mu.Unlock()
	limit := g.replay.due(now, asked)
	if limit == 0 {
		return
	}
	if err := g.replay.write(limit); err != nil {
		g.log.Error("the replay limit could not be written; proofs are refused once the clock reaches the old one", "error", err)
	}
}

// sweep ends the sessions whose time is over.
func (g *StreamGateway) sweep(now time.Time) {
	var ending []*stream
	g.mu.Lock()
	for s := range g.streams {
		switch {
		case s.isPending() && now.Sub(s.opened) >= pendingFor:
			ending = append(ending, s)
		case now.Sub(s.quietSince()) >= s.idleFor():
			ending = append(ending, s)
		}
	}
	var endingFakes []*fakeStream
	for s := range g.fakes {
		if s.isPending() && now.Sub(s.opened) >= pendingFor || now.Sub(s.quietSince()) >= streamIdle {
			endingFakes = append(endingFakes, s)
		}
	}
	for a, b := range g.sources {
		if b.full(sourceProofs, now) {
			delete(g.sources, a)
		}
	}
	g.mu.Unlock()
	for _, s := range ending {
		s.finish()
	}
	for _, s := range endingFakes {
		s.finish()
	}
}

func (g *StreamGateway) drop(why int) { g.stats.dropped[why].Add(1) }

// ServeHTTP takes one request: a proof opens a session, and anything else
// goes to the decoy.
func (g *StreamGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	peer := addrOf(r.RemoteAddr)
	key, upgrade := stealth.WebSocketKey(r)
	path := sha256.Sum256([]byte(r.URL.Path))
	if !g.trusted(peer) || subtle.ConstantTimeCompare(path[:], g.path[:]) != 1 || !upgrade {
		g.toDecoy(w, r)
		return
	}
	visitor := g.visitor(r, peer)
	dev, keys, ok := g.prove(r.URL.RawQuery, visitor, now)
	if !ok {
		g.toDecoy(w, r)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		g.release(dev)
		g.toDecoy(w, r)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		g.release(dev)
		return
	}
	g.run(dev, visitor, conn, brw.Reader, g.upgrade(key, nil), keys, now)
}

func (g *StreamGateway) toDecoy(w http.ResponseWriter, r *http.Request) {
	g.decoyed.Add(1)
	g.decoy.ServeHTTP(w, r)
}

func addrOf(remote string) netip.Addr {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

func (g *StreamGateway) trusted(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	for _, p := range g.proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// visitor is who a request really came from: the last address in the
// front's header that is not itself a trusted front, or the peer when the
// header names nobody.
func (g *StreamGateway) visitor(r *http.Request, peer netip.Addr) netip.Addr {
	var all []string
	for _, v := range r.Header.Values(g.header) {
		all = append(all, strings.Split(v, ",")...)
	}
	for i := len(all) - 1; i >= 0; i-- {
		s := strings.TrimSpace(all[i])
		a, err := netip.ParseAddr(strings.Trim(s, "[]"))
		if err != nil {
			ap, err := netip.ParseAddrPort(s)
			if err != nil {
				return peer // a value nobody could act on: the front is all we know
			}
			a = ap.Addr()
		}
		if a = a.Unmap(); !g.trusted(a) {
			return a
		}
	}
	return peer
}

// prove checks a token and, when it holds and the limits have room, keeps a
// place for its session. Every failure is a request like any other, so it
// says nothing.
func (g *StreamGateway) prove(token string, visitor netip.Addr, now time.Time) (*streamDevice, stealth.StreamKeys, bool) {
	dev, p, keys, ok := g.open(token)
	if !ok {
		return nil, stealth.StreamKeys{}, false
	}
	return g.admit(dev, &p, keys, visitor, now)
}

// open reads a token and proves it against the device its tag finds,
// without taking it: what the replay rule and the limits say comes after.
func (g *StreamGateway) open(token string) (*streamDevice, stealth.Proof, stealth.StreamKeys, bool) {
	p, err := g.proofs.Open(token)
	if err != nil {
		g.drop(dropUnopened)
		return nil, p, stealth.StreamKeys{}, false
	}
	dev := (*g.tags.Load())[p.Tag]
	var dk *stealth.DeviceKeys
	if dev != nil {
		dk = &dev.keys
	}
	keys, err := g.proofs.Check(&p, dk)
	if err != nil {
		g.drop(dropUnknown)
		return nil, p, stealth.StreamKeys{}, false
	}
	return dev, p, keys, true
}

// admit takes an opened proof: the replay rule, the limits, and a place for
// its session.
func (g *StreamGateway) admit(dev *streamDevice, p *stealth.Proof, keys stealth.StreamKeys, visitor netip.Addr, now time.Time) (*streamDevice, stealth.StreamKeys, bool) {
	// The replay rule. A proof above the limit asks for a raise and waits
	// for it a moment, since refusing it costs the client a connection.
	v, raise := g.replay.judge(p.Enc, p.Timestamp, uint64(now.Unix()))
	if v == aboveLimit {
		g.mu.Lock()
		g.askLimit = max(g.askLimit, raise)
		g.mu.Unlock()
		select {
		case g.wake <- struct{}{}:
		default:
		}
		for end := time.Now().Add(limitWait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if _, limit, _ := g.replay.state(); limit >= p.Timestamp {
				break
			}
		}
		v, _ = g.replay.judge(p.Enc, p.Timestamp, uint64(time.Now().Unix()))
	}
	if v != accepted {
		if v == aboveLimit {
			g.spend(dev, p, now)
		}
		g.drop(map[verdict]int{outsideWindow: dropWindow, atFloor: dropFloor, seenBefore: dropSeen, cacheFull: dropCacheFull, aboveLimit: dropLimit}[v])
		return nil, stealth.StreamKeys{}, false
	}
	// The limits, and a place for the session.
	if why := g.reserve(dev, visitor, now); why >= 0 {
		g.spend(dev, p, now)
		g.drop(why)
		return nil, stealth.StreamKeys{}, false
	}
	// The proof is taken now, once: two connections carrying the same one
	// may both have come this far.
	if v, _ := g.replay.claim(p.Enc, p.Timestamp, uint64(now.Unix())); v != accepted {
		g.release(dev)
		g.drop(dropSeen)
		return nil, stealth.StreamKeys{}, false
	}
	g.stats.opened.Add(1)
	return dev, keys, true
}

// reserve takes a pending place for a device's new session, or says which
// limit had no room.
func (g *StreamGateway) reserve(dev *streamDevice, visitor netip.Addr, now time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case dev.gone:
		return dropUnknown
	case g.held >= g.cfg.Sessions || dev.streams >= g.perDevice:
		return dropPool
	case dev.pending >= devicePending:
		return dropPending
	}
	src, ok := g.sources[visitor]
	if !ok {
		if len(g.sources) >= maxSources {
			for a, b := range g.sources {
				if b.full(sourceProofs, now) {
					delete(g.sources, a)
				}
			}
		}
		if len(g.sources) >= maxSources {
			return dropSourceRate
		}
		b := newBucket(sourceProofs, now)
		src = &b
		g.sources[visitor] = src
	}
	if !dev.proofs.take(deviceProofs, now) {
		return dropDeviceRate
	}
	if !src.take(sourceProofs, now) {
		return dropSourceRate
	}
	dev.pending++
	dev.streams++
	g.held++
	return -1
}

// release gives back a place reserve kept for a session that never ran.
func (g *StreamGateway) release(dev *streamDevice) {
	g.mu.Lock()
	defer g.mu.Unlock()
	dev.pending--
	dev.streams--
	g.held--
}

// spend records a proof that opened nothing, while its device's allowance
// for that has room: unrecorded, it would be good later for whoever saw it.
func (g *StreamGateway) spend(dev *streamDevice, p *stealth.Proof, now time.Time) {
	if dev == nil {
		return
	}
	g.mu.Lock()
	ok := dev.spent.take(deviceSpent, now)
	g.mu.Unlock()
	if ok {
		g.replay.record(p.Enc, p.Timestamp)
		g.stats.spent.Add(1)
	}
}

// Source is the address a device really reaches the gateway from, for the
// address the tunnel sees it at: a session's own socket on the loopback.
func (g *StreamGateway) Source(endpoint string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.behind[endpoint]
	if !ok {
		return "", false
	}
	return a.String(), true
}

// StreamStats is what the gateway has done since it started, and holds now.
type StreamStats struct {
	Sessions, Devices              int
	Opened, Spent, Established     uint64
	Sent, Ended, Decoyed           uint64
	Unknown, Unopened              uint64
	Window, Floor, Seen            uint64
	CacheFull, Limit               uint64
	Pool, Pending                  uint64
	DeviceRate, SourceRate         uint64
	Handshakes, Tunnel, Unsendable uint64
	// wui-poll's: requests taken and repeats answered from the answer
	// kept, and requests that named no session in order or whose body was
	// late or did not open.
	Polls, Repeats, Unordered, Bodies uint64
	ReplayFloor, ReplayLimit          uint64
	ReplaySalts                       int
}

// Stats reads the counters.
func (g *StreamGateway) Stats() StreamStats {
	g.mu.Lock()
	st := StreamStats{Sessions: len(g.streams), Devices: len(g.devices)}
	g.mu.Unlock()
	d := func(i int) uint64 { return g.stats.dropped[i].Load() }
	st.Opened, st.Spent, st.Established = g.stats.opened.Load(), g.stats.spent.Load(), g.stats.established.Load()
	st.Sent, st.Ended, st.Decoyed = g.stats.sent.Load(), g.stats.ended.Load(), g.decoyed.Load()
	st.Unknown, st.Unopened = d(dropUnknown), d(dropUnopened)
	st.Window, st.Floor, st.Seen, st.CacheFull, st.Limit = d(dropWindow), d(dropFloor), d(dropSeen), d(dropCacheFull), d(dropLimit)
	st.Pool, st.Pending = d(dropPool), d(dropPending)
	st.DeviceRate, st.SourceRate = d(dropDeviceRate), d(dropSourceRate)
	st.Handshakes, st.Tunnel, st.Unsendable = d(dropHandshakes), d(dropTunnel), d(dropUnsendable)
	st.Polls, st.Repeats, st.Unordered, st.Bodies = g.polled.Load(), g.repeated.Load(), d(dropUnordered), d(dropBody)
	if g.replay != nil {
		st.ReplayFloor, st.ReplayLimit, st.ReplaySalts = g.replay.state()
	}
	return st
}

// stream is one session: its socket to the tunnel, and on wui-ws, wui-tls
// and wui-plain one WebSocket connection, or on wui-poll the requests that
// name it.
type stream struct {
	g       *StreamGateway
	dev     *streamDevice
	visitor netip.Addr
	conn    net.Conn
	r       *bufio.Reader
	wg      *net.UDPConn
	recv    *stealth.StreamReceiver
	send    *stealth.StreamSender // the writer's alone, or the request in flight's
	poll    *pollSession          // wui-poll's, nil on the others
	shape   *shapeSession         // wui-shape's, nil on the others

	out chan []byte // datagrams for the client; nil is padding alone
	ctl chan []byte // pong payloads

	state      atomic.Int32
	opened     time.Time
	lastActive atomic.Int64
	handshakes bucket // the gateway's lock
	echoes     atomic.Int32
	sent       atomic.Uint64
	first      bool // the reader's alone: no chunk has come yet

	live     chan struct{} // closed when the session is established
	done     chan struct{}
	readDone chan struct{}
	once     sync.Once
}

func (s *stream) isPending() bool      { return s.state.Load() == statePending }
func (s *stream) isOver() bool         { return s.state.Load() == stateOver }
func (s *stream) isLive() bool         { return s.state.Load() == stateLive }
func (s *stream) active(now time.Time) { s.lastActive.Store(now.UnixNano()) }

// quietSince is the last moment the session was active: on wui-poll, the
// last request taken, since the tunnel speaking to a client that stopped
// asking keeps nothing alive.
func (s *stream) quietSince() time.Time {
	if s.poll != nil {
		return time.Unix(0, s.poll.last.Load())
	}
	if s.shape != nil {
		return time.Unix(0, s.shape.last.Load())
	}
	return time.Unix(0, s.lastActive.Load())
}

// idleFor is how long the session may be quiet.
func (s *stream) idleFor() time.Duration {
	if s.poll != nil {
		return pollIdle
	}
	if s.shape != nil {
		return shapeIdle
	}
	return streamIdle
}

// newSession is a session for a proof that was taken: its socket to the
// tunnel and its sender, registered with the gateway, and on wui-poll
// under its id. When the device went meanwhile, or the id is a live
// session's, there is none, and the place the proof kept is given back.
func (g *StreamGateway) newSession(dev *streamDevice, visitor netip.Addr, keys stealth.StreamKeys, now time.Time, poll *pollSession) (*stream, bool) {
	wg, err := net.DialUDP("udp", nil, g.wg)
	if err != nil {
		g.release(dev)
		g.drop(dropUnsendable)
		return nil, false
	}
	send, err := stealth.NewStreamSender(keys.Down, stealth.NewPadder(g.cfg.Wide, g.cfg.Normal))
	if err != nil {
		g.release(dev)
		wg.Close()
		return nil, false
	}
	s := &stream{
		g: g, dev: dev, visitor: visitor, wg: wg, send: send, poll: poll,
		out: make(chan []byte, streamQueue), ctl: make(chan []byte, 4),
		opened: now, handshakes: newBucket(sessionHandshakes, now), first: true,
		live: make(chan struct{}), done: make(chan struct{}), readDone: make(chan struct{}),
	}
	s.active(now)
	g.mu.Lock()
	ok := !dev.gone
	if ok && poll != nil {
		// An id is 16 bytes of a fresh proof's export, so two are never
		// one; should they be, the live session is left alone.
		_, taken := g.polls[poll.id]
		ok = !taken
	}
	if ok {
		g.streams[s] = struct{}{}
		g.behind[wg.LocalAddr().String()] = visitor
		if poll != nil {
			g.polls[poll.id] = s
		}
	}
	g.mu.Unlock()
	if !ok {
		g.release(dev)
		wg.Close()
		return nil, false
	}
	return s, true
}

// run answers the upgrade and carries the session until it ends.
func (g *StreamGateway) run(dev *streamDevice, visitor netip.Addr, conn net.Conn, r *bufio.Reader, answer string, keys stealth.StreamKeys, now time.Time) {
	recv, err := stealth.NewStreamReceiver(keys.Up)
	if err != nil {
		g.release(dev)
		conn.Close()
		return
	}
	s, ok := g.newSession(dev, visitor, keys, now, nil)
	if !ok {
		conn.Close()
		return
	}
	s.conn, s.r, s.recv = conn, r, recv
	conn.SetDeadline(time.Time{})
	conn.SetWriteDeadline(time.Now().Add(writeFor))
	if _, err := io.WriteString(conn, answer); err != nil {
		s.finish()
		close(s.readDone)
		conn.Close()
		return
	}
	go s.fromTunnel()
	go s.writer()
	s.reader()
}

// upgrade is the answer to a proof: the switch, the date, and a cookie whose
// value is of a length drawn afresh, so no two answers are one length.
//
// learned is nil behind a front, which writes its own fields around the
// answer. A gateway that is the server itself gives the header lines it
// learned from the decoy, possibly none: they lead, with the date after
// them, as a web server writes its own first.
func (g *StreamGateway) upgrade(key string, learned []string) string {
	var b strings.Builder
	date := "Date: " + time.Now().UTC().Format(http.TimeFormat) + "\r\n"
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	if learned != nil {
		for _, l := range learned {
			b.WriteString(l + "\r\n")
		}
		b.WriteString(date)
	}
	b.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Accept: " + stealth.WebSocketAccept(key) + "\r\n")
	if learned == nil {
		b.WriteString(date)
	}
	b.WriteString("Set-Cookie: " + g.cookie + "=" + cookieValue() + "; Path=/; HttpOnly; Secure; SameSite=Lax\r\n\r\n")
	return b.String()
}

const cookieChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

func cookieValue() string {
	n := 16 + int(jitter(80))
	b := make([]byte, n)
	for i := range b {
		b[i] = cookieChars[jitter(63)]
	}
	return string(b)
}

// finish ends the session: once, whoever asks first.
func (s *stream) finish() {
	s.once.Do(func() {
		g := s.g
		// Under the gateway's lock, as establish is, so a session's pending
		// place is given back exactly once whichever comes first.
		g.mu.Lock()
		prev := s.state.Swap(stateOver)
		if _, ok := g.streams[s]; ok {
			delete(g.streams, s)
			delete(g.behind, s.wg.LocalAddr().String())
			if s.poll != nil && g.polls[s.poll.id] == s {
				delete(g.polls, s.poll.id)
			}
			if s.shape != nil && g.shapes[s.shape.id] == s {
				delete(g.shapes, s.shape.id)
			}
			if prev == statePending {
				s.dev.pending--
			}
			s.dev.streams--
			g.held--
			g.stats.ended.Add(1)
		}
		g.mu.Unlock()
		close(s.done)
		s.wg.Close()
	})
}

// reader takes the client's frames until the session ends.
func (s *stream) reader() {
	defer close(s.readDone)
	defer s.finish()
	buf := make([]byte, stealth.MaxSealed)
	for {
		op, payload, err := stealth.ReadFrame(s.r, buf, true)
		if err != nil {
			return
		}
		switch op {
		case stealth.OpClose:
			return
		case stealth.OpPing:
			select {
			case s.ctl <- append([]byte(nil), payload...):
			default:
			}
			continue
		case stealth.OpPong:
			continue
		}
		if s.state.Load() == stateOver {
			return
		}
		if !s.chunk(payload, time.Now()) {
			return
		}
	}
}

// chunk is one sealed chunk from the client. A chunk that does not open ends
// the session, and so does a first chunk that holds anything but a
// handshake initiation of the session's device or a keepalive: a session is
// never opened with user data.
func (s *stream) chunk(sealed []byte, now time.Time) bool {
	var items [][]byte
	if err := s.recv.Open(sealed, func(d []byte) { items = append(items, d) }); err != nil {
		s.g.drop(dropUnopened)
		return false
	}
	// A chunk in order under the session's key is the client's and nobody
	// else's -- a stream cannot be replayed -- so every one is activity.
	s.active(now)
	if s.first {
		s.first = false
		if len(items) == 0 || !(isInitiation(items[0]) || isKeepalive(items[0])) {
			s.g.drop(dropTunnel)
			return false
		}
	}
	quiet := true
	for _, d := range items {
		if !s.datagram(d, now) {
			return false
		}
		quiet = quiet && isKeepalive(d)
	}
	if quiet && s.isLive() {
		s.echo()
	}
	return true
}

// datagram is one datagram from the client: it goes to the tunnel as the
// session's device's, or the session ends.
func (s *stream) datagram(d []byte, now time.Time) bool {
	g := s.g
	switch wgType(d) {
	case 1:
		g.mu.Lock()
		ok := s.handshakes.take(sessionHandshakes, now)
		g.mu.Unlock()
		if !ok {
			g.drop(dropHandshakes)
			return true // WireGuard tries again; the session stays
		}
		if !isInitiation(d) || !s.dev.usable || !g.resp.from(d, s.dev.pub, s.dev.ss) {
			g.drop(dropTunnel)
			return false
		}
	case 2:
		if len(d) != 92 {
			g.drop(dropTunnel)
			return false
		}
	case 3:
		if len(d) != 64 {
			g.drop(dropTunnel)
			return false
		}
	case 4:
		if len(d) < 32 {
			g.drop(dropTunnel)
			return false
		}
		// A transport message under an index the tunnel gave this device in
		// a response the gateway carried is the device's: the session is
		// established without waiting for the tunnel, which says nothing to
		// a keepalive.
		if s.isPending() {
			g.mu.Lock()
			at, known := s.dev.indexes[receiverIndex(d)]
			known = known && now.Sub(at) < indexFor
			g.mu.Unlock()
			if known && s.establish() {
				s.queue(nil)
			}
		}
	default:
		g.drop(dropTunnel)
		return false
	}
	if _, err := s.wg.Write(d); err != nil {
		g.drop(dropUnsendable)
	}
	return true
}

// echo answers a chunk that said nothing -- padding, or keepalives alone --
// with a padding-only chunk after a short random wait, unless something else
// went out meanwhile: a client that hears nothing cannot tell a live session
// from a dead one, and a front closes a connection that is quiet too long.
func (s *stream) echo() {
	if s.echoes.Add(1) > maxEchoes {
		s.echoes.Add(-1)
		return
	}
	sent := s.sent.Load()
	time.AfterFunc(jitter(echoDelay), func() {
		s.echoes.Add(-1)
		if s.sent.Load() == sent && s.isLive() {
			s.queue(nil)
		}
	})
}

// establish makes a pending session live.
func (s *stream) establish() bool {
	g := s.g
	g.mu.Lock()
	defer g.mu.Unlock()
	if !s.state.CompareAndSwap(statePending, stateLive) {
		return false
	}
	s.dev.pending-- // registered: establish runs only once the session does
	g.stats.established.Add(1)
	close(s.live)
	return true
}

// queue hands a datagram, or nil for padding alone, to the writer. A full
// queue drops it, as a full link would.
func (s *stream) queue(d []byte) {
	select {
	case s.out <- d:
	default:
		s.g.drop(dropUnsendable)
	}
}

// fromTunnel is what the tunnel sends the device: it makes a pending session
// live, since the tunnel speaks to an endpoint only once something authentic
// came from it, and goes out on it.
func (s *stream) fromTunnel() {
	buf := make([]byte, 65536)
	for {
		n, err := s.wg.Read(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.state.Load() == stateOver {
				return
			}
			continue
		}
		d := buf[:n]
		if n < stealth.MinDatagram || n > stealth.MaxDatagram {
			continue
		}
		now := time.Now()
		if idx, ok := responseSender(d); ok {
			g := s.g
			g.mu.Lock()
			for i, at := range s.dev.indexes {
				if now.Sub(at) >= indexFor {
					delete(s.dev.indexes, i)
				}
			}
			s.dev.indexes[idx] = now
			g.mu.Unlock()
		}
		s.active(now)
		s.establish()
		if !s.isLive() {
			continue
		}
		d = append([]byte(nil), d...)
		// A keepalive alone is held a moment, so the tunnel's keepalives
		// keep no rhythm of their own.
		if isKeepalive(d) {
			time.AfterFunc(jitter(keepaliveDelay), func() {
				if s.isLive() {
					s.queue(d)
				}
			})
			continue
		}
		s.queue(d)
	}
}

// writer seals what waits into chunks, as many datagrams to a chunk as wait
// and fit, and closes the connection when the session ends.
func (s *stream) writer() {
	defer s.closeConn()
	var (
		batch  [][]byte
		carry  []byte
		sealed = make([]byte, 0, stealth.MaxSealed)
		frame  = make([]byte, 0, stealth.MaxSealed+14)
	)
	for {
		var first []byte
		var ok bool
		if carry != nil {
			first, ok, carry = carry, true, nil
		} else {
			select {
			case <-s.done:
				return
			case p := <-s.ctl:
				if !s.write(stealth.AppendFrame(frame[:0], stealth.OpPong, p, false)) {
					return
				}
				continue
			case first, ok = <-s.out:
			}
		}
		if !ok {
			return
		}
		batch = batch[:0]
		if first != nil {
			batch = append(batch, first)
		}
	drain:
		for {
			select {
			case d := <-s.out:
				if d == nil {
					continue // padding is what a chunk has anyway
				}
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
			s.g.drop(dropUnsendable)
			s.finish()
			return
		}
		if !s.write(stealth.AppendFrame(frame[:0], stealth.OpBinary, sealed, false)) {
			return
		}
		s.sent.Add(1)
		s.g.stats.sent.Add(1)
	}
}

func (s *stream) write(frame []byte) bool {
	s.conn.SetWriteDeadline(time.Now().Add(writeFor))
	if _, err := s.conn.Write(frame); err != nil {
		s.finish()
		return false
	}
	return true
}

// closeConn ends the connection the same way whatever ended the session: a
// close of 1000 without a reason, a wait for the client's close, and the
// connection let go. The cause cannot be read from outside.
func (s *stream) closeConn() {
	s.finish()
	s.conn.SetWriteDeadline(time.Now().Add(closeWait))
	s.conn.Write(stealth.AppendFrame(nil, stealth.OpClose, stealth.CloseNormal, false))
	s.conn.SetReadDeadline(time.Now().Add(closeWait))
	select {
	case <-s.readDone:
	case <-time.After(closeWait):
	}
	s.conn.Close()
}
