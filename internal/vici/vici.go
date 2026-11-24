// Package vici speaks strongSwan's control protocol, VICI, over charon's
// socket: what swanctl speaks, for the few requests the agent makes one at a
// time -- one connection, one pool, one secret loaded or unloaded -- where
// swanctl would hand strongSwan every one of them again.
//
// A message is a tree of sections, each holding values and lists of values.
// The same tree is written out in swanctl's own syntax (Section.Swanctl), so
// the files strongSwan reads when it starts and what the agent hands it
// between starts are one rendering, and cannot drift apart.
package vici

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// Sockets are where charon listens, by distribution: swanctl's default
// first.
var Sockets = []string{"/var/run/charon.vici", "/run/strongswan/charon.vici"}

// Entry is one item of a section: a value, a list of values, or a section.
type Entry struct {
	Key string
	// Values: one for a value, any number for a list.
	Values []string
	List   bool
	// Quote has swanctl's syntax quote the value: identities and secrets,
	// which can hold what the syntax would otherwise read.
	Quote bool
	// File, on a list, names the file swanctl's syntax gives -- a path from
	// swanctl's x509 directory -- where VICI is sent Values, its contents.
	File string
	Sub  *Section
}

// Section is a named section.
type Section struct {
	Name    string
	Entries []Entry
}

// Value adds key = value.
func (s *Section) Value(key, value string) *Section {
	s.Entries = append(s.Entries, Entry{Key: key, Values: []string{value}})
	return s
}

// Quoted adds key = "value".
func (s *Section) Quoted(key, value string) *Section {
	s.Entries = append(s.Entries, Entry{Key: key, Values: []string{value}, Quote: true})
	return s
}

// List adds key = a,b,c, sent to VICI as a list.
func (s *Section) List(key string, values ...string) *Section {
	s.Entries = append(s.Entries, Entry{Key: key, Values: values, List: true})
	return s
}

// FileList adds key = file, sent to VICI as a list holding the file's body.
func (s *Section) FileList(key, file, body string) *Section {
	s.Entries = append(s.Entries, Entry{Key: key, Values: []string{body}, List: true, File: file})
	return s
}

// Sub adds a section and returns it.
func (s *Section) Sub(name string) *Section {
	sub := &Section{Name: name}
	s.Entries = append(s.Entries, Entry{Key: name, Sub: sub})
	return sub
}

