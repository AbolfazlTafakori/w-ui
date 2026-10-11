package service

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// The sizes a WireGuard message has before AmneziaWG pads it, and the most a
// packet may be on the way: a 1500-byte link, less IPv4 and UDP.
const (
	awgInitSize      = 148
	awgResponseSize  = 92
	awgCookieSize    = 64
	awgDataOverhead  = 32
	awgPacketLimit   = 1500 - 20 - 8
	awgMaxJunk       = 1280
	awgMaxJunkCount  = 128
	awgMaxSpecial    = 1400 // an I packet, so it is never split on the way
	awgMaxSpecialLen = 4096 // the text of one
)

// awgMaxS4 is the most padding a data packet can take on a tunnel of this
// MTU before it no longer fits a 1500-byte link and is split in two, which
// costs every customer speed and some networks drop outright.
func awgMaxS4(mtu int) int {
	if mtu <= 0 {
		mtu = 1420
	}
	return max(0, awgPacketLimit-awgDataOverhead-mtu)
}

// checkAWG refuses an obfuscation profile AmneziaWG would refuse, or that
// would quietly not work: everything an operator can type into the form is
// checked here, against the rules of AmneziaWG's own kernel module and its
// userspace implementation, and each refusal names its field.
func checkAWG(p model.AWGParams, mtu int) error {
	field := func(name, format string, a ...any) error {
		return invalidField("awg."+name, format, a...)
	}
	if p.Jc < 0 || p.Jc > awgMaxJunkCount {
		return field("jc", "Jc is 0 to %d junk packets", awgMaxJunkCount)
	}
	if p.Jc > 0 {
		if p.Jmin < 1 || p.Jmin >= p.Jmax || p.Jmax > awgMaxJunk {
			return field("jmin", "Jmin and Jmax are sizes from 1 to %d, Jmin smaller than Jmax", awgMaxJunk)
		}
	}
	limits := []struct {
		name string
		v    int
		max  int
	}{
		{"s1", p.S1, awgPacketLimit - awgInitSize},
		{"s2", p.S2, awgPacketLimit - awgResponseSize},
		{"s3", p.S3, awgPacketLimit - awgCookieSize},
		{"s4", p.S4, awgMaxS4(mtu)},
	}
	for _, l := range limits {
		if l.v < 0 || l.v > l.max {
			if l.name == "s4" {
				return field("s4", "S4 is 0 to %d on a tunnel with MTU %d: more makes every data packet too big for the link", l.max, mtu)
			}
			return field(l.name, "%s is 0 to %d", strings.ToUpper(l.name), l.max)
		}
	}
	if p.S2-p.S1 == handshakeLengthGap {
		return field("s2", "S2 must not be S1 + %d: the two handshake packets would be the same size", handshakeLengthGap)
	}
	if p.HeaderProtectionKey != "" {
		k, err := base64.StdEncoding.DecodeString(p.HeaderProtectionKey)
		if err != nil || len(k) != 32 {
			return field("headerProtectionKey", "the header protection key is 32 bytes in base64, as a WireGuard key is")
		}
		for _, l := range limits {
			if l.v < 12 {
				return field(l.name, "header protection needs %s of at least 12", strings.ToUpper(l.name))
			}
		}
	}
	type span struct{ lo, hi uint32 }
	var spans [4]span
	for n := 1; n <= 4; n++ {
		lo, hi := p.HeaderRange(n)
		name := fmt.Sprintf("h%d", n)
		if lo <= 4 {
			return field(name, "H%d must be above 4: 1 to 4 are plain WireGuard's own", n)
		}
		spans[n-1] = span{lo, hi}
		for m := 0; m < n-1; m++ {
			if lo <= spans[m].hi && spans[m].lo <= hi {
				return field(name, "H%d overlaps H%d: each message type needs values of its own", n, m+1)
			}
		}
	}
	for i, s := range p.Specials() {
		if err := checkSpecial(s); err != nil {
			return field(fmt.Sprintf("i%d", i+1), "I%d: %v", i+1, err)
		}
	}
	return nil
}

// checkSpecial checks one of I1-I5 against the tags both AmneziaWG
// implementations read -- the kernel module and amneziawg-go, which every
// phone app is built on -- and returns the packet's size.
func checkSpecial(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > awgMaxSpecialLen {
		return fmt.Errorf("too long")
	}
	// A configuration ends a line at a newline and a comment at #.
	if strings.ContainsAny(s, "\r\n#") {
		return fmt.Errorf("no line breaks or #")
	}
	size, rest := 0, s
	for {
		start := strings.IndexByte(rest, '<')
		if strings.TrimSpace(rest[:max(start, 0)]) != "" || (start < 0 && strings.TrimSpace(rest) != "") {
			return fmt.Errorf("text outside a tag; every part is a tag such as <b 0x01ff> or <r 16>")
		}
		if start < 0 {
			break
		}
		end := strings.IndexByte(rest[start:], '>')
		if end < 0 {
			return fmt.Errorf("a tag is not closed with >")
		}
		parts := strings.Fields(rest[start+1 : start+end])
		rest = rest[start+end+1:]
		if len(parts) == 0 || len(parts) > 2 {
			return fmt.Errorf("an empty or malformed tag")
		}
		switch parts[0] {
		case "b":
			if len(parts) != 2 || !strings.HasPrefix(parts[1], "0x") {
				return fmt.Errorf("<b> takes bytes in hex, as <b 0x01ff>")
			}
			h := parts[1][2:]
			b, err := hex.DecodeString(h)
			if err != nil || len(b) == 0 {
				return fmt.Errorf("<b %s> is not whole bytes in hex", parts[1])
			}
			size += len(b)
		case "t":
			if len(parts) != 1 {
				return fmt.Errorf("<t> takes nothing")
			}
			size += 4
		case "r", "rc", "rd":
			if len(parts) != 2 {
				return fmt.Errorf("<%s> takes a length, as <%s 16>", parts[0], parts[0])
			}
			n, err := strconv.Atoi(parts[1])
			if err != nil || n < 1 || n > awgMaxSpecial {
				return fmt.Errorf("<%s %s>: the length is 1 to %d", parts[0], parts[1], awgMaxSpecial)
			}
			size += n
		default:
			// <c> is the kernel module's alone and <d>, <ds>, <dz> the
			// userspace one's: a profile has to work on both ends.
			return fmt.Errorf("<%s> is not a tag both AmneziaWG implementations know; use b, r, rc, rd or t", parts[0])
		}
	}
	if size == 0 {
		return fmt.Errorf("an empty packet")
	}
	if size > awgMaxSpecial {
		return fmt.Errorf("the packet is %d bytes; at most %d fits the link", size, awgMaxSpecial)
	}
	return nil
}

// AWGPreset is a ready I1 for the form: a packet that looks like the start
// of a protocol networks let through.
type AWGPreset struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// AWGPresets are the I1 packets the form offers. Each passes checkSpecial.
func AWGPresets() []AWGPreset {
	return []AWGPreset{
		// A QUIC Initial: long header, version 1, an 8-byte connection id
		// of its own each time, a length that matches what follows, and
		// what would be the encrypted payload -- random to anyone watching.
		{ID: "quic", Value: "<b 0xc30000000108><r 8><b 0x000044d0><r 1232>"},
		// A DNS query for www.google.com, with an id of its own each time.
		{ID: "dns", Value: "<r 2><b 0x01000001000000000000037777770667" +
			"6f6f676c6503636f6d0000010001>"},
		// A STUN binding request, what calls and video meetings begin with.
		{ID: "stun", Value: "<b 0x000100002112a442><r 12>"},
	}
}
