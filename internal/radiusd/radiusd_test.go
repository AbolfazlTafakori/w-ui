package radiusd

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"io"
	"log/slog"
	"net/netip"
	"testing"
)

// The server's decisions, as strongSwan and ocserv meet them: who is let in,
// who is not, and what a forged, broken or replayed packet gets.

var testSecret = []byte("shared radius secret")

func testServer(users map[string]User) *Server {
	return &Server{
		secret:   testSecret,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		lookup:   func(u string) (User, bool) { v, ok := users[u]; return v, ok },
		sessions: map[string]*session{},
		replies:  map[string]cachedReply{},
	}
}

var ali = User{Password: "Secret11", IP: netip.MustParseAddr("10.77.0.2"),
	DNS: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}}

// request is an Access-Request from a client that knows secret; withMAC adds
// a Message-Authenticator.
func request(secret []byte, id byte, attrs []attr, withMAC bool) []byte {
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return requestWith(secret, id, auth, attrs, withMAC)
}

func requestWith(secret []byte, id byte, auth []byte, attrs []attr, withMAC bool) []byte {
	if withMAC {
		attrs = append(attrs, attr{attrMessageAuth, make([]byte, 16)})
	}
	var b bytes.Buffer
	b.Write([]byte{codeAccessRequest, id, 0, 0})
	b.Write(auth)
	for _, a := range attrs {
		b.WriteByte(a.t)
		b.WriteByte(byte(2 + len(a.value)))
		b.Write(a.value)
	}
	out := b.Bytes()
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)))
	if withMAC {
		h := hmac.New(md5.New, secret)
		h.Write(out)
		copy(out[len(out)-16:], h.Sum(nil))
	}
	return out
}

// hide is a PAP password as RFC 2865 5.2 hides it.
func hide(secret, auth []byte, password string) []byte {
	p := []byte(password)
	for len(p) == 0 || len(p)%16 != 0 {
		p = append(p, 0)
	}
	out := make([]byte, len(p))
	prev := auth
	for i := 0; i < len(p); i += 16 {
		h := md5.New()
		h.Write(secret)
		h.Write(prev)
		b := h.Sum(nil)
		for j := 0; j < 16; j++ {
			out[i+j] = p[i+j] ^ b[j]
		}
		prev = out[i : i+16]
	}
	return out
}

func papRequest(secret []byte, user, password string, withMAC bool) []byte {
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return requestWith(secret, 7, auth, []attr{
		{attrUserName, []byte(user)},
		{attrUserPassword, hide(secret, auth, password)},
	}, withMAC)
}

// answerOf parses a reply and checks its Response Authenticator, which only
// the holder of the secret can write.
func answerOf(t *testing.T, req, reply []byte) *packet {
	t.Helper()
	p, err := parse(reply)
	if err != nil {
		t.Fatalf("the reply does not parse: %v", err)
	}
	check := append([]byte(nil), reply...)
	copy(check[4:20], req[4:20])
	m := md5.New()
	m.Write(check)
	m.Write(testSecret)
	if !bytes.Equal(m.Sum(nil), reply[4:20]) {
		t.Fatal("the reply's authenticator does not check out")
	}
	return p
}

