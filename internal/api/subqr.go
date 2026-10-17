package api

import (
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"sync"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/abolfazl/w-ui/internal/service"
)

// A customer's page carries each QR as its pattern of dots, and draws it
// the moment it is opened.
//
// Drawn into the page as an image it cost 25 KB a QR, two to a file, on
// every visit when hardly any is ever opened. Asked for when opened it was
// a request on a phone's network between the tap and the code -- a second
// or more of an empty box, on the networks the customers are on. The
// pattern is the code itself, a bit a dot: about 700 bytes for a
// WireGuard file, drawn by the page with no request at all.
//
// Working a pattern out is the costly part, a few milliseconds a code, so
// each is worked out once and kept: a file's QR changes only when its keys
// do, and a page is opened far more often than that.

// qrText is what a file's QR carries -- its configuration, or with key
// "amnezia" its AmneziaVPN key -- or nothing when it has none: an L2TP, PPTP,
// IKEv2 or OpenConnect sign-in is typed into the device and no app scans it,
// and a configuration past subPageQRLimit is a QR no phone resolves.
func qrText(d service.SubPageDevice, key string) string {
	text := d.Config
	if key == "amnezia" {
		text = d.AmneziaKey
	} else if d.Protocol == "l2tp" || d.Protocol == "pptp" || d.Protocol == "ikev2" || d.Protocol == "openconnect" {
		return ""
	}
	if len(text) > subPageQRLimit {
		return ""
	}
	return text
}

// subQRKept bounds the patterns kept: one or two a file, a few hundred bytes
// each, so a few megabytes at most.
const subQRKept = 8192

var subQRs = struct {
	sync.Mutex
	kept map[[32]byte]string
}{kept: map[[32]byte]string{}}

// subQR is text's QR as the page draws it: its width in dots, a colon, and
// its dots row by row, a bit each from the high bit, in unpadded base64 of
// URLs, which has nothing in it for the page to escape. The quiet margin
// around it is the page's to draw. "" when text makes no QR.
//
// Kept by a digest of the text, so what is kept names no key.
func subQR(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	subQRs.Lock()
	code, ok := subQRs.kept[sum]
	subQRs.Unlock()
	if ok {
		return code
	}

	q, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		return ""
	}
	q.DisableBorder = true
	dots := q.Bitmap()
	n := len(dots)
	bits := make([]byte, (n*n+7)/8)
	for y, row := range dots {
		for x, on := range row {
			if on {
				i := y*n + x
				bits[i/8] |= 0x80 >> (i % 8)
			}
		}
	}
	code = strconv.Itoa(n) + ":" + base64.RawURLEncoding.EncodeToString(bits)

	subQRs.Lock()
	if len(subQRs.kept) >= subQRKept {
		// Any one goes: a map is walked in no order, and one worked out
		// again costs a few milliseconds.
		for k := range subQRs.kept {
			delete(subQRs.kept, k)
			break
		}
	}
	subQRs.kept[sum] = code
	subQRs.Unlock()
	return code
}
