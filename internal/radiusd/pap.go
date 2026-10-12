package radiusd

import (
	"bytes"
	"crypto/md5"
	"crypto/subtle"
	"net/netip"
)

// PAP: a username and a password, the password hidden with the shared
// secret (RFC 2865, 5.2). What ocserv sends for an OpenConnect sign-in.
const attrUserPassword = 2

// OnAccept is told a user is about to be let in, before the Accept goes
// out: the agent ends the user's other sessions there, so the newest one is
// the one that stays and its address is free for it.
type OnAccept func(username string)

// SetOnAccept sets what is told of each sign-in about to be accepted.
func (s *Server) SetOnAccept(f OnAccept) {
	s.mu.Lock()
	s.onAccept = f
	s.mu.Unlock()
}

// papPassword recovers the password a request carries, or nothing when the
// attribute is not a whole number of 16-byte blocks.
func (s *Server) papPassword(reqAuth, hidden []byte) ([]byte, bool) {
	if len(hidden) == 0 || len(hidden)%16 != 0 || len(hidden) > 128 {
		return nil, false
	}
	out := make([]byte, len(hidden))
	prev := reqAuth
	for i := 0; i < len(hidden); i += 16 {
		h := md5.New()
		h.Write(s.secret)
		h.Write(prev)
		b := h.Sum(nil)
		for j := range 16 {
			out[i+j] = hidden[i+j] ^ b[j]
		}
		prev = hidden[i : i+16]
	}
	return bytes.TrimRight(out, "\x00"), true
}

// pap answers a PAP request: Accept with the user's address when the
// password is theirs, Reject otherwise. A request is only ever believed by
// its password: one forged without the shared secret cannot carry a
// password that decodes to the right one.
func (s *Server) pap(p *packet) []byte {
	name := string(p.get(attrUserName))
	got, ok := s.papPassword(p.auth, p.get(attrUserPassword))
	s.mu.Lock()
	lookup, onAccept := s.lookup, s.onAccept
	s.mu.Unlock()
	if !ok || name == "" || lookup == nil {
		return s.reject(p, 0)
	}
	u, known := lookup(name)
	if !known || subtle.ConstantTimeCompare(got, []byte(u.Password)) != 1 {
		s.log.Info("a RADIUS sign-in was refused", "user", name)
		return s.reject(p, 0)
	}
	if onAccept != nil {
		onAccept(name)
	}
	return s.build(codeAccessAccept, p, papAttrs(u))
}

// papAttrs are what an Accept tells the server of the user: its address,
// and its resolvers.
func papAttrs(u User) []attr {
	var attrs []attr
	if u.IP.Is4() {
		a4 := u.IP.As4()
		attrs = append(attrs, attr{attrFramedIP, a4[:]})
	}
	for i, d := range u.DNS {
		if i > 1 || !d.Is4() {
			break
		}
		attrs = append(attrs, msDNS(i, d))
	}
	return attrs
}

func msDNS(i int, d netip.Addr) attr {
	d4 := d.As4()
	v := []byte{0, 0, 1, 55, byte(msPrimaryDNS + i), 6} // vendor 311
	return attr{attrVendor, append(v, d4[:]...)}
}
