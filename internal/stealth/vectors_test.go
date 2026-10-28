package stealth

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"testing/cryptotest"
)

var update = flag.Bool("update", false, "write the test vectors again")

// udpVector is one opening and a data packet each way, from fixed inputs and
// a fixed random source. testdata/udp.json pins the format, and
// scripts/stealth/udp_vectors.py checks it with an implementation of its own.
type udpVector struct {
	Secret    string `json:"secret"`
	Anchor    string `json:"anchor"`
	Timestamp int64  `json:"timestamp"`
	Datagram  string `json:"datagram"`
	Room      int    `json:"room"`
	Opening   string `json:"opening"`
	Up        string `json:"up"`
	Down      string `json:"down"`
	DataUp    string `json:"dataUp"`
	DataDown  string `json:"dataDown"`
}

func TestTheUDPVectors(t *testing.T) {
	cryptotest.SetGlobalRandom(t, 1)
	secret, anchor := Secret{}, Anchor{}
	for i := range 32 {
		secret[i], anchor[i] = byte(i), byte(32+i)
	}
	d := keepalive()
	pad := NewPadder(0, 0)
	opening, keys, err := NewUDPClient(secret, anchor).Open(nil, testNow, d, &pad, room)
	if err != nil {
		t.Fatal(err)
	}
	up, err := NewUDPSender(keys.Up, pad).Seal(nil, d, false, room)
	if err != nil {
		t.Fatal(err)
	}
	down, err := NewUDPSender(keys.Down, NewPadder(0, 0)).Seal(nil, nil, false, room)
	if err != nil {
		t.Fatal(err)
	}
	h := hex.EncodeToString
	got := udpVector{h(secret[:]), h(anchor[:]), testNow.Unix(), h(d), room, h(opening), h(keys.Up), h(keys.Down), h(up), h(down)}
	b, _ := json.MarshalIndent(got, "", "  ")
	b = append(b, '\n')
	path := filepath.Join("testdata", "udp.json")
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		t.Fatalf("the UDP packets are not what testdata/udp.json says; a change to the format is a new Version")
	}
}

// wsVector is one wui-ws proof and a chunk each way, from fixed inputs and a
// fixed random source. testdata/ws.json pins the format, and
// scripts/stealth/ws_vectors.py checks it with an implementation of its own,
// HPKE included.
type wsVector struct {
	Secret       string `json:"secret"`
	Anchor       string `json:"anchor"`
	TransportKey string `json:"transportKey"`
	Transport    string `json:"transport"`
	Timestamp    int64  `json:"timestamp"`
	Token        string `json:"token"`
	Up           string `json:"up"`
	Down         string `json:"down"`
	Datagram     string `json:"datagram"`
	ChunkUp      string `json:"chunkUp"`
	ChunkDown    string `json:"chunkDown"`
}

func TestTheWSVectors(t *testing.T) {
	checkVector(t, "ws.json", streamVector(t, WS, 2))
}

func TestTheTLSVectors(t *testing.T) {
	checkVector(t, "tls.json", streamVector(t, TLS, 4))
}

// plainVector is wui-ws's vector for wui-plain, and the Cookie field its
// request carries the token in.
type plainVector struct {
	wsVector
	Cookie string `json:"cookie"`
}

func TestThePlainVectors(t *testing.T) {
	v := streamVector(t, Plain, 3)
	checkVector(t, "plain.json", plainVector{v, "sid=" + v.Token})
}

// pollVector is wui-ws's vector for wui-poll, with the session's id, the
// query of its first later request, and a body each way: the opening's,
// whose one chunk holds the datagram, and an answer of padding alone. The
// bodies are sealed from the start of each direction, as the chunks are.
type pollVector struct {
	wsVector
	ID       string `json:"id"`
	Query    string `json:"query"`
	BodyUp   string `json:"bodyUp"`
	BodyDown string `json:"bodyDown"`
}

