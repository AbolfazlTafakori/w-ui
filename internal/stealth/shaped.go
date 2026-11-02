package stealth

import (
	"crypto/cipher"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"
)

// The mold requests and answers.
//
// mold carries relay's chunks, but where relay numbers a stream's
// chunks straight across its bodies, mold ties each chunk's number to
// the request it rides in: the chunk at index i of request n has number
//
//	n*ShapePerRequest + i        i = 0 .. ShapeChunks-1
//
// so a request that is replayed cannot smuggle a chunk in under a number the
// gateway has already moved past, and a request number may repeat (a front's
// retry) without the numbers of its chunks shifting. Index ShapeLateIndex is
// kept for the "late answer" alone and is used for nothing else.
//
// A request has one canonical form. Its data -- a list of framed chunks,
// exactly as a relay body is framed -- is base64url without padding when
// it rides in a query, a path or a header, and the raw bytes when it rides in
// a body. In a path the text is cut into segments joined by '/'. The gateway
// reads a request strictly and decodes its data only when it is needed, and a
// spelling that has a twin is not read at all, so a copy of a request cannot
// look new and a new request cannot look like a copy.
//
// Nothing here keeps state between requests: a session's window of request
// numbers, its stored answers and its counters are the gateway's. The format
// is in shape.go, shaped.go and sse.go; the spec is docs/reference/stealth.md.

// Numbering.
const (
	// ShapePerRequest is how far apart two requests' chunk numbers lie.
	ShapePerRequest = 4096
	// ShapeChunks is the most data chunks one request or list may hold,
	// numbered 0 to ShapeChunks-1.
	ShapeChunks = 4095
	// ShapeLateIndex is the index kept for the late answer.
	ShapeLateIndex = 4095
	// ShapeRequestMax is the last request number. A client opens a new
	// session before it would pass it.
	ShapeRequestMax = 1<<32 - 1
	// ShapeLatePlain is the plaintext of a late answer: 64 zero bytes.
	ShapeLatePlain = 64
	// ShapeLateSize is a sealed late answer: its plaintext and the seal.
	ShapeLateSize = ShapeLatePlain + Overhead
	// ShapeWindow is how far on either side of the highest accepted number a
	// gateway tracks a request number.
	ShapeWindow = 64
	// ShapeInflight is the most requests of a session read at once, beside a
	// stream.
	ShapeInflight = 4
	// ShapeDataMax is the most bytes of framed data one request carries.
	ShapeDataMax = PollBodyMax
	// ShapeDataMin is the least: relay's shortest body.
	ShapeDataMin = PollBodyMin

	segMin, segMax = 8, 64
	base64Group    = 4
)

// ShapeNoStream is the one 32-byte chunk item that tells the gateway a client
// has left its stream and the gateway is to drop it. Its first byte is no
// WireGuard message type, so it is never a datagram, yet it is authenticated
// like any other item of the chunk it rides in.
var ShapeNoStream = func() [32]byte {
	var item [32]byte
	item[0] = 0xfe
	return item
}()

// ShapeNumber is the number of the chunk at index of request n, for a data
// chunk: ok is false from ShapeLateIndex, which is the late answer's alone, so
// no caller seals a data chunk at that nonce by a slip.
func ShapeNumber(n uint32, index int) (uint64, bool) {
	if index < 0 || index >= ShapeLateIndex {
		return 0, false
	}
	return uint64(n)*ShapePerRequest + uint64(index), true
}

// ShapeLateNumber is the number of the late answer of request n.
func ShapeLateNumber(n uint32) uint64 {
	return uint64(n)*ShapePerRequest + ShapeLateIndex
}

// sextet is the value of one base64url character, or 0xff.
func sextet(c byte) byte {
	switch {
	case c >= 'A' && c <= 'Z':
		return c - 'A'
	case c >= 'a' && c <= 'z':
		return c - 'a' + 26
	case c >= '0' && c <= '9':
		return c - '0' + 52
	case c == '-':
		return 62
	case c == '_':
		return 63
	}
	return 0xff
}

