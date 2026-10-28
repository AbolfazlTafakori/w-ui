package stealthgw

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-poll gateway.
//
// wui-poll is wui-ws for a CDN that carries no WebSocket: behind the same
// front, the tunnel's datagrams travel in the bodies of POST requests to the
// transport's path and in the bodies of their answers (package stealth). The
// first request is the proof, and the session it opens is a row the later
// requests name by its id. One request of a session is in flight at a time:
// the gateway holds it until it has something for the device, or for
// pollHold, and answers 200 with a body that is never empty.
//
// A request is decided by its head before its body is read, and its body is
// opened whole before anything in it is used:
//
//  1. Not a POST of the transport's path from a trusted front, with exactly
//     one Content-Length from stealth.PollBodyMin to stealth.PollBodyMax
//     and no Transfer-Encoding: the decoy's.
//  2. A query that is a session's id and a number: the session must be
//     this gateway's, have no request in flight, and the number must be
//     the one after the last taken, or that one again. Otherwise the
//     decoy's, and the session is not touched.
//  3. Any other query is a proof, judged as wui-ws judges one; a proof
//     that is taken opens a pending session, whatever comes next.
//  4. The body must arrive within pollBodyFor.
//  5. A repeat of the last request taken, with the same body to the byte,
//     gets the answer the gateway kept, since a front may send a request
//     twice. A repeat with another body is the decoy's.
//  6. Every chunk of the body must open at the session's next numbers;
//     otherwise the decoy's, and nothing about the session changes, so
//     whoever learns an id cannot disturb the session it names. A body
//     that opens and holds a chunk that does not parse is the device's own
//     fault, and ends the session.
//  7. The opening request's first datagram is a handshake initiation of the
//     session's device or a keepalive, as on wui-ws, and its answer waits
//     until the tunnel has spoken, since nothing is sent on a pending
//     session. A session that never is established ends, and the request
//     goes to the decoy.
//
// Whatever goes to the decoy goes as it came, its body included. The answer
// the gateway keeps is the last one, stored before it is written, so a
// request whose answer never reached the front is answered again.

// PollConfig is one tunnel's wui-poll gateway: wui-ws's settings.
type PollConfig struct {
	StreamConfig
}

// pollBodyFor is how long a body may take to arrive. A test shortens it.
var pollBodyFor = 10 * time.Second

// wui-poll's limits and timers.
const (
	// pollHold is how long a request is held for something to answer it
	// with.
	pollHold = 250 * time.Millisecond
	// pollIdle ends a session no request was taken on for this long: a
	// client polls at least every few seconds.
	pollIdle = 60 * time.Second
	// devicePolls is how many wui-poll sessions one device may hold at once.
	devicePolls = 2
	// pollReaders is how many bodies under one session's id are read at
	// once while nothing is proven of them: whoever learns an id holds no
	// more than this many connections with it.
	pollReaders = 4
)

// PollGateway is wui-poll in front of one WireGuard tunnel. Its devices,
// limits, replay rule and sessions are wui-ws's; only how a session's
// datagrams travel differs.
type PollGateway struct {
	*StreamGateway
}

// NewPoll prepares a gateway. Nothing listens until Serve.
func NewPoll(cfg PollConfig) (*PollGateway, error) {
	decoy, err := newDecoy(cfg.Origin)
	if err != nil {
		return nil, err
	}
	g, err := newStreamGateway(cfg.StreamConfig, stealth.Poll)
	if err != nil {
		return nil, err
	}
	g.decoy, g.perDevice = decoy, devicePolls
	return &PollGateway{StreamGateway: g}, nil
}

