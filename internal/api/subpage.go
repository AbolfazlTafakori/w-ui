package api

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/abolfazl/w-ui/internal/service"
)

// The page a customer sees when they open their subscription link in a browser.
//
// Laid out as the classic panel's SubPage is -- one centred Card with the Descriptions
// table, the usage summary, the link rows, a config block per device and
// the two app buttons -- in this panel's own colours. Rendered whole on the
// server and carrying no external reference of any kind: a customer opening
// this on a phone on a bad connection, or from a network that blocks
// whatever CDN was convenient, still gets a working page.

// subPageQRLimit is the largest configuration that will fit in a scannable QR.
const subPageQRLimit = 1500

//go:embed assets/logo.png
var subLogoPNG []byte

// subLogo is the mark, inlined: the page's policy loads images from itself
// and from data: only, and the page has no other files to speak of.
var subLogo = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(subLogoPNG))

type subPageView struct {
	Logo    template.URL
	Page    *service.SubPage
	Nonce   string
	SubID   string
	Devices []subPageDevice
	// Groups is the files by tunnel: a plan for one shows one row per
	// tunnel with the actions on it; a plan for several shows one row per
	// tunnel that opens on the users, each with their own actions.
	Groups []subGroup
	// Usage is who spent what on which tunnel: a row per user, a column
	// per tunnel, a total at the end of each. For people sharing a plan
	// and its cost.
	Usage      *subUsage
	HasWG      bool // any device on a WireGuard tunnel: its apps are offered
	HasOVPN    bool // any on OpenVPN
	HasQuota   bool
	Unlimited  bool
	Active     bool
	StatusKey  string // active | inactive | unlimited
	Used       string
	Total      string
	Remained   string
	Percent    float64
	PercentTxt string
	Expiry     string
	ExpiryChip string // e.g. 12d, 3h, expired; empty when no expiry
	ExpiryCls  string
	LastOnline string
	SubQR      template.URL
	Strings    template.JS // the two dictionaries, for the language button
	Lang       string
	Icons      map[string]template.HTML
	// Template is the look, one of service.SubTemplates; Preview marks a
	// page rendered from sample figures for the settings page.
	Template string
	Preview  bool
}