// decodedLen is how many bytes n base64url characters decode to, or -1 for a
// length no base64 text has. Computed from the length alone, before anything
// is allocated.
func decodedLen(n int) int {
	switch n % base64Group {
	case 1:
		return -1
	case 0:
		return n / base64Group * 3
	case 2:
		return n/base64Group*3 + 1
	default:
		return n/base64Group*3 + 2
	}
}

// canonical says n characters ending in last (-1 for none, else a sextet
// value) are the one canonical base64url text of some bytes: a length base64
// has, and the unused bits of the last character zero.
func canonical(n int, last int) bool {
	switch n % base64Group {
	case 0:
		return true
	case 2:
		return last >= 0 && last&0x0f == 0
	case 3:
		return last >= 0 && last&0x03 == 0
	}
	return false
}

// allSextets says every byte of b is a base64url character.
func allSextets(b []byte) bool {
	for _, c := range b {
		if sextet(c) == 0xff {
			return false
		}
	}
	return true
}

// shapeEncode is a list of framed chunks as base64url without padding, the
// one canonical form.
func shapeEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// shapeDecode is the bytes of a canonical base64url text without padding, or
// nil for what is not one: a character outside the alphabet, a length no
// base64 has, last bits that are set, or more than ShapeDataMax bytes. The
// decoded length is checked from the encoded length before anything is
// allocated.
func shapeDecode(text []byte) []byte {
	size := decodedLen(len(text))
	if size < 0 || size > ShapeDataMax || !allSextets(text) {
		return nil
	}
	last := -1
	if len(text) > 0 {
		last = int(sextet(text[len(text)-1]))
	}
	if !canonical(len(text), last) {
		return nil
	}
	out, err := base64.RawURLEncoding.DecodeString(string(text))
	if err != nil {
		return nil
	}
	return out
}

// ShapeSegments cuts text into segments of segMin to segMax characters joined
// by '/', the last 1 to segMax, so a text of segMax characters or fewer is
// one segment. The draws come from the client's padding source of randomness.
func ShapeSegments(text string) string {
	out := make([]byte, 0, len(text)+len(text)/segMin+1)
	rest := text
	for len(rest) > segMax {
		cut := uniform(segMin, segMax)
		out = append(out, rest[:cut]...)
		out = append(out, '/')
		rest = rest[cut:]
	}
	out = append(out, rest...)
	return string(out)
}

// ShapeCapacity is the most bytes of data one request of this shape carries,
// for a transport path of pathLen bytes and a who (the token, or "<id>.<n>")
// of whoLen bytes: what is left of the limit after the rest of the target, as
// base64 and, for a path, with a slash after every 8 characters at the worst.
// Never above ShapeDataMax.
func ShapeCapacity(s Shape, pathLen, whoLen int) int {
	min3 := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}
	switch s.Place {
	case InBody:
		return min3(s.Limit, ShapeDataMax)
	case InHeader:
		return min3(decodedLen(s.Limit), ShapeDataMax)
	}
	target := s.Limit - (pathLen + 1 + whoLen + 1)
	if target < 0 {
		target = 0
	}
	chars := target
	if s.Place == InPath {
		chars = target * 8 / 9
	}
	return min3(decodedLen(chars), ShapeDataMax)
}

// ShapeParts is a request as the client writes it, before its common header
// fields: the request target, the value of the header field up.name (for
// InHeader), and the body (for InBody).
type ShapeParts struct {
	Target string
	Header string // empty unless the shape rides in a header
	Body   []byte
}

