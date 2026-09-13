// Package dnsproxy is the resolver handed to customers inside the tunnel.
//
// A VPN that leaves DNS to whatever the device had before leaks every name
// the customer looks up to their old resolver, outside the tunnel. This
// answers on the tunnel's own gateway address instead: names the operator
// pinned are answered here, the rest are forwarded to the upstreams the
// operator chose -- one per domain list, or the general ones -- and cached.
package dnsproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Config is what the DNS page saves.
type Config struct {
	Enabled bool `json:"enabled"`
	// QueryStrategy is UseIP, UseIPv4 or UseIPv6: which families are
	// answered. The other family gets an empty answer, which is what makes
	// a client stop trying it.
	QueryStrategy string `json:"queryStrategy"`
	DisableCache  bool   `json:"disableCache"`
	// ServeExpiredTTL is how many seconds past its TTL a cached answer may
	// still be served while a fresh one is fetched; 0 never serves stale.
	ServeExpiredTTL int      `json:"serveExpiredTTL"`
	Hosts           []Host   `json:"hosts"`
	Servers         []Server `json:"servers"`
}

// Host pins a name to addresses.
type Host struct {
	Domain string   `json:"domain"`
	Values []string `json:"values"`
}

// Server is one upstream. Address is an IP, ip:port, tcp://ip[:port] or an
// https:// DNS-over-HTTPS URL. Domains, when given, are the names this
// server is asked about; a server without them takes everything else.
type Server struct {
	Address      string   `json:"address"`
	Port         int      `json:"port"`
	Domains      []string `json:"domains"`
	SkipFallback bool     `json:"skipFallback"`
}

// Proxy is the resolver: any number of listeners sharing one configuration
// and one cache.
type Proxy struct {
	log *slog.Logger

	mu        sync.RWMutex
	cfg       Config
	listeners map[netip.Addr]*listener
	cache     map[string]*entry
	http      *http.Client
	warned    map[string]bool
	// mark is stamped on every upstream socket, so the query leaves the
	// way customer traffic leaves: through the default outbound when the
	// operator has put one first. Zero is the server's own route.
	mark uint32
}

type listener struct {
	udp *net.UDPConn
	tcp net.Listener
}

type entry struct {
	msg     []byte
	expires time.Time
	fetch   bool // a refresh is under way
}

// New makes a proxy that serves nothing until Reconfigure.
func New(log *slog.Logger) *Proxy {
	return &Proxy{
		log:       log,
		listeners: map[netip.Addr]*listener{},
		cache:     map[string]*entry{},
		http:      &http.Client{Timeout: 5 * time.Second},
		warned:    map[string]bool{},
	}
}

// SetMark chooses the routing mark upstream queries carry.
func (p *Proxy) SetMark(mark uint32) {
	p.mu.Lock()
	if p.mark != mark {
		p.mark = mark
		p.http = p.httpClient()
		p.cache = map[string]*entry{}
	}
	p.mu.Unlock()
}

// httpClient is the DoH client for the current mark.
func (p *Proxy) httpClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: markControl(p.mark)}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: d.DialContext}}
}

// Reconfigure applies a configuration and the set of addresses to answer
// on. Listeners that are no longer wanted are closed; new ones opened. Bind
// failures are logged once per address and the rest carry on.
func (p *Proxy) Reconfigure(cfg Config, addrs []netip.Addr) {
	p.mu.Lock()
	p.cfg = cfg
	if cfg.DisableCache {
		p.cache = map[string]*entry{}
	}
	want := map[netip.Addr]bool{}
	if cfg.Enabled && len(cfg.Servers) > 0 {
		for _, a := range addrs {
			want[a] = true
		}
	}
	for a, l := range p.listeners {
		if !want[a] {
			l.close()
			delete(p.listeners, a)
			p.log.Info("dns: stopped answering", "address", a)
		}
	}
	for a := range want {
		if _, ok := p.listeners[a]; ok {
			continue
		}
		l, err := p.open(a)
		if err != nil {
			if !p.warned[a.String()] {
				p.warned[a.String()] = true
				p.log.Warn("dns: cannot answer on the tunnel address", "address", a, "error", err,
					"fix", "the service needs CAP_NET_BIND_SERVICE, and nothing else may hold port 53 there")
			}
			continue
		}
		delete(p.warned, a.String())
		p.listeners[a] = l
		p.log.Info("dns: answering", "address", a)
	}
	p.mu.Unlock()
}