func TestAPAPSignInWithTheRightPasswordIsLetIn(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	var told string
	s.SetOnAccept(func(u string) { told = u })
	for _, withMAC := range []bool{false, true} {
		req := papRequest(testSecret, "s1-ali", "Secret11", withMAC)
		reply, err := s.handle(req, "127.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		p := answerOf(t, req, reply)
		if p.code != codeAccessAccept {
			t.Fatalf("code %d", p.code)
		}
		if ip := p.get(attrFramedIP); !bytes.Equal(ip, []byte{10, 77, 0, 2}) {
			t.Fatalf("given address %v", ip)
		}
		if told != "s1-ali" {
			t.Fatalf("the agent was not told of the sign-in: %q", told)
		}
	}
}

func TestAPAPSignInIsRefusedWhenItShouldBe(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	called := false
	s.SetOnAccept(func(string) { called = true })
	for name, req := range map[string][]byte{
		"a wrong password":                papRequest(testSecret, "s1-ali", "Secret12", false),
		"the password with more after it": papRequest(testSecret, "s1-ali", "Secret11x", false),
		"an empty password":               papRequest(testSecret, "s1-ali", "", false),
		"a user nobody has":               papRequest(testSecret, "s9-nobody", "Secret11", false),
		"no username":                     papRequest(testSecret, "", "Secret11", false),
		"hidden with another secret":      papRequest([]byte("not the secret"), "s1-ali", "Secret11", false),
	} {
		reply, err := s.handle(req, "127.0.0.1:2")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p := answerOf(t, req, reply); p.code != codeAccessReject {
			t.Errorf("%s: code %d", name, p.code)
		}
	}
	if called {
		t.Fatal("the agent was told of a refused sign-in")
	}
}

// A Message-Authenticator that is there has to be right, and a packet that
// is not an Access-Request, or does not parse, is dropped without an answer.
func TestForgedAndBrokenPacketsGetNoAnswer(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	forged := papRequest([]byte("not the secret"), "s1-ali", "Secret11", true)
	if reply, err := s.handle(forged, "127.0.0.1:3"); err == nil || reply != nil {
		t.Fatal("a Message-Authenticator made without the secret was answered")
	}
	notRequest := papRequest(testSecret, "s1-ali", "Secret11", false)
	notRequest[0] = codeAccessAccept
	if _, err := s.handle(notRequest, "127.0.0.1:3"); err == nil {
		t.Fatal("an Access-Accept sent to the server was taken as a request")
	}
	good := papRequest(testSecret, "s1-ali", "Secret11", true)
	broken := map[string][]byte{
		"empty":                 {},
		"shorter than a header": good[:10],
		"length past the end":   append(append([]byte{}, good[:2]...), append([]byte{0xff, 0xff}, good[4:]...)...),
		"an attribute of length 1": func() []byte {
			b := append([]byte{}, good...)
			b[headerLen+1] = 1
			return b
		}(),
	}
	for name, b := range broken {
		if reply, err := s.handle(b, "127.0.0.1:3"); err == nil || reply != nil {
			t.Errorf("%s: answered", name)
		}
	}
	// And nothing anybody can send makes it fall over.
	for i := 0; i < 2000; i++ {
		b := make([]byte, 20+i%300)
		_, _ = rand.Read(b)
		b[0] = codeAccessRequest
		binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
		_, _ = s.handle(b, "127.0.0.1:4")
	}
}

// A retransmission -- the same id and authenticator from the same place --
// gets the same answer, not a second decision.
func TestARetransmissionGetsTheSameAnswer(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	n := 0
	s.SetOnAccept(func(string) { n++ })
	req := papRequest(testSecret, "s1-ali", "Secret11", false)
	a, _ := s.handle(req, "127.0.0.1:5")
	b, _ := s.handle(req, "127.0.0.1:5")
	if !bytes.Equal(a, b) || n != 1 {
		t.Fatalf("answered twice (%d sign-ins), same %v", n, bytes.Equal(a, b))
	}
}

// EAP-MSCHAPv2, as strongSwan relays it for an IKEv2 sign-in.

type eapClient struct {
	t     *testing.T
	s     *Server
	state []byte
	eapID byte
	id    byte
}

func (c *eapClient) send(eap []byte) *packet {
	c.t.Helper()
	attrs := eapAttrs(eap)
	if c.state != nil {
		attrs = append(attrs, attr{attrState, c.state})
	}
	c.id++
	req := request(testSecret, c.id, attrs, true)
	reply, err := c.s.handle(req, "127.0.0.1:500")
	if err != nil {
		c.t.Fatal(err)
	}
	p := answerOf(c.t, req, reply)
	c.state = p.get(attrState)
	if m := p.eap(); len(m) >= 2 {
		c.eapID = m[1]
	}
	return p
}

// signIn runs the exchange as a device that knows password, and is the
// server's last answer.
func signIn(t *testing.T, s *Server, user, password string) *packet {
	t.Helper()
	c := &eapClient{t: t, s: s}
	p := c.send(append([]byte{eapResponse, 1, 0, 0, eapTypeIdentity}, "phone"...))
	if p.code != codeAccessChallenge {
		t.Fatalf("identity: code %d", p.code)
	}
	m := p.eap()
	challenge := m[5+5 : 5+5+16]
	peer := make([]byte, 16)
	_, _ = rand.Read(peer)
	nt := ntResponse(challenge, peer, lastName(user), password)
	resp := append(append(append([]byte{}, peer...), make([]byte, 8)...), nt...)
	resp = append(resp, 0)
	ms := []byte{opResponse, m[6], 0, 0, 49}
	ms = append(ms, resp...)
	ms = append(ms, user...)
	binary.BigEndian.PutUint16(ms[2:4], uint16(len(ms)))
	p = c.send(eapPacket(eapResponse, c.eapID, eapTypeMSCHAPv2, ms))
	if p.code != codeAccessChallenge {
		return p
	}
	return c.send(eapPacket(eapResponse, c.eapID, eapTypeMSCHAPv2, []byte{opSuccess}))
}

func lastName(u string) string {
	if i := bytes.LastIndexByte([]byte(u), '\\'); i >= 0 {
		return u[i+1:]
	}
	return u
}

func TestAnIKEv2SignInWithTheRightPasswordIsLetIn(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	for _, user := range []string{"s1-ali", `WORKGROUP\s1-ali`} {
		p := signIn(t, s, user, "Secret11")
		if p.code != codeAccessAccept {
			t.Fatalf("%s: code %d", user, p.code)
		}
		if ip := p.get(attrFramedIP); !bytes.Equal(ip, []byte{10, 77, 0, 2}) {
			t.Fatalf("%s: address %v", user, ip)
		}
		keys := 0
		for _, a := range p.attrs {
			if a.t == attrVendor && len(a.value) > 5 && (a.value[4] == msMPPESendKey || a.value[4] == msMPPERecvKey) {
				keys++
			}
		}
		if keys != 2 {
			t.Fatalf("%s: %d session keys", user, keys)
		}
	}
	if len(s.sessions) != 0 {
		t.Fatalf("%d sessions left behind", len(s.sessions))
	}
}

func TestAnIKEv2SignInIsRefusedWhenItShouldBe(t *testing.T) {
	noAddress := ali
	noAddress.IP = netip.Addr{}
	s := testServer(map[string]User{"s1-ali": ali, "s2-none": noAddress})
	for name, tc := range map[string][2]string{
		"a wrong password":       {"s1-ali", "Secret12"},
		"a user nobody has":      {"s9-nobody", "Secret11"},
		"a user with no address": {"s2-none", "Secret11"},
	} {
		if p := signIn(t, s, tc[0], tc[1]); p.code != codeAccessReject {
			t.Errorf("%s: code %d", name, p.code)
		}
	}
}

// A state the server did not hand out, or one already used to sign in, is
// refused: a captured exchange cannot be played again.
func TestAnExchangeCannotBePlayedAgain(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	c := &eapClient{t: t, s: s}
	c.send(append([]byte{eapResponse, 1, 0, 0, eapTypeIdentity}, "phone"...))
	c.state = []byte("a state nobody handed out")
	if p := c.send(eapPacket(eapResponse, c.eapID, eapTypeMSCHAPv2, []byte{opSuccess})); p.code != codeAccessReject {
		t.Fatalf("an invented state: code %d", p.code)
	}
	// An EAP request with no Message-Authenticator is not believed at all.
	req := request(testSecret, 9, eapAttrs(append([]byte{eapResponse, 1, 0, 0, eapTypeIdentity}, "x"...)), false)
	if reply, err := s.handle(req, "127.0.0.1:501"); err == nil || reply != nil {
		t.Fatal("an EAP request without a Message-Authenticator was answered")
	}
}

// The state an exchange moved on from is spent: a success sent with it, after
// the password was proved, is not a second way in.
func TestASpentStateIsNotASecondWayIn(t *testing.T) {
	s := testServer(map[string]User{"s1-ali": ali})
	c := &eapClient{t: t, s: s}
	p := c.send(append([]byte{eapResponse, 1, 0, 0, eapTypeIdentity}, "phone"...))
	m := p.eap()
	challenge := m[10:26]
	peer := make([]byte, 16)
	_, _ = rand.Read(peer)
	resp := append(append(append([]byte{}, peer...), make([]byte, 8)...), ntResponse(challenge, peer, "s1-ali", "Secret11")...)
	ms := append([]byte{opResponse, m[6], 0, 0, 49}, append(resp, 0)...)
	ms = append(ms, "s1-ali"...)
	binary.BigEndian.PutUint16(ms[2:4], uint16(len(ms)))
	spent := c.state
	if p := c.send(eapPacket(eapResponse, c.eapID, eapTypeMSCHAPv2, ms)); p.code != codeAccessChallenge {
		t.Fatalf("the proof: code %d", p.code)
	}
	c.state = spent
	if p := c.send(eapPacket(eapResponse, c.eapID, eapTypeMSCHAPv2, []byte{opSuccess})); p.code != codeAccessReject {
		t.Fatalf("a success with the spent state: code %d", p.code)
	}
}

func TestItListensOnLoopbackOnly(t *testing.T) {
	if _, err := Listen("0.0.0.0:0", testSecret, nil, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("listened on every address")
	}
	s, err := Listen("127.0.0.1:0", testSecret, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