func TestThePollVectors(t *testing.T) {
	v := streamVector(t, Poll, 5)
	up, _ := hex.DecodeString(v.Up)
	down, _ := hex.DecodeString(v.Down)
	d, _ := hex.DecodeString(v.Datagram)
	k := streamKeysOf(t, v)
	su, _ := NewStreamSender(up, NewPadder(0, 0))
	sd, _ := NewStreamSender(down, NewPadder(0, 0))
	bu, err := su.AppendBody(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	bd, err := sd.AppendBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := hex.EncodeToString
	checkVector(t, "poll.json", pollVector{v, h(k.ID[:]), PollQuery(k.ID, 1), h(bu), h(bd)})
}

// streamKeysOf opens a vector's token as the server does, for the keys a
// vector does not carry.
func streamKeysOf(t *testing.T, v wsVector) StreamKeys {
	t.Helper()
	b := func(s string) []byte { x, _ := hex.DecodeString(s); return x }
	var secret Secret
	var anchor Anchor
	copy(secret[:], b(v.Secret))
	copy(anchor[:], b(v.Anchor))
	tk, err := ParseTransportKey(base64.StdEncoding.EncodeToString(b(v.TransportKey)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewProofServer(anchor, v.Transport, tk)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Open(v.Token)
	if err != nil {
		t.Fatal(err)
	}
	dk := Derive(secret, anchor)
	k, err := s.Check(&p, &dk)
	if err != nil {
		t.Fatal(err)
	}
	if h := hex.EncodeToString; h(k.Up) != v.Up || h(k.Down) != v.Down {
		t.Fatal("the server's keys are not the client's")
	}
	return k
}

// streamVector is a stream transport's proof and a chunk each way, from
// fixed inputs and the random source seed.
func streamVector(t *testing.T, transport string, seed uint64) wsVector {
	t.Helper()
	cryptotest.SetGlobalRandom(t, seed)
	secret, anchor, priv := Secret{}, Anchor{}, make([]byte, 32)
	for i := range 32 {
		secret[i], anchor[i], priv[i] = byte(i), byte(32+i), byte(64+i)
	}
	tk, err := ParseTransportKey(base64.StdEncoding.EncodeToString(priv))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewProofClient(secret, anchor, transport, tk.Public())
	if err != nil {
		t.Fatal(err)
	}
	token, keys, err := c.Token(testNow)
	if err != nil {
		t.Fatal(err)
	}
	d := keepalive()
	up, _ := NewStreamSender(keys.Up, NewPadder(0, 0))
	down, _ := NewStreamSender(keys.Down, NewPadder(0, 0))
	cu, err := up.Seal(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	cd, err := down.Seal(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := hex.EncodeToString
	return wsVector{h(secret[:]), h(anchor[:]), h(priv), transport, testNow.Unix(), token, h(keys.Up), h(keys.Down), h(d), h(cu), h(cd)}
}

func checkVector(t *testing.T, name string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	b = append(b, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		t.Fatalf("the format is not what testdata/%s says; a change to the format is a new Version", name)
	}
}

// shapeVector is a wui-shape proof, a chunk each way (as every stream
// transport has), and the pieces a shaped session adds: the get-poll shape,
// a request's list of chunks, the query target that carries it, the late
// answer, and one stream event. testdata/shape.json pins the format and
// scripts/stealth/ws_vectors.py checks it with an implementation of its own.
type shapeVector struct {
	Secret       string    `json:"secret"`
	Anchor       string    `json:"anchor"`
	TransportKey string    `json:"transportKey"`
	Transport    string    `json:"transport"`
	Timestamp    int64     `json:"timestamp"`
	Token        string    `json:"token"`
	Up           string    `json:"up"`
	Down         string    `json:"down"`
	Datagram     string    `json:"datagram"`
	ChunkUp      string    `json:"chunkUp"`
	ChunkDown    string    `json:"chunkDown"`
	ID           string    `json:"id"`
	Shape        shapeJSON `json:"shape"`
	Path         string    `json:"path"`
	ListUp       string    `json:"listUp"`
	Target       string    `json:"target"`
	ListDown     string    `json:"listDown"`
	Late         string    `json:"late"`
	Event        string    `json:"event"`
}

type shapeJSON struct {
	Up   shapeJSONUp   `json:"up"`
	Down shapeJSONDown `json:"down"`
}

type shapeJSONUp struct {
	Method   string `json:"method"`
	In       string `json:"in"`
	Limit    int    `json:"limit"`
	Inflight int    `json:"inflight"`
	Gather   [2]int `json:"gather"`
}

type shapeJSONDown struct {
	Mode string `json:"mode"`
}

func TestTheShapeVectors(t *testing.T) {
	cryptotest.SetGlobalRandom(t, 6)
	secret, anchor, priv := Secret{}, Anchor{}, make([]byte, 32)
	for i := range 32 {
		secret[i], anchor[i], priv[i] = byte(i), byte(32+i), byte(64+i)
	}
	tk, err := ParseTransportKey(base64.StdEncoding.EncodeToString(priv))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewProofClient(secret, anchor, Shaped, tk.Public())
	if err != nil {
		t.Fatal(err)
	}
	token, keys, err := c.Token(testNow)
	if err != nil {
		t.Fatal(err)
	}
	d := keepalive()

	// The generic stream chunk each way, at number 0, as on wui-ws.
	up0, _ := NewStreamSender(keys.Up, NewPadder(0, 0))
	down0, _ := NewStreamSender(keys.Down, NewPadder(0, 0))
	cu, err := up0.Seal(nil, d)
	if err != nil {
		t.Fatal(err)
	}
	cd, err := down0.Seal(nil)
	if err != nil {
		t.Fatal(err)
	}

	shape, ok := Preset("get-poll")
	if !ok {
		t.Fatal("no get-poll preset")
	}
	const path = "/assets/sync-90be44f1.js"

	// Request 1, upstream: a chunk with the datagram, then a padding chunk.
	su, _ := NewShapeSender(keys.Up, NewPadder(0, 0))
	listUp, err := su.AppendChunk(nil, 1, 0, false, ShapeDataMax, d)
	if err != nil {
		t.Fatal(err)
	}
	if listUp, err = su.AppendChunk(listUp, 1, 1, false, ShapeDataMax); err != nil {
		t.Fatal(err)
	}
	parts, ok := BuildParts(shape, path, ShapeQuery(keys.ID, 1), listUp)
	if !ok {
		t.Fatal("the request is over its limit")
	}

	// The answer to request 1 is padding alone; then the late answer of
	// request 1, and one stream event carrying the datagram.
	sd, _ := NewShapeSender(keys.Down, NewPadder(0, 0))
	listDown, err := sd.AppendChunk(nil, 1, 0, false, ShapeDataMax)
	if err != nil {
		t.Fatal(err)
	}
	late := sd.LateAnswer(1)
	ev, err := sd.SealEvent(nil, 3, 2, false, d)
	if err != nil {
		t.Fatal(err)
	}
	event := SseEvent(ev)

	h := hex.EncodeToString
	got := shapeVector{
		Secret: h(secret[:]), Anchor: h(anchor[:]), TransportKey: h(priv),
		Transport: Shaped, Timestamp: testNow.Unix(), Token: token,
		Up: h(keys.Up), Down: h(keys.Down), Datagram: h(d),
		ChunkUp: h(cu), ChunkDown: h(cd), ID: h(keys.ID[:]),
		Shape: shapeJSON{
			Up:   shapeJSONUp{shape.Method, shape.Place.Word(), shape.Limit, shape.Inflight, [2]int{int(shape.Gather.Lo), int(shape.Gather.Hi)}},
			Down: shapeJSONDown{shape.Mode.Word()},
		},
		Path: path, ListUp: h(listUp), Target: parts.Target,
		ListDown: h(listDown), Late: h(late), Event: string(event),
	}
	checkVector(t, "shape.json", got)
}
