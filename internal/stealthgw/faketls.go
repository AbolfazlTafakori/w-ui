package stealthgw

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The mirror gateway.
//
// mirror borrows a real site. The gateway is the server, on a port of its
// own, and every connection opens with a TLS 1.3 ClientHello. A device's hello
// carries a proof in its random and legacy session id (package stealth); any
// other hello carries none. So:
//
//  1. A connection to the borrowed site is opened at once, before the hello is
//     read, so the site's timers run as they would for a visitor that reached
//     it directly.
//  2. The ClientHello record is read, within a deadline and a size.
//  3. The hello is opened (stealth.FakeNode.Open), in constant work: a proof
//     that holds names a device and binds to its secret; anything else -- an
//     ordinary visitor, or a prober who sealed a carrier without a device's
//     bind key -- does not.
//  4. A proof that holds, and that the replay rule and the limits admit, is
//     answered by the gateway's own hand-written TLS 1.3 handshake, with a
//     throwaway leaf only the two ends can compute, and opens a session (the
//     record carry, fakestream.go). The site's connection is closed unused.
//  5. Every other hello, and every byte read with it, goes to the site
//     unchanged, and from then on the gateway relays both ways and reads
//     nothing of its own: the visitor gets the site's real certificate and
//     page. The gateway never writes a byte of its own to such a peer.
//
// The records the site sends a relayed visitor teach the shaper (fakeSizer):
// a session's own records are padded to the lengths the real site showed, so
// they carry its shape without a probe of our own.
//
// What it cannot hide: the handshake a device is answered with is the
// gateway's, not the site's, and its leaf is a throwaway, so a middlebox that
// holds the site's true certificate and compares would see the difference.
// Only someone who cannot tell a device's hello from a visitor's -- which is
// everyone without a device's secret -- is kept out by the proof alone.

// FakeTLSConfig is one tunnel's mirror gateway.
type FakeTLSConfig struct {
	// Listen is the TCP address devices and visitors reach the gateway at.
	Listen string
	// Site is the borrowed site, host:port: whoever is not a device is
	// relayed to it byte for byte, and its records teach the shaper.
	Site string
	// Names are the SNI names a device's hello may ask for; the site's own
	// hostname when empty. A hello for any other name is a visitor's.
	Names []string
	// NodeKey is the transport's X25519 private key, which a proof is sealed
	// against. Its public half is what a client builds its hello on.
	NodeKey [32]byte

	WireGuard    string
	PrivateKey   [32]byte
	ReplayFile   string
	Sessions     int
	Wide, Normal int
	Log          *slog.Logger
}

// mirror limits and timers.
const (
	// fakeHelloFor bounds the read of the ClientHello, from the accept.
	fakeHelloFor = 10 * time.Second
	// fakeSiteDialFor bounds a connection to the borrowed site.
	fakeSiteDialFor = 5 * time.Second
	// fakeHelloMax bounds the first record: a ClientHello, hybrid key share
	// and all, fits well under a full TLS record.
	fakeHelloMax = 16384
	// fakeAcceptMax is how many connections may be in hand at once, beyond the
	// sessions: ones being read or relayed.
	fakeAcceptMax = 2048
	// fakeRelayBuf is what each direction of a relay reads into.
	fakeRelayBuf = 16 << 10
	// fakeTagEvery is how often the hour-tag table is rebuilt, so the hour's
	// tags are always in it.
	fakeTagEvery = time.Minute
	// fakeSizesMax is how many of the site's record sizes the shaper keeps.
	fakeSizesMax = 64
	// fakeSayAgain is how often a site that stays down is said to be.
	fakeSayAgain = time.Minute
	// fakeRelayIdle tears a relay down when no byte has crossed it either way
	// for this long, so a half-closed peer that then goes silent does not hold
	// its place until the gateway stops.
	fakeRelayIdle = 60 * time.Second
)

