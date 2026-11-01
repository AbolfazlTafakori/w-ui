package stealthgw

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The wui-faketls session carry.
//
// Once a hello has proved a device and the two ends hold the same application
// traffic keys (package stealth, the handshake flight), the session is a run
// of TLS 1.3 application records. Each record the gateway opens holds one or
// more session frames (`[len u16 | chunk]`), and each frame's chunk holds the
// WireGuard datagrams the device sent and their padding; each record the
// gateway seals carries the tunnel's datagrams back the same way, padded to a
// length drawn from the sizes the borrowed site's own records showed. The loop
// is wui-ws's -- a socket of its own to the tunnel, nothing sent before
// WireGuard has spoken, a pending session made live by the first authentic
// word -- over the record layer in place of WebSocket frames.

// wui-faketls record limits.
const (
	// fakeRecordInner is the most chunk plaintext one record carries, well
	// under a TLS record's 16 KiB so the content type, padding and tag fit.
	fakeRecordInner = 15 << 10
	// fakeHandshakeFor bounds the wait for the client's Finished after the
	// flight is sent.
	fakeHandshakeFor = 10 * time.Second
	// fakeRecordMax is the largest record body (header excluded) the reader
	// will take: a 16 KiB plaintext, its content type, padding and tag.
	fakeRecordMax = (16 << 10) + 256
)

// fakeStream is one wui-faketls session.
type fakeStream struct {
	g       *StreamGateway
	dev     *streamDevice
	visitor netip.Addr
	conn    net.Conn
	r       *bufio.Reader
	wg      *net.UDPConn
	conn13  *stealth.FakeConn // the record layer: Send seals, Recv opens
	sizer   *fakeSizer

	out chan []byte // datagrams for the client; nil is padding alone

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

func (s *fakeStream) isPending() bool      { return s.state.Load() == statePending }
func (s *fakeStream) isLive() bool         { return s.state.Load() == stateLive }
func (s *fakeStream) active(now time.Time) { s.lastActive.Store(now.UnixNano()) }
func (s *fakeStream) quietSince() time.Time {
	return time.Unix(0, s.lastActive.Load())
}

// fakeSizer draws a record's padding target from the lengths a site's own
// records showed, so the gateway's records carry the site's shape. With no
// sizes learned yet it pads nothing, and a datagram larger than any sampled
// size is never cut: padding only ever gives way.
type fakeSizer struct {
	mu    sync.Mutex
	sizes []int
	next  uint64
}

// learn keeps the plaintext lengths a site's records showed.
func (z *fakeSizer) learn(sizes []int) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.sizes = append([]int(nil)[:0:0], sizes...)
}

// pad says how many zero bytes to add after data bytes of chunk plaintext, so
// the record reaches a sampled size when it can; never below data, never over
// the record ceiling.
func (z *fakeSizer) pad(data int) int {
	z.mu.Lock()
	defer z.mu.Unlock()
	if len(z.sizes) == 0 {
		return 0
	}
	target := z.sizes[z.next%uint64(len(z.sizes))]
	z.next++
	if target <= data {
		return 0
	}
	if target > fakeRecordInner {
		target = fakeRecordInner
	}
	if target <= data {
		return 0
	}
	return target - data
}

// newFakeSession is a session for a hello that proved a device: its socket to
// the tunnel, registered with the gateway. When the device went meanwhile the
// place the proof kept is given back and there is none.
func (g *StreamGateway) newFakeSession(dev *streamDevice, visitor netip.Addr, sizer *fakeSizer, now time.Time) (*fakeStream, bool) {
	wg, err := net.DialUDP("udp", nil, g.wg)
	if err != nil {
		g.release(dev)
		g.drop(dropUnsendable)
		return nil, false
	}
	s := &fakeStream{
		g: g, dev: dev, visitor: visitor, wg: wg, sizer: sizer,
		out:    make(chan []byte, streamQueue),
		opened: now, handshakes: newBucket(sessionHandshakes, now), first: true,
		live: make(chan struct{}), done: make(chan struct{}), readDone: make(chan struct{}),
	}
	s.active(now)
	g.mu.Lock()
	ok := !dev.gone
	if ok {
		g.fakes[s] = struct{}{}
		g.behind[wg.LocalAddr().String()] = visitor
	}
	g.mu.Unlock()
	if !ok {
		g.release(dev)
		wg.Close()
		return nil, false
	}
	return s, true
}

