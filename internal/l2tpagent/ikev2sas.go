package l2tpagent

import (
	"bufio"
	"net/netip"
	"strconv"
	"strings"
)

// IKESA is one IKEv2 session strongSwan holds.
type IKESA struct {
	// Conn is the connection it came in on, which names its account
	// (ikev2conf.ParseConnName).
	Conn string
	// ID is strongSwan's own number for it, which ends it.
	ID uint
	// Established is whether it is up: one still being negotiated has no
	// address yet and is no session.
	Established bool
	// RemoteHost is the device's public address; RemoteID the identity it
	// signed in as.
	RemoteHost string
	RemoteID   string
	// VIP is the address the device was given.
	VIP netip.Addr
	// Age is how long ago it was established, in seconds.
	Age int64
	// BytesIn is what the device sent, BytesOut what it was sent, over its
	// child SAs.
	BytesIn, BytesOut uint64
}

// parseSAs reads `swanctl --list-sas --raw`: one line per session,
//
//	list-sa event {NAME {k=v k=[a b] child-sas {CHILD {k=v ...}}}}
//
// Unknown keys are passed over, so a newer strongSwan that says more is
// still read.
func parseSAs(out []byte) []IKESA {
	var sas []IKESA
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		rest, ok := strings.CutPrefix(line, "list-sa event ")
		if !ok {
			continue
		}
		root := parseRaw(rest)
		for name, v := range root {
			sec, ok := v.(map[string]any)
			if !ok {
				continue
			}
			sa := IKESA{Conn: name}
			sa.ID = uint(atou(str(sec["uniqueid"])))
			sa.Established = str(sec["state"]) == "ESTABLISHED"
			sa.RemoteHost = str(sec["remote-host"])
			sa.RemoteID = str(sec["remote-id"])
			sa.Age = int64(atou(str(sec["established"])))
			if vips, ok := sec["remote-vips"].([]string); ok && len(vips) > 0 {
				if a, err := netip.ParseAddr(vips[0]); err == nil {
					sa.VIP = a
				}
			}
			if children, ok := sec["child-sas"].(map[string]any); ok {
				for _, c := range children {
					if cs, ok := c.(map[string]any); ok {
						sa.BytesIn += atou(str(cs["bytes-in"]))
						sa.BytesOut += atou(str(cs["bytes-out"]))
					}
				}
			}
			sas = append(sas, sa)
		}
	}
	return sas
}

// parseRaw reads one "{...}" body of vici's raw form into nested maps:
// sections are maps, lists are []string, values are strings.
func parseRaw(s string) map[string]any {
	toks := tokenize(s)
	i := 0
	var section func() map[string]any
	section = func() map[string]any {
		m := map[string]any{}
		for i < len(toks) {
			t := toks[i]
			switch {
			case t == "}":
				i++
				return m
			case t == "{":
				i++
				// An unnamed section: the outermost brace.
				for k, v := range section() {
					m[k] = v
				}
			case i+1 < len(toks) && toks[i+1] == "{":
				i += 2
				m[t] = section()
			case strings.Contains(t, "="):
				k, v, _ := strings.Cut(t, "=")
				if v == "[" {
					i++
					var list []string
					for i < len(toks) && toks[i] != "]" {
						list = append(list, toks[i])
						i++
					}
					m[k] = list
				} else {
					m[k] = v
				}
				i++
			default:
				i++
			}
		}
		return m
	}
	return section()
}

// tokenize splits on spaces, and makes braces and list brackets tokens of
// their own: "k=[a b]" is "k=[", "a", "b", "]".
func tokenize(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		for f != "" {
			switch {
			case f[0] == '{' || f[0] == '}' || f[0] == ']':
				out = append(out, f[:1])
				f = f[1:]
			default:
				j := strings.IndexAny(f, "{}]")
				if k := strings.Index(f, "=["); k >= 0 && (j < 0 || k < j) {
					out = append(out, f[:k+2])
					f = f[k+2:]
					continue
				}
				if j < 0 {
					out = append(out, f)
					f = ""
				} else {
					out = append(out, f[:j])
					f = f[j:]
				}
			}
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func atou(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}
