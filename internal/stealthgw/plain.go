package stealthgw

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The bare gateway.
//
// bare is drift for a network where TLS itself is what gets a
// connection noticed or cut: the device speaks plain HTTP to the operator's
// web server, which hands the gateway the request as drift's front does.
// Everything between the device and the front can be read and written by
// whoever is on the path, so the transport differs from drift in what the
// network sees, never in what protects the session:
//
//   - The token travels in a cookie, under a name of the transport's own,
//     and the target is the path alone: a target is in every log on the
//     way, and a request whose query holds anything is not a device's.
//   - The proof names bare, so a token seen here opens on no covered
//     transport, and the transport has a key of its own, turned over more
//     often than a covered one's, since every proof it opens was seen.
//   - The answer to a proof sets no cookie: on a plain path a cookie the
//     client never sends back is one more thing to read.
//   - A decoy is required. A stranger's answer crosses the network in the
//     clear, so it must be the site's, never one the gateway made.
//
// What a reader of the path learns is that a request with a cookie of random
// text was upgraded to WebSocket, and the sizes and times of what followed.
// What it cannot do is open a session with a token it saw: the token is
// taken once, and the session's keys need the device's secret as well as
// the token's own. A forged or changed frame ends the session; it never
// reaches the tunnel.

// PlainConfig is one tunnel's bare gateway: drift's settings, with a
// decoy that must be set, and the cookie a device's token comes in.
type PlainConfig struct {
	StreamConfig
	// Cookie is the name of the cookie a device's request carries its token
	// in: 1 to stealth.CookieMax characters of A-Z a-z 0-9 - _.
	Cookie string
}

// PlainGateway is bare in front of one WireGuard tunnel. Its devices,
// sessions, limits and replay rule are drift's, and so is everything after
// the upgrade.
type PlainGateway struct {
	*StreamGateway
	cookie string
}

// NewPlain prepares a gateway. Nothing listens until Serve.
func NewPlain(cfg PlainConfig) (*PlainGateway, error) {
	if !stealth.CookieName(cfg.Cookie) {
		return nil, errors.New("stealthgw: bare's cookie is named by 1 to 32 characters of A-Z a-z 0-9 - _")
	}
	if cfg.Origin == "" {
		return nil, errors.New("stealthgw: bare needs a decoy: a stranger's answer crosses the network in the clear, so it must be the site's own")
	}
	decoy, err := newDecoy(cfg.Origin)
	if err != nil {
		return nil, err
	}
	g, err := newStreamGateway(cfg.StreamConfig, stealth.Bare)
	if err != nil {
		return nil, err
	}
	g.decoy = decoy
	return &PlainGateway{StreamGateway: g, cookie: cfg.Cookie}, nil
}

// Serve runs the gateway until ctx ends.
func (p *PlainGateway) Serve(ctx context.Context) error {
	g := p.StreamGateway
	if err := g.Listen(); err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           p,
		ReadHeaderTimeout: headerFor,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    headerMax,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	err := g.serve(ctx, func() error { return srv.Serve(g.ln) }, func() { srv.Close() })
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ServeHTTP takes one request: a proof opens a session, and anything else
// goes to the decoy as it came.
func (p *PlainGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g := p.StreamGateway
	now := time.Now()
	peer := addrOf(r.RemoteAddr)
	key, upgrade := stealth.WebSocketKey(r)
	path := sha256.Sum256([]byte(r.URL.Path))
	token, inCookie := stealth.CookieToken(r.Header, p.cookie)
	bare := r.URL.RawQuery == "" && !r.URL.ForceQuery
	if !g.trusted(peer) || subtle.ConstantTimeCompare(path[:], g.path[:]) != 1 || !upgrade || !bare || !inCookie {
		g.toDecoy(w, r)
		return
	}
	visitor := g.visitor(r, peer)
	dev, keys, ok := g.prove(token, visitor, now)
	if !ok {
		g.toDecoy(w, r)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		g.release(dev)
		g.toDecoy(w, r)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		g.release(dev)
		return
	}
	g.run(dev, visitor, conn, brw.Reader, plainUpgrade(key), keys, now)
}

// plainUpgrade is the answer to a bare proof: drift's, without the
// cookie. The front writes its own fields around it.
func plainUpgrade(key string) string {
	var b strings.Builder
	b.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Accept: " + stealth.WebSocketAccept(key) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(http.TimeFormat) + "\r\n\r\n")
	return b.String()
}
