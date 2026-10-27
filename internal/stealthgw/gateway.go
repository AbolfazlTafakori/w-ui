package stealthgw

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// Config is one tunnel's wui-udp gateway.
type Config struct {
	// Listen is the UDP address clients reach the gateway at.
	Listen string
	// WireGuard is the tunnel's own listen address, as the gateway reaches
	// it: the loopback and the tunnel's port.
	WireGuard string
	// PrivateKey is the tunnel's WireGuard private key. The gateway needs it
	// to tell whose a handshake initiation is, and for nothing else.
	PrivateKey [32]byte
	// MTU is the tunnel's MTU; no packet leaves larger than it plus
	// stealth.UDPOverhead.
	MTU int
	// ReplayFile is where the replay limit is kept, one file per gateway.
	ReplayFile string
	// Sessions bounds the source addresses, across devices, held at once.
	// Zero is the default.
	Sessions int
	// Wide and Normal are the padding ceilings; zero is the largest.
	Wide, Normal int
	Log          *slog.Logger
}

// Device is one device that may use the gateway: its WireGuard public key,
// which the tunnel knows it by, and its transport secret.
type Device struct {
	PublicKey [32]byte
	Secret    stealth.Secret
}

// Gateway is wui-udp in front of one WireGuard tunnel.
//
// Each source address and device gets a socket of its own to the tunnel, so
// that WireGuard sees every device at an endpoint of its own and roams it
// exactly as it would a device on plain UDP: only on a packet that
// authenticated. The gateway unwraps what comes in, wraps what goes out, and
// keeps every rule of the wrapping; WireGuard keeps all of its own.
type Gateway struct {
	cfg    Config
	log    *slog.Logger
	server stealth.UDPServer
	resp   *responder
	replay *replay
	wg     *net.UDPAddr
	conn   *net.UDPConn
	room   int // the largest packet a session may send

	table atomic.Pointer[tagTable]

	mu       sync.Mutex
	devices  map[[32]byte]*device
	slots    map[netip.AddrPort][]*slot // by source
	nslots   int
	sources  map[netip.Addr]*bucket
	behind   map[string]netip.AddrPort // a slot socket's address -> its source
	askLimit uint64                    // a raise a client above the limit asked for

	stats counters
	wake  chan struct{}
	wgSet sync.WaitGroup
}

// device is what the gateway knows of one device.
type device struct {
	pub     [32]byte
	secret  stealth.Secret
	keys    stealth.DeviceKeys
	ss      [32]byte // static-static secret with the tunnel
	usable  bool     // its public key gives a secret at all
	proofs  bucket
	spent   bucket
	pending int
	nslots  int
	// indexes are the WireGuard session indexes the tunnel gave the device
	// in its handshake responses, and when.
	indexes map[uint32]time.Time
	gone    bool
}

// tagTable finds a device by the tag of an opening: the tags of the epochs
// either side of the current one too, so a client a little off the clock is
// still found.
type tagTable struct {
	epoch uint64
	byTag map[stealth.Tag]*device
}

