// Package radiusd is the RADIUS server strongSwan asks when a device signs
// in to the IKEv2 tunnel by username and password, and ocserv asks for an
// OpenConnect sign-in (PAP, pap.go).
//
// strongSwan picks a device's connection by the identity it announces, and
// a device signing in by password often announces none of its own --
// Windows announces its address, and Android and iOS only what someone typed
// in an optional field. So password sign-ins come in on one connection, and
// strongSwan hands the EAP exchange to this server (its eap-radius plugin):
// it learns the username from the exchange itself, checks the device's
// MS-CHAPv2 answer against that user's password, proves it knows the
// password too, and tells strongSwan the session keys and the device's own
// address -- which is what the kernel counts its traffic by.
//
// It listens on loopback only, for strongSwan on the same machine, and
// checks every packet's Message-Authenticator against the shared secret.
// The root agent runs it.
package radiusd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// RADIUS codes and attributes (RFC 2865, 2869, 3579, 2548).
const (
	codeAccessRequest   = 1
	codeAccessAccept    = 2
	codeAccessReject    = 3
	codeAccessChallenge = 11

	attrUserName      = 1
	attrFramedIP      = 8
	attrState         = 24
	attrVendor        = 26
	attrEAPMessage    = 79
	attrMessageAuth   = 80
	vendorMicrosoft   = 311
	msMPPESendKey     = 16
	msMPPERecvKey     = 17
	msPrimaryDNS      = 28 // and 29, the secondary
	maxPacket         = 4096
	headerLen         = 20
	sessionTTL        = 60 * time.Second
	replyCacheTTL     = 15 * time.Second
	eapRequest        = 1
	eapResponse       = 2
	eapSuccess        = 3
	eapFailure        = 4
	eapTypeIdentity   = 1
	eapTypeNak        = 3
	eapTypeMSCHAPv2   = 26
	opChallenge       = 1
	opResponse        = 2
	opSuccess         = 3
	opFailure         = 4
	serverName        = "wui"
	authChallengeSize = 16
)

// User is what the server knows of one device.
type User struct {
	Password string
	IP       netip.Addr
	// DNS are the resolvers the device is given, two at most
	// (MS-Primary- and MS-Secondary-DNS-Server, which strongSwan hands on).
	DNS []netip.Addr
}

// Lookup finds a device by its username.
type Lookup func(username string) (User, bool)

// Server is one running RADIUS server.
type Server struct {
	secret []byte
	conn   *net.UDPConn
	log    *slog.Logger

	mu       sync.Mutex
	lookup   Lookup
	onAccept OnAccept
	sessions map[string]*session
	replies  map[string]cachedReply
}

type session struct {
	identity  string
	eapID     byte
	challenge []byte
	user      string
	nt        []byte
	peer      []byte
	expires   time.Time
	succeeded bool
}

type cachedReply struct {
	body    []byte
	expires time.Time
}

// Listen starts a server on a loopback address ("127.0.0.1:port").
func Listen(addr string, secret []byte, lookup Lookup, log *slog.Logger) (*Server, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	if !ua.IP.IsLoopback() {
		return nil, errors.New("radiusd: listens on loopback only")
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	return &Server{secret: secret, conn: conn, log: log, lookup: lookup,
		sessions: map[string]*session{}, replies: map[string]cachedReply{}}, nil
}

// SetLookup replaces who the server knows: the tunnel's devices changed.
func (s *Server) SetLookup(l Lookup) {
	s.mu.Lock()
	s.lookup = l
	s.mu.Unlock()
}

// Close stops the server.
func (s *Server) Close() error { return s.conn.Close() }

// Serve answers until ctx ends or the server is closed.
func (s *Server) Serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.conn.Close()
	}()
	buf := make([]byte, maxPacket)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		reply, err := s.handle(pkt, from.String())
		if err != nil {
			s.log.Warn("a RADIUS request was refused", "error", err)
			continue
		}
		if reply != nil {
			_, _ = s.conn.WriteToUDP(reply, from)
		}
	}
}

