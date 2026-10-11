package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/devguide"
)

// Handing over an L2TP or PPTP sign-in.
//
// The customer's page never carries a username, password or key in what it
// sends: it shows a mask, so whatever reads the page -- a script fetching the
// link, a crawler, an extension that copies pages, someone looking over a
// shoulder -- finds nothing there. The value is fetched when the customer
// presses its copy button, goes straight to the clipboard, and is never put
// in the page.
//
// The request that fetches it is held to the page that asked: a POST, from
// the page's own origin (the browser's Sec-Fetch-Site and Origin say so), with
// a header no other site can send without a preflight this server never
// answers, carrying a mark the page was issued and that expires. It is
// counted, so nothing can draw the values out one request after another, and
// each one is written to the log.
//
// What this cannot stop is said plainly where it matters, in the docs: a
// browser extension allowed to read and change the page runs inside it, and
// sees what the page sees the moment it sees it; and what is on the
// clipboard, every program on the device can read. Nothing a page does can
// change either.

const (
	// revealTTL is how long a page's mark is good for: long enough to set up
	// a router with the page open, short enough that a saved copy of the page
	// is no key.
	revealTTL = 30 * time.Minute

	// revealHeader must be on every request. A cross-site page cannot set a
	// header of its own without a preflight, and nothing here answers one.
	revealHeader = "X-Wui-Reveal"

	// Per link and per address, in revealWindow. A customer setting up a
	// router copies a handful of values; this is several times that.
	revealPerLink = 30
	revealPerAddr = 60
	revealWindow  = time.Minute
)

// subReveals issues and checks the marks, and counts the requests.
type subReveals struct {
	key []byte

	mu   sync.Mutex
	hits map[string][]time.Time
}

func newSubReveals() *subReveals {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// No key is no reveal: every mark fails, and the page says to try
		// again, rather than signing with something guessable.
		key = nil
	}
	return &subReveals{key: key, hits: map[string][]time.Time{}}
}

// issue is the mark a page for this link carries.
func (v *subReveals) issue(token string, now time.Time) string {
	if v == nil || v.key == nil {
		return ""
	}
	at := strconv.FormatInt(now.Unix(), 10)
	return at + "." + v.sign(token, at)
}

func (v *subReveals) sign(token, at string) string {
	m := hmac.New(sha256.New, v.key)
	m.Write([]byte(token))
	m.Write([]byte{0})
	m.Write([]byte(at))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// valid reports whether mark was issued for this link and has not expired.
func (v *subReveals) valid(token, mark string, now time.Time) bool {
	if v == nil || v.key == nil {
		return false
	}
	at, sig, ok := strings.Cut(mark, ".")
	if !ok {
		return false
	}
	secs, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return false
	}
	issued := time.Unix(secs, 0)
	if now.Before(issued.Add(-time.Minute)) || now.Sub(issued) > revealTTL {
		return false
	}
	want := v.sign(token, at)
	return subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1
}

// allow counts one request against key, and reports whether it is within
// limit for the window.
func (v *subReveals) allow(key string, limit int, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	recent := v.hits[key][:0]
	for _, t := range v.hits[key] {
		if now.Sub(t) < revealWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limit {
		v.hits[key] = recent
		return false
	}
	v.hits[key] = append(recent, now)
	// Now and then, forget the keys nobody has used for a while.
	if len(v.hits) > 4096 {
		for k, ts := range v.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= revealWindow {
				delete(v.hits, k)
			}
		}
	}
	return true
}

// revealRequest is what the page sends.
type revealRequest struct {
	Mark string `json:"mark"`
	// Ref is the file: its device and host, "ID.HostID".
	Ref string `json:"ref"`
	// What is devguide.SecretUsername, SecretPassword, SecretPSK, or "cmd"
	// for a device's command, which carries the sign-in in it.
	What     string `json:"what"`
	Device   string `json:"device,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// sameOrigin reports whether the browser says the request came from this
// site's own page. A request with neither header is no browser's, and is
// refused too: a script has no business here.
func sameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	origin := r.Header.Get("Origin")
	if site == "" && origin == "" {
		return false
	}
	if site != "" && site != "same-origin" {
		return false
	}
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

// serveSubReveal answers the page's copy button with one value.
func (s *Server) serveSubReveal(w http.ResponseWriter, r *http.Request, token string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")

	if r.Method != http.MethodPost {
		h.Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get(revealHeader) != "1" || !sameOrigin(r) ||
		!strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ip, now := clientIP(r), time.Now()
	if !s.reveals.allow("t:"+token, revealPerLink, now) || !s.reveals.allow("a:"+ip, revealPerAddr, now) {
		h.Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	var req revealRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.reveals.valid(token, req.Mark, now) {
		// The page is older than its mark, or the panel restarted since:
		// the page says to reload.
		http.Error(w, "expired", http.StatusConflict)
		return
	}

	page, err := s.subs.PageFor(r.Context(), token, "")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	value, ok := "", false
	for _, d := range page.Devices {
		if d.Guide == nil || !devguide.Protocol(d.Guide.Protocol).IsPPP() ||
			req.Ref != strconv.FormatUint(uint64(d.ID), 10)+"."+strconv.FormatUint(uint64(d.HostID), 10) {
			continue
		}
		if req.What == "cmd" {
			// Only a command the page withholds: one the sign-in is in.
			masked := devguide.Views(d.Guide.Masked())
			for n, view := range devguide.Views(*d.Guide) {
				if view.Device == req.Device && string(view.Protocol) == req.Protocol && view.Command != "" &&
					n < len(masked) && masked[n].Command != view.Command {
					value, ok = view.Command, true
					break
				}
			}
		} else {
			value, ok = d.Guide.SecretOf(req.What)
		}
		if ok {
			s.log.Info("a sign-in was copied from a customer's page",
				"client", page.Name, "device", d.Name, "what", req.What, "ip", ip)
		}
		break
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": value})
}
