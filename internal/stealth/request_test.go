package stealth

import (
	"strings"
	"testing"
)

// scanBytes feeds a line one byte at a time, as a connection's bytes come,
// and says what the scanner decided and after how many.
func scanBytes(b string) (LineScan, int) {
	var s LineScanner
	for i := range len(b) {
		if r := s.Feed([]byte{b[i]}); r != LineMore {
			return r, i + 1
		}
	}
	return LineMore, len(b)
}

func TestLineScannerDecidesAtTheFirstByteThatCannotFit(t *testing.T) {
	for _, c := range []struct {
		in   string
		want LineScan
		at   int
	}{
		{"GET /a HTTP/1.1\r\n", LineDone, 17},
		{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", LineDone, 16},
		{"GET /p?tok-_en HTTP/1.1\r\n", LineDone, 25},
		{"GET /a%20b;c=d HTTP/1.1\r\n", LineDone, 25},
		{"POST /a HTTP/1.1\r\n", LineStranger, 1},
		{"get /a HTTP/1.1\r\n", LineStranger, 1},
		{"GET a HTTP/1.1\r\n", LineStranger, 5},
		{"GET /a\"b HTTP/1.1\r\n", LineStranger, 7},
		{"GET /a HTTP/1.0\r\n", LineStranger, 15},
		{"GET /a HTTP/1.1\n", LineStranger, 16},
		{"GET /a b HTTP/1.1\r\n", LineStranger, 8},
		{"\x16\x03\x01", LineStranger, 1},
		{"GET /a", LineMore, 6},
	} {
		got, at := scanBytes(c.in)
		if got != c.want || at != c.at {
			t.Errorf("%q: got %v after %d bytes, want %v after %d", c.in, got, at, c.want, c.at)
		}
	}
}

func TestLineScannerHasALimit(t *testing.T) {
	line := "GET /" + strings.Repeat("a", LineMax)
	got, at := scanBytes(line)
	if got != LineStranger || at != LineMax {
		t.Fatalf("got %v after %d, want a stranger at %d", got, at, LineMax)
	}
	fits := "GET /" + strings.Repeat("a", LineMax-len("GET / HTTP/1.1\r\n")) + " HTTP/1.1\r\n"
	if got, at := scanBytes(fits); got != LineDone || at != LineMax {
		t.Fatalf("a line of exactly the limit: got %v after %d", got, at)
	}
}

func TestLineScannerIsFinal(t *testing.T) {
	var s LineScanner
	if s.Feed([]byte("GET /abc HTTP/1.1\r\nxyz")) != LineDone || s.Len() != 19 {
		t.Fatalf("whole: %d", s.Len())
	}
	if s.Feed([]byte("\x00\x00")) != LineDone {
		t.Fatal("a decided line changed its mind")
	}
}

func TestParseLine(t *testing.T) {
	l, ok := ParseLine([]byte("GET /media/abc?x=1?y HTTP/1.1\r\n"))
	if !ok || string(l.Path) != "/media/abc" || string(l.Query) != "x=1?y" {
		t.Fatalf("%v %q %q", ok, l.Path, l.Query)
	}
	if !l.PathIs("/media/abc") || l.PathIs("/media/abd") || l.PathIs("/media/ab") {
		t.Fatal("PathIs")
	}
	if l, ok := ParseLine([]byte("GET /x HTTP/1.1\r\n")); !ok || l.Query != nil {
		t.Fatalf("no query: %v %q", ok, l.Query)
	}
	for _, bad := range []string{"GET /x HTTP/1.1\r\nmore", "GET /x HTTP/1.1", "POST /x HTTP/1.1\r\n"} {
		if _, ok := ParseLine([]byte(bad)); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