// packet is a parsed RADIUS packet.
type packet struct {
	code, id byte
	auth     []byte
	attrs    []attr
}

type attr struct {
	t     byte
	value []byte
}

func parse(b []byte) (*packet, error) {
	if len(b) < headerLen {
		return nil, errors.New("short packet")
	}
	length := int(binary.BigEndian.Uint16(b[2:4]))
	if length < headerLen || length > len(b) {
		return nil, errors.New("bad length")
	}
	p := &packet{code: b[0], id: b[1], auth: b[4:20]}
	for i := headerLen; i < length; {
		if i+2 > length {
			return nil, errors.New("truncated attribute")
		}
		t, l := b[i], int(b[i+1])
		if l < 2 || i+l > length {
			return nil, errors.New("bad attribute length")
		}
		p.attrs = append(p.attrs, attr{t, b[i+2 : i+l]})
		i += l
	}
	return p, nil
}

func (p *packet) get(t byte) []byte {
	for _, a := range p.attrs {
		if a.t == t {
			return a.value
		}
	}
	return nil
}

// eap joins the EAP-Message attributes into one EAP packet.
func (p *packet) eap() []byte {
	var out []byte
	for _, a := range p.attrs {
		if a.t == attrEAPMessage {
			out = append(out, a.value...)
		}
	}
	return out
}

// verify checks a request's Message-Authenticator: HMAC-MD5 over the
// packet with the attribute's value zeroed.
func (s *Server) verify(raw []byte, p *packet) bool {
	mac := p.get(attrMessageAuth)
	if len(mac) != 16 {
		return false
	}
	z := append([]byte(nil), raw[:binary.BigEndian.Uint16(raw[2:4])]...)
	for i := headerLen; i < len(z); {
		t, l := z[i], int(z[i+1])
		if t == attrMessageAuth {
			for j := i + 2; j < i+l; j++ {
				z[j] = 0
			}
		}
		i += l
	}
	h := hmac.New(md5.New, s.secret)
	h.Write(z)
	return subtle.ConstantTimeCompare(h.Sum(nil), mac) == 1
}

func (s *Server) handle(raw []byte, from string) ([]byte, error) {
	p, err := parse(raw)
	if err != nil {
		return nil, err
	}
	if p.code != codeAccessRequest {
		return nil, fmt.Errorf("code %d is not an Access-Request", p.code)
	}
	// A Message-Authenticator, where there is one, has to be right. A PAP
	// request may come without (radcli sends none): it is believed only by
	// the password it carries, which cannot be right without the secret.
	pap := p.get(attrUserPassword) != nil && p.get(attrEAPMessage) == nil
	if p.get(attrMessageAuth) != nil || !pap {
		if !s.verify(raw, p) {
			return nil, errors.New("the Message-Authenticator does not check out")
		}
	}
	// A retransmission gets the same answer.
	key := from + string(p.id) + string(p.auth)
	now := time.Now()
	s.mu.Lock()
	for k, r := range s.replies {
		if now.After(r.expires) {
			delete(s.replies, k)
		}
	}
	if r, ok := s.replies[key]; ok {
		s.mu.Unlock()
		return r.body, nil
	}
	s.mu.Unlock()

	var reply []byte
	if pap {
		reply = s.pap(p)
	} else {
		reply = s.answer(p, now)
	}
	s.mu.Lock()
	s.replies[key] = cachedReply{body: reply, expires: now.Add(replyCacheTTL)}
	s.mu.Unlock()
	return reply, nil
}

