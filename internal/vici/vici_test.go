package vici

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *Section {
	c := &Section{Name: "c1"}
	c.Value("version", "2").List("pools", "p1")
	r := c.Sub("remote")
	r.Quoted("id", "s1-ali").FileList("certs", "../d/c1.pem", "PEM")
	return c
}

// The wire form is the protocol's: a section, a value, a list, the file's
// body where swanctl's syntax names the file.
func TestARequestIsEncodedAsTheProtocolSays(t *testing.T) {
	got, err := Request("load-conn", &Section{Entries: []Entry{{Key: "c1", Sub: sample()}}})
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	w.Write([]byte{pkCmdRequest, 9})
	w.WriteString("load-conn")
	w.Write([]byte{elSectionStart, 2})
	w.WriteString("c1")
	kv := func(k, v string) {
		w.Write([]byte{elKeyValue, byte(len(k))})
		w.WriteString(k)
		w.Write(binary.BigEndian.AppendUint16(nil, uint16(len(v))))
		w.WriteString(v)
	}
	list := func(k string, vs ...string) {
		w.Write([]byte{elListStart, byte(len(k))})
		w.WriteString(k)
		for _, v := range vs {
			w.Write([]byte{elListItem})
			w.Write(binary.BigEndian.AppendUint16(nil, uint16(len(v))))
			w.WriteString(v)
		}
		w.Write([]byte{elListEnd})
	}
	kv("version", "2")
	list("pools", "p1")
	w.Write([]byte{elSectionStart, 6})
	w.WriteString("remote")
	kv("id", "s1-ali")
	list("certs", "PEM")
	w.Write([]byte{elSectionEnd, elSectionEnd})
	if !bytes.Equal(got, w.Bytes()) {
		t.Fatalf("got  %v\nwant %v", got, w.Bytes())
	}
}

func TestTheSameSectionIsWrittenForSwanctl(t *testing.T) {
	var b strings.Builder
	sample().Swanctl(&b, 1)
	want := "  c1 {\n    version = 2\n    pools = p1\n    remote {\n      id = \"s1-ali\"\n      certs = ../d/c1.pem\n    }\n  }\n"
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

// charon answering on a socket: a success, a refusal with its message, and
// an event it was never asked for, skipped.
func TestACallReadsTheReply(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "charon.vici")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets here:", err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var head [4]byte
			if _, err := io.ReadFull(c, head[:]); err != nil {
				return
			}
			body := make([]byte, binary.BigEndian.Uint32(head[:]))
			io.ReadFull(c, body)
			// An event first, then the reply.
			ev := []byte{pkEvent, 3, 'l', 'o', 'g'}
			c.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(ev))), ev...))
			m := &Section{}
			if strings.Contains(string(body), "unload-pool") {
				m.Value("success", "no").Value("errmsg", "pool has online leases")
			} else {
				m.Value("success", "yes")
			}
			// A nested success is not the reply's.
			m.Sub("ignored").Value("success", "no")
			payload, _ := m.encode([]byte{pkCmdResponse})
			c.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(payload))), payload...))
		}
	}()
	old := Sockets
	Sockets = []string{filepath.Join(dir, "absent"), sock}
	defer func() { Sockets = old }()
	c, err := Dial(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if r, err := c.Call("load-conn", &Section{Entries: []Entry{{Key: "c1", Sub: sample()}}}); err != nil || r["success"] != "yes" {
		t.Fatalf("%v %v", r, err)
	}
	if _, err := c.Call("unload-pool", (&Section{}).Value("name", "p1")); err == nil || !strings.Contains(err.Error(), "online leases") {
		t.Fatalf("refusal: %v", err)
	}
}