// Swanctl writes the section in swanctl.conf's syntax, indented by depth.
func (s *Section) Swanctl(b *strings.Builder, depth int) {
	pad := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s%s {\n", pad, s.Name)
	for _, e := range s.Entries {
		switch {
		case e.Sub != nil:
			e.Sub.Swanctl(b, depth+1)
		case e.File != "":
			fmt.Fprintf(b, "%s  %s = %s\n", pad, e.Key, e.File)
		case e.Quote:
			fmt.Fprintf(b, "%s  %s = %q\n", pad, e.Key, strings.Join(e.Values, ","))
		default:
			fmt.Fprintf(b, "%s  %s = %s\n", pad, e.Key, strings.Join(e.Values, ","))
		}
	}
	fmt.Fprintf(b, "%s}\n", pad)
}

// The protocol's element and packet types.
const (
	elSectionStart = 1
	elSectionEnd   = 2
	elKeyValue     = 3
	elListStart    = 4
	elListItem     = 5
	elListEnd      = 6

	pkCmdRequest  = 0
	pkCmdResponse = 1
	pkCmdUnknown  = 2
	pkEvent       = 7
)

// encode appends the section's entries -- not its own name: a request's
// top level is unnamed -- to b.
func (s *Section) encode(b []byte) ([]byte, error) {
	for _, e := range s.Entries {
		if len(e.Key) > 255 {
			return nil, fmt.Errorf("vici: name %.20q too long", e.Key)
		}
		switch {
		case e.Sub != nil:
			b = append(b, elSectionStart, byte(len(e.Key)))
			b = append(b, e.Key...)
			var err error
			if b, err = e.Sub.encode(b); err != nil {
				return nil, err
			}
			b = append(b, elSectionEnd)
		case e.List:
			b = append(b, elListStart, byte(len(e.Key)))
			b = append(b, e.Key...)
			for _, v := range e.Values {
				if len(v) > 65535 {
					return nil, fmt.Errorf("vici: %s too long", e.Key)
				}
				b = append(b, elListItem)
				b = binary.BigEndian.AppendUint16(b, uint16(len(v)))
				b = append(b, v...)
			}
			b = append(b, elListEnd)
		default:
			v := strings.Join(e.Values, ",")
			if len(v) > 65535 {
				return nil, fmt.Errorf("vici: %s too long", e.Key)
			}
			b = append(b, elKeyValue, byte(len(e.Key)))
			b = append(b, e.Key...)
			b = binary.BigEndian.AppendUint16(b, uint16(len(v)))
			b = append(b, v...)
		}
	}
	return b, nil
}

// Request encodes a command and its message as one packet, without the
// length that leads it on the wire.
func Request(cmd string, msg *Section) ([]byte, error) {
	if len(cmd) > 255 {
		return nil, errors.New("vici: command name too long")
	}
	b := []byte{pkCmdRequest, byte(len(cmd))}
	b = append(b, cmd...)
	if msg == nil {
		return b, nil
	}
	return msg.encode(b)
}

// Reply is a response's top-level values: all the agent reads of one.
type Reply map[string]string

// decodeReply reads a response's message, keeping its top-level values and
// skipping its sections and lists.
func decodeReply(b []byte) (Reply, error) {
	out := Reply{}
	depth := 0
	name := func() (string, error) {
		if len(b) < 1 || len(b) < 1+int(b[0]) {
			return "", io.ErrUnexpectedEOF
		}
		n := string(b[1 : 1+int(b[0])])
		b = b[1+int(b[0]):]
		return n, nil
	}
	value := func() (string, error) {
		if len(b) < 2 {
			return "", io.ErrUnexpectedEOF
		}
		l := int(binary.BigEndian.Uint16(b))
		if len(b) < 2+l {
			return "", io.ErrUnexpectedEOF
		}
		v := string(b[2 : 2+l])
		b = b[2+l:]
		return v, nil
	}
	for len(b) > 0 {
		t := b[0]
		b = b[1:]
		var err error
		switch t {
		case elSectionStart:
			_, err = name()
			depth++
		case elSectionEnd:
			depth--
		case elKeyValue:
			var k, v string
			if k, err = name(); err == nil {
				if v, err = value(); err == nil && depth == 0 {
					out[k] = v
				}
			}
		case elListStart:
			_, err = name()
		case elListItem:
			_, err = value()
		case elListEnd:
		default:
			return nil, fmt.Errorf("vici: unknown element %d", t)
		}
		if err != nil {
			return nil, fmt.Errorf("vici: malformed reply: %w", err)
		}
	}
	return out, nil
}

// Client is one connection to charon.
type Client struct {
	conn    net.Conn
	timeout time.Duration
}

// Dial connects to charon on the first of Sockets that answers.
func Dial(timeout time.Duration) (*Client, error) {
	last := errors.New("vici: no socket")
	for _, p := range Sockets {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		c, err := net.DialTimeout("unix", p, timeout)
		if err == nil {
			return &Client{conn: c, timeout: timeout}, nil
		}
		last = err
	}
	return nil, last
}

// Close ends the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Call sends a command and returns its reply; a reply with success = no is
// an error carrying strongSwan's message.
func (c *Client) Call(cmd string, msg *Section) (Reply, error) {
	pkt, err := Request(cmd, msg)
	if err != nil {
		return nil, err
	}
	if err := c.conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(pkt)))
	if _, err := c.conn.Write(append(frame, pkt...)); err != nil {
		return nil, err
	}
	for {
		var head [4]byte
		if _, err := io.ReadFull(c.conn, head[:]); err != nil {
			return nil, err
		}
		body := make([]byte, binary.BigEndian.Uint32(head[:]))
		if _, err := io.ReadFull(c.conn, body); err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, errors.New("vici: empty packet")
		}
		switch body[0] {
		case pkEvent:
			continue // not asked for; never registered
		case pkCmdUnknown:
			return nil, fmt.Errorf("vici: strongSwan does not know %q", cmd)
		case pkCmdResponse:
			r, err := decodeReply(body[1:])
			if err != nil {
				return nil, err
			}
			if r["success"] == "no" {
				return r, fmt.Errorf("vici %s: %s", cmd, r["errmsg"])
			}
			return r, nil
		default:
			return nil, fmt.Errorf("vici: unexpected packet %d", body[0])
		}
	}
}