// BuildParts writes the request of data (a list of framed chunks) in this
// shape. path is the transport's, who is the token or "<id>.<n>". The caller
// has held data to ShapeCapacity; a request that is over its limit all the
// same is ok=false. A path shape cuts the text into segments at random.
func BuildParts(s Shape, path, who string, data []byte) (ShapeParts, bool) {
	text := shapeEncode(data)
	var p ShapeParts
	switch s.Place {
	case InBody:
		p = ShapeParts{Target: path + "?" + who, Body: data}
	case InQuery:
		p = ShapeParts{Target: path + "?" + who + "." + text}
	case InPath:
		p = ShapeParts{Target: path + "/" + who + "/" + ShapeSegments(text)}
	case InHeader:
		p = ShapeParts{Target: path + "?" + who, Header: text}
	}
	var over bool
	switch s.Place {
	case InBody:
		over = len(p.Body) > s.Limit || len(p.Body) < ShapeDataMin
	case InHeader:
		over = len(p.Header) > s.Limit
	default:
		over = len(p.Target) > s.Limit
	}
	if over || len(data) > ShapeDataMax {
		return ShapeParts{}, false
	}
	return p, true
}

// ShapeWho says who a request is from.
type ShapeWho struct {
	// Opening holds the proof token when the request is a session's first.
	Opening []byte
	// ID and N name the session and the request number of a later request.
	ID [PollIDLen]byte
	N  uint32
	// IsOpening tells the two apart.
	IsOpening bool
}

// whoKind reads "<id>.<n>" or "<token>" at the front of text, and returns
// what follows it. more says data may follow a "." behind it (a query shape).
func whoKind(text []byte, more bool) (ShapeWho, []byte, bool) {
	first := indexByte(text, '.')
	if first < 0 {
		first = len(text)
	}
	if first == pollIDText && len(text) > first {
		// A later request: "<id>.<n>", with optional ".<data>" behind it.
		digitsEnd := indexByte(text[first+1:], '.')
		var who string
		if digitsEnd < 0 {
			who = string(text)
		} else {
			who = string(text[:first+1+digitsEnd])
		}
		id, n, ok := ParsePollQuery(who)
		if !ok {
			return ShapeWho{}, nil, false
		}
		w := ShapeWho{ID: id, N: n}
		rest := text[len(who):]
		switch {
		case len(rest) == 0:
			return w, nil, true
		case more && len(rest) > 1 && rest[0] == '.':
			return w, rest[1:], true
		}
		return ShapeWho{}, nil, false
	}
	// A token: base64url in its one form, so no '%', '&', '=' or '+' stands
	// in a target that is read.
	if first == 0 || !allSextets(text[:first]) {
		return ShapeWho{}, nil, false
	}
	w := ShapeWho{Opening: text[:first], IsOpening: true}
	switch {
	case first == len(text):
		return w, nil, true
	case more && first+1 < len(text):
		return w, text[first+1:], true
	}
	return ShapeWho{}, nil, false
}

// dataOK says text is data: base64url characters, in a length and with a last
// character that make one canonical text, of ShapeDataMin to ShapeDataMax
// bytes once decoded.
func dataOK(text []byte) bool {
	if !allSextets(text) {
		return false
	}
	last := -1
	if len(text) > 0 {
		last = int(sextet(text[len(text)-1]))
	}
	if !canonical(len(text), last) {
		return false
	}
	size := decodedLen(len(text))
	return size >= ShapeDataMin && size <= ShapeDataMax
}

// ShapeTarget is a request target (and header value) in the one form its
// shape allows. Whether the id names a session, and whether a token is a
// proof, are the gateway's lookups.
type ShapeTarget struct {
	Who ShapeWho
	// data is the base64url text (query, header) or the slashed segments
	// (path); nil for a body shape, whose data is the body itself.
	data   []byte
	path   bool // data is slashed segments
	bodied bool
}

