package stealth

import (
	"errors"
	"strconv"
	"strings"
)

// The shape of a mold transport.
//
// mold is relay that need not look like relay: the operator picks
// the "shape" of a request, the one form every request of the transport
// takes. A shape says which method a request uses, where in the request the
// tunnel's data rides (the body, the query, the path or a header field), and
// whether the gateway answers each request with the downstream data (poll) or
// writes it all to one long stream (sse). Everything below the shape -- the
// keys, the chunk, its padding and the proof -- is relay's, under
// mold's own name.
//
// A shape is read once, from a profile or from the gateway's config, and both
// sides end at the same rules (Resolve), so a shape one side takes the other
// takes too. A shape that breaks a rule is refused outright: nothing is
// repaired and nothing is guessed from a member that is missing but its
// default. The rules are in docs/reference/stealth.md, "mold".

// Where the data of a request rides.
type Place int

const (
	// InBody is the body of the request. Only POST, PUT, PATCH and custom
	// methods may carry a body.
	InBody Place = iota
	// InQuery is the request target's query, behind the id and number.
	InQuery
	// InPath is the request target's path, cut into segments by '/'.
	InPath
	// InHeader is one header field, whose name the shape gives.
	InHeader
)

var placeWords = [...]string{InBody: "body", InQuery: "query", InPath: "path", InHeader: "header"}

// Word is the place as a profile writes it.
func (p Place) Word() string {
	if int(p) < len(placeWords) {
		return placeWords[p]
	}
	return ""
}

func readPlace(word string) (Place, bool) {
	for p, w := range placeWords {
		if w == word {
			return Place(p), true
		}
	}
	return 0, false
}

// Mode is how the downstream data travels.
type Mode int

const (
	// ModePoll answers every request with the data that waits, as relay
	// does.
	ModePoll Mode = iota
	// ModeSse writes the downstream data to one Server-Sent-Events stream,
	// an event per chunk, and answers every other request with a single
	// padding-only chunk.
	ModeSse
)

// Word is the mode as a profile writes it.
func (m Mode) Word() string {
	if m == ModeSse {
		return "sse"
	}
	return "poll"
}

// A Range is a closed range [Lo, Hi] of milliseconds or seconds a client or
// gateway draws from. Lo is never above Hi.
type Range struct{ Lo, Hi uint32 }

// Shape is a shape that obeys every rule of its page. The only ways to make
// one are Resolve, ParseShape and Preset, so a Shape in hand is always valid.
type Shape struct {
	Method string
	Place  Place
	// Name is the header field that carries the data, set only for InHeader.
	Name string
	// Limit is the most one request carries: bytes of body, of request
	// target, or of the header value.
	Limit int
	// Inflight is how many requests of a session may be read at once, beside
	// a stream.
	Inflight int
	// Gather is the span, in ms, the client waits to collect datagrams into
	// one request.
	Gather Range

	Mode Mode
	// DownGather is the span, in ms, the gateway waits to collect datagrams
	// into one event. Used only by ModeSse.
	DownGather Range
	// Life is the span, in seconds, the client draws a stream's length from.
	// Used only by ModeSse.
	Life Range
	// Fallback is whether the client may fall back to polling when a stream
	// will not hold. Used only by ModeSse.
	Fallback bool
}

// Shape rules, every one from the page.
const (
	methodMin, methodMax = 3, 16
	shapeNameMin         = 1
	shapeNameMax         = 32

	targetLimitLo, targetLimitHi, targetLimitDef = 1024, 8000, 4000
	headerLimitLo, headerLimitHi, headerLimitDef = 1024, 4096, 3000
	bodyLimitLo, bodyLimitHi, bodyLimitDef       = 1024, 65536, 65536

	inflightMin, inflightMax = 1, 4

	gatherMax = 250
	// ShapeLineMax is the request line a gateway reads for a shape whose
	// up.limit is above shapeLineBound; ShapeLineSmall otherwise.
	ShapeLineSmall = LineMax
	ShapeLineMax   = 8192
	shapeLineBound = 4000

	lifeLo, lifeHi = 30, 3600
)

