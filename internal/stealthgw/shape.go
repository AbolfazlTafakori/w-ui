package stealthgw

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The mold gateway.
//
// mold is relay that need not look like relay: the operator fixes
// the shape of a request -- its method, where the tunnel's data rides, and
// whether answers carry the downstream data (poll) or one long stream does
// (sse). The keys, the chunk, its padding and the proof are drift's; only
// the dress of a request and the numbering of its chunks differ (package
// stealth).
//
// A request is read strictly and by its head before its body: a request that
// is not of the shape in its one canonical form goes to the decoy as it came.
// The opening request carries the proof in the one place a token may go for
// the shape, and opens a session; a later request names the session by its id
// and its number n. The chunks of request n are numbered n*4096 and up, so a
// request that a front replays cannot slip a chunk in under a number the
// gateway has moved past, and the gateway keeps a window of numbers on either
// side of the highest it has accepted.
//
// Up to four requests of a session are read at once, beside one stream. A
// request whose every chunk opens at its numbers is accepted; one that does
// not open changes nothing, so whoever learns an id cannot disturb the
// session. A repeat of an accepted request, by the length and hash of its
// data within a few seconds, gets the answer the gateway kept, or the late
// answer; anything else is the decoy's.

// mold limits and timers.
const (
	// deviceShapes is how many mold sessions one device may hold.
	deviceShapes = 2
	// shapeIdle ends a session no request was accepted on for this long.
	shapeIdle = 60 * time.Second
	// shapeHold is how long a poll answer is held for something to carry.
	shapeHold = 250 * time.Millisecond
	// shapeRepeatAge is how long after a request it is answered as a repeat;
	// a CDN's retry comes within seconds, an older one is a logged URL.
	shapeRepeatAge = 15 * time.Second
	// shapeStored is how many of a session's last accepted requests keep
	// their answers, and shapeStoredBytes the room they share.
	shapeStored      = 4
	shapeStoredBytes = 65536
	// shapeReaders is how many bodies under one id are read at once while
	// nothing is proven of them.
	shapeReaders = 4
)

// mold per-session allowances.
var (
	shapeStarts  = allowance{burst: 4, rate: 1} // stream requests
	shapeReplays = allowance{burst: 16, rate: 8}
)

// ShapeConfig is one tunnel's mold gateway: drift's settings, and the
// shape every request of the transport takes.
type ShapeConfig struct {
	StreamConfig
	Shape stealth.Shape
}

// ShapeGateway is mold in front of one WireGuard tunnel. Its devices,
// limits, replay rule and sessions are drift's; only how a session's
// datagrams travel differs.
type ShapeGateway struct {
	*StreamGateway
	shape stealth.Shape
	path  string // the transport's path in plain, for the one-form reader
}

// NewShape prepares a gateway. Nothing listens until Serve.
func NewShape(cfg ShapeConfig) (*ShapeGateway, error) {
	decoy, err := newDecoy(cfg.Origin)
	if err != nil {
		return nil, err
	}
	g, err := newStreamGateway(cfg.StreamConfig, stealth.Mold)
	if err != nil {
		return nil, err
	}
	g.decoy, g.perDevice = decoy, deviceShapes
	return &ShapeGateway{StreamGateway: g, shape: cfg.Shape, path: cfg.Path}, nil
}