// ReadTarget reads the target of a request line of a transport with this
// shape and path, and the value of its header field when the shape has one
// (header is nil otherwise). It is ok=false when the request is not one of
// the shape in its one form. Nothing is decoded and nothing is allocated.
func ReadTarget(s Shape, path []byte, line RequestLine, header []byte) (ShapeTarget, bool) {
	samePath := func(p []byte) bool {
		return len(p) == len(path) && subtle.ConstantTimeCompare(p, path) == 1
	}
	switch s.Place {
	case InBody, InHeader:
		if line.Query == nil {
			return ShapeTarget{}, false
		}
		who, _, ok := whoKind(line.Query, false)
		if !ok || !samePath(line.Path) {
			return ShapeTarget{}, false
		}
		if s.Place == InBody {
			if header != nil {
				return ShapeTarget{}, false
			}
			return ShapeTarget{Who: who, bodied: true}, true
		}
		if len(header) == 0 || len(header) > s.Limit || !dataOK(header) {
			return ShapeTarget{}, false
		}
		return ShapeTarget{Who: who, data: header}, true
	case InQuery:
		if line.Query == nil || header != nil {
			return ShapeTarget{}, false
		}
		who, data, ok := whoKind(line.Query, true)
		if !ok || data == nil || !samePath(line.Path) {
			return ShapeTarget{}, false
		}
		if len(line.Path)+1+len(line.Query) > s.Limit || !dataOK(data) {
			return ShapeTarget{}, false
		}
		return ShapeTarget{Who: who, data: data}, true
	default: // InPath
		if line.Query != nil || header != nil || len(line.Path) <= len(path) {
			return ShapeTarget{}, false
		}
		if subtle.ConstantTimeCompare(line.Path[:len(path)], path) != 1 || line.Path[len(path)] != '/' {
			return ShapeTarget{}, false
		}
		rest := line.Path[len(path)+1:]
		slash := indexByte(rest, '/')
		if slash < 0 {
			return ShapeTarget{}, false
		}
		who, _, ok := whoKind(rest[:slash], false)
		if !ok {
			return ShapeTarget{}, false
		}
		segments := rest[slash+1:]
		chars, last := 0, -1
		for _, seg := range splitByte(segments, '/') {
			if len(seg) == 0 || len(seg) > segMax || !allSextets(seg) {
				return ShapeTarget{}, false
			}
			chars += len(seg)
			last = int(sextet(seg[len(seg)-1]))
		}
		if len(line.Path) > s.Limit || !canonical(chars, last) {
			return ShapeTarget{}, false
		}
		size := decodedLen(chars)
		if size < ShapeDataMin || size > ShapeDataMax {
			return ShapeTarget{}, false
		}
		return ShapeTarget{Who: who, data: segments, path: true}, true
	}
}

// Bodied says the data is in the body of the request, not the target.
func (t ShapeTarget) Bodied() bool { return t.bodied }

// Data is the framed bytes of a request whose data is in its target or
// header, which the repeat test runs on; nil for a body shape.
func (t ShapeTarget) Data() []byte {
	switch {
	case t.bodied:
		return nil
	case t.path:
		joined := make([]byte, 0, len(t.data))
		for _, c := range t.data {
			if c != '/' {
				joined = append(joined, c)
			}
		}
		return shapeDecode(joined)
	default:
		return shapeDecode(t.data)
	}
}

// ShapeSender seals one direction of a mold session, sealing each chunk
// at the number of its request and index, and keeping the padding of its
// direction. It is not safe for concurrent use.
type ShapeSender struct {
	aead  cipher.AEAD
	pad   Padder
	plain []byte
}

// NewShapeSender starts a direction with its key and its padder.
func NewShapeSender(key []byte, pad Padder) (*ShapeSender, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, errors.New("stealth: stream key: " + err.Error())
	}
	return &ShapeSender{aead: a, pad: pad, plain: make([]byte, 0, MaxPlain)}, nil
}