// carry sends the server's flight, takes the client's Finished, and runs the
// session until it ends. The connection's read side is s.r, conn its write
// side; server is what built the flight and takes the client's Finished.
func (g *StreamGateway) carry(s *fakeStream, server *stealth.FakeServerSide) {
	s.conn.SetWriteDeadline(time.Now().Add(writeFor))
	if _, err := s.conn.Write(server.Flight); err != nil {
		s.finish()
		close(s.readDone)
		s.conn.Close()
		return
	}
	// The client's Finished, after an optional compatibility change_cipher_spec.
	s.conn.SetReadDeadline(time.Now().Add(fakeHandshakeFor))
	conn13, err := s.acceptFinished(server)
	if err != nil {
		s.finish()
		close(s.readDone)
		s.conn.Close()
		return
	}
	s.conn13 = conn13
	s.conn.SetReadDeadline(time.Time{})
	s.conn.SetWriteDeadline(time.Time{})
	go s.fromTunnel()
	go s.writer()
	s.reader()
}

// acceptFinished reads records until the client's Finished and opens it.
func (s *fakeStream) acceptFinished(server *stealth.FakeServerSide) (*stealth.FakeConn, error) {
	for {
		outer, record, err := readRecord(s.r)
		if err != nil {
			return nil, err
		}
		if outer == 20 { // change_cipher_spec, ignored
			continue
		}
		// The Finished is a protected record, so its outer type is application
		// data; AcceptClientFinished opens it and checks the handshake inside.
		return server.AcceptClientFinished(record)
	}
}

// readRecord reads one TLS record: its outer content type and the whole
// record (header and body), for the keys to open.
func readRecord(r *bufio.Reader) (outer byte, record []byte, err error) {
	var head [stealth.TLSHeader]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	length := int(binary.BigEndian.Uint16(head[3:5]))
	if length == 0 || length > fakeRecordMax {
		return 0, nil, errors.New("stealthgw: a record of an impossible length")
	}
	record = make([]byte, stealth.TLSHeader+length)
	copy(record, head[:])
	if _, err := io.ReadFull(r, record[stealth.TLSHeader:]); err != nil {
		return 0, nil, err
	}
	return head[0], record, nil
}

// reader opens the client's records and carries their frames' datagrams to the
// tunnel until the session ends.
func (s *fakeStream) reader() {
	defer close(s.readDone)
	defer s.finish()
	var carry []byte // the start of a frame split across records
	for {
		outer, record, err := readRecord(s.r)
		if err != nil {
			return
		}
		if outer == 20 { // a late change_cipher_spec
			continue
		}
		kind, content, ok := openRecord13(s.conn13.Recv, record)
		if !ok {
			s.g.drop(dropUnopened)
			return
		}
		switch kind {
		case 23: // application data: the session's frames
		case 21: // an alert: the client is closing
			return
		default:
			return
		}
		// A record opened in order under the session's key is the client's and
		// nobody else's, so every one is activity.
		now := time.Now()
		s.active(now)
		plain := content
		if carry != nil {
			plain = append(carry, content...)
			carry = nil
		}
		if !s.frames(plain, &carry, now) {
			return
		}
	}
}

// frames reads whole frames from a record's plaintext and carries each
// datagram to the tunnel. The tail of a frame split across records is kept in
// carry for the next record.
func (s *fakeStream) frames(plain []byte, carry *[]byte, now time.Time) bool {
	ok := true
	ending := false
	quiet := true
	any := false
	consumed, err := stealth.ReadFakeFrames(plain, func(d []byte) {
		if ending {
			return
		}
		any = true
		if s.first {
			s.first = false
			if !(isInitiation(d) || isKeepalive(d)) {
				s.g.drop(dropTunnel)
				ok, ending = false, true
				return
			}
		}
		if !s.datagram(d, now) {
			ok, ending = false, true
			return
		}
		quiet = quiet && isKeepalive(d)
	})
	if err != nil {
		s.g.drop(dropUnopened)
		return false
	}
	if !ok {
		return false
	}
	if any && quiet && s.isLive() {
		s.echo()
	}
	if consumed < len(plain) {
		*carry = append([]byte(nil), plain[consumed:]...)
	}
	return true
}