// FakeTLSGateway is mirror in front of one WireGuard tunnel.
type FakeTLSGateway struct {
	*StreamGateway
	fcfg  FakeTLSConfig
	site  string
	node  *stealth.FakeNode
	names [][]byte
	sizer *fakeSizer

	hourTags atomic.Pointer[map[[stealth.TagLen]byte]*streamDevice]
	room     chan struct{}
	done     chan struct{}

	pmu    sync.Mutex
	nextID uint64
	relays map[uint64]net.Conn // a relayed peer's connection, for shutdown
	siteAt time.Time           // when a site that is down was last said to be
	closed bool

	fstats struct {
		accepted, proved, relayed, siteDown, helloBad, admitRefused atomic.Uint64
	}
}

// NewFakeTLS prepares a gateway. Nothing listens until Serve.
func NewFakeTLS(cfg FakeTLSConfig) (*FakeTLSGateway, error) {
	host, _, err := net.SplitHostPort(cfg.Site)
	if err != nil || host == "" {
		return nil, fmt.Errorf("stealthgw: the site %q is not a host:port", cfg.Site)
	}
	sg, err := newStreamGateway(StreamConfig{
		Listen: cfg.Listen, WireGuard: cfg.WireGuard, PrivateKey: cfg.PrivateKey,
		ReplayFile: cfg.ReplayFile, Sessions: cfg.Sessions, Wide: cfg.Wide, Normal: cfg.Normal, Log: cfg.Log,
	}, stealth.Mirror)
	if err != nil {
		return nil, err
	}
	names := cfg.Names
	if len(names) == 0 {
		names = []string{host}
	}
	nb := make([][]byte, len(names))
	for i, n := range names {
		nb[i] = []byte(n)
	}
	// The constant-work binder of an unknown tag is checked with a key nobody
	// else holds, drawn fresh for this gateway.
	var dummy [32]byte
	if _, err := io.ReadFull(rand.Reader, dummy[:]); err != nil {
		return nil, fmt.Errorf("stealthgw: read entropy: %w", err)
	}
	inbound := sg.resp.priv.PublicKey().Bytes()
	g := &FakeTLSGateway{
		StreamGateway: sg, fcfg: cfg, site: cfg.Site,
		node:  stealth.NewFakeNode(cfg.NodeKey[:], inbound, dummy[:]),
		names: nb, sizer: &fakeSizer{},
		room:   make(chan struct{}, fakeAcceptMax+sg.cfg.Sessions),
		done:   make(chan struct{}),
		relays: map[uint64]net.Conn{},
	}
	empty := map[[stealth.TagLen]byte]*streamDevice{}
	g.hourTags.Store(&empty)
	return g, nil
}

// SetDevices puts the devices in force and rebuilds the hour-tag table.
func (g *FakeTLSGateway) SetDevices(list []Device) {
	g.StreamGateway.SetDevices(list)
	g.refreshTags(time.Now())
}

// refreshTags rebuilds the table that finds a device by the hour tag its
// proof carries. It holds the tags of the hour before, the hour, and the hour
// after, so a clock a little off either way still finds its device. A tag two
// devices share finds neither, as a shared stream tag does.
func (g *FakeTLSGateway) refreshTags(now time.Time) {
	hour := stealth.FakeHourOf(now.Unix())
	g.mu.Lock()
	devs := make([]*streamDevice, 0, len(g.devices))
	for _, d := range g.devices {
		devs = append(devs, d)
	}
	g.mu.Unlock()
	tags := make(map[[stealth.TagLen]byte]*streamDevice, len(devs)*3)
	bad := map[[stealth.TagLen]byte]bool{}
	for _, d := range devs {
		for h := hour - 1; h <= hour+1; h++ {
			t := d.keys.HourTag(h)
			if other, ok := tags[t]; ok && other != d {
				bad[t] = true
				continue
			}
			tags[t] = d
		}
	}
	for t := range bad {
		delete(tags, t)
	}
	g.hourTags.Store(&tags)
}

// Serve runs the gateway until ctx ends.
func (g *FakeTLSGateway) Serve(ctx context.Context) error {
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

// keep rebuilds the hour-tag table as the clock crosses into a new hour.
func (g *FakeTLSGateway) keep(ctx context.Context) {
	t := time.NewTicker(fakeTagEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			g.refreshTags(now)
		}
	}
}