// answer runs one step of EAP-MSCHAPv2 for a request.
func (s *Server) answer(p *packet, now time.Time) []byte {
	msg := p.eap()
	if len(msg) < 4 || msg[0] != eapResponse {
		return s.reject(p, 0)
	}
	eapID := msg[1]
	s.mu.Lock()
	for k, ss := range s.sessions {
		if now.After(ss.expires) {
			delete(s.sessions, k)
		}
	}
	var ss *session
	if st := p.get(attrState); st != nil {
		ss = s.sessions[string(st)]
	}
	lookup := s.lookup
	s.mu.Unlock()

	switch {
	case ss == nil && len(msg) >= 5 && msg[4] == eapTypeIdentity:
		// The device's identity: answered with a challenge.
		ss = &session{identity: string(msg[5:]), expires: now.Add(sessionTTL)}
		ss.challenge = make([]byte, authChallengeSize)
		if _, err := rand.Read(ss.challenge); err != nil {
			return s.reject(p, eapID)
		}
		ss.eapID = eapID + 1
		ms := []byte{opChallenge, ss.eapID, 0, 0, authChallengeSize}
		ms = append(ms, ss.challenge...)
		ms = append(ms, serverName...)
		binary.BigEndian.PutUint16(ms[2:4], uint16(len(ms)))
		return s.challenge(p, s.store(ss), eapPacket(eapRequest, ss.eapID, eapTypeMSCHAPv2, ms))

	case ss != nil && !ss.succeeded && len(msg) >= 5 && msg[4] == eapTypeMSCHAPv2 && len(msg) >= 5+4+1+49 && msg[5] == opResponse:
		// The device's answer: its username and its proof of the password.
		ms := msg[5:]
		if ms[4] != 49 {
			return s.reject(p, eapID)
		}
		resp := ms[5 : 5+49]
		peer, nt := resp[0:16], resp[24:48]
		name := string(ms[5+49:])
		if i := strings.LastIndexByte(name, '\\'); i >= 0 {
			name = name[i+1:]
		}
		u, ok := lookup(name)
		if !ok || !u.IP.IsValid() || !hmac.Equal(nt, ntResponse(ss.challenge, peer, name, u.Password)) {
			s.log.Info("an IKEv2 sign-in was refused", "username", name)
			s.forget(p)
			return s.reject(p, eapID)
		}
		ss.user, ss.nt, ss.peer, ss.succeeded = name, append([]byte(nil), nt...), append([]byte(nil), peer...), true
		ss.eapID = eapID + 1
		// Each state is used once: the one this answer came with is retired
		// before the next is handed out. Left in place it named a session
		// whose password was proved, and a success sent with it was let in
		// again, without the password, until it expired.
		s.forget(p)
		ok2 := []byte(authenticatorResponse(u.Password, nt, peer, ss.challenge, name) + " M=OK")
		sm := []byte{opSuccess, ms[1], 0, 0}
		sm = append(sm, ok2...)
		binary.BigEndian.PutUint16(sm[2:4], uint16(len(sm)))
		return s.challenge(p, s.store(ss), eapPacket(eapRequest, ss.eapID, eapTypeMSCHAPv2, sm))

	case ss != nil && ss.succeeded && len(msg) >= 6 && msg[4] == eapTypeMSCHAPv2 && msg[5] == opSuccess:
		// The device accepts the server's proof: in, with its keys and its
		// address.
		u, ok := lookup(ss.user)
		s.forget(p)
		if !ok {
			return s.reject(p, eapID)
		}
		recv, send := serverKeys(u.Password, ss.nt)
		return s.accept(p, eapPacket(eapSuccess, eapID, 0, nil), u, recv, send)
	}
	s.forget(p)
	return s.reject(p, eapID)
}

func (s *Server) store(ss *session) []byte {
	st := make([]byte, 16)
	_, _ = rand.Read(st)
	s.mu.Lock()
	s.sessions[string(st)] = ss
	s.mu.Unlock()
	return st
}

func (s *Server) forget(p *packet) {
	if st := p.get(attrState); st != nil {
		s.mu.Lock()
		delete(s.sessions, string(st))
		s.mu.Unlock()
	}
}