// Close stops every listener.
func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for a, l := range p.listeners {
		l.close()
		delete(p.listeners, a)
	}
}

// Listening reports the addresses currently answering.
func (p *Proxy) Listening() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.listeners))
	for a := range p.listeners {
		out = append(out, a.String())
	}
	return out
}

func (l *listener) close() {
	if l.udp != nil {
		l.udp.Close()
	}
	if l.tcp != nil {
		l.tcp.Close()
	}
}

func (p *Proxy) open(a netip.Addr) (*listener, error) {
	udp, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, 53)))
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp", net.JoinHostPort(a.String(), "53"))
	if err != nil {
		udp.Close()
		return nil, err
	}
	l := &listener{udp: udp, tcp: tcp}
	go p.serveUDP(udp)
	go p.serveTCP(tcp)
	return l, nil
}

func (p *Proxy) serveUDP(c *net.UDPConn) {
	buf := make([]byte, 4096)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return
		}
		q := append([]byte(nil), buf[:n]...)
		go func() {
			if resp := p.handle(q, false); resp != nil {
				_, _ = c.WriteToUDP(resp, from)
			}
		}()
	}
}

func (p *Proxy) serveTCP(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			var lenb [2]byte
			if _, err := io.ReadFull(conn, lenb[:]); err != nil {
				return
			}
			n := int(lenb[0])<<8 | int(lenb[1])
			q := make([]byte, n)
			if _, err := io.ReadFull(conn, q); err != nil {
				return
			}
			resp := p.handle(q, true)
			if resp == nil {
				return
			}
			out := make([]byte, 2+len(resp))
			out[0], out[1] = byte(len(resp)>>8), byte(len(resp))
			copy(out[2:], resp)
			_, _ = conn.Write(out)
		}()
	}
}

// handle answers one query: from the hosts, the cache, or an upstream.
func (p *Proxy) handle(q []byte, tcp bool) []byte {
	var parser dnsmessage.Parser
	hdr, err := parser.Start(q)
	if err != nil {
		return nil
	}
	qs, err := parser.AllQuestions()
	if err != nil || len(qs) == 0 {
		return p.failure(hdr, nil, dnsmessage.RCodeFormatError)
	}
	question := qs[0]
	name := strings.TrimSuffix(strings.ToLower(question.Name.String()), ".")

	p.mu.RLock()
	cfg := p.cfg
	p.mu.RUnlock()

	// The family the strategy refuses gets an empty, successful answer.
	switch {
	case cfg.QueryStrategy == "UseIPv4" && question.Type == dnsmessage.TypeAAAA,
		cfg.QueryStrategy == "UseIPv6" && question.Type == dnsmessage.TypeA:
		return p.empty(hdr, question)
	}

	if ans := p.fromHosts(cfg, hdr, question, name); ans != nil {
		return ans
	}

	key := name + "/" + strconv.Itoa(int(question.Type))
	if !cfg.DisableCache {
		if msg, ok := p.cached(key, cfg.ServeExpiredTTL, func() { p.refresh(cfg, key, q, name) }); ok {
			return withID(msg, hdr.ID)
		}
	}
	resp, err := p.forward(cfg, q, name)
	if err != nil {
		return p.failure(hdr, &question, dnsmessage.RCodeServerFailure)
	}
	if !cfg.DisableCache {
		p.store(key, resp)
	}
	return resp
}