// New prepares a gateway. Nothing listens until Serve.
func New(cfg Config) (*Gateway, error) {
	if cfg.MTU <= 0 {
		return nil, errors.New("stealthgw: the tunnel's MTU is not set")
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
	var anchor stealth.Anchor
	copy(anchor[:], resp.priv.PublicKey().Bytes())
	g := &Gateway{
		cfg: cfg, log: cfg.Log.With("transport", stealth.UDP, "listen", cfg.Listen),
		server: stealth.NewUDPServer(anchor), resp: resp, wg: wg,
		room:    cfg.MTU + stealth.UDPOverhead,
		devices: map[[32]byte]*device{}, slots: map[netip.AddrPort][]*slot{}, sources: map[netip.Addr]*bucket{},
		behind: map[string]netip.AddrPort{},
		wake:   make(chan struct{}, 1),
	}
	g.table.Store(&tagTable{byTag: map[stealth.Tag]*device{}})
	return g, nil
}

// anchor is the tunnel's public key, which every device key is bound to.
func (g *Gateway) anchor() stealth.Anchor {
	var a stealth.Anchor
	copy(a[:], g.resp.priv.PublicKey().Bytes())
	return a
}

// SetDevices puts the devices that may use the gateway in force. A device
// that is no longer among them loses its sessions at once.
func (g *Gateway) SetDevices(list []Device) {
	anchor := g.anchor()
	now := time.Now()
	g.mu.Lock()
	keep := make(map[[32]byte]*device, len(list))
	for _, d := range list {
		if old, ok := g.devices[d.PublicKey]; ok && old.secret == d.Secret {
			keep[d.PublicKey] = old
			continue
		}
		dev := &device{pub: d.PublicKey, secret: d.Secret, keys: stealth.Derive(d.Secret, anchor), indexes: map[uint32]time.Time{}}
		dev.ss, dev.usable = g.resp.static(d.PublicKey)
		dev.proofs, dev.spent = newBucket(deviceProofs, now), newBucket(deviceSpent, now)
		keep[d.PublicKey] = dev
	}
	for pub, dev := range g.devices {
		if keep[pub] != dev {
			dev.gone = true
		}
	}
	g.devices = keep
	g.dropGoneLocked()
	g.mu.Unlock()
	g.rebuild(stealth.Epoch(now))
}

// rebuild makes the tag table for an epoch.
func (g *Gateway) rebuild(epoch uint64) {
	g.mu.Lock()
	devs := make([]*device, 0, len(g.devices))
	for _, d := range g.devices {
		devs = append(devs, d)
	}
	g.mu.Unlock()
	t := &tagTable{epoch: epoch, byTag: make(map[stealth.Tag]*device, 3*len(devs))}
	collisions := 0
	for _, d := range devs {
		for e := epoch - 1; e <= epoch+1; e++ {
			tag := d.keys.EpochTag(e)
			if other, ok := t.byTag[tag]; ok && other != d {
				// Two random secrets never meet here; a copied secret does.
				delete(t.byTag, tag)
				collisions++
				continue
			}
			t.byTag[tag] = d
		}
	}
	if collisions > 0 {
		g.log.Error("two devices share a tag, so neither can open a session in that epoch; their transport secrets are copies and must be replaced", "tags", collisions)
	}
	g.table.Store(t)
}

// Listen opens the gateway's socket and its replay limit. Serve calls it
// when it has not been called.
func (g *Gateway) Listen() error {
	if g.conn != nil {
		return nil
	}
	now := time.Now()
	r, err := openReplay(g.cfg.ReplayFile, uint64(now.Unix()))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// A gateway's first start, or its file was taken away: nothing says
		// what an earlier run took, so nothing is taken for a minute.
		g.log.Info("no replay limit is kept yet; the gateway takes openings from a minute from now")
	case err != nil:
		g.log.Error("the replay limit could not be read; the gateway takes openings from a minute from now", "error", err)
	}
	g.replay = r
	addr, err := net.ResolveUDPAddr("udp", g.cfg.Listen)
	if err != nil {
		return fmt.Errorf("stealthgw: listen address: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("stealthgw: listen: %w", err)
	}
	g.conn = conn
	g.rebuild(stealth.Epoch(now))
	// The first limit is written before the first opening can be taken.
	g.writeDue(uint64(now.Unix()))
	return nil
}

// Serve runs the gateway until ctx ends.
func (g *Gateway) Serve(ctx context.Context) error {
	if err := g.Listen(); err != nil {
		return err
	}
	conn := g.conn
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g.wgSet.Add(1)
	go func() {
		defer g.wgSet.Done()
		g.tick(ctx)
	}()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	g.read(ctx)
	cancel()
	g.mu.Lock()
	for _, list := range g.slots {
		for _, s := range list {
			s.close()
		}
	}
	g.slots, g.nslots = map[netip.AddrPort][]*slot{}, 0
	clear(g.behind)
	g.mu.Unlock()
	g.wgSet.Wait()
	return nil
}

// Addr is the address the gateway listens at, once it listens.
func (g *Gateway) Addr() net.Addr {
	if g.conn == nil {
		return nil
	}
	return g.conn.LocalAddr()
}

// read takes every datagram off the public socket.
func (g *Gateway) read(ctx context.Context) {
	buf := make([]byte, 65536)
	scratch := make([]byte, 0, 65536)
	for {
		n, from, err := g.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			continue // an ICMP error read back as one; nothing to do
		}
		g.datagram(netip.AddrPortFrom(from.Addr().Unmap(), from.Port()), buf[:n], scratch, time.Now())
	}
}

