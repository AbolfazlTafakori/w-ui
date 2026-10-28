package stealth

import (
	"bytes"
	"crypto/subtle"
)

// The request line of wui-tls.
//
// A wui-tls server ends TLS itself, so nothing in front of it has read the
// request yet, and it must decide who it is talking to from the bytes alone.
// It reads the request line as the bytes arrive, and the line stays a
// candidate only while it can still become
//
//	GET /<target> HTTP/1.1 CR LF
//
// with a target of the characters RFC 3986 lets a path and a query carry
// unescaped: letters, digits and - . _ ~ ! $ & ' ( ) * + , ; = : @ / ? %. The
// first byte that cannot belong to such a line decides that the peer is not a
// device, and everything read so far goes to the decoy as it came. A web
// server takes far more than this; whatever it takes beyond it, it gets.
//
// No secret is used for any of it, so the decision is the same work for
// everyone.

// Request limits of a server that reads the line itself.
const (
	// LineMax bounds a request line, its end included.
	LineMax = 4096
	// HeadMax bounds a device's whole request head.
	HeadMax = 16 << 10
)

// lineEnd is what follows the target.
const lineEnd = " HTTP/1.1\r\n"

// LineScan is what the bytes read so far say about a request line.
type LineScan int

const (
	// LineMore is a candidate that is not complete.
	LineMore LineScan = iota
	// LineStranger is a line that can be no device's: a byte that does not
	// fit, or the size limit reached.
	LineStranger
	// LineDone is a complete candidate.
	LineDone
)

// LineScanner decides a request line byte by byte. Its zero value is ready.
type LineScanner struct {
	read  int
	end   int // how much of lineEnd has been matched
	scan  LineScan
	total int
}

// Feed takes the next bytes of the connection as they arrive. Once it says
// LineStranger or LineDone it is final, and later bytes are not looked at:
// they belong to the decoy or to the request head.
func (s *LineScanner) Feed(b []byte) LineScan {
	for _, c := range b {
		if s.scan != LineMore {
			break
		}
		const prefix = "GET /"
		var fits bool
		switch {
		case s.read < len(prefix):
			fits = c == prefix[s.read]
		case s.end > 0:
			fits = c == lineEnd[s.end]
		default:
			fits = c == ' ' || targetByte(c)
		}
		if s.read >= len(prefix) && (s.end > 0 || c == ' ') {
			s.end++
		}
		s.read++
		switch {
		case !fits:
			s.scan = LineStranger
		case s.end == len(lineEnd):
			s.scan, s.total = LineDone, s.read
		case s.read >= LineMax:
			s.scan = LineStranger
		}
	}
	return s.scan
}

// Len is the length of a complete line, its end included.
func (s *LineScanner) Len() int { return s.total }

func targetByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return bytes.IndexByte([]byte("-._~!$&'()*+,;=:@/?%"), c) >= 0
}

// RequestLine is a complete candidate line taken apart: the path up to the
// first ?, and what follows it, which may be a token and says nothing until
// it is opened.
type RequestLine struct {
	Path  []byte
	Query []byte
}

// ParseLine takes apart a line the scanner called complete. It is false for
// anything the scanner would not have.
func ParseLine(line []byte) (RequestLine, bool) {
	var s LineScanner
	if s.Feed(line) != LineDone || s.Len() != len(line) {
		return RequestLine{}, false
	}
	target := line[len("GET ") : len(line)-len(lineEnd)]
	path, query, _ := bytes.Cut(target, []byte("?"))
	return RequestLine{Path: path, Query: query}, true
}

// PathIs compares the path with a transport's own over its whole length, so
// the time it takes does not say how much of the path a prober had right.
// A path of another length shows at once; its length is no secret worth the
// cost of hiding.
func (l RequestLine) PathIs(path string) bool {
	return subtle.ConstantTimeCompare(l.Path, []byte(path)) == 1
}