// seal writes one chunk of datagrams sealed at number into dst and returns
// it. wide forces wide padding. room is how much plaintext the padding may
// grow into.
func (s *ShapeSender) seal(dst []byte, number uint64, index int, wide bool, room int, datagrams [][]byte) ([]byte, error) {
	if index < 0 || index >= ShapeChunks {
		return dst, ErrBodyFull
	}
	plain := s.plain[:0]
	handshake := wide
	for _, d := range datagrams {
		var err error
		if plain, err = AppendItem(plain, d); err != nil {
			return dst, err
		}
		handshake = handshake || IsHandshake(d)
	}
	plain = AppendPadding(plain, s.pad.DrawWithin(handshake, len(plain), room))
	s.plain = plain
	out := s.aead.Seal(dst, streamNonce(number), plain, nil)
	return out, nil
}

// AppendChunk appends one chunk of a list to body: its length, then the chunk
// sealed at index of request n. wide says it holds a handshake or a control
// item. limit is how long the body may grow in all. ErrBodyFull is a chunk
// that might not fit; nothing is appended and nothing is counted then.
func (s *ShapeSender) AppendChunk(body []byte, n uint32, index int, wide bool, limit int, datagrams ...[]byte) ([]byte, error) {
	number, ok := ShapeNumber(n, index)
	if !ok {
		return body, ErrBodyFull
	}
	used := 0
	for _, d := range datagrams {
		used += 2 + len(d)
	}
	room := limit - (len(body) + 2 + Overhead)
	if room > MaxPlain {
		room = MaxPlain
	}
	if limit > PollBodyMax {
		if m := PollBodyMax - (len(body) + 2 + Overhead); m < room {
			room = m
		}
	}
	if room < used {
		return body, ErrBodyFull
	}
	start := len(body)
	body = append(body, 0, 0)
	var err error
	if body, err = s.seal(body, number, index, wide, room, datagrams); err != nil {
		return body[:start], err
	}
	binary.BigEndian.PutUint16(body[start:], uint16(len(body)-start-2))
	return body, nil
}

// SealEvent appends one chunk without a length frame, as a stream event holds
// it: the chunk sealed at index of request n, in out. wide is as AppendChunk.
func (s *ShapeSender) SealEvent(out []byte, n uint32, index int, wide bool, datagrams ...[]byte) ([]byte, error) {
	number, ok := ShapeNumber(n, index)
	if !ok {
		return out, ErrBodyFull
	}
	return s.seal(out, number, index, wide, MaxPlain, datagrams)
}

// LateAnswer is the late answer of request n, byte for byte: 64 zero bytes
// sealed at ShapeLateIndex of n. It seals the same bytes every time, so any
// number of them for one n are one, and it is not counted among the chunks.
func (s *ShapeSender) LateAnswer(n uint32) []byte {
	var plain [ShapeLatePlain]byte
	return s.aead.Seal(nil, streamNonce(ShapeLateNumber(n)), plain[:], nil)
}

// IsLate says body is the late answer of request n: ShapeLateSize bytes that
// open at ShapeLateIndex of n to all zeroes. Any other chunk at that index is
// refused.
func (s *ShapeReceiver) IsLate(n uint32, body []byte) bool {
	if len(body) != ShapeLateSize {
		return false
	}
	plain, err := s.aead.Open(nil, streamNonce(ShapeLateNumber(n)), body, nil)
	if err != nil || len(plain) != ShapeLatePlain {
		return false
	}
	for _, c := range plain {
		if c != 0 {
			return false
		}
	}
	return true
}

// ShapeReceiver opens one direction of a mold session: request lists
// (chunks at n*ShapePerRequest), stream events (at an explicit number) and
// late answers. Unlike a stream's Opener it keeps no running counter -- the
// number is the request's -- so a list that does not open leaves it as it
// was. It is not safe for concurrent use.
type ShapeReceiver struct {
	aead  cipher.AEAD
	plain []byte
	ended bool
}

