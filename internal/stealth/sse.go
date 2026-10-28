package stealth

import (
	"encoding/base64"
	"strconv"
	"time"
)

// The wui-shape stream.
//
// When a shape's down.mode is sse, the gateway answers one request -- a
// stream request -- with a never-ending HTTP response of Content-Type
// text/event-stream, and all the downstream data rides it: one Server-Sent
// Event per chunk. Every other request of the session is answered at once
// with a single padding-only chunk. The response body is chunked transfer
// coding, and inside it:
//
//	data: <standard base64 of one sealed chunk>\n\n     an event
//	: <8 to 40 of a-z0-9>\n\n                            a heartbeat
//
// An event's number is the stream request's n*ShapePerRequest plus the
// event's place in the stream, which the reader counts; the wire does not say
// it. A stream ends after ShapeChunks events, so index ShapeLateIndex stays
// the late answer's. The reader is incremental and checks every limit before
// it grows a buffer, so a hostile front cannot make it hold more than one
// line and one event. A fault is final: a reader that failed gives the same
// fault for every later byte.

// Stream limits.
const (
	// SseLifeMax is how long the gateway lets a stream run before it ends
	// it itself.
	SseLifeMax = 3600 * time.Second
	// SseEvents is the most events one stream holds.
	SseEvents = ShapeChunks
	// sseEventMin and sseEventMax bound the base64 of an event: a sealed
	// chunk of Overhead to MaxSealed bytes.
	sseEventMin = 24
	sseEventMax = 21356
	// sseLineMax is the longest line before its ending: "data: " and the
	// longest event.
	sseLineMax = 21362
	// sseSizeDigits is the most digits of a chunk-size line.
	sseSizeDigits = 16
	// sseChunkMax is the most bytes of one transfer-coding chunk.
	sseChunkMax = 65536
	// SseBeatLo and SseBeatHi bound the silence, in ms, before a heartbeat.
	SseBeatLo, SseBeatHi = 10000, 25000
	// sseBeatTextLo and sseBeatTextHi bound a heartbeat's text.
	sseBeatTextLo, sseBeatTextHi = 8, 40
)

// SseHead is the answer's head: 200 with these fields and no other of the
// gateway's own.
var SseHead = []byte("HTTP/1.1 200 OK\r\n" +
	"Content-Type: text/event-stream\r\n" +
	"Cache-Control: no-cache\r\n" +
	"X-Accel-Buffering: no\r\n" +
	"Transfer-Encoding: chunked\r\n\r\n")

// SseEnd is the last transfer-coding chunk, with no trailer.
var SseEnd = []byte("0\r\n\r\n")

const beatChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// SseEvent is the text of one event: "data: ", the sealed chunk in standard
// base64 with padding, and an empty line.
func SseEvent(sealed []byte) []byte {
	b := make([]byte, 0, 6+base64.StdEncoding.EncodedLen(len(sealed))+2)
	b = append(b, "data: "...)
	b = append(b, base64.StdEncoding.EncodeToString(sealed)...)
	return append(b, '\n', '\n')
}

// SseBeat is the text of one heartbeat: a comment line of a colon, a space
// and 8 to 40 characters from a-z0-9, and an empty line. It is not sealed and
// holds nothing.
func SseBeat() []byte {
	n := uniform(sseBeatTextLo, sseBeatTextHi)
	b := make([]byte, 0, 2+n+2)
	b = append(b, ':', ' ')
	for i := 0; i < n; i++ {
		b = append(b, beatChars[uniform(0, len(beatChars)-1)])
	}
	return append(b, '\n', '\n')
}

// SseChunked appends data as one transfer-coding chunk: its size in
// hexadecimal, a line end, the data and a line end.
func SseChunked(out, data []byte) []byte {
	out = append(out, strconv.FormatInt(int64(len(data)), 16)...)
	out = append(out, '\r', '\n')
	out = append(out, data...)
	return append(out, '\r', '\n')
}

// SseFault is why a stream ended as one that did not work.
type SseFault int

const (
	// FaultChunked is a transfer coding the page does not allow: a size line
	// too long, with an extension or a non-digit, a chunk over sseChunkMax, a
	// trailer, or a byte behind the last chunk.
	FaultChunked SseFault = iota
	// FaultLine is a line over its limit.
	FaultLine
	// FaultEvent is a line that is no event and no comment, an event that does
	// not decode to a sealed chunk, a second data line in one event, or a
	// comment inside an event.
	FaultEvent
	// FaultCount is more than SseEvents events.
	FaultCount
)

func (f SseFault) Error() string {
	switch f {
	case FaultChunked:
		return "stealth: stream: bad chunked coding"
	case FaultLine:
		return "stealth: stream: line too long"
	case FaultEvent:
		return "stealth: stream: bad event"
	default:
		return "stealth: stream: too many events"
	}
}

type coding int

const (
	cSize coding = iota
	cSizeEnd
	cData
	cDataEnd
	cLast
	cDone
)