// tick does what is due once a second: a new epoch's tags, the replay
// limit, and the sessions whose time is over.
func (g *Gateway) tick(ctx context.Context) {
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
		if e := stealth.Epoch(now); e != g.table.Load().epoch {
			g.rebuild(e)
		}
		g.writeDue(uint64(now.Unix()))
		g.replay.expire(uint64(now.Unix()))
		g.sweep(now)
	}
}

// writeDue writes a raise of the replay limit, when one is due. A write that
// fails leaves the old limit, which is always safe: openings above it are
// refused until a write succeeds.
func (g *Gateway) writeDue(now uint64) {
	g.mu.Lock()
	asked := g.askLimit
	g.askLimit = 0
	g.mu.Unlock()
	limit := g.replay.due(now, asked)
	if limit == 0 {
		return
	}
	if err := g.replay.write(limit); err != nil {
		g.log.Error("the replay limit could not be written; openings are refused once the clock reaches the old one", "error", err)
	}
}

// Drop reasons, for the counters. Whoever sent the datagram gets silence
// whatever it is.
const (
	dropShort = iota
	dropUnknown
	dropUnopened
	dropWindow
	dropFloor
	dropSeen
	dropCacheFull
	dropLimit
	dropSourceDevices
	dropPool
	dropPending
	dropDeviceRate
	dropSourceRate
	dropHandshakes
	dropTunnel
	dropUnsendable
	drops
)

type counters struct {
	dropped     [drops]atomic.Uint64
	opened      atomic.Uint64
	spent       atomic.Uint64
	established atomic.Uint64
	sent        atomic.Uint64
	ended       atomic.Uint64
}

func (g *Gateway) drop(why int) { g.stats.dropped[why].Add(1) }

// datagram is everything the gateway does with one datagram from a client.
func (g *Gateway) datagram(from netip.AddrPort, p, scratch []byte, now time.Time) {
	// 1. Too short for any packet.
	if len(p) < stealth.DataMin {
		g.drop(dropShort)
		return
	}
	// 2. A data packet of a session this source has.
	type known struct {
		s    *slot
		sess *session
	}
	var tries []known
	g.mu.Lock()
	for _, s := range g.slots[from] {
		for _, sess := range s.readable(now) {
			tries = append(tries, known{s, sess})
		}
	}
	g.mu.Unlock()
	for _, k := range tries {
		if d, err := k.sess.open(p, scratch); err == nil {
			g.chunk(k.s, k.sess, d, now)
			return
		}
	}
	// 3. An opening: the tag, one HMAC and one lookup, then the seal.
	tag, ok := g.server.Tag(p)
	dev := g.table.Load().byTag[tag]
	if !ok || dev == nil {
		g.drop(dropUnknown)
		return
	}
	o, err := g.server.Unseal(dev.keys, p, scratch)
	if err != nil {
		g.drop(dropUnopened)
		return
	}
	// 4. The replay rule. It records nothing yet: an opening that opens a
	// session spends its salt, and so does one dropped from here on, while
	// its device's allowance for that lasts -- dropped unrecorded, it would
	// be good later for whoever saw it, and recorded without bound, one
	// device could fill the cache for everybody.
	ts := uint64(now.Unix())
	switch v, raise := g.replay.judge(o.Salt, o.Timestamp, ts); v {
	case accepted:
	case aboveLimit:
		g.mu.Lock()
		g.askLimit = max(g.askLimit, raise)
		g.mu.Unlock()
		select {
		case g.wake <- struct{}{}:
		default:
		}
		g.spend(dev, &o, now)
		g.drop(dropLimit)
		return
	default:
		g.drop(map[verdict]int{outsideWindow: dropWindow, atFloor: dropFloor, seenBefore: dropSeen, cacheFull: dropCacheFull}[v])
		return
	}
	// 5. What the opening carries: an initiation of this very device, or a
	// keepalive of its tunnel.
	d := o.Datagram
	switch {
	case isInitiation(d):
		if !dev.usable || !g.resp.from(d, dev.pub, dev.ss) {
			g.spend(dev, &o, now)
			g.drop(dropTunnel)
			return
		}
	case isKeepalive(d):
	default:
		g.spend(dev, &o, now)
		g.drop(dropTunnel)
		return
	}
	// 6. The limits, and the session.
	sess, s, why := g.open(dev, from, o.Keys(), len(p), now)
	if sess == nil {
		g.spend(dev, &o, now)
		g.drop(why)
		return
	}
	g.replay.record(o.Salt, o.Timestamp)
	g.stats.opened.Add(1)
	g.chunk(s, sess, d, now)
}