// fromHosts answers a pinned name. A pin with no address of the asked
// family gives an empty answer, so the client does not fall through to the
// real one.
func (p *Proxy) fromHosts(cfg Config, hdr dnsmessage.Header, question dnsmessage.Question, name string) []byte {
	var hit *Host
	for i := range cfg.Hosts {
		h := &cfg.Hosts[i]
		d := strings.ToLower(strings.TrimSpace(h.Domain))
		if d == "" {
			continue
		}
		if d == name || (strings.HasPrefix(d, "*.") && strings.HasSuffix(name, d[1:])) {
			hit = h
			break
		}
	}
	if hit == nil {
		return nil
	}
	if question.Type != dnsmessage.TypeA && question.Type != dnsmessage.TypeAAAA {
		return p.empty(hdr, question)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired, RecursionAvailable: true})
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(question)
	_ = b.StartAnswers()
	for _, v := range hit.Values {
		addr, err := netip.ParseAddr(strings.TrimSpace(v))
		if err != nil {
			continue
		}
		rh := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}
		if addr.Is4() && question.Type == dnsmessage.TypeA {
			_ = b.AResource(rh, dnsmessage.AResource{A: addr.As4()})
		} else if addr.Is6() && question.Type == dnsmessage.TypeAAAA {
			_ = b.AAAAResource(rh, dnsmessage.AAAAResource{AAAA: addr.As16()})
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

func (p *Proxy) empty(hdr dnsmessage.Header, question dnsmessage.Question) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: hdr.ID, Response: true, RecursionDesired: hdr.RecursionDesired, RecursionAvailable: true})
	_ = b.StartQuestions()
	_ = b.Question(question)
	out, _ := b.Finish()
	return out
}

func (p *Proxy) failure(hdr dnsmessage.Header, question *dnsmessage.Question, code dnsmessage.RCode) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: hdr.ID, Response: true, RecursionDesired: hdr.RecursionDesired, RecursionAvailable: true, RCode: code})
	if question != nil {
		_ = b.StartQuestions()
		_ = b.Question(*question)
	}
	out, _ := b.Finish()
	return out
}

// cached returns a stored answer while it is fresh -- or, within the stale
// allowance, while a refresh is started in the background.
func (p *Proxy) cached(key string, staleSec int, refresh func()) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[key]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if now.Before(e.expires) {
		return e.msg, true
	}
	if staleSec > 0 && now.Before(e.expires.Add(time.Duration(staleSec)*time.Second)) {
		if !e.fetch {
			e.fetch = true
			go refresh()
		}
		return e.msg, true
	}
	delete(p.cache, key)
	return nil, false
}

func (p *Proxy) refresh(cfg Config, key string, q []byte, name string) {
	resp, err := p.forward(cfg, q, name)
	p.mu.Lock()
	if e, ok := p.cache[key]; ok {
		e.fetch = false
	}
	p.mu.Unlock()
	if err == nil {
		p.store(key, resp)
	}
}

// store keeps an answer for the smallest TTL in it, and never for less
// than a few seconds so a burst of the same lookup is one upstream query.
func (p *Proxy) store(key string, msg []byte) {
	ttl := uint32(300)
	var parser dnsmessage.Parser
	if _, err := parser.Start(msg); err == nil {
		_ = parser.SkipAllQuestions()
		for {
			h, err := parser.AnswerHeader()
			if err != nil {
				break
			}
			if h.TTL < ttl {
				ttl = h.TTL
			}
			_ = parser.SkipAnswer()
		}
	}
	if ttl < 5 {
		ttl = 5
	}
	p.mu.Lock()
	if len(p.cache) > 20000 {
		p.cache = map[string]*entry{}
	}
	p.cache[key] = &entry{msg: msg, expires: time.Now().Add(time.Duration(ttl) * time.Second)}
	p.mu.Unlock()
}

