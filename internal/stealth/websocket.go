package stealth

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"strings"
)

// The WebSocket pieces.
//
// drift speaks as little of WebSocket (RFC 6455) as a session needs, and
// refuses the rest: one sealed chunk is one binary message in a single frame.
// A text message, a fragmented one, one longer than a sealed chunk can be, a
// frame with a reserved bit, or a frame masked the wrong way for its side
// ends the session. Pings are answered, pongs ignored, and a close is the
// end. No extension is offered or taken, so nothing is compressed.

// WebSocket opcodes.
const (
	OpBinary = 0x2
	OpClose  = 0x8
	OpPing   = 0x9
	OpPong   = 0xA
)

// maxControl is the longest a control frame's payload may be.
const maxControl = 125

// CloseNormal is the only close a stream transport sends: 1000 and no
// reason, whatever the cause, so the cause cannot be read from outside.
var CloseNormal = []byte{0x03, 0xE8}

// ErrFrame is a frame a session does not take.
var ErrFrame = errors.New("stealth: frame not taken")

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocketAccept is the Sec-WebSocket-Accept for a Sec-WebSocket-Key.
func WebSocketAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// NewWebSocketKey draws a client's Sec-WebSocket-Key.
func NewWebSocketKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

// WebSocketKey is the key of a request that asks for a WebSocket the way
// RFC 6455 says, or false: a GET with Upgrade: websocket, Connection: upgrade,
// version 13 and a key of sixteen bytes.
func WebSocketKey(r *http.Request) (string, bool) {
	if r.Method != http.MethodGet || !headerHas(r.Header, "Upgrade", "websocket") ||
		!headerHas(r.Header, "Connection", "upgrade") || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return "", false
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if b, err := base64.StdEncoding.DecodeString(key); err != nil || len(b) != 16 {
		return "", false
	}
	return key, true
}

// WebSocketHeadKey is WebSocketKey for a GET whose header fields are head,
// as they came after the request line up to and with the empty line: what a
// server that reads requests itself has. Every other field is ignored.
func WebSocketHeadKey(head []byte) (string, bool) {
	h, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(head))).ReadMIMEHeader()
	if err != nil {
		return "", false
	}
	return WebSocketKey(&http.Request{Method: http.MethodGet, Header: http.Header(h)})
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// AppendFrame appends one final frame to dst. A client masks what it sends
// and a server does not, so mask is true on the client side.
func AppendFrame(dst []byte, op byte, payload []byte, mask bool) []byte {
	dst = append(dst, 0x80|op)
	var bit byte
	if mask {
		bit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		dst = append(dst, bit|byte(n))
	case n <= 0xFFFF:
		dst = append(dst, bit|126)
		dst = binary.BigEndian.AppendUint16(dst, uint16(n))
	default:
		dst = append(dst, bit|127)
		dst = binary.BigEndian.AppendUint64(dst, uint64(n))
	}
	if !mask {
		return append(dst, payload...)
	}
	var key [4]byte
	if _, err := rand.Read(key[:]); err != nil {
		panic(err)
	}
	dst = append(dst, key[:]...)
	start := len(dst)
	dst = append(dst, payload...)
	for i := range payload {
		dst[start+i] ^= key[i&3]
	}
	return dst
}

// ReadFrame reads one frame into buf, which must hold MaxSealed bytes, and
// gives its opcode and payload, unmasked; the payload aliases buf. masked says
// the peer is a client, whose frames must be masked; a server's must not be.
// Anything a session does not take is ErrFrame.
func ReadFrame(r io.Reader, buf []byte, masked bool) (op byte, payload []byte, err error) {
	var head [14]byte
	if _, err := io.ReadFull(r, head[:2]); err != nil {
		return 0, nil, err
	}
	fin, rsv, op := head[0]&0x80 != 0, head[0]&0x70, head[0]&0x0F
	isMasked, n := head[1]&0x80 != 0, uint64(head[1]&0x7F)
	if !fin || rsv != 0 || isMasked != masked {
		return 0, nil, ErrFrame
	}
	switch op {
	case OpBinary:
	case OpClose, OpPing, OpPong:
		if n > maxControl {
			return 0, nil, ErrFrame
		}
	default:
		return 0, nil, ErrFrame // text, continuation, or reserved
	}
	switch n {
	case 126:
		if _, err := io.ReadFull(r, head[2:4]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(head[2:4]))
	case 127:
		if _, err := io.ReadFull(r, head[2:10]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(head[2:10])
	}
	if n > MaxSealed || n > uint64(len(buf)) {
		return 0, nil, ErrFrame
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(r, key[:]); err != nil {
			return 0, nil, err
		}
	}
	payload = buf[:n]
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= key[i&3]
		}
	}
	return op, payload, nil
}