// SseReader reads the body of a stream answer: the transfer coding, then
// lines, then events. It keeps less than one line and no more than one event.
// It is not safe for concurrent use.
type SseReader struct {
	coding   coding
	size     int
	digits   int
	crSeen   bool // within cDataEnd/cLast/cSizeEnd: the CR was read
	left     int  // bytes left of the current data chunk
	line     []byte
	pending  []byte // the sealed chunk of a data line, until its empty line
	havePend bool
	beating  bool
	events   int
	failed   error
}

// NewSseReader starts a reader.
func NewSseReader() *SseReader { return &SseReader{coding: cSize} }

// Events is how many events have been read.
func (r *SseReader) Events() int { return r.events }

// Feed takes the next bytes of the body as they came and appends the sealed
// chunk of every event they complete to out, in order. done is true once the
// last chunk has been read: the stream is over, and any byte behind it is a
// fault. The result does not depend on how the bytes are cut. A fault is
// final.
func (r *SseReader) Feed(bytes []byte, out *[][]byte) (done bool, err error) {
	if r.failed != nil {
		return false, r.failed
	}
	done, err = r.read(bytes, out)
	if err != nil {
		r.failed = err
	}
	return done, err
}

func (r *SseReader) read(b []byte, out *[][]byte) (bool, error) {
	at := 0
	for {
		switch r.coding {
		case cDone:
			if at < len(b) {
				return false, FaultChunked
			}
			return true, nil
		case cData:
			if r.left == 0 {
				r.coding, r.crSeen = cDataEnd, false
				continue
			}
			take := r.left
			if n := len(b) - at; n < take {
				take = n
			}
			if take == 0 {
				return false, nil
			}
			if err := r.lines(b[at:at+take], out); err != nil {
				return false, err
			}
			at += take
			r.left -= take
		default:
			if at >= len(b) {
				return false, nil
			}
			c := b[at]
			at++
			if err := r.step(c); err != nil {
				return false, err
			}
		}
	}
}

// step takes one byte of the transfer-coding frame (everything but chunk
// data).
func (r *SseReader) step(c byte) error {
	switch r.coding {
	case cSize:
		switch {
		case (c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') && r.digits < sseSizeDigits:
			v := r.size*16 + hexVal(c)
			if v > sseChunkMax {
				return FaultChunked
			}
			r.size, r.digits = v, r.digits+1
		case c == '\r' && r.digits > 0:
			r.coding = cSizeEnd
		default:
			return FaultChunked
		}
	case cSizeEnd:
		if c != '\n' {
			return FaultChunked
		}
		if r.size == 0 {
			r.coding, r.crSeen = cLast, false
		} else {
			r.coding, r.left = cData, r.size
		}
	case cDataEnd:
		switch {
		case !r.crSeen && c == '\r':
			r.crSeen = true
		case r.crSeen && c == '\n':
			r.coding, r.size, r.digits = cSize, 0, 0
		default:
			return FaultChunked
		}
	case cLast:
		switch {
		case !r.crSeen && c == '\r':
			r.crSeen = true
		case r.crSeen && c == '\n':
			r.coding = cDone
		default:
			return FaultChunked
		}
	}
	return nil
}

// lines takes the data of a chunk as lines; a line is never joined with the
// next across more than its limit.
func (r *SseReader) lines(data []byte, out *[][]byte) error {
	for _, c := range data {
		switch {
		case c == '\n':
			line := r.line
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			if err := r.complete(line, out); err != nil {
				return err
			}
			r.line = r.line[:0]
		case len(r.line) > sseLineMax || (len(r.line) == sseLineMax && c != '\r'):
			return FaultLine
		default:
			r.line = append(r.line, c)
		}
	}
	return nil
}

// complete takes one whole line (its CRLF stripped).
func (r *SseReader) complete(line []byte, out *[][]byte) error {
	empty := len(line) == 0
	switch {
	case empty && r.havePend:
		if r.events >= SseEvents {
			return FaultCount
		}
		r.events++
		*out = append(*out, r.pending)
		r.pending, r.havePend = nil, false
	case empty && r.beating:
		r.beating = false
	case empty:
		return FaultEvent
	case r.havePend || r.beating:
		// Data within an event, or a line within a comment: neither is a
		// shape the page writes.
		return FaultEvent
	case len(line) >= 6 && string(line[:6]) == "data: ":
		sealed, err := sseSealed(line[6:])
		if err != nil {
			return err
		}
		r.pending, r.havePend = sealed, true
	case line[0] == ':':
		r.beating = true
	default:
		return FaultEvent
	}
	return nil
}

// sseSealed is the sealed chunk an event line holds: canonical standard
// base64 with padding of sseEventMin to sseEventMax characters, decoding to
// Overhead to MaxSealed bytes. The text length is checked before decoding and
// the chunk length after.
func sseSealed(text []byte) ([]byte, error) {
	if len(text) < sseEventMin || len(text) > sseEventMax || len(text)%4 != 0 {
		return nil, FaultEvent
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(string(text))
	if err != nil || len(raw) < Overhead || len(raw) > MaxSealed {
		return nil, FaultEvent
	}
	return raw, nil
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
