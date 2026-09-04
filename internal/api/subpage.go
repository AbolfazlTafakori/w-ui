package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/abolfazl/w-ui/internal/service"
)

// The page a customer sees when they open their subscription link in a browser.
//
// Without it they get a wall of configuration text, which tells them nothing
// about what they have bought and gives them no way to install it. This is the
// only page in the panel a customer ever sees, and it is the one that decides
// whether they ask the operator how much data they have left.
//
// It is rendered whole on the server and carries no external reference of any
// kind — no font, no script file, no image URL. A customer opening this on a
// phone on a bad connection, or from a network that blocks whatever CDN was
// convenient, still gets a working page.

// subPageQRLimit is the largest configuration that will fit in a scannable QR.
//
// A QR code has a hard capacity, and past it the encoder either fails or emits
// something so dense that no phone camera resolves it. An OpenVPN profile with
// an inline certificate chain is comfortably past that, so those get a download
// button and no QR rather than a picture nobody can scan.
const subPageQRLimit = 1500

type subPageView struct {
	Page      *service.SubPage
	Nonce     string
	Dir       string
	Devices   []subPageDevice
	HasQuota  bool
	Expiry    string
	ExpiresIn string
	StatusTxt string
	StatusCls string
}

type subPageDevice struct {
	service.SubPageDevice
	QR template.URL
}

// maybeServeSubPage answers with the customer's page when a browser asked.
//
// A client app fetching the same URL must keep getting the configuration, so
// the choice is made on what the caller said it accepts. `?view=html` is there
// for a customer whose browser sends something unusual, and `?view=raw` for an
// operator who wants to see exactly what a client app would receive.
func (s *Server) maybeServeSubPage(w http.ResponseWriter, r *http.Request, token string) bool {
	view := strings.ToLower(r.URL.Query().Get("view"))
	if view == "raw" || r.URL.Query().Get("format") != "" {
		return false
	}
	wantsHTML := view == "html" ||
		strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
	if !wantsHTML {
		return false
	}

	link, _ := s.subs.LinkFor(r.Context(), token, r.Host)
	page, err := s.subs.PageFor(r.Context(), token, link)
	if err != nil {
		// The same answer as a token that never existed: this page must not be
		// a way to find out which tokens are real.
		http.NotFound(w, r)
		return true
	}

	view2 := subPageView{
		Page:     page,
		Nonce:    newNonce(),
		Dir:      "ltr",
		HasQuota: page.QuotaBytes > 0,
	}
	for _, d := range page.Devices {
		entry := subPageDevice{SubPageDevice: d}
		if len(d.Config) <= subPageQRLimit {
			if png, err := qrcode.Encode(d.Config, qrcode.Medium, 320); err == nil {
				entry.QR = template.URL("data:image/png;base64," +
					base64.StdEncoding.EncodeToString(png))
			}
		}
		view2.Devices = append(view2.Devices, entry)
	}

	view2.StatusTxt, view2.StatusCls = subStatus(page)
	if page.ExpiresAt != nil {
		view2.Expiry = page.ExpiresAt.UTC().Format("2006-01-02")
		view2.ExpiresIn = humanUntil(*page.ExpiresAt)
	}

	var buf bytes.Buffer
	if err := subPageTemplate.Execute(&buf, view2); err != nil {
		s.log.Error("could not render the subscription page", "error", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return true
	}

	h := w.Header()
	// This page's own policy, replacing the panel's. It needs one inline style
	// block and one inline script, both of which are ours and both of which are
	// named by nonce rather than by opening the door to every inline script on
	// the page. Nothing may be loaded from anywhere else at all.
	h.Set("Content-Security-Policy",
		"default-src 'none'; "+
			"img-src 'self' data:; "+
			"style-src 'nonce-"+view2.Nonce+"'; "+
			"script-src 'nonce-"+view2.Nonce+"'; "+
			"form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
	return true
}

// maybeServeSubDevice hands over one device's file.
func (s *Server) maybeServeSubDevice(w http.ResponseWriter, r *http.Request, token string) bool {
	raw := r.URL.Query().Get("device")
	if raw == "" {
		return false
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return true
	}

	profile, err := s.subs.DeviceConfig(r.Context(), token, uint(id))
	if err != nil {
		http.NotFound(w, r)
		return true
	}

	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", profile.Filename))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(profile.Body)
	return true
}

func newNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// A predictable nonce is worse than none: it would let injected markup
		// name it. Falling back to the time would be exactly that.
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(b)
}

func subStatus(p *service.SubPage) (text, class string) {
	switch p.Status {
	case "active":
		if p.QuotaBytes > 0 && p.UsedBytes >= p.QuotaBytes {
			return "Data used up", "bad"
		}
		return "Active", "good"
	case "disabled":
		return "Switched off", "bad"
	case "expired":
		return "Expired", "bad"
	case "exhausted":
		return "Data used up", "bad"
	default:
		return p.Status, "warn"
	}
}

// humanUntil says how long is left in the largest unit that is still useful.
func humanUntil(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes left", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours left", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days left", int(d.Hours()/24))
	}
}