// NewShapeReceiver starts reading a direction with its key.
func NewShapeReceiver(key []byte) (*ShapeReceiver, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, errors.New("stealth: stream key: " + err.Error())
	}
	return &ShapeReceiver{aead: a, plain: make([]byte, 0, MaxPlain)}, nil
}

// Ended says a list whose every chunk opened held one that did not parse: the
// session is over, and every later list is refused.
func (r *ShapeReceiver) Ended() bool { return r.ended }

// ShapeFramed is how many chunks a list holds, or ok=false when the list is
// not framed: a frame length outside Overhead to MaxSealed, a frame that runs
// past the data, more than ShapeChunks chunks, or no chunk at all. This is the
// check that runs before any chunk is opened.
func ShapeFramed(data []byte) (int, bool) {
	at, count := 0, 0
	for at < len(data) {
		if at+2 > len(data) {
			return 0, false
		}
		size := int(binary.BigEndian.Uint16(data[at:]))
		at += 2 + size
		count++
		if size < Overhead || size > MaxSealed || at > len(data) || count > ShapeChunks {
			return 0, false
		}
	}
	if count == 0 {
		return 0, false
	}
	return count, true
}

// OpenList opens every chunk of a request's data at the numbers of request n:
// chunk i at n*ShapePerRequest+i. A list that does not open whole changes
// nothing (ErrBodyRefused). A list whose every chunk opened and one did not
// parse is the peer's own, and ends the session (ErrBodyEnded). The returned
// chunks lie in the receiver's own buffer, good until the next OpenList.
func (r *ShapeReceiver) OpenList(n uint32, data []byte) (chunks [][][]byte, err error) {
	if r.ended {
		return nil, ErrBodyEnded
	}
	count, ok := ShapeFramed(data)
	if !ok || len(data) < ShapeDataMin || len(data) > ShapeDataMax {
		return nil, ErrBodyRefused
	}
	type span struct{ at, end int }
	spans := make([]span, 0, count)
	plain := r.plain[:0]
	at, index := 0, 0
	for at < len(data) {
		size := int(binary.BigEndian.Uint16(data[at:]))
		number, ok := ShapeNumber(n, index)
		if !ok {
			return nil, ErrBodyRefused
		}
		start := len(plain)
		var e error
		if plain, e = r.aead.Open(plain, streamNonce(number), data[at+2:at+2+size], nil); e != nil {
			r.plain = plain[:0]
			return nil, ErrBodyRefused
		}
		spans = append(spans, span{start, len(plain)})
		at += 2 + size
		index++
	}
	r.plain = plain
	out := make([][][]byte, 0, len(spans))
	for _, s := range spans {
		var items [][]byte
		if e := Items(plain[s.at:s.end], func(d []byte) { items = append(items, d) }); e != nil {
			r.ended = true
			return nil, ErrBodyEnded
		}
		out = append(out, items)
	}
	return out, nil
}

// OpenEvent opens one sealed chunk at number, as a stream event is opened, and
// calls fn with each datagram. The number of an event is the stream request's
// n*ShapePerRequest plus the event's place in the stream, which the reader
// counts and the wire does not say. It does not end the session on a fault:
// an event that does not open is the stream's end, not a peer's bad chunk.
func (r *ShapeReceiver) OpenEvent(number uint64, sealed []byte, fn func(datagram []byte)) error {
	if len(sealed) < Overhead || len(sealed) > MaxSealed {
		return ErrChunk
	}
	plain, err := r.aead.Open(r.plain[:0], streamNonce(number), sealed, nil)
	if err != nil {
		return ErrChunk
	}
	r.plain = plain
	return Items(plain, fn)
}

// helpers kept here so the file stands on its own.

func indexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func splitByte(b []byte, c byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return append(out, b[start:])
}

// ShapeQuery is the query of a later request in a shape that puts its data
// elsewhere than the query, or the leading "<id>.<n>" of a query shape.
func ShapeQuery(id [PollIDLen]byte, n uint32) string {
	return PollQuery(id, n)
}