// The methods a shape may never use: a web server or CDN treats them
// specially, so a request of one would not reach the gateway whole.
var methodsRefused = [...]string{"CONNECT", "TRACE", "HEAD", "OPTIONS"}

// The header names a shape may not borrow: a field the client always writes
// (so a shaped one would stand twice and neither be read), or a hop-by-hop or
// body field a front rewrites. Any name that starts "Sec-" or "Proxy-" is
// refused too.
var namesRefused = map[string]bool{
	"host": true, "content-length": true, "content-type": true,
	"transfer-encoding": true, "connection": true, "upgrade": true,
	"accept": true, "range": true, "expect": true, "te": true, "trailer": true,
	"user-agent": true, "origin": true, "referer": true, "accept-encoding": true,
	"accept-language": true, "cache-control": true, "pragma": true,
	"keep-alive": true, "x-forwarded-for": true, "forwarded": true, "via": true,
	"cookie": true, "authorization": true,
}

// Given is what a profile or a config wrote before the defaults and the
// rules. A member that was not written is nil, so a default is never told
// from a written value that happens to equal it.
type Given struct {
	Method     string
	Place      string
	Name       *string
	Limit      *uint64
	Inflight   *uint64
	Gather     *[2]uint64
	Mode       string
	DownGather *[2]uint64
	Life       *[2]uint64
	Fallback   *bool
}

// ErrShape is any Given that breaks a rule. Its message is the dotted name of
// the member at fault, so a profile or config can point at it.
type ErrShape struct{ Member string }

func (e ErrShape) Error() string { return "stealth: shape: " + e.Member }

func badShape(member string) error { return ErrShape{member} }

func isUpperASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

func nameOK(name string) bool {
	if len(name) < shapeNameMin || len(name) > shapeNameMax {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	lower := strings.ToLower(name)
	if namesRefused[lower] || strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "proxy-") {
		return false
	}
	return true
}

// aRange reads a written pair into a Range inside bounds, the low one not
// above the high one, or def when nothing was written.
func aRange(pair *[2]uint64, lo, hi uint32, def Range) (Range, bool) {
	if pair == nil {
		return def, true
	}
	a, b := pair[0], pair[1]
	if a > uint64(hi) || b > uint64(hi) || a < uint64(lo) || b < uint64(lo) || a > b {
		return Range{}, false
	}
	return Range{uint32(a), uint32(b)}, true
}