// accept takes connections while there is room, leaving the rest to the
// system's queue as a busy server does.
func (g *FakeTLSGateway) accept() error {
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
		g.fstats.accepted.Add(1)
		go func() {
			defer func() { <-g.room }()
			g.handle(c)
		}()
	}
}

// stop closes the port and every relay; the sessions are ended by serve.
func (g *FakeTLSGateway) stop() {
	g.ln.Close()
	g.pmu.Lock()
	if !g.closed {
		close(g.done)
	}
	g.closed = true
	for _, c := range g.relays {
		c.Close()
	}
	g.pmu.Unlock()
}

// handle is one connection, from its accept on.
func (g *FakeTLSGateway) handle(raw net.Conn) {
	if tc, ok := raw.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	peer := addrPortOf(raw.RemoteAddr()).Addr()

	// The site, now, before the hello is read.
	site := g.dialSite()

	// The ClientHello record, as it arrives.
	raw.SetReadDeadline(time.Now().Add(fakeHelloFor))
	record, message, ok := readClientHello(raw)
	if !ok {
		g.fstats.helloBad.Add(1)
		g.relay(raw, site, record) // relay whatever came, so a half hello is the site's to answer
		return
	}
	raw.SetReadDeadline(time.Time{})

	now := time.Now()
	proof := g.node.Open(message, g.names, func(tag [stealth.TagLen]byte) (any, []byte, bool) {
		dev := (*g.hourTags.Load())[tag]
		if dev == nil {
			return nil, nil, false
		}
		return dev, dev.keys.Bind, true
	})
	if proof.Kind != stealth.FakeProved {
		// Relay to the site: a visitor, or a prober without a device's secret,
		// gets the site's own answer and nothing of the gateway's.
		g.relay(raw, site, record)
		return
	}

	// A proof that holds: the gateway's own handshake, and a session. The
	// site's connection is closed unused.
	site.abandon()
	if !g.device(raw, peer, message, proof, now) {
		raw.Close()
	}
}

// device answers a proved hello with the gateway's flight and runs its
// session. False when nothing opened and the caller closes the connection.
func (g *FakeTLSGateway) device(raw net.Conn, peer netip.Addr, message []byte, proof stealth.FakeProof, now time.Time) bool {
	hello, ok := stealth.ParseHello(message)
	if !ok {
		return false
	}
	leaf, _ := stealth.LeafKey(proof.Seed)
	if leaf == nil {
		return false
	}
	server, err := stealth.FakeServerFlight(message, hello, stealth.SuiteAES128, leaf)
	if err != nil {
		return false
	}
	dev := proof.Device.(*streamDevice)
	salt := sha256.Sum256(message)
	if !g.admit(dev, salt, proof.Timestamp, peer, now) {
		g.fstats.admitRefused.Add(1)
		return false
	}
	s, ok := g.newFakeSession(dev, peer, g.sizer, now)
	if !ok {
		return false
	}
	g.fstats.proved.Add(1)
	s.conn, s.r = raw, bufio.NewReader(raw)
	g.carry(s, server)
	return true
}

// admit is the replay rule, the limits and a place for the session, for a
// proof that held. salt is the hello's hash, which no two proofs share.
func (g *FakeTLSGateway) admit(dev *streamDevice, salt [32]byte, ts uint64, peer netip.Addr, now time.Time) bool {
	v, raise := g.replay.judge(salt, ts, uint64(now.Unix()))
	if v == aboveLimit {
		g.mu.Lock()
		g.askLimit = max(g.askLimit, raise)
		g.mu.Unlock()
		select {
		case g.wake <- struct{}{}:
		default:
		}
		for end := time.Now().Add(limitWait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if _, limit, _ := g.replay.state(); limit >= ts {
				break
			}
		}
		v, _ = g.replay.judge(salt, ts, uint64(time.Now().Unix()))
	}
	if v != accepted {
		if v == aboveLimit {
			g.fakeSpend(dev, salt, ts, now)
		}
		g.drop(map[verdict]int{outsideWindow: dropWindow, atFloor: dropFloor, seenBefore: dropSeen, cacheFull: dropCacheFull, aboveLimit: dropLimit}[v])
		return false
	}
	if why := g.reserve(dev, peer, now); why >= 0 {
		g.fakeSpend(dev, salt, ts, now)
		g.drop(why)
		return false
	}
	if v, _ := g.replay.claim(salt, ts, uint64(now.Unix())); v != accepted {
		g.release(dev)
		g.drop(dropSeen)
		return false
	}
	g.stats.opened.Add(1)
	return true
}