// Serve runs the gateway until ctx ends.
func (p *ShapeGateway) Serve(ctx context.Context) error {
	g := p.StreamGateway
	if err := g.Listen(); err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           p,
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

// shapeSlot is what is known of one of the request numbers a session tracks.
type shapeSlot struct {
	seq    uint64 // the order the request was accepted in
	stream bool   // the request asked for a stream
	length int
	hash   [sha256.Size]byte
	at     time.Time
	answer []byte // the answer it got, while the gateway still has it
	size   int
}

// shapeSession is what a mold session has besides the stream's.
type shapeSession struct {
	id    [stealth.PollIDLen]byte
	shape stealth.Shape
	last  atomic.Int64 // when the last request was accepted, Unix nanoseconds

	mu       sync.Mutex
	recv     *stealth.ShapeReceiver // the client's chunks
	send     *stealth.ShapeSender   // the gateway's chunks, sealed at the request's number
	highest  uint32
	accepted uint64
	slots    map[uint32]*shapeSlot
	flying   int
	readers  int
	// the live stream, if any: its number among the session's streams, and a
	// channel closed to end it.
	streamID uint64
	streams  uint64
	kill     chan struct{}
	ended    bool
	starts   bucket
	replays  bucket
}

// ServeHTTP takes one request: a device's is taken or answered, and anything
// else goes to the decoy as it came.
func (p *ShapeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g := p.StreamGateway
	now := time.Now()
	peer := addrOf(r.RemoteAddr)
	if !g.trusted(peer) || r.Method != p.shape.Method {
		g.toDecoy(w, r)
		return
	}
	// The data rides where the shape says; read the target in its one form.
	var header []byte
	if p.shape.Place == stealth.InHeader {
		if vs := r.Header.Values(p.shape.Name); len(vs) == 1 {
			header = []byte(vs[0])
		}
	}
	line := stealth.RequestLine{Path: []byte(r.URL.EscapedPath())}
	if r.URL.RawQuery != "" {
		line.Query = []byte(r.URL.RawQuery)
	}
	target, ok := stealth.ReadTarget(p.shape, []byte(p.path), line, header)
	if !ok {
		g.toDecoy(w, r)
		return
	}
	visitor := g.visitor(r, peer)
	if target.Who.IsOpening {
		dev, keys, ok := g.prove(string(target.Who.Opening), visitor, now)
		if !ok {
			g.toDecoy(w, r)
			return
		}
		p.opening(w, r, dev, visitor, keys, target, now)
		return
	}
	p.later(w, r, target, now)
}

// datadataForBody reads the data of a request: the body for a body shape (it
// must arrive whole within pollBodyFor), or the decoded target otherwise.
func (p *ShapeGateway) data(w http.ResponseWriter, r *http.Request, target stealth.ShapeTarget) (data []byte, ok bool, settle func()) {
	if target.Bodied() {
		return readBody(w, r)
	}
	return target.Data(), true, func() {}
}

// opening is the request a proof was taken from: it opens the session and is
// answered once the tunnel has spoken on it.
func (p *ShapeGateway) opening(w http.ResponseWriter, r *http.Request, dev *streamDevice, visitor netip.Addr, keys stealth.StreamKeys, target stealth.ShapeTarget, now time.Time) {
	g := p.StreamGateway
	recv, err1 := stealth.NewShapeReceiver(keys.Up)
	send, err2 := stealth.NewShapeSender(keys.Down, stealth.NewPadder(g.cfg.Wide, g.cfg.Normal))
	if err1 != nil || err2 != nil {
		g.release(dev)
		g.toDecoy(w, r)
		return
	}
	sh := &shapeSession{
		id: keys.ID, shape: p.shape, recv: recv, send: send,
		slots: map[uint32]*shapeSlot{}, flying: 1,
		starts: newBucket(shapeStarts, now), replays: newBucket(shapeReplays, now),
	}
	sh.last.Store(now.UnixNano())
	s, ok := g.newShapeSession(dev, visitor, keys, now, sh)
	if !ok {
		g.toDecoy(w, r)
		return
	}
	go s.fromTunnel()
	refuse := func(why int) {
		s.finish()
		g.drop(why)
		g.toDecoy(w, r)
	}
	data, arrived, settle := p.data(w, r, target)
	defer settle()
	if !arrived || data == nil {
		refuse(dropBody)
		return
	}
	chunks, err := recv.OpenList(0, data)
	if err != nil {
		refuse(dropBody)
		return
	}
	var ds [][]byte
	for _, c := range chunks {
		ds = append(ds, c...)
	}
	// A session is never opened with user data.
	if len(ds) == 0 || !(isInitiation(ds[0]) || isKeepalive(ds[0])) {
		refuse(dropTunnel)
		return
	}
	sh.mu.Lock()
	sh.accepted, sh.highest = 1, 0
	sh.slots[0] = &shapeSlot{seq: 1, length: len(data), hash: sha256.Sum256(data), at: now}
	sh.mu.Unlock()
	s.active(now)
	for _, d := range ds {
		if !s.datagram(d, now) {
			refuse(dropTunnel)
			return
		}
	}
	select {
	case <-s.live:
	case <-s.done:
		g.toDecoy(w, r)
		return
	}
	g.polled.Add(1)
	wanted := accepting(r)
	if p.shape.Streamed() && wanted {
		p.live(w, r, s, 0)
		return
	}
	p.answer(w, s, 0)
}

// later is a request that names a session by its id and its number.
func (p *ShapeGateway) later(w http.ResponseWriter, r *http.Request, target stealth.ShapeTarget, now time.Time) {
	g := p.StreamGateway
	id, n := target.Who.ID, target.Who.N
	g.mu.Lock()
	s := g.shapes[id]
	g.mu.Unlock()
	if s == nil {
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	sh := s.shape
	// Nothing is proven yet: the session is only looked at. A request that
	// cannot be accepted and is no repeat is not read on.
	sh.mu.Lock()
	within := !s.isOver() && !sh.ended && n+stealth.ShapeWindow >= sh.highest && n <= sh.highest+stealth.ShapeWindow
	slot, tracked := sh.slots[n]
	hasPlace := (tracked && !slot.stream) || (!tracked && sh.flying < stealth.ShapeInflight)
	proceed := within && hasPlace && sh.readers < shapeReaders
	if proceed {
		sh.readers++
		if !tracked {
			sh.flying++
		}
	}
	sh.mu.Unlock()
	if !proceed {
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	reserved := !tracked
	flying := false
	if reserved {
		flying = true
	}
	give := func() {
		sh.mu.Lock()
		sh.readers--
		if flying {
			sh.flying--
			flying = false
		}
		sh.mu.Unlock()
	}
	data, arrived, settle := p.data(w, r, target)
	defer settle()
	if !arrived || data == nil {
		give()
		g.drop(dropBody)
		g.toDecoy(w, r)
		return
	}
	// 6 to 8, with the session held.
	sh.mu.Lock()
	if s.isOver() || sh.ended {
		sh.mu.Unlock()
		give()
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	slot, tracked = sh.slots[n]
	if tracked {
		// A stream request that was accepted takes the decoy whatever it
		// carries; otherwise a repeat is known by its length and hash.
		same := !slot.stream && slot.length == len(data) && slot.hash == sha256.Sum256(data)
		stored := slot.answer
		fresh := now.Sub(slot.at) <= shapeRepeatAge
		allowed := sh.replays.take(shapeReplays, now)
		sh.mu.Unlock()
		give()
		if slot.stream || !same || !fresh || !allowed {
			g.drop(dropBody)
			g.toDecoy(w, r)
			return
		}
		if stored != nil {
			g.repeated.Add(1)
			writeAnswer(w, stored)
			return
		}
		// The late answer: it is sealed afresh, the same bytes every time.
		late := sh.send.LateAnswer(n)
		g.repeated.Add(1)
		writeAnswer(w, late)
		return
	}
	sh.mu.Unlock()
	// A number that may be new opens chunk by chunk, outside the lock.
	chunks, err := sh.recv.OpenList(n, data)
	if err != nil {
		give()
		if errors.Is(err, stealth.ErrBodyEnded) {
			sh.mu.Lock()
			sh.ended = true
			sh.mu.Unlock()
			s.finish()
		}
		g.drop(dropBody)
		g.toDecoy(w, r)
		return
	}
	stream := sh.shape.Streamed() && accepting(r)
	sh.mu.Lock()
	switch {
	case s.isOver() || sh.ended || n+stealth.ShapeWindow < sh.highest || n > sh.highest+stealth.ShapeWindow:
		sh.mu.Unlock()
		give()
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	case stream && !sh.starts.take(shapeStarts, now):
		sh.mu.Unlock()
		give()
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	sh.accepted++
	if n > sh.highest {
		sh.highest = n
	}
	sh.slots[n] = &shapeSlot{seq: sh.accepted, stream: stream, length: len(data), hash: sha256.Sum256(data), at: now}
	sh.forget()
	// The request is the session's own from here on: the reader place is
	// given back, but the in-flight place stays until the answer is sent.
	sh.readers--
	flying = false
	sh.mu.Unlock()
	sh.last.Store(now.UnixNano())
	s.active(now)
	g.polled.Add(1)

	left := false
	for _, c := range chunks {
		for _, d := range c {
			if isNoStream(d) {
				left = true
				continue
			}
			if !s.datagram(d, now) {
				sh.unfly()
				s.finish()
				g.toDecoy(w, r)
				return
			}
		}
	}
	if left {
		sh.endStream()
	}
	if stream {
		p.live(w, r, s, n)
		return
	}
	p.answer(w, s, n)
}

// isNoStream says a chunk item is the no-stream marker: the client has left
// its stream and the gateway is to drop it.
func isNoStream(d []byte) bool {
	m := stealth.ShapeNoStream
	if len(d) != len(m) {
		return false
	}
	for i := range m {
		if d[i] != m[i] {
			return false
		}
	}
	return true
}

// accepting says the request asked for a stream by its Accept field.
func accepting(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		if v == "text/event-stream" {
			return true
		}
	}
	return false
}

// answer answers an accepted request n with what waits for the device, as
// soon as there is something or after the hold, and keeps the answer for a
// repeat before it is written. A session with a live stream answers every
// request but the stream's with one padding-only chunk at once.
func (p *ShapeGateway) answer(w http.ResponseWriter, s *stream, n uint32) {
	sh := s.shape
	defer sh.unfly()

	sh.mu.Lock()
	streaming := sh.kill != nil
	sh.mu.Unlock()

	var batch [][]byte
	size := 0
	if !streaming {
		// Wait for the first datagram, or the hold.
		hold := time.NewTimer(shapeHold)
		select {
		case d := <-s.out:
			if d != nil {
				batch, size = append(batch, d), 2+len(d)
			}
		case <-hold.C:
		case <-s.done:
		}
		hold.Stop()
	drain:
		for size < stealth.ShapeDataMax {
			select {
			case d := <-s.out:
				if d != nil {
					batch, size = append(batch, d), size+2+len(d)
				}
			default:
				break drain
			}
		}
	}

	body := p.sealList(sh, n, batch)
	response := make([]byte, len(body))
	copy(response, body)
	sh.store(n, response)
	writeAnswer(w, response)
}

// sealList seals a poll answer: batch's datagrams in chunks at request n,
// then a padding-only chunk if nothing went in, so a body is never empty.
func (p *ShapeGateway) sealList(sh *shapeSession, n uint32, batch [][]byte) []byte {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	var body []byte
	index := 0
	for len(batch) > 0 && index < stealth.ShapeChunks {
		take := max(stealth.StreamFits(batch), 1)
		wide := anyHandshake(batch[:take])
		next, err := sh.send.AppendChunk(body, n, index, wide, stealth.ShapeDataMax, batch[:take]...)
		if errors.Is(err, stealth.ErrBodyFull) {
			break
		}
		if err != nil {
			break
		}
		body = next
		batch = batch[take:]
		index++
	}
	if len(body) == 0 {
		if next, err := sh.send.AppendChunk(nil, n, 0, false, stealth.ShapeDataMax); err == nil {
			body = next
		}
	}
	return body
}

func anyHandshake(ds [][]byte) bool {
	for _, d := range ds {
		if stealth.IsHandshake(d) {
			return true
		}
	}
	return false
}

// live answers a stream request: the head, then one event per chunk of what
// the gateway has for the device, a heartbeat after a silence, until the
// stream is replaced, runs out of life or events, or the session is over.
func (p *ShapeGateway) live(w http.ResponseWriter, r *http.Request, s *stream, n uint32) {
	g := p.StreamGateway
	sh := s.shape
	flusher, ok := w.(http.Flusher)
	if !ok {
		sh.unfly()
		g.toDecoy(w, r)
		return
	}
	kill := make(chan struct{})
	sh.mu.Lock()
	sh.streams++
	id := sh.streams
	older := sh.kill
	sh.streamID, sh.kill = id, kill
	sh.mu.Unlock()
	if older != nil {
		close(older)
	}
	// In flight is a request whose answer is not yet sent; a stream request
	// is answered by its head.
	sh.unfly()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	defer func() {
		sh.mu.Lock()
		if sh.streamID == id {
			sh.kill = nil
		}
		sh.mu.Unlock()
	}()

	events := 0
	start := time.Now()
	ctx := r.Context()
	beatTimer := time.NewTimer(sseGap())
	defer beatTimer.Stop()
	resetBeat := func() {
		if !beatTimer.Stop() {
			select {
			case <-beatTimer.C:
			default:
			}
		}
		beatTimer.Reset(sseGap())
	}
	for {
		if events >= stealth.SseEvents || time.Since(start) >= stealth.SseLifeMax || s.isOver() {
			return
		}
		var first []byte
		select {
		case <-kill:
			return
		case <-s.done:
			return
		case <-ctx.Done():
			return
		case first = <-s.out:
		case <-beatTimer.C:
			if !writeRaw(w, flusher, stealth.SseBeat()) {
				return
			}
			beatTimer.Reset(sseGap())
			continue
		}
		// One event: the datagrams that wait now, up to one chunk's worth.
		batch := [][]byte{}
		if first != nil {
			batch = append(batch, first)
		}
	drain:
		for {
			select {
			case nx := <-s.out:
				if nx == nil {
					continue
				}
				if stealth.StreamFits(append(batch, nx)) <= len(batch) {
					// It does not fit this event; seal what we have, then it
					// leads the next. Put it back at the front by handling it
					// as the next first via a small carry.
					if !p.event(w, flusher, s, n, events, batch) {
						return
					}
					events++
					resetBeat()
					batch = [][]byte{nx}
					continue
				}
				batch = append(batch, nx)
			default:
				break drain
			}
		}
		if len(batch) == 0 {
			continue
		}
		if !p.event(w, flusher, s, n, events, batch) {
			return
		}
		events++
		resetBeat()
	}
}

// event seals one event of the live stream at (n, index) and writes it.
func (p *ShapeGateway) event(w http.ResponseWriter, f http.Flusher, s *stream, n uint32, index int, batch [][]byte) bool {
	sh := s.shape
	sh.mu.Lock()
	sealed, err := sh.send.SealEvent(nil, n, index, anyHandshake(batch), batch...)
	sh.mu.Unlock()
	if err != nil {
		return false
	}
	if !writeRaw(w, f, stealth.SseEvent(sealed)) {
		return false
	}
	s.g.stats.sent.Add(1)
	return true
}

func writeRaw(w http.ResponseWriter, f http.Flusher, b []byte) bool {
	if _, err := w.Write(b); err != nil {
		return false
	}
	f.Flush()
	return true
}

// sseGap is the span a heartbeat waits: SseBeatLo to SseBeatHi ms.
func sseGap() time.Duration {
	return time.Duration(stealth.SseBeatLo)*time.Millisecond + jitter(time.Duration(stealth.SseBeatHi-stealth.SseBeatLo)*time.Millisecond)
}

// --- shapeSession bookkeeping, under its own lock ---

// unfly gives back a request's in-flight place once its answer is sent.
func (sh *shapeSession) unfly() {
	sh.mu.Lock()
	if sh.flying > 0 {
		sh.flying--
	}
	sh.mu.Unlock()
}

// forget drops the numbers below the window and the answers older than the
// last shapeStored accepted. Called with the lock held.
func (sh *shapeSession) forget() {
	for k, slot := range sh.slots {
		if k+stealth.ShapeWindow < sh.highest {
			delete(sh.slots, k)
			continue
		}
		if slot.seq+shapeStored <= sh.accepted {
			slot.answer, slot.size = nil, 0
		}
	}
}

// store keeps the answer of request n for a repeat, if it is among the last
// shapeStored accepted and the kept answers fit shapeStoredBytes together.
func (sh *shapeSession) store(n uint32, answer []byte) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	slot, ok := sh.slots[n]
	if !ok || slot.seq+shapeStored <= sh.accepted || len(answer) > shapeStoredBytes {
		return
	}
	kept := 0
	type aged struct {
		seq uint64
		key uint32
	}
	var older []aged
	for k, sl := range sh.slots {
		if sl.answer != nil {
			kept += sl.size
			if k != n {
				older = append(older, aged{sl.seq, k})
			}
		}
	}
	// Drop the oldest answers until the new one fits.
	for kept+len(answer) > shapeStoredBytes && len(older) > 0 {
		oldest := 0
		for i := range older {
			if older[i].seq < older[oldest].seq {
				oldest = i
			}
		}
		if sl := sh.slots[older[oldest].key]; sl != nil {
			kept -= sl.size
			sl.answer, sl.size = nil, 0
		}
		older = append(older[:oldest], older[oldest+1:]...)
	}
	if kept+len(answer) <= shapeStoredBytes {
		slot.answer, slot.size = answer, len(answer)
	}
}

// endStream ends the live stream, if any, because the client has left it.
func (sh *shapeSession) endStream() {
	sh.mu.Lock()
	kill := sh.kill
	sh.kill = nil
	sh.mu.Unlock()
	if kill != nil {
		close(kill)
	}
}

// newShapeSession is a session for a proof that was taken: a socket to the
// tunnel, registered with the gateway under its id. The counting of the
// device's place was done by reserve; this only registers the session.
func (g *StreamGateway) newShapeSession(dev *streamDevice, visitor netip.Addr, keys stealth.StreamKeys, now time.Time, sh *shapeSession) (*stream, bool) {
	wg, err := net.DialUDP("udp", nil, g.wg)
	if err != nil {
		g.release(dev)
		g.drop(dropUnsendable)
		return nil, false
	}
	s := &stream{
		g: g, dev: dev, visitor: visitor, wg: wg, shape: sh,
		out: make(chan []byte, streamQueue), ctl: make(chan []byte, 4),
		opened: now, handshakes: newBucket(sessionHandshakes, now), first: true,
		live: make(chan struct{}), done: make(chan struct{}), readDone: make(chan struct{}),
	}
	s.active(now)
	g.mu.Lock()
	ok := !dev.gone
	if ok {
		_, taken := g.shapes[sh.id]
		ok = !taken
	}
	if ok {
		g.streams[s] = struct{}{}
		g.behind[wg.LocalAddr().String()] = visitor
		g.shapes[sh.id] = s
	}
	g.mu.Unlock()
	if !ok {
		g.release(dev)
		wg.Close()
		return nil, false
	}
	return s, true
}