// datagram is one datagram from the client: it goes to the tunnel as the
// session's device's, or the session ends. It is wui-ws's rule, unchanged.
func (s *fakeStream) datagram(d []byte, now time.Time) bool {
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

// fromTunnel is what the tunnel sends the device: it makes a pending session
// live, and goes out on it.
func (s *fakeStream) fromTunnel() {
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

// writer seals what waits into records, as many datagrams to a record as wait
// and fit, padded to the site's shape, and closes the connection when the
// session ends.
func (s *fakeStream) writer() {
	defer s.closeConn()
	var (
		batch  [][]byte
		carry  []byte
		record = make([]byte, 0, fakeRecordInner+512)
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
					continue // padding is what a record has anyway
				}
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
		plain, pad, ok := fakeBuild(batch, s.sizer)
		if !ok {
			s.g.drop(dropUnsendable)
			s.finish()
			return
		}
		sealed, ok := s.conn13.Send.Seal(23, plain, pad, record[:0])
		if !ok {
			s.g.drop(dropUnsendable)
			s.finish()
			return
		}
		if !s.write(sealed) {
			return
		}
		s.sent.Add(1)
		s.g.stats.sent.Add(1)
	}
}

// fakeFits says whether d may join batch within one record's frame.
func fakeFits(batch [][]byte, d []byte) bool {
	used := 0
	for _, b := range batch {
		used += 2 + len(b)
	}
	return stealth.FakeFrameOverhead+used+2+len(d) <= fakeRecordInner
}

// fakeBuild makes a record's chunk plaintext from a batch of datagrams, in one
// frame, with the padding the sizer draws, and returns the pad for the seal.
func fakeBuild(batch [][]byte, sizer *fakeSizer) (plain []byte, pad int, ok bool) {
	frame, fok := stealth.AppendFakeFrame(nil, 0, batch...)
	if !fok {
		return nil, 0, false
	}
	pad = sizer.pad(len(frame))
	if len(frame)+pad > fakeRecordInner {
		pad = fakeRecordInner - len(frame)
	}
	return frame, pad, true
}

// openRecord13 opens one protected record (header and body) under keys.
func openRecord13(keys *stealth.TLSKeys, record []byte) (byte, []byte, bool) {
	if len(record) < stealth.TLSHeader {
		return 0, nil, false
	}
	header := record[:stealth.TLSHeader]
	body := append([]byte(nil), record[stealth.TLSHeader:]...)
	return keys.Open(header, body)
}

// establish makes a pending session live.
func (s *fakeStream) establish() bool {
	g := s.g
	g.mu.Lock()
	defer g.mu.Unlock()
	if !s.state.CompareAndSwap(statePending, stateLive) {
		return false
	}
	s.dev.pending--
	g.stats.established.Add(1)
	close(s.live)
	return true
}

// queue hands a datagram, or nil for padding alone, to the writer.
func (s *fakeStream) queue(d []byte) {
	select {
	case s.out <- d:
	default:
		s.g.drop(dropUnsendable)
	}
}

// echo answers a record that said nothing with a padding-only record after a
// short random wait, unless something else went out meanwhile.
func (s *fakeStream) echo() {
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

func (s *fakeStream) write(record []byte) bool {
	s.conn.SetWriteDeadline(time.Now().Add(writeFor))
	if _, err := s.conn.Write(record); err != nil {
		s.finish()
		return false
	}
	return true
}

// finish ends the session: once, whoever asks first.
func (s *fakeStream) finish() {
	s.once.Do(func() {
		g := s.g
		g.mu.Lock()
		prev := s.state.Swap(stateOver)
		if _, ok := g.fakes[s]; ok {
			delete(g.fakes, s)
			delete(g.behind, s.wg.LocalAddr().String())
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

// closeConn ends the connection: a TLS close_notify, a wait for the client's
// own close, and the connection let go.
func (s *fakeStream) closeConn() {
	s.finish()
	if sealed, ok := s.conn13.Send.Seal(21, []byte{1, 0}, 0, nil); ok { // warning close_notify
		s.conn.SetWriteDeadline(time.Now().Add(closeWait))
		s.conn.Write(sealed)
	}
	s.conn.SetReadDeadline(time.Now().Add(closeWait))
	select {
	case <-s.readDone:
	case <-time.After(closeWait):
	}
	s.conn.Close()
}