// open makes a pending session for an accepted opening, if the limits have
// room: in place of a pending one of the same source and device.
func (g *Gateway) open(dev *device, from netip.AddrPort, keys stealth.UDPKeys, budget int, now time.Time) (*session, *slot, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if dev.gone {
		return nil, nil, dropUnknown
	}
	list := g.slots[from]
	var s *slot
	for _, x := range list {
		if x.dev == dev {
			s = x
		}
	}
	switch {
	case s == nil && len(list) >= sourceDevices:
		return nil, nil, dropSourceDevices
	case s == nil && (g.nslots >= g.cfg.Sessions || dev.nslots >= deviceSlots):
		return nil, nil, dropPool
	case dev.pending >= devicePending && (s == nil || s.pending == nil):
		return nil, nil, dropPending
	}
	src, ok := g.sources[from.Addr()]
	if !ok {
		if len(g.sources) >= maxSources {
			g.pruneSourcesLocked(now)
		}
		if len(g.sources) >= maxSources {
			return nil, nil, dropSourceRate
		}
		b := newBucket(sourceProofs, now)
		src = &b
		g.sources[from.Addr()] = src
	}
	if !dev.proofs.take(deviceProofs, now) {
		return nil, nil, dropDeviceRate
	}
	if !src.take(sourceProofs, now) {
		return nil, nil, dropSourceRate
	}
	if s == nil {
		var err error
		if s, err = g.newSlotLocked(dev, from, now); err != nil {
			g.log.Warn("no socket to the tunnel could be opened", "error", err)
			return nil, nil, dropUnsendable
		}
	}
	sess := newSession(keys, stealth.NewPadder(g.cfg.Wide, g.cfg.Normal), budget, now)
	if s.pending != nil {
		s.pending.end()
	} else {
		dev.pending++
	}
	s.pending = sess
	return sess, s, 0
}

// spend records the salt of an opening that opened nothing, while its
// device's allowance for that has room.
func (g *Gateway) spend(dev *device, o *stealth.Opening, now time.Time) {
	g.mu.Lock()
	ok := dev.spent.take(deviceSpent, now)
	g.mu.Unlock()
	if ok {
		g.replay.record(o.Salt, o.Timestamp)
		g.stats.spent.Add(1)
	}
}

func (g *Gateway) pruneSourcesLocked(now time.Time) {
	for a, b := range g.sources {
		if b.full(sourceProofs, now) {
			delete(g.sources, a)
		}
	}
}