// forward asks the upstreams in order: those whose domain lists match the
// name first, then the general ones, stopping at the first that answers.
func (p *Proxy) forward(cfg Config, q []byte, name string) ([]byte, error) {
	var matched, general []Server
	for _, s := range cfg.Servers {
		if strings.TrimSpace(s.Address) == "" {
			continue
		}
		if len(s.Domains) == 0 {
			general = append(general, s)
			continue
		}
		for _, d := range s.Domains {
			if matchDomain(name, d) {
				matched = append(matched, s)
				break
			}
		}
	}
	order := append(matched, general...)
	if len(order) == 0 {
		return nil, errors.New("dns: no upstream")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var last error
	for i, s := range order {
		resp, err := p.ask(ctx, s, q)
		if err == nil {
			return resp, nil
		}
		last = err
		if s.SkipFallback && i < len(matched) {
			break
		}
	}
	return nil, last
}

// matchDomain is a suffix match, with the forms 3x-ui's lists use:
// "example.com" (and its subdomains), "domain:example.com", "full:x.y" for
// exactly that name, "regexp:" is not supported and never matches.
func matchDomain(name, rule string) bool {
	rule = strings.ToLower(strings.TrimSpace(rule))
	switch {
	case strings.HasPrefix(rule, "full:"):
		return name == rule[5:]
	case strings.HasPrefix(rule, "domain:"):
		rule = rule[7:]
	case strings.HasPrefix(rule, "keyword:"):
		return strings.Contains(name, rule[8:])
	case strings.HasPrefix(rule, "regexp:"):
		return false
	}
	rule = strings.TrimPrefix(rule, "*.")
	return name == rule || strings.HasSuffix(name, "."+rule)
}

// ask sends one query to one upstream and returns the raw answer.
func (p *Proxy) ask(ctx context.Context, s Server, q []byte) ([]byte, error) {
	addr := strings.TrimSpace(s.Address)
	if strings.HasPrefix(addr, "https://") {
		return p.askDoH(ctx, addr, q)
	}
	proto := "udp"
	if strings.HasPrefix(addr, "tcp://") {
		proto, addr = "tcp", strings.TrimPrefix(addr, "tcp://")
	}
	addr = strings.TrimPrefix(addr, "udp://")
	port := s.Port
	if h, pt, err := net.SplitHostPort(addr); err == nil {
		addr = h
		if n, err := strconv.Atoi(pt); err == nil {
			port = n
		}
	}
	if port <= 0 {
		port = 53
	}
	target := net.JoinHostPort(strings.Trim(addr, "[]"), strconv.Itoa(port))
	p.mu.RLock()
	d := net.Dialer{Control: markControl(p.mark)}
	p.mu.RUnlock()
	conn, err := d.DialContext(ctx, proto, target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if proto == "tcp" {
		return exchangeTCP(conn, q)
	}
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	resp := buf[:n]
	// Truncated over UDP: ask again over TCP for the whole of it.
	if len(resp) >= 3 && resp[2]&0x02 != 0 {
		tc, err := d.DialContext(ctx, "tcp", target)
		if err == nil {
			defer tc.Close()
			if dl, ok := ctx.Deadline(); ok {
				_ = tc.SetDeadline(dl)
			}
			if full, err := exchangeTCP(tc, q); err == nil {
				return full, nil
			}
		}
	}
	return append([]byte(nil), resp...), nil
}

func exchangeTCP(conn net.Conn, q []byte) ([]byte, error) {
	out := make([]byte, 2+len(q))
	out[0], out[1] = byte(len(q)>>8), byte(len(q))
	copy(out[2:], q)
	if _, err := conn.Write(out); err != nil {
		return nil, err
	}
	var lenb [2]byte
	if _, err := io.ReadFull(conn, lenb[:]); err != nil {
		return nil, err
	}
	n := int(lenb[0])<<8 | int(lenb[1])
	resp := make([]byte, n)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (p *Proxy) askDoH(ctx context.Context, url string, q []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(q))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	p.mu.RLock()
	client := p.http
	p.mu.RUnlock()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dns: %s answered %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 65535))
}

// withID puts the asker's transaction id on a cached answer.
func withID(msg []byte, id uint16) []byte {
	out := append([]byte(nil), msg...)
	if len(out) >= 2 {
		out[0], out[1] = byte(id>>8), byte(id)
	}
	return out
}