// Resolve applies the defaults and every rule of the page to what was
// written, and is the one gate a Shape passes through.
func Resolve(g Given) (Shape, error) {
	method := g.Method
	custom := method != "GET" && method != "POST" && method != "PUT" && method != "PATCH"
	for _, bad := range methodsRefused {
		if method == bad {
			return Shape{}, badShape("up.method")
		}
	}
	if custom && (len(method) < methodMin || len(method) > methodMax || !isUpperASCII(method)) {
		return Shape{}, badShape("up.method")
	}
	place, ok := readPlace(g.Place)
	if !ok {
		return Shape{}, badShape("up.in")
	}
	// GET never carries a body; POST, PUT and PATCH carry only a body.
	bodied := method == "POST" || method == "PUT" || method == "PATCH"
	if (method == "GET" && place == InBody) || (bodied && place != InBody) {
		return Shape{}, badShape("up.in")
	}

	var name string
	switch {
	case place == InHeader:
		if g.Name == nil || !nameOK(*g.Name) {
			return Shape{}, badShape("up.name")
		}
		name = *g.Name
	case g.Name != nil:
		return Shape{}, badShape("up.name")
	}

	lo, hi, def := targetLimitLo, targetLimitHi, targetLimitDef
	switch place {
	case InBody:
		lo, hi, def = bodyLimitLo, bodyLimitHi, bodyLimitDef
	case InHeader:
		lo, hi, def = headerLimitLo, headerLimitHi, headerLimitDef
	}
	limit := def
	if g.Limit != nil {
		if *g.Limit < uint64(lo) || *g.Limit > uint64(hi) {
			return Shape{}, badShape("up.limit")
		}
		limit = int(*g.Limit)
	}

	var mode Mode
	switch g.Mode {
	case "poll":
		mode = ModePoll
	case "sse":
		mode = ModeSse
	default:
		return Shape{}, badShape("down.mode")
	}

	inflight := 1
	if mode == ModeSse {
		inflight = 2
	}
	if g.Inflight != nil {
		v := *g.Inflight
		if v < inflightMin || v > inflightMax || (mode == ModePoll && v != 1) {
			return Shape{}, badShape("up.inflight")
		}
		inflight = int(v)
	}

	gather, ok := aRange(g.Gather, 0, gatherMax, Range{10, 60})
	if !ok {
		return Shape{}, badShape("up.gather")
	}

	// The three stream members say nothing for poll, and a config that sets
	// one has not said what it means.
	if mode == ModePoll && (g.DownGather != nil || g.Life != nil || g.Fallback != nil) {
		return Shape{}, badShape("down")
	}
	downGather, ok := aRange(g.DownGather, 0, gatherMax, Range{0, 20})
	if !ok {
		return Shape{}, badShape("down.gather")
	}
	life, ok := aRange(g.Life, lifeLo, lifeHi, Range{120, 900})
	if !ok {
		return Shape{}, badShape("down.life")
	}
	fallback := true
	if g.Fallback != nil {
		fallback = *g.Fallback
	}

	return Shape{
		Method: method, Place: place, Name: name, Limit: limit,
		Inflight: inflight, Gather: gather,
		Mode: mode, DownGather: downGather, Life: life, Fallback: fallback,
	}, nil
}

// Preset is the shape of a named preset, every free member at its default:
// post-stream, get-stream, put-poll and get-poll. A client knows no presets;
// this is for the panel and a doctor's examples.
func Preset(name string) (Shape, bool) {
	var method, place, mode string
	switch name {
	case "post-stream":
		method, place, mode = "POST", "body", "sse"
	case "get-stream":
		method, place, mode = "GET", "query", "sse"
	case "put-poll":
		method, place, mode = "PUT", "body", "poll"
	case "get-poll":
		method, place, mode = "GET", "query", "poll"
	default:
		return Shape{}, false
	}
	s, err := Resolve(Given{Method: method, Place: place, Mode: mode})
	return s, err == nil
}

// LineMaxFor is the longest request line a gateway reads for this shape.
func (s Shape) LineMaxFor() int {
	if s.Limit > shapeLineBound {
		return ShapeLineMax
	}
	return ShapeLineSmall
}

// Bodied says the request carries a body.
func (s Shape) Bodied() bool { return s.Place == InBody }

// Streamed says the downstream data comes on one stream.
func (s Shape) Streamed() bool { return s.Mode == ModeSse }

// String prints a shape in one line, for the panel and the doctor.
func (s Shape) String() string {
	var b strings.Builder
	b.WriteString(s.Method)
	b.WriteString(" in ")
	b.WriteString(s.Place.Word())
	if s.Name != "" {
		b.WriteByte(' ')
		b.WriteString(s.Name)
	}
	fmt := func(r Range) string {
		return strconv.Itoa(int(r.Lo)) + " to " + strconv.Itoa(int(r.Hi))
	}
	b.WriteString(", limit " + strconv.Itoa(s.Limit) + ", " + strconv.Itoa(s.Inflight) + " in flight, gather " + fmt(s.Gather) + " ms, ")
	if s.Mode == ModePoll {
		b.WriteString("poll")
	} else {
		tail := "falls back to polling"
		if !s.Fallback {
			tail = "no fallback"
		}
		b.WriteString("sse, gather " + fmt(s.DownGather) + " ms, life " + fmt(s.Life) + " s, " + tail)
	}
	return b.String()
}

// errNoShape is a profile or config object that is not a shape at all.
var errNoShape = errors.New("stealth: not a shape")