type subPageDevice struct {
	service.SubPageDevice
	QR template.URL
	// Row is what the user's line is called inside a tunnel's menu: the
	// customer's own name for a plan of one, "User n" for a plan of
	// several, a file's own name when it was given one.
	Row string
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
	// The stream a page holds open to be told the moment something changed.
	// Only for a link that exists, which the caller has already checked.
	if view == "events" {
		s.serveSubEvents(w, r)
		return true
	}
	// The figures alone, for the page to refresh itself with while it is open.
	if view == "status" {
		page, err := s.subs.StatusFor(r.Context(), token)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		s.serveSubStatus(w, page, token)
		return true
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

	s.renderSubPage(w, page, token, false)
	return true
}

// renderSubPage writes the page for one customer -- or, for a preview, for
// nobody in particular.
func (s *Server) renderSubPage(w http.ResponseWriter, page *service.SubPage, token string, preview bool) {
	v := newSubView(page, token, preview)

	var buf bytes.Buffer
	if err := subPageTemplate.Execute(&buf, v); err != nil {
		s.log.Error("could not render the subscription page", "error", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	h := w.Header()
	// This page's own policy, replacing the panel's. It needs one inline style
	// block and one inline script, both of which are ours and both of which are
	// named by nonce rather than by opening the door to every inline script on
	// the page. Nothing may be loaded from anywhere else at all -- except the
	// page's own address, which it asks for fresh figures.
	h.Set("Content-Security-Policy",
		"default-src 'none'; "+
			"img-src 'self' data:; "+
			"connect-src 'self'; "+
			"style-src 'nonce-"+v.Nonce+"'; "+
			"script-src 'nonce-"+v.Nonce+"'; "+
			"form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// subStatus is what the page polls: the figures that move while a customer
// is connected, in the same words the page was rendered with.
type subLiveStatus struct {
	Rev        string  `json:"rev"`
	Active     bool    `json:"active"`
	Online     bool    `json:"online"`
	StatusKey  string  `json:"statusKey"`
	HasQuota   bool    `json:"hasQuota"`
	Used       string  `json:"used"`
	Total      string  `json:"total"`
	Remained   string  `json:"remained"`
	Percent    float64 `json:"percent"`
	PercentTxt string  `json:"percentTxt"`
	Down       string  `json:"down"`
	Up         string  `json:"up"`
	Expiry     string  `json:"expiry"`
	ExpiryChip string  `json:"expiryChip"`
	ExpiryCls  string  `json:"expiryCls"`
	LastOnline string  `json:"lastOnline"`
	Devices    int     `json:"devices"`
}

// onlineWindow is how recent a handshake has to be for a device to count as
// connected: WireGuard renews every two minutes while traffic flows.
const onlineWindow = 3 * time.Minute

func (s *Server) serveSubStatus(w http.ResponseWriter, page *service.SubPage, token string) {
	v := newSubView(page, token, false)
	st := subLiveStatus{
		Rev:    page.Rev,
		Active: v.Active, StatusKey: v.StatusKey, HasQuota: v.HasQuota,
		Used: v.Used, Total: v.Total, Remained: v.Remained,
		Percent: v.Percent, PercentTxt: v.PercentTxt,
		Down: humanBytes(page.DownBytes), Up: humanBytes(page.UpBytes),
		Expiry: v.Expiry, ExpiryChip: v.ExpiryChip, ExpiryCls: v.ExpiryCls,
		LastOnline: v.LastOnline, Devices: len(page.Devices),
	}
	if page.LastOnline != nil && time.Since(*page.LastOnline) < onlineWindow {
		st.Online = true
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(st)
}

// newSubView works the page's figures out from the customer's record.
func newSubView(page *service.SubPage, token string, preview bool) subPageView {
	v := subPageView{
		Logo:     subLogo,
		Page:     page,
		Nonce:    newNonce(),
		SubID:    token,
		Template: page.Template,
		Preview:  preview,
		HasQuota: page.QuotaBytes > 0,
		Used:     humanBytes(page.UsedBytes),
		Lang:     page.Locale,
		Icons:    map[string]template.HTML{},
	}
	if v.Lang != "fa" {
		v.Lang = "en"
	}
	if v.Template == "" {
		v.Template = "classic"
	}
	for name, paths := range antIconPaths {
		var b strings.Builder
		b.WriteString(`<svg viewBox="64 64 896 896" width="1em" height="1em" fill="currentColor" focusable="false" aria-hidden="true">`)
		for _, d := range paths {
			b.WriteString(`<path d="` + d + `"/>`)
		}
		b.WriteString(`</svg>`)
		v.Icons[name] = template.HTML(b.String())
	}
	v.Unlimited = page.QuotaBytes == 0 && page.ExpiresAt == nil
	v.Active = page.Status == "active" &&
		!(page.QuotaBytes > 0 && page.UsedBytes >= page.QuotaBytes) &&
		!(page.ExpiresAt != nil && !page.ExpiresAt.After(time.Now()))
	switch {
	case page.Status != "active":
		v.StatusKey = "inactive"
	case v.Unlimited:
		v.StatusKey = "unlimited"
	case v.Active:
		v.StatusKey = "active"
	default:
		v.StatusKey = "inactive"
	}
	if v.HasQuota {
		v.Total = humanBytes(page.QuotaBytes)
		v.Remained = humanBytes(page.Remaining())
		v.Percent = float64(page.UsedBytes) / float64(page.QuotaBytes) * 100
		if v.Percent > 100 {
			v.Percent = 100
		}
		v.PercentTxt = strconv.FormatFloat(v.Percent, 'f', 1, 64)
	} else {
		v.Total = "∞"
	}
	if page.ExpiresAt != nil {
		v.Expiry = page.ExpiresAt.Local().Format("2006-01-02 15:04")
		d := time.Until(*page.ExpiresAt)
		switch {
		case d <= 0:
			v.ExpiryChip, v.ExpiryCls = "expired", "red"
		case d >= 24*time.Hour:
			days := int(d.Hours() / 24)
			v.ExpiryChip = fmt.Sprintf("%dd", days)
			v.ExpiryCls = "blue"
			if days <= 3 {
				v.ExpiryCls = "orange"
			}
		default:
			h := int(d.Hours())
			if h < 1 {
				h = 1
			}
			v.ExpiryChip, v.ExpiryCls = fmt.Sprintf("%dh", h), "orange"
		}
	}
	if page.LastOnline != nil {
		v.LastOnline = page.LastOnline.Local().Format("2006-01-02 15:04")
	}
	if page.SubURL != "" {
		if png, err := qrcode.Encode(page.SubURL, qrcode.Low, 240); err == nil {
			v.SubQR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
		}
	}
	for _, d := range page.Devices {
		entry := subPageDevice{SubPageDevice: d}
		if len(d.Config) <= subPageQRLimit {
			if png, err := qrcode.Encode(d.Config, qrcode.Low, 220); err == nil {
				entry.QR = template.URL("data:image/png;base64," +
					base64.StdEncoding.EncodeToString(png))
			}
		}
		v.Devices = append(v.Devices, entry)
		if d.Protocol == "openvpn" {
			v.HasOVPN = true
		} else {
			v.HasWG = true
		}
	}
	v.Groups = groupDevices(v.Devices, page.Name)
	v.Usage = usageTable(v.Devices, page.Name)
	dict, _ := json.Marshal(subPageStrings)
	v.Strings = template.JS(dict)
	return v
}

// subPageStrings are the page's words in both languages, keyed as the classic panel's
// subscription strings are, so the language button can swap them in place.
var subPageStrings = map[string]map[string]string{
	"en": {
		"title": "Subscription info", "subId": "Subscription ID", "email": "Email", "status": "Status",
		"active": "Active", "inactive": "Inactive", "unlimited": "Unlimited",
		"downloaded": "Downloaded", "uploaded": "Uploaded", "usage": "Usage", "totalQuota": "Total Quota",
		"remained": "Remaining", "lastOnline": "Last Online", "expiry": "Expiry", "noExpiry": "No expiry",
		"expired": "Expired", "copy": "Copy", "copied": "Copied", "download": "Download",
		"copyLink": "Copy URL", "copyAll": "Copy all configs", "copyAllDone": "All configs copied",
		"config": "WireGuard config", "ovpnConfig": "OpenVPN config", "theme": "Theme", "language": "Language", "users": "users", "user": "User", "show": "Show", "oneUser": "1 user", "usageTable": "Usage by user and tunnel", "total": "Total", "allUsers": "All users", "tunnel": "Tunnel",
		"live": "Live", "online": "Online", "idle": "Idle", "offline": "Off",
		"subSettings": "Subscription", "tapToClose": "Tap outside to close",
	},
	"fa": {
		"title": "اطلاعات سابسکریپشن", "subId": "شناسه اشتراک", "email": "ایمیل", "status": "وضعیت",
		"active": "فعال", "inactive": "غیرفعال", "unlimited": "نامحدود",
		"downloaded": "دانلود شده", "uploaded": "آپلود شده", "usage": "مصرف", "totalQuota": "حجم کل",
		"remained": "باقی‌مانده", "lastOnline": "آخرین فعالیت", "expiry": "انقضا", "noExpiry": "بدون انقضا",
		"expired": "منقضی", "copy": "کپی", "copied": "کپی شد", "download": "دانلود",
		"copyLink": "کپی لینک", "copyAll": "کپی همه کانفیگ‌ها", "copyAllDone": "همه کانفیگ‌ها کپی شد",
		"config": "پیکربندی WireGuard", "ovpnConfig": "پیکربندی OpenVPN", "theme": "تم", "language": "زبان", "users": "کاربر", "user": "کاربر", "show": "نمایش", "oneUser": "۱ کاربر", "usageTable": "مصرف هر کاربر روی هر تانل", "total": "جمع", "allUsers": "همهٔ کاربران", "tunnel": "تانل",
		"live": "زنده", "online": "آنلاین", "idle": "بی‌کار", "offline": "خاموش",
		"subSettings": "اشتراک", "tapToClose": "برای بستن بیرون بزنید",
	},
}

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

	var hostID uint64
	if h := r.URL.Query().Get("host"); h != "" {
		hostID, _ = strconv.ParseUint(h, 10, 64)
	}
	profile, err := s.subs.DeviceConfig(r.Context(), token, uint(id), uint(hostID))
	if err != nil {
		http.NotFound(w, r)
		return true
	}

	h := w.Header()
	setDownload(h, profile.Filename)
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
<html lang="{{ .Lang }}" dir="ltr" data-lang="{{ .Lang }}" data-layout="{{ .Template }}" data-rev="{{ .Page.Rev }}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{ .Page.Title }}</title>
<style nonce="{{ .Nonce }}">
/* This panel's palette on the classic panel's subscription page: Ant's geometry, our
   black and red. Three themes, cycled from the button in the card head. */
:root {
  color-scheme: dark;
  --ground: #0a090a; --surface: #141215; --surface-2: #1c191d; --surface-3: #262227;
  --line: #302a31; --line-soft: #241f25;
  --ink: #f4f1f2; --muted: #a1959b; --faint: #746a71;
  --accent: #e02e3d; --accent-hover: #f2404f; --accent-ink: #fff;
  --ok: #4ac36a; --warn: #e0ab34; --bad: #f4655f;
  --tag-green-bg: #162312; --tag-green-line: #274916; --tag-green-ink: #6abe39;
  --tag-red-bg: #2a1215; --tag-red-line: #58181c; --tag-red-ink: #e84749;
  --tag-orange-bg: #2b1d11; --tag-orange-line: #593815; --tag-orange-ink: #e89a3c;
  --tag-purple-bg: #1a1325; --tag-purple-line: #3a2a5c; --tag-purple-ink: #a878e6;
  --tag-blue-bg: #111a2c; --tag-blue-line: #15325b; --tag-blue-ink: #4e9bf5;
  --tag-cyan-bg: #112123; --tag-cyan-line: #144848; --tag-cyan-ink: #33bcb7;
  --row-bg: rgba(0, 0, 0, 0.2); --row-line: rgba(255, 255, 255, 0.1);
  --row-bg-h: rgba(0, 0, 0, 0.3); --row-line-h: rgba(255, 255, 255, 0.2);
}
html[data-theme="ultra"] {
  --ground: #000; --surface: #0a0a0c; --surface-2: #121215; --surface-3: #1a1a1e;
  --line: #26262b; --line-soft: #17171b;
}
html[data-theme="light"] {
  color-scheme: light;
  --ground: #eceef1; --surface: #fff; --surface-2: #f4f5f7; --surface-3: #e9eaee;
  --line: #dcdee3; --line-soft: #eaebef;
  --ink: #141113; --muted: #6b6169; --faint: #8d838a;
  --accent: #c81f2e; --accent-hover: #a81826;
  --ok: #1a7f3c; --warn: #8a6206; --bad: #c62f28;
  --tag-green-bg: #f6ffed; --tag-green-line: #b7eb8f; --tag-green-ink: #389e0d;
  --tag-red-bg: #fff2f0; --tag-red-line: #ffccc7; --tag-red-ink: #cf1322;
  --tag-orange-bg: #fff7e6; --tag-orange-line: #ffd591; --tag-orange-ink: #d46b08;
  --tag-purple-bg: #f9f0ff; --tag-purple-line: #d3adf7; --tag-purple-ink: #531dab;
  --tag-blue-bg: #e6f4ff; --tag-blue-line: #91caff; --tag-blue-ink: #0958d9;
  --tag-cyan-bg: #e6fffb; --tag-cyan-line: #87e8de; --tag-cyan-ink: #08979c;
  --row-bg: rgba(0, 0, 0, 0.03); --row-line: rgba(0, 0, 0, 0.08);
  --row-bg-h: rgba(0, 0, 0, 0.05); --row-line-h: rgba(0, 0, 0, 0.14);
}
* { box-sizing: border-box; }
body {
  margin: 0; min-height: 100vh; background: var(--ground); color: var(--ink);
  font: 14px/1.5714 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif;
}
[dir="rtl"] body { }
.anticon { display: inline-flex; align-items: center; line-height: 0; vertical-align: -0.125em; }
.content { padding: 24px 12px; }
.col { width: 100%; margin: 0 auto; }
@media (min-width: 576px) { .col { width: 91.6667%; } }
@media (min-width: 768px) { .col { width: 75%; } }
@media (min-width: 992px) { .col { width: 58.3333%; } }
@media (min-width: 1200px) { .col { width: 50%; } }

/* Card, hoverable */
.card { margin-top: 8px; background: var(--surface); border: 1px solid var(--line-soft); border-radius: 8px; transition: box-shadow .2s, border-color .2s; }
.card:hover { box-shadow: 0 2px 6px rgba(0,0,0,.45), 0 16px 40px -16px rgba(0,0,0,.9); border-color: transparent; }
.card-head { display: flex; align-items: center; min-height: 56px; padding: 0 24px; border-bottom: 1px solid var(--line-soft); font-size: 16px; font-weight: 600; }
.card-title { flex: 1; display: inline-flex; align-items: center; gap: 8px; min-width: 0; }
.card-extra { display: inline-flex; align-items: center; gap: 8px; font-weight: 400; }
.card-body { padding: 24px; }
@media (max-width: 768px) { .card-head { padding: 0 12px; min-height: 44px; } .card-body { padding: 12px; } }

/* Tag */
.tag { display: inline-block; margin-inline-end: 8px; padding-inline: 7px; border: 1px solid var(--line); border-radius: 4px; background: var(--surface-3); color: var(--ink); font-size: 12px; line-height: 20px; white-space: nowrap; font-weight: 400; }
.tag.green { background: var(--tag-green-bg); border-color: var(--tag-green-line); color: var(--tag-green-ink); }
.tag.red { background: var(--tag-red-bg); border-color: var(--tag-red-line); color: var(--tag-red-ink); }
.tag.orange { background: var(--tag-orange-bg); border-color: var(--tag-orange-line); color: var(--tag-orange-ink); }
.tag.purple { background: var(--tag-purple-bg); border-color: var(--tag-purple-line); color: var(--tag-purple-ink); }
.tag.blue { background: var(--tag-blue-bg); border-color: var(--tag-blue-line); color: var(--tag-blue-ink); }
.tag.cyan { background: var(--tag-cyan-bg); border-color: var(--tag-cyan-line); color: var(--tag-cyan-ink); }
.tag .anticon { margin-inline-end: 4px; }

/* Buttons */
.btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; height: 32px; padding: 4px 15px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface); color: var(--ink); font: inherit; font-size: 14px; line-height: 22px; cursor: pointer; text-decoration: none; white-space: nowrap; transition: all .2s cubic-bezier(.645,.045,.355,1); }
.btn:hover { color: var(--accent-hover); border-color: var(--accent-hover); }
.btn.sm { height: 24px; width: 24px; padding: 0; border-radius: 4px; }
.btn.lg { height: 40px; padding: 6px 15px; border-radius: 8px; font-size: 16px; }
.btn.primary { background: var(--accent); border-color: var(--accent); color: var(--accent-ink); box-shadow: 0 2px 0 rgba(224,46,61,.16); }
.btn.primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); color: var(--accent-ink); }
.btn.block { width: 100%; }
.toolbar-btn { width: 40px; height: 40px; min-width: 40px; padding: 0; border-radius: 50%; }
.toolbar-btn .anticon { font-size: 18px; }

/* Alert info */
.alert { display: flex; align-items: center; gap: 8px; margin-bottom: 16px; padding: 8px 12px; border: 1px solid var(--tag-blue-line); border-radius: 8px; background: var(--tag-blue-bg); }
.alert .anticon { color: var(--tag-blue-ink); }

/* Descriptions bordered size="small" */
.desc { margin-top: 4px; width: 100%; border: 1px solid var(--line-soft); border-radius: 8px; border-collapse: separate; border-spacing: 0; overflow: hidden; }
.desc th, .desc td { padding: 8px 16px; border-bottom: 1px solid var(--line-soft); font-size: 14px; line-height: 22px; text-align: start; vertical-align: top; }
.desc th { width: 1%; white-space: nowrap; background: var(--surface-2); color: var(--muted); font-weight: 400; border-inline-end: 1px solid var(--line-soft); }
.desc tr:last-child th, .desc tr:last-child td { border-bottom: 0; }
.desc td .tag { margin: 0; }

/* Usage summary */
.usage { margin-top: 12px; padding: 14px 16px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: 12px; }
.usage.inactive { opacity: .7; border-color: var(--tag-red-line); }
.usage-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 8px; }
.usage-labels { display: flex; align-items: baseline; gap: 6px; font-variant-numeric: tabular-nums; min-width: 0; }
.usage-used { font-size: 18px; font-weight: 700; color: var(--ink); }
.usage-sep { color: var(--faint); font-size: 16px; }
.usage-total { font-size: 14px; color: var(--muted); font-weight: 500; }
.usage-chips { display: flex; align-items: center; gap: 6px; flex-shrink: 0; }
.usage-chips .tag { margin: 0; }
.bar { height: 10px; margin-bottom: 6px; border-radius: 100px; background: var(--surface-3); overflow: hidden; }
.bar > i { display: block; height: 100%; border-radius: 100px; }
.bar > i.green { background: linear-gradient(90deg, #5fc983, #36b37e); }
.bar > i.orange { background: linear-gradient(90deg, #ffc53d, #fa8c16); }
.bar > i.red { background: linear-gradient(90deg, #ff7875, #ff4d4f); }
.usage-foot { display: flex; align-items: center; justify-content: space-between; min-height: 16px; font-size: 12px; color: var(--faint); font-variant-numeric: tabular-nums; }
.usage-pct { font-weight: 600; color: var(--muted); }

/* Divider with text */
.divider { display: flex; align-items: center; margin: 16px 0; font-size: 16px; font-weight: 500; white-space: nowrap; }
.divider::before, .divider::after { content: ''; flex: 1; border-top: 1px solid var(--line-soft); }
.divider span { padding: 0 1em; }

/* Link rows */
.links { display: flex; flex-direction: column; gap: 8px; }
.row { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-radius: 10px; background: var(--row-bg); border: 1px solid var(--row-line); transition: background .12s, border-color .12s; }
.row:hover { background: var(--row-bg-h); border-color: var(--row-line-h); }
.row-tag { margin: 0; flex-shrink: 0; font-weight: 600; letter-spacing: .3px; }
.row-title { flex: 1; min-width: 0; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: inherit; text-decoration: none; }
a.row-title:hover { text-decoration: underline; }
.row-actions { display: flex; gap: 4px; flex-shrink: 0; position: relative; }
.pop { position: fixed; inset: 0; z-index: 20; display: none; align-items: center; justify-content: center; padding: 16px; background: rgba(0,0,0,.45); }
.pop.open { display: flex; }
.pop-card { display: flex; flex-direction: column; align-items: center; gap: 10px; width: min(100%, 320px); padding: 16px; border-radius: 8px; background: var(--surface); box-shadow: 0 6px 16px rgba(0,0,0,.08), 0 3px 6px -4px rgba(0,0,0,.12), 0 9px 28px 8px rgba(0,0,0,.05); }
.pop img { display: block; width: 100%; height: auto; max-width: 288px; padding: 8px; background: #fff; border-radius: 4px; }
.pop-hint { font-size: 12px; color: var(--faint); }
.qr-tag { width: 100%; text-align: center; margin: 0; }

/* Config block: a one-panel collapse */
.cfg { border: 1px solid var(--line); border-radius: 8px; background: var(--surface-2); }
.cfg-head { display: flex; align-items: center; gap: 8px; padding: 12px 16px; cursor: pointer; user-select: none; }
.cfg-head .caret { font-size: 12px; transition: transform .3s; }
.cfg.open .cfg-head .caret { transform: rotate(90deg); }
.cfg-head .row-actions { margin-inline-start: auto; }
.cfg-meta { font-size: 12px; opacity: .85; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cfg-body { display: none; padding: 16px; border-top: 1px solid var(--line); background: var(--surface); }
.cfg.open .cfg-body { display: block; }
/* A plan for several: the tunnel's row opens on one line per user, each
   with its own actions; a user's own file opens under their line. */
.cfg-count { margin-inline-start: auto; font-size: 12px; color: var(--muted); white-space: nowrap; }
.cfg-users { padding: 4px 0; }
.cfg-user { border-top: 1px solid var(--line-soft); }
.cfg-user:first-child { border-top: 0; }
.cfg-user-head { display: flex; align-items: center; gap: 8px; padding: 10px 16px; }
.cfg-user-name { font-size: 14px; font-weight: 600; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cfg-user-head .row-actions { margin-inline-start: auto; }
.cfg-user-login { margin-inline-start: 8px; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; font-weight: 400; color: var(--muted); }
.cfg-user-body { display: none; padding: 0 16px 14px; }
.cfg-user.open .cfg-user-body { display: block; }
.cfg-user .btn.show .anticon { transition: transform .3s; }
.cfg-user.open .btn.show .anticon { transform: rotate(90deg); }
.cfg-text { display: block; margin: 0; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 11px; white-space: pre-wrap; word-break: break-all; direction: ltr; text-align: left; }

/* The usage table. One container -- the fold-out panel -- and nothing
   boxed inside it: the grid is grouped by rhythm and two rules, one under
   the head and one above the sum, both drawn in the page's visible line
   colour so they read on every theme. Names lead in the ink colour; the
   numbers sit right-aligned in tabular figures so columns of them line
   up; the sums are heavier, not tinted; zeros step back to the muted
   colour, which still clears 4.5:1 on each theme. On a phone the first
   column stays put while the rest scrolls under it. */
.usage-table { margin-top: 12px; }
.usage-body { padding: 4px 0 8px; }
.usage-scroll { overflow-x: auto; -webkit-overflow-scrolling: touch; }
.usage-grid { width: 100%; border-collapse: collapse; font-size: 14px; line-height: 22px; }
.usage-grid th, .usage-grid td { padding: 8px 16px; white-space: nowrap; vertical-align: middle; }
.usage-grid thead th { padding-top: 10px; padding-bottom: 10px; border-bottom: 1px solid var(--line); color: var(--muted); font-size: 12px; font-weight: 500; letter-spacing: .01em; text-align: end; }
.usage-grid thead th:first-child { text-align: start; }
.usage-grid tbody th { text-align: start; font-weight: 500; color: var(--ink); }
.usage-grid tbody tr + tr th, .usage-grid tbody tr + tr td { border-top: 1px solid var(--line-soft); }
.usage-grid td { text-align: end; color: var(--ink); font-variant-numeric: tabular-nums; direction: ltr; }
.usage-grid .usage-sum { font-weight: 600; }
.usage-grid thead .usage-sum { color: var(--ink); }
.usage-grid tfoot th, .usage-grid tfoot td { border-top: 1px solid var(--line); padding-top: 10px; padding-bottom: 10px; font-weight: 600; color: var(--ink); }
.usage-grid tfoot th { text-align: start; }
.usage-grid td.usage-zero { color: var(--muted); font-weight: 400; }
.usage-dot { display: inline-block; width: 7px; height: 7px; margin-inline-end: 7px; border-radius: 50%; vertical-align: 1px; }
.usage-dot.cyan { background: var(--tag-cyan-ink); }
.usage-dot.orange { background: var(--tag-orange-ink); }
.usage-grid tbody tr:hover th, .usage-grid tbody tr:hover td { background: var(--surface-2); }
@media (max-width: 560px) {
  .usage-grid th, .usage-grid td { padding-inline: 12px; }
  .usage-grid th:first-child { position: sticky; inset-inline-start: 0; background: var(--surface); z-index: 1; }
  .usage-grid tbody tr:hover th:first-child { background: var(--surface-2); }
}

/* Apps row */
.apps { display: flex; flex-wrap: wrap; gap: 8px; justify-content: center; margin-top: 24px; }
.app { position: relative; flex: 1 1 calc(50% - 4px); text-align: center; }
@media (max-width: 575px) { .app { flex: 1 1 100%; } .app .btn { width: 100%; } }
/* The app menus sit on the last row of the page, so they open upward,
   over the card, rather than down past its foot; a long one (an app per
   protocol and a file per user) scrolls inside itself. The language and
   theme menus at the head of the page still open downward. */
.menu { position: absolute; top: calc(100% + 4px); inset-inline-start: 50%; transform: translateX(-50%); z-index: 6; display: none; min-width: 200px; max-height: 420px; overflow-y: auto; padding: 4px; border-radius: 8px; background: var(--surface-3); box-shadow: 0 6px 16px rgba(0,0,0,.08), 0 3px 6px -4px rgba(0,0,0,.12), 0 9px 28px 8px rgba(0,0,0,.05); text-align: start; }
.app .menu { top: auto; bottom: calc(100% + 4px); box-shadow: 0 -6px 16px rgba(0,0,0,.08), 0 -3px 6px -4px rgba(0,0,0,.12), 0 -9px 28px 8px rgba(0,0,0,.05); }
.menu.open { display: block; }
.menu a, .menu button { display: flex; width: 100%; align-items: center; gap: 8px; padding: 5px 12px; border: 0; border-radius: 4px; background: none; color: var(--ink); font: inherit; font-size: 14px; line-height: 22px; text-decoration: none; cursor: pointer; text-align: start; }
.menu a:hover, .menu button:hover { background: var(--surface-2); }
.lang-menu { inset-inline-start: auto; inset-inline-end: 0; transform: none; }
.lang-menu .on { color: var(--accent); background: rgba(224,46,61,.12); }
.toast { position: fixed; top: 8px; left: 50%; transform: translateX(-50%); z-index: 9; padding: 9px 12px; border-radius: 8px; background: var(--surface-3); color: var(--ink); font-size: 14px; box-shadow: 0 6px 16px rgba(0,0,0,.2); opacity: 0; transition: opacity .2s; pointer-events: none; }
.toast.show { opacity: 1; }
.hidden { display: none !important; }

/* ── The templates. The same page in six looks; the owner picks one. ── */
.preview-bar { position: sticky; top: 0; z-index: 30; padding: 6px 12px; background: var(--accent); color: #fff; font-size: 13px; text-align: center; }
.live-bg { position: fixed; inset: 0; z-index: 0; overflow: hidden; pointer-events: none; display: none; }
.content { position: relative; z-index: 1; }
.hero { display: none; }
.quick { display: none; }

/* aurora: soft coloured light drifting behind a glass card */
[data-layout="aurora"] { --ground: #07111f; --surface: rgba(16, 26, 44, 0.72); --surface-2: rgba(255,255,255,0.05); --surface-3: rgba(255,255,255,0.09); --line: rgba(255,255,255,0.14); --line-soft: rgba(255,255,255,0.1); --ink: #eef4ff; --muted: #a9b7cc; --faint: #7a889c; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="aurora"] .live-bg { display: block; background: radial-gradient(1200px 600px at 20% -10%, rgba(224,46,61,.25), transparent 60%), #07111f; }
[data-layout="aurora"] .orb { position: absolute; border-radius: 50%; filter: blur(70px); opacity: .55; animation: drift 18s ease-in-out infinite alternate; }
[data-layout="aurora"] .orb.one { width: 46vw; height: 46vw; left: -10vw; top: -12vw; background: #e02e3d; }
[data-layout="aurora"] .orb.two { width: 40vw; height: 40vw; right: -12vw; top: 20vh; background: #3a5bff; animation-delay: -6s; }
[data-layout="aurora"] .orb.three { width: 36vw; height: 36vw; left: 30vw; bottom: -14vw; background: #17b3a6; animation-delay: -12s; }
@keyframes drift { from { transform: translate(0, 0) scale(1); } to { transform: translate(6vw, 5vh) scale(1.12); } }
[data-layout="aurora"] .card { backdrop-filter: blur(18px); -webkit-backdrop-filter: blur(18px); border-radius: 16px; border-color: rgba(255,255,255,.12); box-shadow: 0 30px 80px -30px rgba(0,0,0,.8); }
[data-layout="aurora"] .card:hover { border-color: rgba(255,255,255,.2); }
[data-layout="aurora"] .hero, [data-layout="aurora"] .quick { display: flex; }
[data-layout="aurora"] .desc, [data-layout="aurora"] .usage, [data-layout="aurora"] .cfg, [data-layout="aurora"] .row { border-radius: 12px; }

/* waves: a gradient sky with two slow tides */
[data-layout="waves"] { --ground: #0b1220; --surface: #111a2b; --surface-2: #17233a; --surface-3: #1f2d47; --line: #2a3a58; --line-soft: #1f2d47; --ink: #eaf0ff; --muted: #a5b3cf; --faint: #74829e; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="waves"] .live-bg { display: block; background: linear-gradient(180deg, #0b1220 0%, #12203a 60%, #0b1220 100%); }
[data-layout="waves"] .wave { position: absolute; left: -50%; width: 200%; height: 46vh; bottom: -12vh; border-radius: 45%; opacity: .35; animation: tide 14s linear infinite; }
[data-layout="waves"] .wave.one { background: linear-gradient(90deg, #e02e3d, #7a1f2b); }
[data-layout="waves"] .wave.two { background: linear-gradient(90deg, #2f6df6, #17b3a6); bottom: -18vh; opacity: .25; animation-duration: 22s; animation-direction: reverse; }
@keyframes tide { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
[data-layout="waves"] .card { border-radius: 14px; overflow: clip; overflow-clip-margin: 0; }
[data-layout="waves"] .card:has(.menu.open) { overflow: visible; }
[data-layout="waves"] .card-head { background: linear-gradient(90deg, rgba(224,46,61,.35), rgba(47,109,246,.25)); border-bottom-color: rgba(255,255,255,.1); }
[data-layout="waves"] .hero, [data-layout="waves"] .quick { display: flex; }

/* network: a black screen with a slow constellation behind it */
[data-layout="network"] { --ground: #050608; --surface: #0c0e12; --surface-2: #12151b; --surface-3: #1a1e26; --line: #232833; --line-soft: #1a1e26; --ink: #e9edf3; --muted: #97a3b3; --faint: #66717f; --row-bg: rgba(255,255,255,0.03); --row-line: rgba(255,255,255,0.08); --row-bg-h: rgba(255,255,255,0.06); --row-line-h: rgba(255,255,255,0.16); }
[data-layout="network"] .live-bg { display: block; background: #050608; }
[data-layout="network"] canvas { position: absolute; inset: 0; width: 100%; height: 100%; opacity: .9; }
[data-layout="network"] .card { border-radius: 6px; border-color: #232833; box-shadow: 0 0 0 1px rgba(224,46,61,.15), 0 30px 60px -30px rgba(0,0,0,.9); }
[data-layout="network"] .card-head { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; letter-spacing: .5px; }
[data-layout="network"] .desc td, [data-layout="network"] .usage-labels, [data-layout="network"] .stat-v { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
[data-layout="network"] .desc, [data-layout="network"] .usage, [data-layout="network"] .cfg, [data-layout="network"] .row, [data-layout="network"] .btn, [data-layout="network"] .tag, [data-layout="network"] .quick > div { border-radius: 4px; }
[data-layout="network"] .hero, [data-layout="network"] .quick { display: flex; }

/* minimal: paper, ink, and nothing else */
[data-layout="minimal"] { color-scheme: light; --ground: #fafafa; --surface: #fff; --surface-2: #f5f5f5; --surface-3: #ececec; --line: #e2e2e2; --line-soft: #ededed; --ink: #111; --muted: #666; --faint: #999; --accent: #c81f2e; --accent-hover: #a81826; --ok: #1a7f3c; --warn: #8a6206; --bad: #c62f28; --tag-green-bg: #f6ffed; --tag-green-line: #b7eb8f; --tag-green-ink: #389e0d; --tag-red-bg: #fff2f0; --tag-red-line: #ffccc7; --tag-red-ink: #cf1322; --tag-orange-bg: #fff7e6; --tag-orange-line: #ffd591; --tag-orange-ink: #d46b08; --tag-purple-bg: #f9f0ff; --tag-purple-line: #d3adf7; --tag-purple-ink: #531dab; --tag-blue-bg: #e6f4ff; --tag-blue-line: #91caff; --tag-blue-ink: #0958d9; --tag-cyan-bg: #e6fffb; --tag-cyan-line: #87e8de; --tag-cyan-ink: #08979c; --row-bg: #fff; --row-line: #e8e8e8; --row-bg-h: #fafafa; --row-line-h: #d0d0d0; }
[data-layout="minimal"] .card { border: 0; box-shadow: none; background: transparent; }
[data-layout="minimal"] .card:hover { box-shadow: none; }
[data-layout="minimal"] .card-head { padding-inline: 0; border-bottom: 2px solid var(--ink); font-size: 22px; font-weight: 700; letter-spacing: -.01em; }
[data-layout="minimal"] .card-body { padding-inline: 0; }
[data-layout="minimal"] .desc { border: 0; border-radius: 0; }
[data-layout="minimal"] .desc th { background: transparent; border-inline-end: 0; padding-inline-start: 0; }
[data-layout="minimal"] .desc th, [data-layout="minimal"] .desc td { border-bottom-color: var(--line-soft); }
[data-layout="minimal"] .usage { background: transparent; border: 0; border-top: 1px solid var(--line); border-bottom: 1px solid var(--line); border-radius: 0; padding-inline: 0; }
[data-layout="minimal"] .usage-used { font-size: 28px; }
[data-layout="minimal"] .toolbar-btn { border: 0; background: transparent; }
[data-layout="minimal"] .row { border-radius: 0; border-inline: 0; border-top: 0; padding-inline: 0; }
[data-layout="minimal"] .row:first-child { border-top: 1px solid var(--row-line); }
[data-layout="minimal"] .cfg { border-radius: 0; background: transparent; }
[data-layout="minimal"] .btn.lg.primary { border-radius: 0; }
[data-layout="minimal"] .divider { font-size: 13px; text-transform: uppercase; letter-spacing: .12em; color: var(--muted); }

/* midnight: pure black under a faint grid, the panel's own red glowing */
[data-layout="midnight"] { --ground: #000; --surface: #0a0a0c; --surface-2: #121215; --surface-3: #1a1a1e; --line: #26262b; --line-soft: #17171b; }
[data-layout="midnight"] .live-bg { display: block; background: radial-gradient(900px 500px at 50% -10%, rgba(224,46,61,.28), transparent 65%), #000; }
[data-layout="midnight"] .grid { position: absolute; inset: 0; background-image: linear-gradient(to right, rgba(255,255,255,.045) 1px, transparent 1px), linear-gradient(to bottom, rgba(255,255,255,.045) 1px, transparent 1px); background-size: 40px 40px; mask-image: radial-gradient(ellipse at 50% 0%, #000 30%, transparent 75%); -webkit-mask-image: radial-gradient(ellipse at 50% 0%, #000 30%, transparent 75%); }
[data-layout="midnight"] .card { border-radius: 14px; border-color: rgba(224,46,61,.3); box-shadow: 0 0 0 1px rgba(224,46,61,.12), 0 0 60px -10px rgba(224,46,61,.35), 0 40px 80px -40px #000; }
[data-layout="midnight"] .card-head { border-bottom-color: rgba(224,46,61,.25); }
[data-layout="midnight"] .hero, [data-layout="midnight"] .quick { display: flex; }
[data-layout="midnight"] .quick > div { border-color: rgba(224,46,61,.2); }

/* ember: charcoal with a warm fire glowing up from below */
[data-layout="ember"] { --ground: #0f0b0a; --surface: #1a1412; --surface-2: #221a17; --surface-3: #2c221e; --line: #3a2c26; --line-soft: #2a201c; --ink: #fbf2ec; --muted: #b8a59a; --faint: #7f6f66; --accent: #ff6a3d; --accent-hover: #ff8a63; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="ember"] .live-bg { display: block; background: radial-gradient(900px 520px at 50% 115%, rgba(255,106,61,.45), rgba(224,46,61,.18) 45%, transparent 70%), #0f0b0a; }
[data-layout="ember"] .orb { position: absolute; border-radius: 50%; filter: blur(60px); opacity: .5; animation: rise 16s ease-in-out infinite alternate; }
[data-layout="ember"] .orb.one { width: 34vw; height: 34vw; left: 8vw; bottom: -20vw; background: #ff6a3d; }
[data-layout="ember"] .orb.two { width: 26vw; height: 26vw; right: 10vw; bottom: -16vw; background: #e02e3d; animation-delay: -7s; }
[data-layout="ember"] .orb.three { display: none; }
@keyframes rise { from { transform: translateY(0) scale(1); } to { transform: translateY(-10vh) scale(1.15); } }
[data-layout="ember"] .card { border-radius: 18px; border-color: rgba(255,106,61,.22); box-shadow: 0 30px 80px -30px rgba(0,0,0,.9), 0 0 0 1px rgba(255,106,61,.08); }
[data-layout="ember"] .card-head { background: linear-gradient(180deg, rgba(255,106,61,.12), transparent); }
[data-layout="ember"] .hero, [data-layout="ember"] .quick { display: flex; }

/* ocean: deep water, a cyan surface, glass */
[data-layout="ocean"] { --ground: #04121c; --surface: rgba(10, 30, 46, .78); --surface-2: rgba(255,255,255,.05); --surface-3: rgba(255,255,255,.09); --line: rgba(120,200,240,.18); --line-soft: rgba(120,200,240,.1); --ink: #e8f6ff; --muted: #9cc4d8; --faint: #6b8fa3; --accent: #19b8e6; --accent-hover: #3fcbf2; --accent-ink: #04121c; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="ocean"] .live-bg { display: block; background: linear-gradient(180deg, #06202f 0%, #04121c 55%, #020a10 100%); }
[data-layout="ocean"] .wave { position: absolute; left: -50%; width: 200%; height: 52vh; top: -30vh; border-radius: 42%; opacity: .28; animation: tide 26s linear infinite; background: linear-gradient(90deg, #19b8e6, #0b5f8a); }
[data-layout="ocean"] .wave.two { top: -36vh; opacity: .18; animation-duration: 38s; animation-direction: reverse; background: linear-gradient(90deg, #17b3a6, #19b8e6); }
[data-layout="ocean"] .card { backdrop-filter: blur(16px); -webkit-backdrop-filter: blur(16px); border-radius: 16px; box-shadow: 0 30px 80px -30px rgba(0,0,0,.8); }
[data-layout="ocean"] .hero, [data-layout="ocean"] .quick { display: flex; }

/* forest: dark moss, emerald light through the canopy */
[data-layout="forest"] { --ground: #08120c; --surface: #0f1b13; --surface-2: #142319; --surface-3: #1b2e21; --line: #244030; --line-soft: #1a2e22; --ink: #eef8f0; --muted: #a3bfab; --faint: #6f8a76; --accent: #2ecc71; --accent-hover: #4ddb88; --accent-ink: #08120c; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="forest"] .live-bg { display: block; background: radial-gradient(800px 500px at 80% -10%, rgba(46,204,113,.22), transparent 60%), radial-gradient(700px 400px at 0% 100%, rgba(23,179,166,.14), transparent 60%), #08120c; }
[data-layout="forest"] .orb { position: absolute; border-radius: 50%; filter: blur(80px); opacity: .35; animation: drift 24s ease-in-out infinite alternate; }
[data-layout="forest"] .orb.one { width: 40vw; height: 40vw; right: -14vw; top: -18vw; background: #2ecc71; }
[data-layout="forest"] .orb.two { width: 30vw; height: 30vw; left: -10vw; bottom: -12vw; background: #17b3a6; animation-delay: -9s; }
[data-layout="forest"] .orb.three { display: none; }
[data-layout="forest"] .card { border-radius: 16px; border-color: rgba(46,204,113,.2); }
[data-layout="forest"] .hero, [data-layout="forest"] .quick { display: flex; }

/* sunset: violet to amber across the sky, warm glass */
[data-layout="sunset"] { --ground: #1a0f2e; --surface: rgba(38, 20, 60, .72); --surface-2: rgba(255,255,255,.06); --surface-3: rgba(255,255,255,.1); --line: rgba(255,190,120,.2); --line-soft: rgba(255,255,255,.1); --ink: #fff4ea; --muted: #d9bfc9; --faint: #9c8598; --accent: #ff8c42; --accent-hover: #ffa86a; --accent-ink: #1a0f2e; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="sunset"] .live-bg { display: block; background: linear-gradient(180deg, #2b1449 0%, #6a2a5f 45%, #c8553d 80%, #ff8c42 100%); }
[data-layout="sunset"] .orb { position: absolute; border-radius: 50%; filter: blur(40px); opacity: .7; }
[data-layout="sunset"] .orb.one { width: 34vw; height: 34vw; left: 50%; bottom: -14vw; transform: translateX(-50%); background: radial-gradient(circle, #ffd27a, #ff8c42 60%, transparent 70%); animation: none; }
[data-layout="sunset"] .orb.two, [data-layout="sunset"] .orb.three { display: none; }
[data-layout="sunset"] .card { backdrop-filter: blur(20px); -webkit-backdrop-filter: blur(20px); border-radius: 18px; box-shadow: 0 30px 80px -30px rgba(0,0,0,.7); }
[data-layout="sunset"] .hero, [data-layout="sunset"] .quick { display: flex; }

/* neon: black, magenta and cyan edges that glow */
[data-layout="neon"] { --ground: #050008; --surface: #0c0512; --surface-2: #130a1c; --surface-3: #1b1026; --line: #ff2bd6; --line-soft: rgba(255,43,214,.35); --ink: #f6ecff; --muted: #c9a6e6; --faint: #8b6fa6; --accent: #00e5ff; --accent-hover: #5df0ff; --accent-ink: #050008; --row-bg: rgba(255,43,214,.05); --row-line: rgba(255,43,214,.3); --row-bg-h: rgba(0,229,255,.06); --row-line-h: rgba(0,229,255,.6); }
[data-layout="neon"] .live-bg { display: block; background: radial-gradient(700px 400px at 0% 0%, rgba(255,43,214,.18), transparent 60%), radial-gradient(700px 400px at 100% 100%, rgba(0,229,255,.16), transparent 60%), #050008; }
[data-layout="neon"] .grid { position: absolute; inset: 0; background-image: linear-gradient(to right, rgba(255,43,214,.08) 1px, transparent 1px), linear-gradient(to bottom, rgba(0,229,255,.08) 1px, transparent 1px); background-size: 32px 32px; }
[data-layout="neon"] .card { border-radius: 8px; border-color: rgba(255,43,214,.6); box-shadow: 0 0 0 1px rgba(255,43,214,.25), 0 0 30px -6px rgba(255,43,214,.7), inset 0 0 40px -30px rgba(0,229,255,.6); }
[data-layout="neon"] .card-head { text-shadow: 0 0 12px rgba(255,43,214,.8); border-bottom-color: rgba(255,43,214,.4); font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; letter-spacing: 1px; text-transform: uppercase; }
[data-layout="neon"] .btn.lg.primary { box-shadow: 0 0 20px -4px #00e5ff; }
[data-layout="neon"] .hero, [data-layout="neon"] .quick { display: flex; }

/* glass: frosted white over a colourful blur, light */
[data-layout="glass"] { color-scheme: light; --ok: #1a7f3c; --warn: #8a6206; --bad: #c62f28; --tag-green-bg: #f6ffed; --tag-green-line: #b7eb8f; --tag-green-ink: #389e0d; --tag-red-bg: #fff2f0; --tag-red-line: #ffccc7; --tag-red-ink: #cf1322; --tag-orange-bg: #fff7e6; --tag-orange-line: #ffd591; --tag-orange-ink: #d46b08; --tag-purple-bg: #f9f0ff; --tag-purple-line: #d3adf7; --tag-purple-ink: #531dab; --tag-blue-bg: #e6f4ff; --tag-blue-line: #91caff; --tag-blue-ink: #0958d9; --tag-cyan-bg: #e6fffb; --tag-cyan-line: #87e8de; --tag-cyan-ink: #08979c; --row-bg: #fff; --row-line: #e8e8e8; --row-bg-h: #fafafa; --row-line-h: #d0d0d0; --ground: #dfe6f3; --surface: rgba(255,255,255,.62); --surface-2: rgba(255,255,255,.55); --surface-3: rgba(255,255,255,.8); --line: rgba(255,255,255,.7); --line-soft: rgba(0,0,0,.06); --ink: #141a26; --muted: #5b6577; --faint: #8b93a3; --accent: #4f46e5; --accent-hover: #6366f1; --row-bg: rgba(255,255,255,.5); --row-line: rgba(0,0,0,.06); --row-bg-h: rgba(255,255,255,.75); --row-line-h: rgba(0,0,0,.12); }
[data-layout="glass"] .live-bg { display: block; background: linear-gradient(135deg, #dfe6f3, #eadff5 50%, #d9f0ea); }
[data-layout="glass"] .orb { position: absolute; border-radius: 50%; filter: blur(70px); opacity: .55; animation: drift 20s ease-in-out infinite alternate; }
[data-layout="glass"] .orb.one { width: 44vw; height: 44vw; left: -12vw; top: -10vw; background: #8b5cf6; }
[data-layout="glass"] .orb.two { width: 38vw; height: 38vw; right: -10vw; top: 30vh; background: #22d3ee; animation-delay: -8s; }
[data-layout="glass"] .orb.three { width: 34vw; height: 34vw; left: 35vw; bottom: -16vw; background: #f472b6; animation-delay: -14s; }
[data-layout="glass"] .card { backdrop-filter: blur(24px) saturate(1.4); -webkit-backdrop-filter: blur(24px) saturate(1.4); border-radius: 20px; box-shadow: 0 20px 60px -20px rgba(20,26,38,.35), inset 0 1px 0 rgba(255,255,255,.9); }
[data-layout="glass"] .hero, [data-layout="glass"] .quick { display: flex; }

/* paper: warm cream, serif headings, ruled lines */
[data-layout="paper"] { color-scheme: light; --ok: #1a7f3c; --warn: #8a6206; --bad: #c62f28; --tag-green-bg: #f6ffed; --tag-green-line: #b7eb8f; --tag-green-ink: #389e0d; --tag-red-bg: #fff2f0; --tag-red-line: #ffccc7; --tag-red-ink: #cf1322; --tag-orange-bg: #fff7e6; --tag-orange-line: #ffd591; --tag-orange-ink: #d46b08; --tag-purple-bg: #f9f0ff; --tag-purple-line: #d3adf7; --tag-purple-ink: #531dab; --tag-blue-bg: #e6f4ff; --tag-blue-line: #91caff; --tag-blue-ink: #0958d9; --tag-cyan-bg: #e6fffb; --tag-cyan-line: #87e8de; --tag-cyan-ink: #08979c; --row-bg: #fff; --row-line: #e8e8e8; --row-bg-h: #fafafa; --row-line-h: #d0d0d0; --ground: #f4efe6; --surface: #fbf8f2; --surface-2: #f3eee4; --surface-3: #ebe4d6; --line: #d9d0be; --line-soft: #e8e1d2; --ink: #2b241c; --muted: #6f6355; --faint: #9a8f80; --accent: #9c2f2f; --accent-hover: #7f2424; --row-bg: #fbf8f2; --row-line: #e3dbcb; --row-bg-h: #f6f1e7; --row-line-h: #cfc4ae; }
[data-layout="paper"] body, [data-layout="paper"] .card-head, [data-layout="paper"] .hero h1, [data-layout="paper"] .divider { font-family: Georgia, 'Times New Roman', 'Noto Serif', serif; }
[data-layout="paper"] .card { border-radius: 4px; border-color: var(--line); box-shadow: 0 1px 0 #fff inset, 0 12px 30px -20px rgba(43,36,28,.35); background-image: repeating-linear-gradient(0deg, transparent 0 27px, rgba(43,36,28,.035) 27px 28px); }
[data-layout="paper"] .card-head { border-bottom: 2px solid var(--ink); font-size: 21px; }
[data-layout="paper"] .hero, [data-layout="paper"] .quick { display: flex; }
[data-layout="paper"] .quick > div, [data-layout="paper"] .desc, [data-layout="paper"] .usage, [data-layout="paper"] .cfg, [data-layout="paper"] .row { border-radius: 3px; }

/* terminal: green phosphor on black, scanlines */
[data-layout="terminal"] { --ground: #020402; --surface: #050a05; --surface-2: #08110a; --surface-3: #0c180f; --line: #1d3d24; --line-soft: #12271a; --ink: #b6f7c1; --muted: #63b072; --faint: #3f7a4c; --accent: #37ff6e; --accent-hover: #7bff9d; --accent-ink: #020402; --ok: #37ff6e; --row-bg: rgba(55,255,110,.04); --row-line: rgba(55,255,110,.25); --row-bg-h: rgba(55,255,110,.08); --row-line-h: rgba(55,255,110,.5); --tag-green-bg: #05200c; --tag-green-line: #1e6b33; --tag-green-ink: #37ff6e; --tag-red-bg: #200808; --tag-red-line: #6b1e1e; --tag-red-ink: #ff6b6b; --tag-orange-bg: #201a08; --tag-orange-line: #6b5a1e; --tag-orange-ink: #ffd166; --tag-purple-bg: #0c0c1c; --tag-purple-line: #2b2b6b; --tag-purple-ink: #9d9dff; --tag-blue-bg: #06141c; --tag-blue-line: #1e4a6b; --tag-blue-ink: #66c7ff; --tag-cyan-bg: #061c1a; --tag-cyan-line: #1e6b66; --tag-cyan-ink: #66ffe6; }
[data-layout="terminal"] body, [data-layout="terminal"] .card, [data-layout="terminal"] .btn, [data-layout="terminal"] .tag, [data-layout="terminal"] .desc td, [data-layout="terminal"] .stat-v { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace; }
[data-layout="terminal"] .live-bg { display: block; background: #020402; }
[data-layout="terminal"] .grid { position: absolute; inset: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,.35) 0 2px, transparent 2px 4px); mix-blend-mode: multiply; opacity: .6; }
[data-layout="terminal"] .card { border-radius: 2px; border: 1px solid #1d3d24; box-shadow: 0 0 0 1px rgba(55,255,110,.12), 0 0 40px -10px rgba(55,255,110,.35); }
[data-layout="terminal"] .card-head { text-transform: uppercase; letter-spacing: 2px; border-bottom: 1px dashed #1d3d24; }
[data-layout="terminal"] .card-head .card-title > span:first-child::before { content: '> '; color: var(--accent); }
[data-layout="terminal"] .hero, [data-layout="terminal"] .quick { display: flex; }
[data-layout="terminal"] .desc, [data-layout="terminal"] .usage, [data-layout="terminal"] .cfg, [data-layout="terminal"] .row, [data-layout="terminal"] .btn, [data-layout="terminal"] .tag, [data-layout="terminal"] .quick > div { border-radius: 2px; }

/* carbon: woven carbon under brushed steel, sharp and quiet */
[data-layout="carbon"] { --ground: #101113; --surface: #17191c; --surface-2: #1e2125; --surface-3: #262a2f; --line: #33383e; --line-soft: #272b30; --ink: #eef0f2; --muted: #a3aab2; --faint: #6f767e; --accent: #c9ced4; --accent-hover: #e6e9ec; --accent-ink: #101113; --row-bg: rgba(255,255,255,.03); --row-line: rgba(255,255,255,.08); --row-bg-h: rgba(255,255,255,.06); --row-line-h: rgba(255,255,255,.18); }
[data-layout="carbon"] .live-bg { display: block; background-color: #101113; background-image: linear-gradient(27deg, #151618 5px, transparent 5px), linear-gradient(207deg, #151618 5px, transparent 5px), linear-gradient(27deg, #0c0d0f 5px, transparent 5px), linear-gradient(207deg, #0c0d0f 5px, transparent 5px), linear-gradient(90deg, #131416 10px, transparent 10px); background-size: 20px 20px; background-position: 0 5px, 10px 0, 0 10px, 10px 5px, 0 0; }
[data-layout="carbon"] .card { border-radius: 4px; border-color: #33383e; box-shadow: inset 0 1px 0 rgba(255,255,255,.06), 0 30px 60px -30px #000; background: linear-gradient(180deg, #1b1e22, #17191c); }
[data-layout="carbon"] .card-head { background: linear-gradient(180deg, #23272c, #1b1e22); border-bottom-color: #33383e; letter-spacing: .5px; text-transform: uppercase; font-size: 14px; }
[data-layout="carbon"] .btn.lg.primary { background: linear-gradient(180deg, #e6e9ec, #b9bfc6); color: #101113; }
[data-layout="carbon"] .hero, [data-layout="carbon"] .quick { display: flex; }
[data-layout="carbon"] .desc, [data-layout="carbon"] .usage, [data-layout="carbon"] .cfg, [data-layout="carbon"] .row, [data-layout="carbon"] .btn, [data-layout="carbon"] .tag, [data-layout="carbon"] .quick > div { border-radius: 3px; }

/* royal: navy and gold, a card with a gilded edge */
[data-layout="royal"] { --ground: #070b1a; --surface: #0d1330; --surface-2: #121a3c; --surface-3: #1a2450; --line: #2a3566; --line-soft: #1c2650; --ink: #f4f0e4; --muted: #b9b39c; --faint: #7f7a68; --accent: #d4af37; --accent-hover: #e6c65a; --accent-ink: #070b1a; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="royal"] .live-bg { display: block; background: radial-gradient(900px 600px at 50% -20%, rgba(212,175,55,.18), transparent 60%), linear-gradient(180deg, #070b1a, #0b1128); }
[data-layout="royal"] .card { border-radius: 14px; border: 1px solid rgba(212,175,55,.45); box-shadow: 0 0 0 4px #0d1330, 0 0 0 5px rgba(212,175,55,.35), 0 40px 80px -40px #000; }
[data-layout="royal"] .card-head { border-bottom: 1px solid rgba(212,175,55,.35); font-family: Georgia, 'Times New Roman', serif; letter-spacing: .5px; }
[data-layout="royal"] .hero h1 { font-family: Georgia, 'Times New Roman', serif; }
[data-layout="royal"] .hero, [data-layout="royal"] .quick { display: flex; }

/* sakura: white and blossom pink, soft and round, light */
[data-layout="sakura"] { color-scheme: light; --ok: #1a7f3c; --warn: #8a6206; --bad: #c62f28; --tag-green-bg: #f6ffed; --tag-green-line: #b7eb8f; --tag-green-ink: #389e0d; --tag-red-bg: #fff2f0; --tag-red-line: #ffccc7; --tag-red-ink: #cf1322; --tag-orange-bg: #fff7e6; --tag-orange-line: #ffd591; --tag-orange-ink: #d46b08; --tag-purple-bg: #f9f0ff; --tag-purple-line: #d3adf7; --tag-purple-ink: #531dab; --tag-blue-bg: #e6f4ff; --tag-blue-line: #91caff; --tag-blue-ink: #0958d9; --tag-cyan-bg: #e6fffb; --tag-cyan-line: #87e8de; --tag-cyan-ink: #08979c; --row-bg: #fff; --row-line: #e8e8e8; --row-bg-h: #fafafa; --row-line-h: #d0d0d0; --ground: #fff6f8; --surface: #ffffff; --surface-2: #fff1f4; --surface-3: #ffe4ea; --line: #f5cfd9; --line-soft: #fbe3ea; --ink: #3a2530; --muted: #8a6b78; --faint: #b596a3; --accent: #e8618c; --accent-hover: #d84a78; --row-bg: #fff; --row-line: #f5dbe3; --row-bg-h: #fff6f8; --row-line-h: #eab6c6; }
[data-layout="sakura"] .live-bg { display: block; background: radial-gradient(700px 400px at 100% 0%, rgba(232,97,140,.16), transparent 60%), radial-gradient(600px 400px at 0% 100%, rgba(255,183,197,.25), transparent 60%), #fff6f8; }
[data-layout="sakura"] .orb { position: absolute; width: 14px; height: 14px; border-radius: 50% 0; background: #f7b6c8; opacity: .8; animation: petal 14s linear infinite; }
[data-layout="sakura"] .orb.one { left: 20vw; top: -5vh; }
[data-layout="sakura"] .orb.two { left: 60vw; top: -12vh; animation-delay: -5s; width: 10px; height: 10px; }
[data-layout="sakura"] .orb.three { left: 85vw; top: -2vh; animation-delay: -9s; }
@keyframes petal { from { transform: translateY(0) rotate(0deg); } to { transform: translateY(110vh) rotate(540deg); } }
[data-layout="sakura"] .card { border-radius: 22px; box-shadow: 0 20px 50px -25px rgba(232,97,140,.45); }
[data-layout="sakura"] .hero, [data-layout="sakura"] .quick { display: flex; }
[data-layout="sakura"] .quick > div, [data-layout="sakura"] .desc, [data-layout="sakura"] .usage, [data-layout="sakura"] .cfg, [data-layout="sakura"] .row, [data-layout="sakura"] .btn { border-radius: 14px; }

/* frost: ice blue and white, crisp, light */
[data-layout="frost"] { color-scheme: light; --ok: #1a7f3c; --warn: #8a6206; --bad: #c62f28; --tag-green-bg: #f6ffed; --tag-green-line: #b7eb8f; --tag-green-ink: #389e0d; --tag-red-bg: #fff2f0; --tag-red-line: #ffccc7; --tag-red-ink: #cf1322; --tag-orange-bg: #fff7e6; --tag-orange-line: #ffd591; --tag-orange-ink: #d46b08; --tag-purple-bg: #f9f0ff; --tag-purple-line: #d3adf7; --tag-purple-ink: #531dab; --tag-blue-bg: #e6f4ff; --tag-blue-line: #91caff; --tag-blue-ink: #0958d9; --tag-cyan-bg: #e6fffb; --tag-cyan-line: #87e8de; --tag-cyan-ink: #08979c; --row-bg: #fff; --row-line: #e8e8e8; --row-bg-h: #fafafa; --row-line-h: #d0d0d0; --ground: #e9f2fb; --surface: #ffffff; --surface-2: #f2f7fc; --surface-3: #e3edf8; --line: #cfe0f1; --line-soft: #e1ecf7; --ink: #10233a; --muted: #5c728a; --faint: #8fa3b8; --accent: #1f6fe0; --accent-hover: #1858b5; --row-bg: #fff; --row-line: #dbe7f3; --row-bg-h: #f5f9fd; --row-line-h: #b9cfe6; }
[data-layout="frost"] .live-bg { display: block; background: linear-gradient(180deg, #e9f2fb, #f6fafe 60%, #e9f2fb); }
[data-layout="frost"] .grid { position: absolute; inset: 0; background-image: radial-gradient(circle at 1px 1px, rgba(31,111,224,.12) 1px, transparent 1.5px); background-size: 22px 22px; mask-image: radial-gradient(ellipse at 50% 0%, #000 20%, transparent 70%); -webkit-mask-image: radial-gradient(ellipse at 50% 0%, #000 20%, transparent 70%); }
[data-layout="frost"] .card { border-radius: 14px; border-color: #cfe0f1; box-shadow: 0 16px 40px -24px rgba(16,35,58,.35), inset 0 1px 0 #fff; }
[data-layout="frost"] .hero, [data-layout="frost"] .quick { display: flex; }

/* graphite: flat mid-grey surfaces, one blue, big radii */
[data-layout="graphite"] { --ground: #1f2124; --surface: #292c30; --surface-2: #33373c; --surface-3: #3d4248; --line: #4a5057; --line-soft: #3a3f45; --ink: #f3f4f6; --muted: #b3b9c1; --faint: #7f868f; --accent: #3b82f6; --accent-hover: #60a5fa; --row-bg: rgba(255,255,255,.04); --row-line: rgba(255,255,255,.08); --row-bg-h: rgba(255,255,255,.07); --row-line-h: rgba(255,255,255,.16); }
[data-layout="graphite"] .card { border: 0; border-radius: 24px; box-shadow: 0 1px 2px rgba(0,0,0,.3), 0 24px 48px -32px #000; }
[data-layout="graphite"] .card-head { border-bottom: 0; padding-bottom: 4px; }
[data-layout="graphite"] .hero, [data-layout="graphite"] .quick { display: flex; }
[data-layout="graphite"] .quick > div, [data-layout="graphite"] .desc, [data-layout="graphite"] .usage, [data-layout="graphite"] .cfg, [data-layout="graphite"] .row { border: 0; border-radius: 16px; }
[data-layout="graphite"] .btn, [data-layout="graphite"] .tag { border-radius: 999px; }

/* mesh: a slow multicolour mesh behind dark glass */
[data-layout="mesh"] { --ground: #0b0b14; --surface: rgba(18, 18, 32, .7); --surface-2: rgba(255,255,255,.05); --surface-3: rgba(255,255,255,.09); --line: rgba(255,255,255,.14); --line-soft: rgba(255,255,255,.09); --ink: #f2f0ff; --muted: #bcb6d6; --faint: #837d9e; --accent: #a855f7; --accent-hover: #c084fc; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="mesh"] .live-bg { display: block; background: #0b0b14; }
[data-layout="mesh"] .orb { position: absolute; border-radius: 50%; filter: blur(60px); opacity: .7; animation: meshmove 22s ease-in-out infinite alternate; }
[data-layout="mesh"] .orb.one { width: 50vw; height: 50vw; left: -15vw; top: -20vw; background: #a855f7; }
[data-layout="mesh"] .orb.two { width: 44vw; height: 44vw; right: -14vw; top: 10vh; background: #ec4899; animation-delay: -8s; }
[data-layout="mesh"] .orb.three { width: 46vw; height: 46vw; left: 25vw; bottom: -22vw; background: #06b6d4; animation-delay: -15s; }
@keyframes meshmove { 0% { transform: translate(0,0) scale(1); } 50% { transform: translate(8vw,-6vh) scale(1.2); } 100% { transform: translate(-4vw,6vh) scale(.95); } }
[data-layout="mesh"] .card { backdrop-filter: blur(22px) saturate(1.3); -webkit-backdrop-filter: blur(22px) saturate(1.3); border-radius: 20px; box-shadow: 0 30px 80px -30px rgba(0,0,0,.8), inset 0 1px 0 rgba(255,255,255,.12); }
[data-layout="mesh"] .hero, [data-layout="mesh"] .quick { display: flex; }

/* retro: synthwave -- a sun on the horizon and a grid running to it */
[data-layout="retro"] { --ground: #12041f; --surface: rgba(24, 6, 44, .82); --surface-2: rgba(255,255,255,.05); --surface-3: rgba(255,255,255,.09); --line: rgba(255,0,153,.45); --line-soft: rgba(255,255,255,.1); --ink: #fff0fb; --muted: #d5a6e6; --faint: #9a72b3; --accent: #ff0099; --accent-hover: #ff47b8; --row-bg: rgba(255,255,255,0.04); --row-line: rgba(255,255,255,0.1); --row-bg-h: rgba(255,255,255,0.07); --row-line-h: rgba(255,255,255,0.2); }
[data-layout="retro"] .live-bg { display: block; background: linear-gradient(180deg, #12041f 0%, #3a0a5c 45%, #12041f 46%); }
[data-layout="retro"] .orb.one { position: absolute; left: 50%; top: 22vh; width: 34vmin; height: 34vmin; transform: translateX(-50%); border-radius: 50%; background: linear-gradient(180deg, #ffd166 0%, #ff0099 60%, #ff0099 100%); mask-image: repeating-linear-gradient(0deg, transparent 0 6px, #000 6px 14px); -webkit-mask-image: repeating-linear-gradient(0deg, transparent 0 6px, #000 6px 14px); opacity: .9; filter: none; animation: none; }
[data-layout="retro"] .orb.two, [data-layout="retro"] .orb.three { display: none; }
[data-layout="retro"] .grid { position: absolute; left: -50%; right: -50%; top: 46%; bottom: 0; background-image: linear-gradient(to right, rgba(255,0,153,.55) 1px, transparent 1px), linear-gradient(to bottom, rgba(255,0,153,.55) 1px, transparent 1px); background-size: 60px 60px; transform: perspective(400px) rotateX(60deg); transform-origin: 50% 0; animation: gridrun 3s linear infinite; }
@keyframes gridrun { from { background-position: 0 0; } to { background-position: 0 60px; } }
[data-layout="retro"] .card { backdrop-filter: blur(10px); -webkit-backdrop-filter: blur(10px); border-radius: 6px; box-shadow: 0 0 0 1px rgba(255,0,153,.25), 0 0 40px -10px rgba(255,0,153,.7); }
[data-layout="retro"] .card-head { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; text-transform: uppercase; letter-spacing: 2px; text-shadow: 0 0 10px rgba(255,0,153,.8); }
[data-layout="retro"] .hero, [data-layout="retro"] .quick { display: flex; }

/* The hero and quick stats the non-classic looks open with. */
.hero { align-items: center; gap: 14px; margin-bottom: 16px; }
.hero-icon { display: grid; place-items: center; width: 52px; height: 52px; border-radius: 14px; background: #fff; box-shadow: 0 10px 24px -10px var(--accent), inset 0 0 0 1px rgba(0,0,0,.06); flex: none; }
.hero-logo { display: block; width: 34px; height: auto; }
.brand-mark { display: inline-block; width: 26px; height: auto; margin-inline-end: 8px; vertical-align: -3px; }
.hero-copy { min-width: 0; flex: 1; }
.hero-meta { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: .08em; }
.hero-live { display: inline-flex; align-items: center; gap: 5px; text-transform: none; letter-spacing: 0; color: var(--ok); }
.hero-live i { width: 7px; height: 7px; border-radius: 50%; background: var(--ok); animation: blink 1.4s ease-in-out infinite; }
.hero-live.off, .hero-live.off i { color: var(--faint); background: var(--faint); animation: none; }
.app-fixed { flex: none; }
.tag-config { margin: 0; font-weight: 600; letter-spacing: .3px; }
.hero-live.idle, .hero-live.idle i { color: var(--muted); background: var(--muted); animation: none; }
@keyframes blink { 50% { opacity: .35; } }
.hero h1 { margin: 2px 0 0; font-size: 22px; font-weight: 700; letter-spacing: -.01em; }
.hero p { margin: 2px 0 0; color: var(--muted); font-size: 13px; }
.quick { gap: 10px; margin-bottom: 16px; }
.quick > div { flex: 1 1 0; min-width: 0; padding: 12px 14px; border: 1px solid var(--line-soft); border-radius: 12px; background: var(--surface-2); display: flex; align-items: center; gap: 12px; }
.ring { --p: 0; --c: var(--accent); position: relative; width: 48px; height: 48px; border-radius: 50%; background: conic-gradient(var(--c) calc(var(--p) * 1%), var(--surface-3) 0); flex: none; display: grid; place-items: center; font-size: 11px; font-weight: 600; }
.ring::before { content: ''; position: absolute; inset: 6px; border-radius: 50%; background: var(--surface-2); }
.ring span { position: relative; }
.stat-k { font-size: 11px; color: var(--muted); text-transform: uppercase; letter-spacing: .06em; }
.stat-v { font-size: 16px; font-weight: 700; margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.stat-s { font-size: 12px; color: var(--faint); }
@media (max-width: 575px) { .quick { flex-direction: column; } }
/* Nothing on this page may be wider than a phone. Long values -- a token, an
   email, a hostname -- wrap or are cut with an ellipsis; they never push the
   card out of the viewport. */
.card { max-width: 100%; overflow: hidden; }
.card-title > span[data-i] { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.card-title .tag { flex: 0 1 auto; min-width: 0; max-width: 45%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.card-extra { flex-shrink: 0; }
.desc td { overflow-wrap: anywhere; word-break: break-word; min-width: 0; }
@media (max-width: 480px) { .desc th { width: 34%; white-space: normal; padding: 8px 10px; } .desc td { padding: 8px 10px; font-size: 13px; } }
.hero { min-width: 0; }
.hero > .hero-copy { min-width: 0; flex: 1; }
.hero-icon { flex: none; }
.hero h1, .hero p { overflow-wrap: anywhere; }
.hero-meta { flex-wrap: wrap; }
.usage-labels { min-width: 0; overflow: hidden; }
.usage-head { flex-wrap: wrap; gap: 6px; }
.row-sub { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pop-card { max-width: calc(100vw - 32px); }
.pop-card img { max-width: 100%; height: auto; }
.toast { max-width: calc(100vw - 32px); }
.menu { max-width: calc(100vw - 24px); }
/* On a phone the id beside the title is the same one in the table below. */
@media (max-width: 480px) { .card-title .tag { display: none; } .card-head { font-size: 15px; } }
</style>
</head>
<body>
{{ if .Preview }}<div class="preview-bar">Preview — {{ .Template }} — sample figures, not a customer</div>{{ end }}
<div class="live-bg" aria-hidden="true"><span class="orb one"></span><span class="orb two"></span><span class="orb three"></span><span class="wave one"></span><span class="wave two"></span><span class="grid"></span><canvas id="net"></canvas></div>
<div class="content"><div class="col">
  <div class="card">
    <div class="card-head">
      <div class="card-title"><img class="brand-mark" src="{{ .Logo }}" alt="" width="26" height="16"><span data-i="title">{{ .Page.Title }}</span><span class="tag">{{ .SubID }}</span></div>
      <div class="card-extra">
        {{ if eq .Template "classic" }}<button class="btn toolbar-btn" type="button" id="theme" data-i-title="theme">
          <span class="anticon" data-theme-icon="light">{{ index .Icons "SunOutlined" }}</span>
          <span class="anticon" data-theme-icon="dark">{{ index .Icons "MoonOutlined" }}</span>
          <span class="anticon" data-theme-icon="ultra">{{ index .Icons "MoonFilled" }}</span>
        </button>{{ end }}
        <div class="app app-fixed">
          <button class="btn toolbar-btn" type="button" data-menu="lang" data-i-title="language"><span class="anticon">{{ index .Icons "TranslationOutlined" }}</span></button>
          <div class="menu lang-menu" id="menu-lang">
            <button type="button" data-lang="en"><span>🇬🇧</span><span>English</span></button>
            <button type="button" data-lang="fa"><span>🇮🇷</span><span>فارسی</span></button>
          </div>
        </div>
      </div>
    </div>
    <div class="card-body">
      <div class="hero">
        <div class="hero-icon"><img class="hero-logo" src="{{ .Logo }}" alt="" width="34" height="20"></div>
        <div class="hero-copy">
          <div class="hero-meta"><span data-i="subSettings">Subscription</span><span class="hero-live{{ if not .Active }} off{{ end }}" id="lv-live"><i></i><span data-i="{{ if .Active }}live{{ else }}offline{{ end }}">{{ if .Active }}Live{{ else }}Off{{ end }}</span></span></div>
          <h1>{{ .Page.Title }}</h1>
          <p>{{ .Page.Name }}</p>
        </div>
      </div>
      <div class="quick">
        <div>
          <div class="ring" id="lv-ring-status" data-p="100" data-c="{{ if .Active }}var(--ok){{ else }}var(--bad){{ end }}"><span>{{ len .Devices }}</span></div>
          <div><div class="stat-k" data-i="status">Status</div><div class="stat-v" id="lv-status">{{ if eq .StatusKey "inactive" }}<span data-i="inactive">Inactive</span>{{ else if eq .StatusKey "unlimited" }}<span data-i="unlimited">Unlimited</span>{{ else }}<span data-i="active">Active</span>{{ end }}</div><div class="stat-s">{{ len .Devices }} configs</div></div>
        </div>
        <div>
          <div class="ring" id="lv-ring-usage" data-p="{{ if .HasQuota }}{{ .PercentTxt }}{{ else }}100{{ end }}" data-c="{{ if ge .Percent 90.0 }}var(--bad){{ else if ge .Percent 75.0 }}var(--warn){{ else }}var(--ok){{ end }}"><span dir="ltr" id="lv-pct">{{ if .HasQuota }}{{ .PercentTxt }}%{{ else }}∞{{ end }}</span></div>
          <div><div class="stat-k" data-i="usage">Data</div><div class="stat-v" dir="ltr" id="lv-data">{{ if .HasQuota }}{{ .Remained }}{{ else }}{{ .Used }}{{ end }}</div><div class="stat-s" dir="ltr" id="lv-data-sub">{{ if .HasQuota }}{{ .Used }} / {{ .Total }}{{ else }}<span data-i="unlimited">Unlimited</span>{{ end }}</div></div>
        </div>
        <div>
          <div class="ring" data-p="100" data-c="{{ if eq .ExpiryCls "red" }}var(--bad){{ else if eq .ExpiryCls "orange" }}var(--warn){{ else }}var(--accent){{ end }}"><span class="anticon">{{ index .Icons "ClockCircleOutlined" }}</span></div>
          <div><div class="stat-k" data-i="expiry">Time</div><div class="stat-v" dir="ltr" id="lv-expiry">{{ if .ExpiryChip }}{{ if eq .ExpiryChip "expired" }}<span data-i="expired">Expired</span>{{ else }}{{ .ExpiryChip }}{{ end }}{{ else }}∞{{ end }}</div><div class="stat-s" dir="ltr">{{ if .Expiry }}{{ .Expiry }}{{ else }}<span data-i="noExpiry">No expiry</span>{{ end }}</div></div>
        </div>
      </div>
      <table class="desc">
        <tr><th data-i="subId">Subscription ID</th><td dir="ltr">{{ .SubID }}</td></tr>
        <tr><th data-i="email">Email</th><td>{{ .Page.Name }}</td></tr>
        <tr><th data-i="status">Status</th><td id="lv-td-status">
          {{ if eq .StatusKey "inactive" }}<span class="tag red" data-i="inactive">Inactive</span>
          {{ else if eq .StatusKey "unlimited" }}<span class="tag purple" data-i="unlimited">Unlimited</span>
          {{ else }}<span class="tag green" data-i="active">Active</span>{{ end }}
        </td></tr>
        <tr><th data-i="downloaded">Downloaded</th><td dir="ltr" id="lv-down">{{ bytes .Page.DownBytes }}</td></tr>
        <tr><th data-i="uploaded">Uploaded</th><td dir="ltr" id="lv-up">{{ bytes .Page.UpBytes }}</td></tr>
        <tr><th data-i="usage">Usage</th><td dir="ltr" id="lv-used">{{ .Used }}</td></tr>
        <tr><th data-i="totalQuota">Total Quota</th><td dir="ltr">{{ .Total }}</td></tr>
        {{ if .HasQuota }}<tr><th data-i="remained">Remaining</th><td dir="ltr" id="lv-remained">{{ .Remained }}</td></tr>{{ end }}
        <tr><th data-i="lastOnline">Last Online</th><td dir="ltr" id="lv-last">{{ if .LastOnline }}{{ .LastOnline }}{{ else }}-{{ end }}</td></tr>
        <tr><th data-i="expiry">Expiry</th><td dir="ltr">{{ if .Expiry }}{{ .Expiry }}{{ else }}<span data-i="noExpiry">No expiry</span>{{ end }}</td></tr>
      </table>

      <div class="usage{{ if not .Active }} inactive{{ end }}">
        <div class="usage-head">
          <div class="usage-labels" dir="ltr"><span class="usage-used" id="lv-bar-used">{{ .Used }}</span><span class="usage-sep">/</span><span class="usage-total" id="lv-bar-total">{{ .Total }}</span></div>
          <div class="usage-chips">
            {{ if not .HasQuota }}<span class="tag purple"><span class="anticon">{{ index .Icons "ThunderboltOutlined" }}</span><span data-i="unlimited">Unlimited</span></span>{{ end }}
            {{ if .ExpiryChip }}<span class="tag {{ .ExpiryCls }}"><span class="anticon">{{ index .Icons "ClockCircleOutlined" }}</span>{{ if eq .ExpiryChip "expired" }}<span data-i="expired">Expired</span>{{ else }}{{ .ExpiryChip }}{{ end }}</span>{{ end }}
          </div>
        </div>
        {{ if .HasQuota }}<div class="bar"><i id="lv-bar" class="{{ if ge .Percent 90.0 }}red{{ else if ge .Percent 75.0 }}orange{{ else }}green{{ end }}" data-w="{{ .PercentTxt }}"></i></div>{{ end }}
        <div class="usage-foot">{{ if .HasQuota }}<span dir="ltr" id="lv-foot-remained">{{ .Remained }}</span><span class="usage-pct" dir="ltr" id="lv-foot-pct">{{ .PercentTxt }}%</span>{{ end }}</div>
      </div>

      {{ if .Usage }}
      <!-- Who spent what, and where: a row per user, a column per tunnel,
           a total at the end of each row and under each column. A plan
           shared by several people, and paid for by them together, is
           settled from this. One container, no frame inside it: the
           rows are grouped by rhythm and two rules -- under the head and
           above the sum -- rather than by boxes. -->
      <div class="cfg usage-table">
        <div class="cfg-head">
          <span class="anticon caret">{{ index .Icons "RightOutlined" }}</span>
          <span class="tag tag-config purple" data-i="usageTable">Usage by user and tunnel</span>
          <span class="cfg-count" dir="ltr">{{ .Usage.Grand }}</span>
        </div>
        <div class="cfg-body usage-body">
          <div class="usage-scroll">
            <table class="usage-grid">
              <thead><tr>
                <th class="usage-corner"><span data-i="user">User</span></th>
                {{ range .Usage.Tunnels }}<th><span class="usage-dot {{ if eq .Protocol "openvpn" }}orange{{ else }}cyan{{ end }}"></span>{{ .Name }}</th>{{ end }}
                <th class="usage-sum" data-i="total">Total</th>
              </tr></thead>
              <tbody>
                {{ range .Usage.Rows }}
                <tr><th>{{ if .User }}<span data-i="user">User</span> <span dir="ltr">{{ .User }}</span>{{ else }}{{ .Name }}{{ end }}</th>{{ range .Cells }}<td{{ if eq . "0 B" }} class="usage-zero"{{ end }}>{{ . }}</td>{{ end }}<td class="usage-sum">{{ .Total }}</td></tr>
                {{ end }}
              </tbody>
              {{ if gt (len .Usage.Rows) 1 }}<tfoot><tr class="usage-all"><th data-i="allUsers">All users</th>{{ range .Usage.Totals }}<td>{{ . }}</td>{{ end }}<td class="usage-sum">{{ .Usage.Grand }}</td></tr></tfoot>{{ end }}
            </table>
          </div>
        </div>
      </div>
      {{ end }}

      {{ if .Page.SubURL }}
      <div class="divider"><span data-i="title">Subscription info</span></div>
      <div class="links">
        <div class="row">
          <span class="tag green row-tag">SUB</span>
          <a class="row-title" href="{{ .Page.SubURL }}" target="_blank" rel="noopener noreferrer" title="{{ .Page.SubURL }}" dir="ltr">{{ .SubID }}</a>
          <div class="row-actions">
            <button class="btn sm copy" type="button" data-text="{{ .Page.SubURL }}" data-i-title="copy"><span class="anticon">{{ index .Icons "CopyOutlined" }}</span></button>
            {{ if .SubQR }}<button class="btn sm qr" type="button" title="QR"><span class="anticon">{{ index .Icons "QrcodeOutlined" }}</span></button>
            <div class="pop"><div class="pop-card"><span class="tag green qr-tag" data-i="subSettings">Subscription</span><img src="{{ .SubQR }}" width="240" height="240" alt="QR"><span class="pop-hint" data-i="tapToClose">Tap outside to close</span></div></div>{{ end }}
          </div>
        </div>
      </div>
      {{ end }}

      {{ if .Devices }}
      <div class="divider"><span data-i="copyLink">Copy URL</span></div>
      <div class="links">
        <div class="row">
          <span class="row-title" data-i="copyAll">Copy all configs</span>
          <div class="row-actions"><button class="btn sm copy-all" type="button" data-i-title="copyAll"><span class="anticon">{{ index .Icons "CopyOutlined" }}</span></button></div>
        </div>
        {{ range .Groups }}
        <!-- A tunnel is a menu: the row names the protocol (and the tunnel
             when there are several), and opens on one line per user, each
             with copy, download and QR of their own file. A plan for one
             opens on one line, named after the customer. -->
        <div class="cfg cfg-group">
          <div class="cfg-head">
            <span class="anticon caret">{{ index $.Icons "RightOutlined" }}</span>
            <span class="tag tag-config {{ if eq .Protocol "openvpn" }}orange{{ else }}cyan{{ end }}" data-i="{{ if eq .Protocol "openvpn" }}ovpnConfig{{ else }}config{{ end }}">Config</span>
            {{ if .Tunnel }}<span class="cfg-meta">{{ .Tunnel }}</span>{{ end }}
            <span class="cfg-count">{{ if gt (len .Devices) 1 }}<span dir="ltr">{{ len .Devices }}</span> <span data-i="users">users</span>{{ else }}<span data-i="oneUser">1 user</span>{{ end }}</span>
          </div>
          <div class="cfg-body cfg-users">
            {{ range .Devices }}
            <div class="cfg-user">
              <div class="cfg-user-head">
                <span class="cfg-user-name">{{ if .User }}<span data-i="user">User</span> <span dir="ltr">{{ .User }}</span>{{ else }}{{ .Row }}{{ end }}{{ if .HostName }} · {{ .HostName }}{{ end }}{{ if and (eq .Protocol "openvpn") .Username }} <span class="cfg-user-login" dir="ltr">{{ .Username }}</span>{{ end }}</span>
                <div class="row-actions">
                  <button class="btn sm copy" type="button" data-text="{{ .Config }}" data-i-title="copy"><span class="anticon">{{ index $.Icons "CopyOutlined" }}</span></button>
                  <a class="btn sm" href="?device={{ .ID }}{{ if .HostID }}&host={{ .HostID }}{{ end }}" download="{{ .Filename }}" data-i-title="download"><span class="anticon">{{ index $.Icons "DownloadOutlined" }}</span></a>
                  {{ if .QR }}<button class="btn sm qr" type="button" title="QR"><span class="anticon">{{ index $.Icons "QrcodeOutlined" }}</span></button>
                  <div class="pop"><div class="pop-card"><span class="tag qr-tag">{{ .Label }}</span><img src="{{ .QR }}" width="220" height="220" alt="QR"><span class="pop-hint" data-i="tapToClose">Tap outside to close</span></div></div>{{ end }}
                  <button class="btn sm show" type="button" data-i-title="show"><span class="anticon">{{ index $.Icons "RightOutlined" }}</span></button>
                </div>
              </div>
              <div class="cfg-user-body"><code class="cfg-text">{{ .Config }}</code></div>
            </div>
            {{ end }}
          </div>
        </div>
        {{ end }}
      </div>
      {{ end }}

      <!-- The apps only: the files are handed out per user in the rows
           above, and repeating them here made the menu a second list. -->
      <div class="apps">
        <div class="app">
          <button class="btn lg primary" type="button" data-menu="android"><span class="anticon">{{ index .Icons "AndroidOutlined" }}</span> Android <span class="anticon">{{ index .Icons "DownOutlined" }}</span></button>
          <div class="menu" id="menu-android">
            {{ if .HasWG }}
            <a href="https://play.google.com/store/apps/details?id=com.wireguard.android" target="_blank" rel="noopener noreferrer">WireGuard</a>
            <a href="https://play.google.com/store/apps/details?id=org.amnezia.awg" target="_blank" rel="noopener noreferrer">AmneziaWG</a>
            {{ end }}{{ if .HasOVPN }}
            <a href="https://play.google.com/store/apps/details?id=net.openvpn.openvpn" target="_blank" rel="noopener noreferrer">OpenVPN Connect</a>
            {{ end }}
          </div>
        </div>
        <div class="app">
          <button class="btn lg primary" type="button" data-menu="ios"><span class="anticon">{{ index .Icons "AppleOutlined" }}</span> iOS <span class="anticon">{{ index .Icons "DownOutlined" }}</span></button>
          <div class="menu" id="menu-ios">
            {{ if .HasWG }}
            <a href="https://apps.apple.com/app/wireguard/id1441195209" target="_blank" rel="noopener noreferrer">WireGuard</a>
            <a href="https://apps.apple.com/app/amneziawg/id6478942365" target="_blank" rel="noopener noreferrer">AmneziaWG</a>
            {{ end }}{{ if .HasOVPN }}
            <a href="https://apps.apple.com/app/openvpn-connect/id590379981" target="_blank" rel="noopener noreferrer">OpenVPN Connect</a>
            {{ end }}
          </div>
        </div>
      </div>
    </div>
  </div>
</div></div>
<div class="toast" id="toast"></div>

<script nonce="{{ .Nonce }}">
(function () {
  var S = {{ .Strings }};
  window.__wuiDict = S;
  // Values the server rendered as data attributes: the page's policy allows
  // no style attributes, and setting them from here is allowed.
  document.querySelectorAll('[data-p]').forEach(function (el) { el.style.setProperty('--p', el.getAttribute('data-p')); el.style.setProperty('--c', el.getAttribute('data-c')); });
  document.querySelectorAll('[data-w]').forEach(function (el) { el.style.width = el.getAttribute('data-w') + '%'; });
  var html = document.documentElement;
  var $ = function (s, r) { return (r || document).querySelectorAll(s); };

  // The words, swapped in place when the language button is used.
  function lang() { try { return localStorage.getItem('wui.sub.lang') || html.getAttribute('data-lang'); } catch (e) { return html.getAttribute('data-lang'); } }
  function applyLang(l) {
    var d = S[l] || S.en;
    html.setAttribute('lang', l);
    $('[data-i]').forEach(function (el) { var k = el.getAttribute('data-i'); if (d[k]) el.textContent = d[k]; });
    $('[data-i-title]').forEach(function (el) { var k = el.getAttribute('data-i-title'); if (d[k]) { el.title = d[k]; el.setAttribute('aria-label', d[k]); } });
    $('#menu-lang button').forEach(function (b) { b.classList.toggle('on', b.getAttribute('data-lang') === l); });
    try { localStorage.setItem('wui.sub.lang', l); } catch (e) {}
  }
  applyLang(lang());
  $('#menu-lang button').forEach(function (b) { b.addEventListener('click', function () { applyLang(b.getAttribute('data-lang')); closeMenus(); }); });

  // The theme: dark, then pure black, then light, as the panel's own cycles.
  // Only the classic look has the switch; every other template is one
  // designed palette, and a light or black override would wreck it.
  var THEMES = ['dark', 'ultra', 'light'];
  var themeBtn = document.getElementById('theme');
  function theme() { try { var t = localStorage.getItem('wui.sub.theme'); return THEMES.indexOf(t) >= 0 ? t : 'dark'; } catch (e) { return 'dark'; } }
  function applyTheme(t) {
    if (t === 'dark') html.removeAttribute('data-theme'); else html.setAttribute('data-theme', t);
    $('[data-theme-icon]').forEach(function (el) { el.classList.toggle('hidden', el.getAttribute('data-theme-icon') !== t); });
    try { localStorage.setItem('wui.sub.theme', t); } catch (e) {}
  }
  if (themeBtn) {
    applyTheme(theme());
    themeBtn.addEventListener('click', function () { applyTheme(THEMES[(THEMES.indexOf(theme()) + 1) % 3]); });
  } else {
    html.removeAttribute('data-theme');
  }

  var toast = document.getElementById('toast'), toastT;
  function say(msg) { toast.textContent = msg; toast.classList.add('show'); clearTimeout(toastT); toastT = setTimeout(function () { toast.classList.remove('show'); }, 1600); }
  function copyText(text, done) {
    var d = S[lang()] || S.en;
    if (navigator.clipboard && window.isSecureContext) { navigator.clipboard.writeText(text).then(function () { say(done || d.copied); }, function () { fallback(text, done); }); return; }
    fallback(text, done);
  }
  function fallback(text, done) {
    var d = S[lang()] || S.en;
    var ta = document.createElement('textarea'); ta.value = text; ta.setAttribute('readonly', ''); ta.style.position = 'fixed'; ta.style.opacity = '0';
    document.body.appendChild(ta); ta.select();
    var ok = false; try { ok = document.execCommand('copy'); } catch (e) {}
    document.body.removeChild(ta); say(ok ? (done || d.copied) : 'Ctrl+C');
  }
  $('button.copy').forEach(function (b) { b.addEventListener('click', function (e) { e.stopPropagation(); copyText(b.getAttribute('data-text') || ''); }); });
  var all = [];
  $('.cfg-text').forEach(function (c) { all.push(c.textContent); });
  $('button.copy-all').forEach(function (b) { b.addEventListener('click', function () { copyText(all.join('\n'), (S[lang()] || S.en).copyAllDone); }); });

  function closeMenus() { $('.menu.open, .pop.open').forEach(function (m) { m.classList.remove('open'); }); }
  $('[data-menu]').forEach(function (b) { b.addEventListener('click', function (e) { e.stopPropagation(); var m = document.getElementById('menu-' + b.getAttribute('data-menu')); var was = m.classList.contains('open'); closeMenus(); if (!was) m.classList.add('open'); }); });
  $('button.qr').forEach(function (b) { b.addEventListener('click', function (e) { e.stopPropagation(); var p = b.nextElementSibling; var was = p.classList.contains('open'); closeMenus(); if (!was) p.classList.add('open'); }); });
  $('.cfg-head').forEach(function (h) { h.addEventListener('click', function () { h.parentNode.classList.toggle('open'); }); });
  $('.cfg-head .row-actions').forEach(function (a) { a.addEventListener('click', function (e) { e.stopPropagation(); }); });
  $('.cfg-user .btn.show').forEach(function (b) { b.addEventListener('click', function () { b.closest('.cfg-user').classList.toggle('open'); }); });
  $('.pop').forEach(function (p) { p.addEventListener('click', function (e) { if (e.target === p) closeMenus(); }); });
  $('.pop-card').forEach(function (c) { c.addEventListener('click', function (e) { e.stopPropagation(); }); });
  document.addEventListener('click', closeMenus);

  // The network look: a slow constellation drawn behind the page. Nothing
  // when the visitor asked for less motion.
  if (html.getAttribute('data-layout') === 'network' && !matchMedia('(prefers-reduced-motion: reduce)').matches) {
    var cv = document.getElementById('net'), ctx = cv.getContext('2d'), pts = [], W, H;
    function size() { W = cv.width = innerWidth; H = cv.height = innerHeight; }
    size(); addEventListener('resize', size);
    for (var i = 0; i < 70; i++) pts.push({ x: Math.random() * innerWidth, y: Math.random() * innerHeight, vx: (Math.random() - .5) * .25, vy: (Math.random() - .5) * .25 });
    (function frame() {
      ctx.clearRect(0, 0, W, H);
      for (var i = 0; i < pts.length; i++) {
        var p = pts[i]; p.x += p.vx; p.y += p.vy;
        if (p.x < 0 || p.x > W) p.vx *= -1; if (p.y < 0 || p.y > H) p.vy *= -1;
        for (var j = i + 1; j < pts.length; j++) {
          var q = pts[j], dx = p.x - q.x, dy = p.y - q.y, d = dx * dx + dy * dy;
          if (d < 140 * 140) { ctx.strokeStyle = 'rgba(224,46,61,' + (0.35 * (1 - d / (140 * 140))) + ')'; ctx.lineWidth = 1; ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(q.x, q.y); ctx.stroke(); }
        }
        ctx.fillStyle = 'rgba(233,237,243,.7)'; ctx.beginPath(); ctx.arc(p.x, p.y, 1.6, 0, Math.PI * 2); ctx.fill();
      }
      requestAnimationFrame(frame);
    })();
  }
})();

// Live figures. The page asks its own address for the numbers every few
// seconds while it is on screen, so a customer watching it sees the data
// move as they use it and the dot turn green when a device connects.
(function () {
  var $id = function (i) { return document.getElementById(i); };
  if (!$id('lv-data')) return;
  var url = location.pathname + '?view=status';
  var timer = null, failures = 0;
  function cls(p) { return p >= 90 ? 'red' : p >= 75 ? 'orange' : 'green'; }
  function colour(p) { return p >= 90 ? 'var(--bad)' : p >= 75 ? 'var(--warn)' : 'var(--ok)'; }
  function word(key) { var d = window.__wuiDict || {}; var l = document.documentElement.getAttribute('lang') || 'en'; return (d[l] && d[l][key]) || (d.en && d.en[key]) || key; }
  function set(id, text) { var el = $id(id); if (el && el.textContent !== text) el.textContent = text; }
  function apply(st) {
    if (st.hasQuota) {
      set('lv-data', st.remained); set('lv-data-sub', st.used + ' / ' + st.total);
      set('lv-pct', st.percentTxt + '%');
      set('lv-remained', st.remained); set('lv-foot-remained', st.remained); set('lv-foot-pct', st.percentTxt + '%');
      var ring = $id('lv-ring-usage'); if (ring) { ring.style.setProperty('--p', st.percentTxt); ring.style.setProperty('--c', colour(st.percent)); }
      var bar = $id('lv-bar'); if (bar) { bar.style.width = st.percentTxt + '%'; bar.className = cls(st.percent); }
    } else {
      set('lv-data', st.used);
    }
    set('lv-bar-used', st.used); set('lv-bar-total', st.total);
    set('lv-used', st.used); set('lv-down', st.down); set('lv-up', st.up);
    set('lv-last', st.lastOnline || '-');
    set('lv-expiry', st.expiryChip === 'expired' ? word('expired') : (st.expiryChip || '\u221e'));
    var live = $id('lv-live');
    if (live) {
      live.classList.toggle('off', !st.online);
      live.classList.toggle('idle', st.active && !st.online);
      var t = live.querySelector('span'); if (t) t.textContent = st.online ? word('online') : (st.active ? word('idle') : word('offline'));
    }
    var sr = $id('lv-ring-status'); if (sr) sr.style.setProperty('--c', st.active ? 'var(--ok)' : 'var(--bad)');
    var key = st.statusKey === 'inactive' ? 'inactive' : st.statusKey === 'unlimited' ? 'unlimited' : 'active';
    set('lv-status', word(key));
    var td = $id('lv-td-status'); if (td) { var tag = td.querySelector('.tag'); if (tag) { tag.textContent = word(key); tag.className = 'tag ' + (key === 'inactive' ? 'red' : key === 'unlimited' ? 'purple' : 'green'); } }
    var usage = document.querySelector('.usage'); if (usage) usage.classList.toggle('inactive', !st.active);
  }
  // The page's own fingerprint. When the panel's answer carries a
  // different one, something on this page changed -- a device, an
  // address, a file -- and the page is loaded again rather than patched.
  var rev = document.documentElement.getAttribute('data-rev') || '';
  function tick() {
    if (document.hidden) return;
    fetch(url, { cache: 'no-store', credentials: 'same-origin' })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (st) {
        failures = 0;
        if (rev && st.rev && st.rev !== rev) { rev = st.rev; location.reload(); return; }
        apply(st);
      })
      .catch(function () { failures++; });
  }
  function schedule() {
    clearInterval(timer);
    // Every 3 seconds on screen; a page that keeps failing backs off to 30.
    timer = setInterval(function () { tick(); if (failures > 5) { clearInterval(timer); timer = setInterval(tick, 30000); } }, 3000);
  }
  document.addEventListener('visibilitychange', function () { if (!document.hidden) { tick(); schedule(); } });
  // The stream: the panel writes a line the moment something changed, and
  // the page asks for its fingerprint right then rather than at the next
  // poll. The browser reopens the stream on its own if it drops; the poll
  // covers the time in between.
  if (window.EventSource) {
    try {
      var es = new EventSource(location.pathname + '?view=events');
      es.addEventListener('changed', function () { tick(); });
    } catch (e) {}
  }
  tick(); schedule();
})();
</script>
</body>
</html>
`))

// setDownload names a configuration file the way phones expect it.
//
// Served as text/plain, Android's browsers append ".txt" to whatever the
// file is called -- "device-1.ovpn.txt" -- and the VPN app no longer
// recognises it. A type of its own for each kind of file keeps the name as
// written, and the name is also given in the encoded form so one with a
// customer's own script in it survives the header.
func setDownload(h http.Header, filename string) {
	ctype := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".ovpn":
		ctype = "application/x-openvpn-profile"
	case ".conf":
		ctype = "application/x-wireguard-profile"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		asciiFilename(filename), url.PathEscape(filename)))
}

// asciiFilename is the plain form of a name for the header's first field,
// for clients that read only that one.
func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// subUsage is the usage table: the tunnels across, the users down, and
// what each spent on each, with totals along both edges.
type subUsage struct {
	Tunnels []subUsageTunnel
	Rows    []subUsageRow
	Totals  []string
	Grand   string
}

// subUsageTunnel heads a column: the tunnel's name and its protocol,
// which colours the mark beside the name the way the config tags are.
type subUsageTunnel struct {
	Name     string
	Protocol string
}

type subUsageRow struct {
	Name  string // the row's name when it is not a numbered user
	User  int    // the user's number, 0 for a named row
	Cells []string
	Total string
}

// usageTable arranges what each file carried by user and tunnel. A user
// is the same person across tunnels -- user 2 on WireGuard and user 2 on
// OpenVPN are one row -- so what a plan for several cost each of them is
// read off one line. A plan for one is one row, named after the customer.
// A file with a name of its own is a row of its own.
func usageTable(devices []subPageDevice, customer string) *subUsage {
	if len(devices) == 0 {
		return nil
	}
	var tunnels []subUsageTunnel
	col := map[string]int{}
	type key struct {
		user int
		name string
	}
	var order []key
	cells := map[key]map[int]uint64{}
	// A file with no user number is the customer's own in a plan for one,
	// and a named file of its own when a tunnel holds several such.
	unnumbered := map[string]int{}
	for _, d := range devices {
		if d.User == 0 {
			unnumbered[d.Tunnel]++
		}
	}
	named := false
	for _, n := range unnumbered {
		if n > 1 {
			named = true
		}
	}
	for _, d := range devices {
		tunnel := d.Tunnel
		if tunnel == "" {
			tunnel = d.Protocol
		}
		c, ok := col[tunnel]
		if !ok {
			c = len(tunnels)
			col[tunnel] = c
			tunnels = append(tunnels, subUsageTunnel{Name: tunnel, Protocol: d.Protocol})
		}
		k := key{user: d.User}
		if d.User == 0 && named {
			k.name = d.Name
		}
		if _, ok := cells[k]; !ok {
			cells[k] = map[int]uint64{}
			order = append(order, k)
		}
		cells[k][c] += d.UsedBytes
	}
	u := &subUsage{Tunnels: tunnels}
	sums := make([]uint64, len(tunnels))
	var grand uint64
	for _, k := range order {
		row := subUsageRow{User: k.user, Name: k.name}
		if row.User == 0 && row.Name == "" {
			row.Name = customer
		}
		var total uint64
		for c := range tunnels {
			n := cells[k][c]
			row.Cells = append(row.Cells, humanBytes(n))
			total += n
			sums[c] += n
		}
		row.Total = humanBytes(total)
		grand += total
		u.Rows = append(u.Rows, row)
	}
	for _, n := range sums {
		u.Totals = append(u.Totals, humanBytes(n))
	}
	u.Grand = humanBytes(grand)
	return u
}

// subGroup is a tunnel's files on the page: a menu that opens on its users.
type subGroup struct {
	Protocol string
	Tunnel   string
	Devices  []subPageDevice
}

// groupDevices arranges the files by tunnel, in the order they came. When
// the customer reaches one tunnel only, its name is left off the row:
// there is nothing to tell apart. Each user's line is named for the
// reader: "User n" in a plan for several, the customer's own name in a
// plan for one, a file's own name when it was given one.
func groupDevices(devices []subPageDevice, customer string) []subGroup {
	var groups []subGroup
	index := map[string]int{}
	tunnels := map[string]bool{}
	for _, d := range devices {
		key := d.Protocol + "/" + d.Tunnel
		tunnels[d.Tunnel] = true
		i, ok := index[key]
		if !ok {
			index[key] = len(groups)
			groups = append(groups, subGroup{Protocol: d.Protocol, Tunnel: d.Tunnel})
			i = index[key]
		}
		groups[i].Devices = append(groups[i].Devices, d)
	}
	for i := range groups {
		if len(tunnels) <= 1 {
			groups[i].Tunnel = ""
		}
		for j := range groups[i].Devices {
			d := &groups[i].Devices[j]
			switch {
			case d.User > 0:
				d.Row = ""
			case len(groups[i].Devices) > 1:
				d.Row = d.Name
			default:
				d.Row = customer
			}
		}
	}
	return groups
}