// fakeSpend records a proof that opened nothing, while its device's allowance
// for that lasts: left unrecorded it would be good later for whoever saw it.
func (g *FakeTLSGateway) fakeSpend(dev *streamDevice, salt [32]byte, ts uint64, now time.Time) {
	if dev == nil {
		return
	}
	g.mu.Lock()
	ok := dev.spent.take(deviceSpent, now)
	g.mu.Unlock()
	if ok {
		g.replay.record(salt, ts)
		g.stats.spent.Add(1)
	}
}

// siteDial is the connection to the borrowed site, started at accept.
type siteDial struct {
	cancel context.CancelFunc
	conn   chan net.Conn
}

func (g *FakeTLSGateway) dialSite() *siteDial {
	ctx, cancel := context.WithTimeout(context.Background(), fakeSiteDialFor)
	s := &siteDial{cancel: cancel, conn: make(chan net.Conn, 1)}
	go func() {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", g.site)
		if err != nil {
			s.conn <- nil
			return
		}
		if tc, ok := c.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		s.conn <- c
	}()
	return s
}

// abandon closes the site's connection unused, whenever it opens.
func (s *siteDial) abandon() {
	s.cancel()
	go func() {
		if c := <-s.conn; c != nil {
			c.Close()
		}
	}()
}

// wait gives the site's connection once it opens, or nil.
func (s *siteDial) wait() net.Conn {
	select {
	case c := <-s.conn:
		return c
	case <-time.After(fakeSiteDialFor + time.Second):
		return nil
	}
}

// relay hands a peer that is not a device to the site: every byte it sent
// first, then both ways until either side ends. The site's records teach the
// shaper on the way down.
func (g *FakeTLSGateway) relay(raw net.Conn, dial *siteDial, sent []byte) {
	defer raw.Close()
	if dial == nil {
		dial = g.dialSite()
	}
	site := dial.wait()
	if site == nil {
		g.siteDown()
		return
	}
	defer site.Close()
	id := g.holdRelay(raw)
	defer g.dropRelay(id)
	if g.isClosed() {
		return
	}
	g.fstats.relayed.Add(1)
	raw.SetWriteDeadline(time.Time{})
	// A byte crossing either way keeps both sides' reads alive; a relay silent
	// both ways for fakeRelayIdle times out and is let go, so neither a
	// half-closed peer nor a stalled site holds its place. A one-way transfer
	// is not idle: the busy direction keeps refreshing the deadline for both.
	touch := func() {
		dl := time.Now().Add(fakeRelayIdle)
		raw.SetReadDeadline(dl)
		site.SetReadDeadline(dl)
	}
	touch()

	if len(sent) > 0 {
		if _, err := site.Write(sent); err != nil {
			return
		}
	}
	done := make(chan struct{}, 2)
	// Up: the peer to the site.
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, fakeRelayBuf)
		for {
			n, err := raw.Read(buf)
			if n > 0 {
				touch()
				if _, werr := site.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if c, ok := site.(*net.TCPConn); ok {
					c.CloseWrite()
				}
				return
			}
		}
	}()
	// Down: the site to the peer, its records measured for the shaper.
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, fakeRelayBuf)
		var sizer recordSizer
		for {
			n, err := site.Read(buf)
			if n > 0 {
				touch()
				sizer.feed(buf[:n], g.sizer)
				if _, werr := raw.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if c, ok := raw.(*net.TCPConn); ok {
					c.CloseWrite()
				}
				return
			}
		}
	}()
	<-done
	<-done
}

func (g *FakeTLSGateway) holdRelay(c net.Conn) uint64 {
	g.pmu.Lock()
	defer g.pmu.Unlock()
	g.nextID++
	id := g.nextID
	if g.closed {
		c.Close()
		return id
	}
	g.relays[id] = c
	return id
}