// chunk is what a session does with the datagram a packet held: it goes to
// the tunnel as the session's device's, or nowhere.
func (g *Gateway) chunk(s *slot, sess *session, d []byte, now time.Time) {
	if d == nil {
		return // padding alone: nothing, and no activity, since a copy opens too
	}
	switch wgType(d) {
	case 1:
		// The allowance first: checking an initiation costs a key exchange.
		g.mu.Lock()
		ok := sess.handshakes.take(sessionHandshakes, now)
		g.mu.Unlock()
		if !ok {
			g.drop(dropHandshakes)
			return
		}
		if !isInitiation(d) || !s.dev.usable || !g.resp.from(d, s.dev.pub, s.dev.ss) {
			g.drop(dropTunnel)
			return
		}
		s.active(now)
	case 2:
		if len(d) != 92 {
			g.drop(dropTunnel)
			return
		}
	case 3:
		if len(d) != 64 {
			g.drop(dropTunnel)
			return
		}
	case 4:
		if len(d) < 32 {
			g.drop(dropTunnel)
			return
		}
		idx, fresh := receiverIndex(d), s.fresh(d)
		if fresh {
			s.active(now)
		}
		// A transport message under an index the tunnel gave this device
		// in a response the gateway carried is the device's: a session
		// that opened on it is established without waiting for the tunnel,
		// which says nothing to a keepalive.
		if sess.isPending() {
			g.mu.Lock()
			at, known := s.dev.indexes[idx]
			known = known && now.Sub(at) < indexFor
			g.mu.Unlock()
			if known && g.establish(s, sess, now) {
				g.send(s, sess, nil)
			}
		}
		if isKeepalive(d) && fresh && sess.isLive() {
			g.echo(s, sess)
		}
	default:
		g.drop(dropTunnel)
		return
	}
	if _, err := s.wg.Write(d); err != nil {
		g.drop(dropUnsendable)
	}
}

// echo answers a keepalive with a padding-only packet after a short random
// wait, unless something else went out meanwhile: WireGuard does not answer
// a keepalive, and a client that hears nothing cannot tell a live session
// from a dead one.
func (g *Gateway) echo(s *slot, sess *session) {
	if sess.echoes.Add(1) > maxEchoes {
		sess.echoes.Add(-1)
		return
	}
	sent := sess.sent.Load()
	time.AfterFunc(jitter(echoDelay), func() {
		sess.echoes.Add(-1)
		if sess.sent.Load() == sent && sess.isLive() {
			g.send(s, sess, nil)
		}
	})
}

// fromTunnel is what the tunnel sends a device: it goes out on the device's
// live session at this source, and makes a pending one live, since the
// tunnel speaks to an endpoint only once something authentic came from it.
func (g *Gateway) fromTunnel(s *slot, d []byte) {
	now := time.Now()
	if len(d) < stealth.MinDatagram || len(d) > stealth.MaxDatagram {
		return
	}
	if idx, ok := responseSender(d); ok {
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
	// A pending session at the slot is newer than its live one: its client
	// opened it on purpose -- a renewal, or a session that went quiet -- and
	// listens on both until it hears from it. The socket is the slot's, so
	// the tunnel's word is the word on either.
	g.mu.Lock()
	sess := s.live
	pending := s.pending
	g.mu.Unlock()
	if pending != nil && g.establish(s, pending, now) {
		sess = pending
	}
	if sess == nil {
		return
	}
	// A keepalive alone is held a moment, so the tunnel's keepalives keep
	// no rhythm of their own.
	if isKeepalive(d) {
		d = append([]byte(nil), d...)
		time.AfterFunc(jitter(keepaliveDelay), func() { g.send(s, sess, d) })
		return
	}
	g.send(s, sess, d)
}

// establish makes a pending session the slot's live one. The one it replaces
// is still read for a while; the one before that ends.
func (g *Gateway) establish(s *slot, sess *session, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s.pending != sess || sess.over() {
		return false
	}
	s.pending = nil
	s.dev.pending--
	sess.state.Store(stateLive)
	if s.live != nil {
		s.live.state.Store(stateRetired)
		if s.old != nil {
			s.old.end()
		}
		s.old, s.oldUntil = s.live, now.Add(oldReadable)
	}
	s.live = sess
	g.stats.established.Add(1)
	return true
}

// send seals one datagram, or padding alone for nil, to the slot's source.
func (g *Gateway) send(s *slot, sess *session, d []byte) {
	p, err := sess.seal(d, g.room)
	switch {
	case errors.Is(err, stealth.ErrNumber):
		g.log.Info("a session reached the packets one key may seal and is ended; its client opens a new one")
		sess.end()
		return
	case err != nil:
		g.drop(dropUnsendable)
		return
	}
	if _, err := g.conn.WriteToUDPAddrPort(p, s.source); err == nil {
		g.stats.sent.Add(1)
	}
}

// sweep ends what is over and lets go of slots with nothing left.
func (g *Gateway) sweep(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for src, list := range g.slots {
		kept := list[:0]
		for _, s := range list {
			if s.tidy(now) {
				kept = append(kept, s)
				continue
			}
			g.closeSlotLocked(s)
		}
		if len(kept) == 0 {
			delete(g.slots, src)
		} else {
			g.slots[src] = kept
		}
	}
	for a, b := range g.sources {
		if b.full(sourceProofs, now) {
			delete(g.sources, a)
		}
	}
}

// dropGoneLocked ends every slot of a device that was taken away.
func (g *Gateway) dropGoneLocked() {
	for src, list := range g.slots {
		kept := list[:0]
		for _, s := range list {
			if s.dev.gone {
				g.closeSlotLocked(s)
				continue
			}
			kept = append(kept, s)
		}
		if len(kept) == 0 {
			delete(g.slots, src)
		} else {
			g.slots[src] = kept
		}
	}
}

func (g *Gateway) closeSlotLocked(s *slot) {
	if s.pending != nil {
		s.dev.pending--
		s.pending = nil
	}
	s.dev.nslots--
	g.nslots--
	delete(g.behind, s.wg.LocalAddr().String())
	g.stats.ended.Add(1)
	s.close()
}

// newSlotLocked opens a slot and its socket to the tunnel, and starts
// reading what the tunnel sends it.
func (g *Gateway) newSlotLocked(dev *device, from netip.AddrPort, now time.Time) (*slot, error) {
	c, err := net.DialUDP("udp", nil, g.wg)
	if err != nil {
		return nil, err
	}
	s := &slot{source: from, dev: dev, wg: c, created: now, counters: map[uint32]uint64{}}
	s.lastActive.Store(now.UnixNano())
	g.slots[from] = append(g.slots[from], s)
	g.behind[c.LocalAddr().String()] = from
	g.nslots++
	dev.nslots++
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := c.Read(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			g.fromTunnel(s, buf[:n])
		}
	}()
	return s, nil
}