// Serve runs the gateway until ctx ends.
func (p *PollGateway) Serve(ctx context.Context) error {
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

// pollSession is what a wui-poll session has besides the stream's.
type pollSession struct {
	id   [stealth.PollIDLen]byte
	last atomic.Int64 // when the last request was taken, Unix nanoseconds

	mu sync.Mutex
	// recv opens the request bodies. Only a request that holds mu opens
	// one, and the datagrams it gives are copied out before mu is let go.
	recv *stealth.PollReceiver
	// busy says a request is in flight, from the moment its body was taken
	// to the moment its answer is stored.
	busy bool
	// number is the last request taken, 0 for the opening one; sum and
	// size are its body's, to know a repeat by, and answer is what it got.
	// A repeat of the opening request is a spent proof, so it has none.
	number  uint32
	sum     [sha256.Size]byte
	size    int
	repeat  bool
	answer  []byte
	readers int
	// held are datagrams the last answer had no room for: the next one's
	// first. Only the request in flight touches them.
	held [][]byte
}

// framed says a request's head is one a device's request could have: a
// POST with one length in a body's range, and no other framing.
func framed(r *http.Request) bool {
	return r.Method == http.MethodPost && len(r.TransferEncoding) == 0 &&
		len(r.Header.Values("Content-Length")) == 1 &&
		r.ContentLength >= stealth.PollBodyMin && r.ContentLength <= stealth.PollBodyMax
}

// ServeHTTP takes one request: a device's is taken or answered, and anything
// else goes to the decoy as it came.
func (p *PollGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g := p.StreamGateway
	now := time.Now()
	peer := addrOf(r.RemoteAddr)
	path := sha256.Sum256([]byte(r.URL.Path))
	if !g.trusted(peer) || subtle.ConstantTimeCompare(path[:], g.path[:]) != 1 || !framed(r) {
		g.toDecoy(w, r)
		return
	}
	visitor := g.visitor(r, peer)
	if id, n, ok := stealth.ParsePollQuery(r.URL.RawQuery); ok {
		p.later(w, r, id, n, now)
		return
	}
	dev, keys, ok := g.prove(r.URL.RawQuery, visitor, now)
	if !ok {
		g.toDecoy(w, r)
		return
	}
	p.opening(w, r, dev, visitor, keys, now)
}

// opening is the request a proof was taken from: it opens the session, and
// is answered once the tunnel has spoken on it.
func (p *PollGateway) opening(w http.ResponseWriter, r *http.Request, dev *streamDevice, visitor netip.Addr, keys stealth.StreamKeys, now time.Time) {
	g := p.StreamGateway
	recv, err := stealth.NewPollReceiver(keys.Up)
	if err != nil {
		g.release(dev)
		g.toDecoy(w, r)
		return
	}
	ps := &pollSession{id: keys.ID, recv: recv, busy: true}
	ps.last.Store(now.UnixNano())
	s, ok := g.newSession(dev, visitor, keys, now, ps)
	if !ok {
		g.toDecoy(w, r)
		return
	}
	go s.fromTunnel()
	body, arrived, settle := readBody(w, r)
	defer settle()
	refuse := func(why int) {
		s.finish()
		g.drop(why)
		g.toDecoy(w, r)
	}
	if !arrived {
		refuse(dropBody)
		return
	}
	pb, err := recv.Open(body)
	if err != nil {
		refuse(dropBody)
		return
	}
	ds := pb.Datagrams()
	// A session is never opened with user data.
	if len(ds) == 0 || !(isInitiation(ds[0]) || isKeepalive(ds[0])) {
		refuse(dropTunnel)
		return
	}
	pb.Commit()
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
		// Never established: the device's tunnel did not know it, or the
		// device went. Whatever it was is nobody's now.
		g.toDecoy(w, r)
		return
	}
	g.polled.Add(1)
	p.answer(w, s)
}