func (g *FakeTLSGateway) dropRelay(id uint64) {
	g.pmu.Lock()
	delete(g.relays, id)
	g.pmu.Unlock()
}

func (g *FakeTLSGateway) isClosed() bool {
	g.pmu.Lock()
	defer g.pmu.Unlock()
	return g.closed
}

// siteDown says, at most once a minute, that the site cannot be reached.
func (g *FakeTLSGateway) siteDown() {
	g.fstats.siteDown.Add(1)
	g.pmu.Lock()
	say := time.Since(g.siteAt) >= fakeSayAgain
	if say {
		g.siteAt = time.Now()
	}
	g.pmu.Unlock()
	if say {
		g.log.Warn("the borrowed site cannot be reached, so a visitor is closed with no answer, which shows the gateway; devices are not affected",
			"site", g.site, "since start", g.fstats.siteDown.Load())
	}
}

// recordSizer reads TLS record lengths from a byte stream, across reads, and
// feeds the plaintext lengths they imply to the shaper.
type recordSizer struct {
	head [stealth.TLSHeader]byte
	have int
	left int // bytes still to skip in the current record body
}

func (z *recordSizer) feed(b []byte, sizer *fakeSizer) {
	var sizes []int
	for len(b) > 0 {
		if z.left > 0 {
			skip := min(z.left, len(b))
			z.left -= skip
			b = b[skip:]
			continue
		}
		need := stealth.TLSHeader - z.have
		take := min(need, len(b))
		copy(z.head[z.have:], b[:take])
		z.have += take
		b = b[take:]
		if z.have < stealth.TLSHeader {
			return
		}
		length := int(z.head[3])<<8 | int(z.head[4])
		z.have = 0
		z.left = length
		// The record's plaintext is its body less the content type and tag; a
		// length too small to hold them is left as is.
		if z.head[0] == 23 && length > 1+stealth.TLSTag {
			sizes = append(sizes, length-1-stealth.TLSTag)
		}
	}
	if len(sizes) > 0 {
		sizer.add(sizes)
	}
}

// FakeTLSStats is what a mirror gateway has done since it started.
type FakeTLSStats struct {
	StreamStats
	Accepted, Proved, Relayed uint64
	SiteDown, HelloBad        uint64
	AdmitRefused              uint64
	Relaying                  int
}

// Stats reads the counters.
func (g *FakeTLSGateway) Stats() FakeTLSStats {
	st := FakeTLSStats{StreamStats: g.StreamGateway.Stats()}
	st.Decoyed = g.fstats.relayed.Load()
	st.Accepted = g.fstats.accepted.Load()
	st.Proved = g.fstats.proved.Load()
	st.Relayed = g.fstats.relayed.Load()
	st.SiteDown = g.fstats.siteDown.Load()
	st.HelloBad = g.fstats.helloBad.Load()
	st.AdmitRefused = g.fstats.admitRefused.Load()
	g.pmu.Lock()
	st.Relaying = len(g.relays)
	g.pmu.Unlock()
	return st
}

// readClientHello reads the first TLS record. A record of type handshake that
// holds exactly one ClientHello message gives that message; otherwise message
// is nil and the record bytes read so far are returned for the relay. A record
// head that is not a TLS handshake record is ok=false at once, with whatever
// was read.
func readClientHello(c net.Conn) (record, message []byte, ok bool) {
	var head [stealth.TLSHeader]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return head[:0], nil, false
	}
	length := int(head[3])<<8 | int(head[4])
	if head[0] != 22 || head[1] != 3 || length == 0 || length > fakeHelloMax {
		return head[:], nil, false
	}
	record = make([]byte, stealth.TLSHeader+length)
	copy(record, head[:])
	if _, err := io.ReadFull(c, record[stealth.TLSHeader:]); err != nil {
		return record[:stealth.TLSHeader], nil, false
	}
	body := record[stealth.TLSHeader:]
	// The record must hold exactly one ClientHello handshake message.
	if len(body) < 4 || body[0] != 1 {
		return record, nil, false
	}
	hsLen := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
	if 4+hsLen != len(body) {
		return record, nil, false
	}
	return record, body, true
}
