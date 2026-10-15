package pptpd

import (
	"encoding/binary"
	"errors"
)

// PPTP's GRE is the enhanced header of RFC 2637, 4.1: version 1, the key
// field split into the payload's length and the receiving side's call id,
// and a sequence number and an acknowledgement, each present when its flag
// is set.
const (
	greFlagKey  = 0x20 // first byte: K, always set
	greFlagSeq  = 0x10 // first byte: S
	greFlagAck  = 0x80 // second byte: A
	greVersion  = 0x01 // second byte: version 1
	greProtoPPP = 0x880B
)

var errNotPPTPGRE = errors.New("not a PPTP GRE packet")

// grePacket is one GRE packet of a call.
type grePacket struct {
	callID  uint16
	seq     uint32
	hasSeq  bool
	ack     uint32
	hasAck  bool
	payload []byte // a PPP frame, address and control fields as the sender left them
}

// parseGRE reads a packet as it arrives on the raw socket, IP header removed.
func parseGRE(b []byte) (grePacket, error) {
	var p grePacket
	if len(b) < 8 {
		return p, errNotPPTPGRE
	}
	f0, f1 := b[0], b[1]
	// C, R, s and the recursion bits clear, K set, version 1: anything else
	// is GRE of some other use.
	if f0&^greFlagSeq != greFlagKey || f1&0x7f != greVersion || binary.BigEndian.Uint16(b[2:4]) != greProtoPPP {
		return p, errNotPPTPGRE
	}
	n := int(binary.BigEndian.Uint16(b[4:6]))
	p.callID = binary.BigEndian.Uint16(b[6:8])
	off := 8
	if f0&greFlagSeq != 0 {
		if len(b) < off+4 {
			return p, errNotPPTPGRE
		}
		p.seq, p.hasSeq = binary.BigEndian.Uint32(b[off:]), true
		off += 4
	}
	if f1&greFlagAck != 0 {
		if len(b) < off+4 {
			return p, errNotPPTPGRE
		}
		p.ack, p.hasAck = binary.BigEndian.Uint32(b[off:]), true
		off += 4
	}
	if len(b)-off < n {
		return p, errNotPPTPGRE
	}
	p.payload = b[off : off+n]
	return p, nil
}

// appendGRE writes a packet: a sequence number when it carries a frame, an
// acknowledgement when there is one to give.
func appendGRE(dst []byte, p grePacket) []byte {
	f0, f1 := byte(greFlagKey), byte(greVersion)
	if p.hasSeq {
		f0 |= greFlagSeq
	}
	if p.hasAck {
		f1 |= greFlagAck
	}
	dst = append(dst, f0, f1)
	dst = binary.BigEndian.AppendUint16(dst, greProtoPPP)
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(p.payload)))
	dst = binary.BigEndian.AppendUint16(dst, p.callID)
	if p.hasSeq {
		dst = binary.BigEndian.AppendUint32(dst, p.seq)
	}
	if p.hasAck {
		dst = binary.BigEndian.AppendUint32(dst, p.ack)
	}
	return append(dst, p.payload...)
}

// seqAfter says whether a comes after b, the sequence numbers wrapping.
func seqAfter(a, b uint32) bool { return int32(a-b) > 0 }
