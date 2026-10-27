package stealth

import (
	"bytes"
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
