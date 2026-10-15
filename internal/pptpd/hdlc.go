package pptpd

// pppd on a terminal speaks asynchronous HDLC (RFC 1662): each frame
// between 0x7E flags, the flag and the escape and every control character
// escaped, and a 16-bit check sequence at the end. PPTP carries the frame
// bare, so the server takes the framing off what pppd writes and puts it on
// what the device sends.

const (
	hdlcFlag   = 0x7E
	hdlcEscape = 0x7D
	hdlcXor    = 0x20
	fcsInit    = 0xFFFF
	fcsGood    = 0xF0B8
)

var fcsTable = func() (t [256]uint16) {
	for i := range t {
		v := uint16(i)
		for range 8 {
			if v&1 != 0 {
				v = v>>1 ^ 0x8408
			} else {
				v >>= 1
			}
		}
		t[i] = v
	}
	return
}()

func fcs16(fcs uint16, b []byte) uint16 {
	for _, c := range b {
		fcs = fcs>>8 ^ fcsTable[byte(fcs)^c]
	}
	return fcs
}

// appendFramed frames one PPP frame for pppd. Every byte below 0x20 is
// escaped whatever the link agreed: pppd reads an escaped byte the same
// either way, and nothing of the device's ever has to be told the map.
func appendFramed(dst, frame []byte) []byte {
	fcs := ^fcs16(fcsInit, frame)
	dst = append(dst, hdlcFlag)
	put := func(c byte) {
		if c < 0x20 || c == hdlcFlag || c == hdlcEscape {
			dst = append(dst, hdlcEscape, c^hdlcXor)
		} else {
			dst = append(dst, c)
		}
	}
	for _, c := range frame {
		put(c)
	}
	put(byte(fcs))
	put(byte(fcs >> 8))
	return append(dst, hdlcFlag)
}

// deframer takes the frames out of what pppd writes, which arrives in
// pieces of any size.
type deframer struct {
	buf     []byte
	escaped bool
	broken  bool // a frame too long to be one; dropped up to the next flag
}

// maxFrame is past the largest frame pppd writes at any MTU this panel
// allows, check sequence included.
const maxFrame = 4096

// write feeds bytes in and hands each whole frame, check sequence verified
// and removed, to out. A frame that fails its check is dropped, as a line
// would drop it.
func (d *deframer) write(b []byte, out func(frame []byte)) {
	for _, c := range b {
		switch {
		case c == hdlcFlag:
			if !d.broken && len(d.buf) >= 4 && fcs16(fcsInit, d.buf) == fcsGood {
				out(d.buf[:len(d.buf)-2])
			}
			d.buf, d.escaped, d.broken = d.buf[:0], false, false
		case d.broken:
		case c == hdlcEscape:
			d.escaped = true
		default:
			if d.escaped {
				c ^= hdlcXor
				d.escaped = false
			}
			if len(d.buf) == maxFrame {
				d.broken = true
				continue
			}
			d.buf = append(d.buf, c)
		}
	}
}