// Source is the address a device really reaches the gateway from, for the
// address the tunnel sees it at: the gateway's own socket on the loopback,
// one per device and source. False for an address that is none of them.
func (g *Gateway) Source(endpoint string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	src, ok := g.behind[endpoint]
	if !ok {
		return "", false
	}
	return src.String(), true
}

// Stats is what the gateway has done since it started, and holds now.
type Stats struct {
	Slots, Devices                 int
	Opened, Spent, Established     uint64
	Sent                           uint64
	Ended                          uint64
	Short, Unknown, Unopened       uint64
	Window, Floor, Seen            uint64
	CacheFull, Limit               uint64
	SourceDevices, Pool, Pending   uint64
	DeviceRate, SourceRate         uint64
	Handshakes, Tunnel, Unsendable uint64
	ReplayFloor, ReplayLimit       uint64
	ReplaySalts                    int
}

// Stats reads the counters.
func (g *Gateway) Stats() Stats {
	g.mu.Lock()
	st := Stats{Slots: g.nslots, Devices: len(g.devices)}
	g.mu.Unlock()
	d := func(i int) uint64 { return g.stats.dropped[i].Load() }
	st.Opened, st.Spent, st.Established = g.stats.opened.Load(), g.stats.spent.Load(), g.stats.established.Load()
	st.Sent, st.Ended = g.stats.sent.Load(), g.stats.ended.Load()
	st.Short, st.Unknown, st.Unopened = d(dropShort), d(dropUnknown), d(dropUnopened)
	st.Window, st.Floor, st.Seen, st.CacheFull, st.Limit = d(dropWindow), d(dropFloor), d(dropSeen), d(dropCacheFull), d(dropLimit)
	st.SourceDevices, st.Pool, st.Pending = d(dropSourceDevices), d(dropPool), d(dropPending)
	st.DeviceRate, st.SourceRate = d(dropDeviceRate), d(dropSourceRate)
	st.Handshakes, st.Tunnel, st.Unsendable = d(dropHandshakes), d(dropTunnel), d(dropUnsendable)
	if g.replay != nil {
		st.ReplayFloor, st.ReplayLimit, st.ReplaySalts = g.replay.state()
	}
	return st
}

// jitter is a wait drawn evenly from [0, max].
func jitter(max time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)+1))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