// later is a request that names a session by its id and its number.
func (p *PollGateway) later(w http.ResponseWriter, r *http.Request, id [stealth.PollIDLen]byte, n uint32, now time.Time) {
	g := p.StreamGateway
	g.mu.Lock()
	s := g.polls[id]
	g.mu.Unlock()
	if s == nil {
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	ps := s.poll
	// Nothing is proven of the request yet, so the session is only looked
	// at: a request that cannot be the next or the last is not read on.
	ps.mu.Lock()
	ordered := !s.isOver() && !ps.busy && (n == ps.number+1 || (n == ps.number && ps.repeat)) && ps.readers < pollReaders
	if ordered {
		ps.readers++
	}
	ps.mu.Unlock()
	if !ordered {
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	body, arrived, settle := readBody(w, r)
	defer settle()
	ps.mu.Lock()
	ps.readers--
	if !arrived {
		ps.mu.Unlock()
		g.drop(dropBody)
		g.toDecoy(w, r)
		return
	}
	switch {
	case s.isOver() || ps.busy:
		ps.mu.Unlock()
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	case n == ps.number && ps.repeat:
		// Known by its bytes: a body that was taken does not open twice.
		if len(body) != ps.size || sha256.Sum256(body) != ps.sum {
			ps.mu.Unlock()
			g.drop(dropBody)
			g.toDecoy(w, r)
			return
		}
		answer := ps.answer
		ps.mu.Unlock()
		g.repeated.Add(1)
		writeAnswer(w, answer)
		return
	case n != ps.number+1:
		ps.mu.Unlock()
		g.drop(dropUnordered)
		g.toDecoy(w, r)
		return
	}
	pb, err := ps.recv.Open(body)
	if err != nil {
		ps.mu.Unlock()
		if errors.Is(err, stealth.ErrBodyEnded) {
			s.finish()
		}
		g.drop(dropBody)
		g.toDecoy(w, r)
		return
	}
	pb.Commit()
	var ds [][]byte
	for _, d := range pb.Datagrams() {
		ds = append(ds, bytes.Clone(d))
	}
	ps.number, ps.sum, ps.size, ps.repeat, ps.answer, ps.busy = n, sha256.Sum256(body), len(body), true, nil, true
	ps.mu.Unlock()
	// A request in order under the session's key is the client's and
	// nobody else's.
	ps.last.Store(now.UnixNano())
	s.active(now)
	g.polled.Add(1)
	for _, d := range ds {
		if !s.datagram(d, now) {
			s.finish()
			ps.mu.Lock()
			ps.busy = false
			ps.mu.Unlock()
			g.toDecoy(w, r)
			return
		}
	}
	p.answer(w, s)
}

// answer answers the request in flight with what waits for the device, as
// soon as there is something or when the hold is over, and keeps the answer
// for a repeat before it is written.
func (p *PollGateway) answer(w http.ResponseWriter, s *stream) {
	ps := s.poll
	batch, size := ps.held, 0
	ps.held = nil
	for _, d := range batch {
		size += 2 + len(d)
	}
	if len(batch) == 0 {
		hold := time.NewTimer(pollHold)
		select {
		case d := <-s.out:
			if d != nil { // padding alone is what an answer has anyway
				batch, size = append(batch, d), 2+len(d)
			}
		case <-hold.C:
		case <-s.done:
		}
		hold.Stop()
	}
	// Everything that waits now, up to what one body takes, and no wait
	// for more.
drain:
	for size < stealth.PollBodyMax {
		select {
		case d := <-s.out:
			if d != nil {
				batch, size = append(batch, d), size+2+len(d)
			}
		default:
			break drain
		}
	}
	var (
		body []byte
		err  error
	)
	for len(batch) > 0 {
		n := max(stealth.StreamFits(batch), 1)
		if body, err = s.send.AppendBody(body, batch[:n]...); err != nil {
			break
		}
		batch = batch[n:]
		s.sent.Add(1)
		s.g.stats.sent.Add(1)
	}
	if errors.Is(err, stealth.ErrBodyFull) {
		ps.held = batch
	} else if err != nil {
		s.g.drop(dropUnsendable)
	}
	if len(body) == 0 {
		if body, err = s.send.AppendBody(nil); err != nil {
			// The stream's numbers are spent: the session is over.
			s.finish()
		}
		s.sent.Add(1)
		s.g.stats.sent.Add(1)
	}
	ps.mu.Lock()
	ps.answer, ps.busy = body, false
	ps.mu.Unlock()
	writeAnswer(w, body)
}

// writeAnswer is a request's answer: 200, the body, and nothing that would
// tell one answer from another but the length. The front adds what it adds
// to every answer.
func writeAnswer(w http.ResponseWriter, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// readBody reads a request's body, which must arrive whole within
// pollBodyFor. Either way the request's Body is left reading the whole of
// it from the start, for the decoy: when it was late, the decoy waits for
// the rest of it as an origin would. settle is called before the handler
// returns: a body still being read then is cut, since nothing may read a
// request's body after its handler.
func readBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool, settle func()) {
	l := &lateBody{buf: make([]byte, r.ContentLength), done: make(chan struct{}), src: r.Body}
	// The read is of the body as it came, never of r.Body, which the decoy's
	// is put in place of.
	go func() {
		l.n, l.err = io.ReadFull(l.src, l.buf)
		close(l.done)
	}()
	t := time.NewTimer(pollBodyFor)
	defer t.Stop()
	select {
	case <-l.done:
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(l.buf[:l.n]), errReader{l.err}))
		return l.buf[:l.n], l.err == nil, func() {}
	case <-t.C:
		r.Body = l
		return nil, false, func() {
			select {
			case <-l.done:
			default:
				http.NewResponseController(w).SetReadDeadline(time.Now())
				<-l.done
			}
		}
	}
}

// lateBody is a body that did not arrive in time: it gives what arrives,
// once the read that waits for it is over.
type lateBody struct {
	buf  []byte
	n    int
	err  error
	at   int
	done chan struct{}
	src  io.ReadCloser
}

func (l *lateBody) Read(p []byte) (int, error) {
	<-l.done
	if l.at < l.n {
		n := copy(p, l.buf[l.at:l.n])
		l.at += n
		return n, nil
	}
	if l.err != nil {
		return 0, l.err
	}
	return 0, io.EOF
}

func (l *lateBody) Close() error { return l.src.Close() }

// errReader ends a body with the error its read ended with, or none.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	return 0, io.EOF
}
