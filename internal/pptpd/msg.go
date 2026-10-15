// Package pptpd is the panel's own PPTP server: the control connection on
// TCP 1723 and the GRE that carries each call, with one pppd per call doing
// everything PPP -- authentication against chap-secrets, MPPE, the device's
// address -- exactly as it did under the pptpd package.
//
// It exists because the distributions stopped shipping pptpd: Ubuntu 24.04
// and later have no package for it, and a PPTP tunnel there could not start.
// pppd is still everywhere, so only the part pptpd did -- the control
// protocol of RFC 2637 and moving frames between GRE and pppd -- is done
// here, and nothing a device sees changes.
package pptpd

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The control connection's message types (RFC 2637, 2.0).
const (
	msgStartReq      = 1  // Start-Control-Connection-Request
	msgStartReply    = 2  // Start-Control-Connection-Reply
	msgStopReq       = 3  // Stop-Control-Connection-Request
	msgStopReply     = 4  // Stop-Control-Connection-Reply
	msgEchoReq       = 5  // Echo-Request
	msgEchoReply     = 6  // Echo-Reply
	msgOutCallReq    = 7  // Outgoing-Call-Request
	msgOutCallReply  = 8  // Outgoing-Call-Reply
	msgInCallReq     = 9  // Incoming-Call-Request
	msgInCallReply   = 10 // Incoming-Call-Reply
	msgInCallConn    = 11 // Incoming-Call-Connected
	msgCallClear     = 12 // Call-Clear-Request
	msgCallDisc      = 13 // Call-Disconnect-Notify
	msgWANError      = 14 // WAN-Error-Notify
	msgSetLinkInfo   = 15 // Set-Link-Info
	magicCookie      = 0x1A2B3C4D
	controlMessage   = 1
	protocolVersion  = 0x0100
	headerLen        = 12
	maxMessageLength = 256 // the longest message is 220 bytes
)

// The length of each message, header included; a message of another length
// is not one this server reads.
var messageLen = map[uint16]int{
	msgStartReq: 156, msgStartReply: 156,
	msgStopReq: 16, msgStopReply: 16,
	msgEchoReq: 16, msgEchoReply: 20,
	msgOutCallReq: 168, msgOutCallReply: 32,
	msgInCallReq: 220, msgInCallReply: 24, msgInCallConn: 28,
	msgCallClear: 16, msgCallDisc: 148,
	msgWANError: 40, msgSetLinkInfo: 24,
}

var errBadMessage = errors.New("not a PPTP control message")

// message is one control message: its type and its body, the bytes after
// the 12-byte header.
type message struct {
	typ  uint16
	body []byte
}

// parseHeader checks a header and says how long the whole message is.
func parseHeader(h []byte) (typ uint16, length int, err error) {
	if len(h) < headerLen {
		return 0, 0, errBadMessage
	}
	length = int(binary.BigEndian.Uint16(h[0:2]))
	if binary.BigEndian.Uint16(h[2:4]) != controlMessage || binary.BigEndian.Uint32(h[4:8]) != magicCookie {
		return 0, 0, errBadMessage
	}
	typ = binary.BigEndian.Uint16(h[8:10])
	want, known := messageLen[typ]
	// A message of a known type and the wrong length, or of any type and a
	// length no message has, ends the connection; one of an unknown type
	// and a sane length is skipped, as the RFC asks.
	if length < headerLen || length > maxMessageLength || (known && length != want) {
		return 0, 0, fmt.Errorf("%w: type %d, length %d", errBadMessage, typ, length)
	}
	return typ, length, nil
}

// encode writes a message whole: the header, then the body padded with
// zeros to the message's length.
func encode(typ uint16, body []byte) []byte {
	n := messageLen[typ]
	b := make([]byte, n)
	binary.BigEndian.PutUint16(b[0:2], uint16(n))
	binary.BigEndian.PutUint16(b[2:4], controlMessage)
	binary.BigEndian.PutUint32(b[4:8], magicCookie)
	binary.BigEndian.PutUint16(b[8:10], typ)
	copy(b[headerLen:], body)
	return b
}

// Result codes this server sends.
const (
	resultOK          = 1
	resultGeneralErr  = 2
	callResultOK      = 1
	callResultErr     = 2 // General Error
	callResultRefused = 4 // Not-Accepting
	stopReasonNone    = 1 // General request to clear the control connection
	cdnAdminShutdown  = 3 // Call-Disconnect-Notify: Request (by the admin)
	cdnLostCarrier    = 1
)

// startReply answers a Start-Control-Connection-Request.
func startReply(result uint8, hostname string) []byte {
	body := make([]byte, 144)
	binary.BigEndian.PutUint16(body[0:2], protocolVersion)
	body[2] = result
	body[3] = 0                                // error code
	binary.BigEndian.PutUint32(body[4:8], 3)   // framing: asynchronous and synchronous
	binary.BigEndian.PutUint32(body[8:12], 3)  // bearer: analog and digital
	binary.BigEndian.PutUint16(body[12:14], 0) // maximum channels: no limit given
	binary.BigEndian.PutUint16(body[14:16], 1) // firmware revision
	copy(body[16:80], hostname)
	copy(body[80:144], "W-UI")
	return encode(msgStartReply, body)
}

// stopRequest asks the device to close the control connection.
func stopRequest() []byte {
	return encode(msgStopReq, []byte{stopReasonNone})
}

// stopReply answers a Stop-Control-Connection-Request.
func stopReply() []byte {
	return encode(msgStopReply, []byte{resultOK})
}

// echoRequest is the server's keepalive, with an identifier the reply
// carries back.
func echoRequest(id uint32) []byte {
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, id)
	return encode(msgEchoReq, body)
}

// echoReply answers a device's Echo-Request with its identifier.
func echoReply(id []byte) []byte {
	body := make([]byte, 8)
	copy(body[0:4], id)
	body[4] = resultOK
	return encode(msgEchoReply, body)
}

// outCall is what the server reads from an Outgoing-Call-Request.
type outCall struct {
	peerCallID uint16 // the id the device's GRE packets for this call carry ours as, and ours carry its
	serial     uint16
	window     uint16
}

func parseOutCall(body []byte) outCall {
	return outCall{
		peerCallID: binary.BigEndian.Uint16(body[0:2]),
		serial:     binary.BigEndian.Uint16(body[2:4]),
		window:     binary.BigEndian.Uint16(body[20:22]),
	}
}

// outCallReply answers an Outgoing-Call-Request: the call's id on this side,
// the device's, and the result.
func outCallReply(callID, peerCallID uint16, result uint8, window uint16) []byte {
	body := make([]byte, 20)
	binary.BigEndian.PutUint16(body[0:2], callID)
	binary.BigEndian.PutUint16(body[2:4], peerCallID)
	body[4] = result
	body[5] = 0                                         // error code
	binary.BigEndian.PutUint16(body[6:8], 0)            // cause code
	binary.BigEndian.PutUint32(body[8:12], 100_000_000) // connect speed
	binary.BigEndian.PutUint16(body[12:14], window)     // our receive window
	binary.BigEndian.PutUint16(body[14:16], 0)          // processing delay
	binary.BigEndian.PutUint32(body[16:20], 0)          // physical channel
	return encode(msgOutCallReply, body)
}

// callDisconnect tells the device a call has ended, by the call's id on
// this side.
func callDisconnect(callID uint16, result uint8) []byte {
	body := make([]byte, 136)
	binary.BigEndian.PutUint16(body[0:2], callID)
	body[2] = result
	return encode(msgCallDisc, body)
}