// humanBytes is the customer-facing one. Binary units, because that is what
// every client app on their phone will also be showing them.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

var subPageFuncs = template.FuncMap{
	"bytes": humanBytes,
}

var subPageTemplate = template.Must(template.New("subpage").Funcs(subPageFuncs).Parse(`<!doctype html>
<html lang="en" dir="{{ .Dir }}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{ .Page.Title }}</title>
<style nonce="{{ .Nonce }}">
:root {
  color-scheme: light dark;
  --bg: #f6f7f9; --card: #fff; --ink: #14161a; --muted: #666e7a;
  --edge: #e3e6ea; --accent: #2f6df6; --good: #1a7f45; --bad: #c0392b;
  --bar: #e8ebef;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0f1115; --card: #161922; --ink: #e8eaee; --muted: #99a1ad;
    --edge: #262b36; --accent: #5b8cff; --good: #3ec27a; --bad: #ef6a5a;
    --bar: #232833;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; padding: 20px 14px 48px;
  background: var(--bg); color: var(--ink);
  font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}
.wrap { max-width: 640px; margin: 0 auto; }
h1 { font-size: 20px; margin: 0 0 2px; }
.sub { color: var(--muted); font-size: 13px; margin: 0 0 18px; }
.card {
  background: var(--card); border: 1px solid var(--edge); border-radius: 12px;
  padding: 16px; margin-bottom: 14px;
}
.tag {
  display: inline-block; padding: 2px 9px; border-radius: 999px;
  font-size: 12px; font-weight: 600; vertical-align: 2px; margin-inline-start: 8px;
}
.tag.good { background: color-mix(in srgb, var(--good) 16%, transparent); color: var(--good); }
.tag.bad  { background: color-mix(in srgb, var(--bad) 16%, transparent);  color: var(--bad); }
.tag.warn { background: color-mix(in srgb, var(--muted) 16%, transparent); color: var(--muted); }
.big { font-size: 26px; font-weight: 650; font-variant-numeric: tabular-nums; }
.big small { font-size: 14px; font-weight: 400; color: var(--muted); }
.bar { height: 8px; background: var(--bar); border-radius: 999px; overflow: hidden; margin: 12px 0 10px; }
.bar > i { display: block; height: 100%; background: var(--accent); border-radius: 999px; }
.bar > i.full { background: var(--bad); }
.rows { display: grid; grid-template-columns: 1fr 1fr; gap: 10px 16px; margin-top: 12px; }
.rows div { font-size: 13px; }
.rows dt { color: var(--muted); }
.rows dd { margin: 2px 0 0; font-variant-numeric: tabular-nums; font-weight: 550; }
.dev { display: flex; gap: 14px; align-items: flex-start; }
.dev img { width: 116px; height: 116px; border-radius: 8px; background: #fff; padding: 5px; flex: none; }
.dev .meta { min-width: 0; flex: 1; }
.dev h2 { font-size: 15px; margin: 0 0 2px; }
.dev .addr { color: var(--muted); font-size: 12px; font-variant-numeric: tabular-nums; }
.btns { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
a.btn, button.btn {
  font: inherit; font-size: 13px; font-weight: 550; cursor: pointer;
  padding: 7px 13px; border-radius: 8px; border: 1px solid var(--edge);
  background: var(--card); color: var(--ink); text-decoration: none;
}
a.btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.link { word-break: break-all; font-size: 12px; color: var(--muted); font-family: ui-monospace, monospace; }
.hint { color: var(--muted); font-size: 12px; margin: 10px 0 0; }
@media (max-width: 460px) {
  .dev { flex-direction: column; }
  .dev img { width: 100%; height: auto; max-width: 260px; align-self: center; }
}
</style>
</head>
<body>
<div class="wrap">

  <h1>{{ .Page.Title }}<span class="tag {{ .StatusCls }}">{{ .StatusTxt }}</span></h1>
  <p class="sub">{{ .Page.Name }}</p>

  <div class="card">
    {{ if .HasQuota }}
      <div class="big">{{ bytes .Page.Remaining }} <small>left of {{ bytes .Page.QuotaBytes }}</small></div>
      <div class="bar"><i class="{{ if ge .Page.UsedPercent 100 }}full{{ end }}" style="width: {{ .Page.UsedPercent }}%"></i></div>
    {{ else }}
      <div class="big">{{ bytes .Page.UsedBytes }} <small>used — no limit</small></div>
    {{ end }}
    <dl class="rows">
      <div><dt>Downloaded</dt><dd>{{ bytes .Page.DownBytes }}</dd></div>
      <div><dt>Uploaded</dt><dd>{{ bytes .Page.UpBytes }}</dd></div>
      {{ if .Expiry }}<div><dt>Expires</dt><dd>{{ .Expiry }}</dd></div>
      <div><dt>Remaining</dt><dd>{{ .ExpiresIn }}</dd></div>{{ end }}
    </dl>
  </div>

  {{ range .Devices }}
  <div class="card dev">
    {{ if .QR }}<img src="{{ .QR }}" alt="QR code for {{ .Name }}" width="116" height="116">{{ end }}
    <div class="meta">
      <h2>{{ .Name }}</h2>
      <div class="addr">{{ .Address }}</div>
      <div class="btns">
        <a class="btn primary" href="?device={{ .ID }}" download="{{ .Filename }}">Download</a>
        <button class="btn copy" type="button" data-config="{{ .Config }}">Copy</button>
      </div>
    </div>
  </div>
  {{ end }}

  {{ if gt (len .Devices) 1 }}
  <div class="card">
    <a class="btn" href="?format=zip">Download all {{ len .Devices }} as a .zip</a>
    <p class="hint">One file per device. A configuration file holds one device, so they cannot be combined.</p>
  </div>
  {{ end }}

  {{ if .Page.SubURL }}
  <div class="card">
    <p class="hint" style="margin-top:0">Your link, for an app that asks for one:</p>
    <p class="link">{{ .Page.SubURL }}</p>
    <div class="btns"><button class="btn copy" type="button" data-config="{{ .Page.SubURL }}">Copy link</button></div>
  </div>
  {{ end }}

  <p class="hint">Keep this link private. Anyone who has it can use your connection.</p>
</div>

<script nonce="{{ .Nonce }}">
// Copy without a library. The clipboard API needs a secure context, so the
// fallback matters: a panel reached over plain http would otherwise have a
// button that does nothing and says nothing.
document.querySelectorAll('button.copy').forEach(function (b) {
  b.addEventListener('click', function () {
    var text = b.getAttribute('data-config') || '';
    var done = function (ok) {
      var was = b.textContent;
      b.textContent = ok ? 'Copied' : 'Press Ctrl+C';
      setTimeout(function () { b.textContent = was; }, 1600);
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(function () { done(true); }, function () { done(false); });
      return;
    }
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    done(ok);
  });
});
</script>
</body>
</html>
`))