// eapPacket builds an EAP packet; a Success or Failure has no type.
func eapPacket(code, id, typ byte, data []byte) []byte {
	if code == eapSuccess || code == eapFailure {
		return []byte{code, id, 0, 4}
	}
	b := []byte{code, id, 0, 0, typ}
	b = append(b, data...)
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	return b
}

func (s *Server) challenge(req *packet, state, eap []byte) []byte {
	return s.build(codeAccessChallenge, req, append(eapAttrs(eap), attr{attrState, state}))
}

func (s *Server) reject(req *packet, eapID byte) []byte {
	return s.build(codeAccessReject, req, eapAttrs(eapPacket(eapFailure, eapID, 0, nil)))
}

func (s *Server) accept(req *packet, eap []byte, u User, recv, send []byte) []byte {
	attrs := eapAttrs(eap)
	a4 := u.IP.As4()
	attrs = append(attrs, attr{attrFramedIP, a4[:]})
	attrs = append(attrs, s.mppeKey(req.auth, msMPPERecvKey, recv), s.mppeKey(req.auth, msMPPESendKey, send))
	for i, d := range u.DNS {
		if i > 1 || !d.Is4() {
			break
		}
		v := make([]byte, 6, 10)
		binary.BigEndian.PutUint32(v[0:4], vendorMicrosoft)
		v[4] = byte(msPrimaryDNS + i)
		v[5] = 6
		d4 := d.As4()
		attrs = append(attrs, attr{attrVendor, append(v, d4[:]...)})
	}
	return s.build(codeAccessAccept, req, attrs)
}

// eapAttrs splits an EAP packet over EAP-Message attributes of 253 bytes.
func eapAttrs(eap []byte) []attr {
	var out []attr
	for len(eap) > 0 {
		n := min(len(eap), 253)
		out = append(out, attr{attrEAPMessage, eap[:n]})
		eap = eap[n:]
	}
	return out
}

// mppeKey is an MS-MPPE key attribute, encrypted as RFC 2548 says: a salt,
// then the length and key, padded, XORed with an MD5 chain of the secret.
func (s *Server) mppeKey(reqAuth []byte, which byte, key []byte) attr {
	salt := make([]byte, 2)
	_, _ = rand.Read(salt)
	salt[0] |= 0x80
	plain := append([]byte{byte(len(key))}, key...)
	for len(plain)%16 != 0 {
		plain = append(plain, 0)
	}
	var out []byte
	prev := append(append([]byte(nil), reqAuth...), salt...)
	for i := 0; i < len(plain); i += 16 {
		h := md5.New()
		h.Write(s.secret)
		h.Write(prev)
		b := h.Sum(nil)
		c := make([]byte, 16)
		for j := range c {
			c[j] = plain[i+j] ^ b[j]
		}
		out = append(out, c...)
		prev = c
	}
	v := make([]byte, 6, 6+2+2+len(out))
	binary.BigEndian.PutUint32(v[0:4], vendorMicrosoft)
	v[4] = which
	v[5] = byte(2 + 2 + len(out))
	v = append(v, salt...)
	v = append(v, out...)
	return attr{attrVendor, v}
}

// build writes a response: its Message-Authenticator, then its
// Response Authenticator, both over the request's authenticator.
func (s *Server) build(code byte, req *packet, attrs []attr) []byte {
	attrs = append(attrs, attr{attrMessageAuth, make([]byte, 16)})
	var b bytes.Buffer
	b.Write([]byte{code, req.id, 0, 0})
	b.Write(req.auth)
	for _, a := range attrs {
		b.WriteByte(a.t)
		b.WriteByte(byte(2 + len(a.value)))
		b.Write(a.value)
	}
	out := b.Bytes()
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
	h := hmac.New(md5.New, s.secret)
	h.Write(out)
	copy(out[len(out)-16:], h.Sum(nil))
	m := md5.New()
	m.Write(out)
	m.Write(s.secret)
	copy(out[4:20], m.Sum(nil))
	return out
}
