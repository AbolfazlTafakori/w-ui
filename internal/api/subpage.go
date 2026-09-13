package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
// Laid out as 3x-ui's SubPage is -- one centred Card with the Descriptions
// table, the usage summary, the link rows, a config block per device and
// the two app buttons -- in this panel's own colours. Rendered whole on the
// server and carrying no external reference of any kind: a customer opening
// this on a phone on a bad connection, or from a network that blocks
// whatever CDN was convenient, still gets a working page.

// subPageQRLimit is the largest configuration that will fit in a scannable QR.
const subPageQRLimit = 1500

type subPageView struct {
	Page       *service.SubPage
	Nonce      string
	SubID      string
	Devices    []subPageDevice
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

	s.renderSubPage(w, page, token, false)
	return true
}

// renderSubPage writes the page for one customer -- or, for a preview, for
// nobody in particular.
func (s *Server) renderSubPage(w http.ResponseWriter, page *service.SubPage, token string, preview bool) {
	v := subPageView{
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
	}
	dict, _ := json.Marshal(subPageStrings)
	v.Strings = template.JS(dict)

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
	// the page. Nothing may be loaded from anywhere else at all.
	h.Set("Content-Security-Policy",
		"default-src 'none'; "+
			"img-src 'self' data:; "+
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

// subPageStrings are the page's words in both languages, keyed as 3x-ui's
// subscription strings are, so the language button can swap them in place.
var subPageStrings = map[string]map[string]string{
	"en": {
		"title": "Subscription info", "subId": "Subscription ID", "email": "Email", "status": "Status",
		"active": "Active", "inactive": "Inactive", "unlimited": "Unlimited",
		"downloaded": "Downloaded", "uploaded": "Uploaded", "usage": "Usage", "totalQuota": "Total Quota",
		"remained": "Remaining", "lastOnline": "Last Online", "expiry": "Expiry", "noExpiry": "No expiry",
		"expired": "Expired", "copy": "Copy", "copied": "Copied", "download": "Download",
		"copyLink": "Copy URL", "copyAll": "Copy all configs", "copyAllDone": "All configs copied",
		"config": "WireGuard config", "ovpnConfig": "OpenVPN config", "theme": "Theme", "language": "Language",
		"subSettings": "Subscription", "tapToClose": "Tap outside to close",
	},
	"fa": {
		"title": "اطلاعات سابسکریپشن", "subId": "شناسه اشتراک", "email": "ایمیل", "status": "وضعیت",
		"active": "فعال", "inactive": "غیرفعال", "unlimited": "نامحدود",
		"downloaded": "دانلود شده", "uploaded": "آپلود شده", "usage": "مصرف", "totalQuota": "حجم کل",
		"remained": "باقی‌مانده", "lastOnline": "آخرین فعالیت", "expiry": "انقضا", "noExpiry": "بدون انقضا",
		"expired": "منقضی", "copy": "کپی", "copied": "کپی شد", "download": "دانلود",
		"copyLink": "کپی لینک", "copyAll": "کپی همه کانفیگ‌ها", "copyAllDone": "همه کانفیگ‌ها کپی شد",
		"config": "پیکربندی WireGuard", "ovpnConfig": "پیکربندی OpenVPN", "theme": "تم", "language": "زبان",
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
<html lang="{{ .Lang }}" dir="ltr" data-lang="{{ .Lang }}" data-layout="{{ .Template }}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{ .Page.Title }}</title>
<style nonce="{{ .Nonce }}">
/* This panel's palette on 3x-ui's subscription page: Ant's geometry, our
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
.cfg-text { display: block; margin: 0; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 11px; white-space: pre-wrap; word-break: break-all; direction: ltr; text-align: left; }

/* Apps row */
.apps { display: flex; flex-wrap: wrap; gap: 8px; justify-content: center; margin-top: 24px; }
.app { position: relative; flex: 1 1 calc(50% - 4px); text-align: center; }
@media (max-width: 575px) { .app { flex: 1 1 100%; } .app .btn { width: 100%; } }
.menu { position: absolute; top: calc(100% + 4px); inset-inline-start: 50%; transform: translateX(-50%); z-index: 6; display: none; min-width: 160px; padding: 4px; border-radius: 8px; background: var(--surface-3); box-shadow: 0 6px 16px rgba(0,0,0,.08), 0 3px 6px -4px rgba(0,0,0,.12), 0 9px 28px 8px rgba(0,0,0,.05); text-align: start; }
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
[data-layout="waves"] .card { border-radius: 14px; overflow: hidden; }
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

/* The hero and quick stats the non-classic looks open with. */
.hero { align-items: center; gap: 14px; margin-bottom: 16px; }
.hero-icon { display: grid; place-items: center; width: 52px; height: 52px; border-radius: 14px; background: linear-gradient(135deg, var(--accent), var(--accent-hover)); color: #fff; font-size: 24px; box-shadow: 0 10px 24px -10px var(--accent); flex: none; }
.hero-copy { min-width: 0; flex: 1; }
.hero-meta { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: .08em; }
.hero-live { display: inline-flex; align-items: center; gap: 5px; text-transform: none; letter-spacing: 0; color: var(--ok); }
.hero-live i { width: 7px; height: 7px; border-radius: 50%; background: var(--ok); animation: blink 1.4s ease-in-out infinite; }
.hero-live.off, .hero-live.off i { color: var(--faint); background: var(--faint); animation: none; }
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
</style>
</head>
<body>
{{ if .Preview }}<div class="preview-bar">Preview — {{ .Template }} — sample figures, not a customer</div>{{ end }}
<div class="live-bg" aria-hidden="true"><span class="orb one"></span><span class="orb two"></span><span class="orb three"></span><span class="wave one"></span><span class="wave two"></span><span class="grid"></span><canvas id="net"></canvas></div>
<div class="content"><div class="col">
  <div class="card">
    <div class="card-head">
      <div class="card-title"><span data-i="title">{{ .Page.Title }}</span><span class="tag">{{ .SubID }}</span></div>
      <div class="card-extra">
        <button class="btn toolbar-btn" type="button" id="theme" data-i-title="theme">
          <span class="anticon" data-theme-icon="light">{{ index .Icons "SunOutlined" }}</span>
          <span class="anticon" data-theme-icon="dark">{{ index .Icons "MoonOutlined" }}</span>
          <span class="anticon" data-theme-icon="ultra">{{ index .Icons "MoonFilled" }}</span>
        </button>
        <div class="app" style="flex: none">
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
        <div class="hero-icon"><span class="anticon">{{ index .Icons "ThunderboltOutlined" }}</span></div>
        <div class="hero-copy">
          <div class="hero-meta"><span data-i="subSettings">Subscription</span><span class="hero-live{{ if not .Active }} off{{ end }}"><i></i>{{ if .Active }}Live{{ else }}Off{{ end }}</span></div>
          <h1>{{ .Page.Title }}</h1>
          <p>{{ .Page.Name }}</p>
        </div>
      </div>
      <div class="quick">
        <div>
          <div class="ring" style="--p: 100; --c: {{ if .Active }}var(--ok){{ else }}var(--bad){{ end }}"><span>{{ len .Devices }}</span></div>
          <div><div class="stat-k" data-i="status">Status</div><div class="stat-v">{{ if eq .StatusKey "inactive" }}<span data-i="inactive">Inactive</span>{{ else if eq .StatusKey "unlimited" }}<span data-i="unlimited">Unlimited</span>{{ else }}<span data-i="active">Active</span>{{ end }}</div><div class="stat-s">{{ len .Devices }} configs</div></div>
        </div>
        <div>
          <div class="ring" style="--p: {{ if .HasQuota }}{{ .PercentTxt }}{{ else }}100{{ end }}; --c: {{ if ge .Percent 90.0 }}var(--bad){{ else if ge .Percent 75.0 }}var(--warn){{ else }}var(--ok){{ end }}"><span dir="ltr">{{ if .HasQuota }}{{ .PercentTxt }}%{{ else }}∞{{ end }}</span></div>
          <div><div class="stat-k" data-i="usage">Data</div><div class="stat-v" dir="ltr">{{ if .HasQuota }}{{ .Remained }}{{ else }}{{ .Used }}{{ end }}</div><div class="stat-s" dir="ltr">{{ if .HasQuota }}{{ .Used }} / {{ .Total }}{{ else }}<span data-i="unlimited">Unlimited</span>{{ end }}</div></div>
        </div>
        <div>
          <div class="ring" style="--p: {{ if .ExpiryChip }}100{{ else }}100{{ end }}; --c: {{ if eq .ExpiryCls "red" }}var(--bad){{ else if eq .ExpiryCls "orange" }}var(--warn){{ else }}var(--accent){{ end }}"><span class="anticon">{{ index .Icons "ClockCircleOutlined" }}</span></div>
          <div><div class="stat-k" data-i="expiry">Time</div><div class="stat-v" dir="ltr">{{ if .ExpiryChip }}{{ if eq .ExpiryChip "expired" }}<span data-i="expired">Expired</span>{{ else }}{{ .ExpiryChip }}{{ end }}{{ else }}∞{{ end }}</div><div class="stat-s" dir="ltr">{{ if .Expiry }}{{ .Expiry }}{{ else }}<span data-i="noExpiry">No expiry</span>{{ end }}</div></div>
        </div>
      </div>
      <table class="desc">
        <tr><th data-i="subId">Subscription ID</th><td dir="ltr">{{ .SubID }}</td></tr>
        <tr><th data-i="email">Email</th><td>{{ .Page.Name }}</td></tr>
        <tr><th data-i="status">Status</th><td>
          {{ if eq .StatusKey "inactive" }}<span class="tag red" data-i="inactive">Inactive</span>
          {{ else if eq .StatusKey "unlimited" }}<span class="tag purple" data-i="unlimited">Unlimited</span>
          {{ else }}<span class="tag green" data-i="active">Active</span>{{ end }}
        </td></tr>
        <tr><th data-i="downloaded">Downloaded</th><td dir="ltr">{{ bytes .Page.DownBytes }}</td></tr>
        <tr><th data-i="uploaded">Uploaded</th><td dir="ltr">{{ bytes .Page.UpBytes }}</td></tr>
        <tr><th data-i="usage">Usage</th><td dir="ltr">{{ .Used }}</td></tr>
        <tr><th data-i="totalQuota">Total Quota</th><td dir="ltr">{{ .Total }}</td></tr>
        {{ if .HasQuota }}<tr><th data-i="remained">Remaining</th><td dir="ltr">{{ .Remained }}</td></tr>{{ end }}
        <tr><th data-i="lastOnline">Last Online</th><td dir="ltr">{{ if .LastOnline }}{{ .LastOnline }}{{ else }}-{{ end }}</td></tr>
        <tr><th data-i="expiry">Expiry</th><td dir="ltr">{{ if .Expiry }}{{ .Expiry }}{{ else }}<span data-i="noExpiry">No expiry</span>{{ end }}</td></tr>
      </table>

      <div class="usage{{ if not .Active }} inactive{{ end }}">
        <div class="usage-head">
          <div class="usage-labels" dir="ltr"><span class="usage-used">{{ .Used }}</span><span class="usage-sep">/</span><span class="usage-total">{{ .Total }}</span></div>
          <div class="usage-chips">
            {{ if not .HasQuota }}<span class="tag purple"><span class="anticon">{{ index .Icons "ThunderboltOutlined" }}</span><span data-i="unlimited">Unlimited</span></span>{{ end }}
            {{ if .ExpiryChip }}<span class="tag {{ .ExpiryCls }}"><span class="anticon">{{ index .Icons "ClockCircleOutlined" }}</span>{{ if eq .ExpiryChip "expired" }}<span data-i="expired">Expired</span>{{ else }}{{ .ExpiryChip }}{{ end }}</span>{{ end }}
          </div>
        </div>
        {{ if .HasQuota }}<div class="bar"><i class="{{ if ge .Percent 90.0 }}red{{ else if ge .Percent 75.0 }}orange{{ else }}green{{ end }}" style="width: {{ .PercentTxt }}%"></i></div>{{ end }}
        <div class="usage-foot">{{ if .HasQuota }}<span dir="ltr">{{ .Remained }}</span><span class="usage-pct" dir="ltr">{{ .PercentTxt }}%</span>{{ end }}</div>
      </div>

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
        {{ range .Devices }}
        <div class="cfg">
          <div class="cfg-head">
            <span class="anticon caret">{{ index $.Icons "RightOutlined" }}</span>
            <span class="tag {{ if eq $.Page.Protocol "wireguard" }}cyan{{ else }}orange{{ end }}" style="margin:0;font-weight:600;letter-spacing:.3px" data-i="{{ if eq $.Page.Protocol "wireguard" }}config{{ else }}ovpnConfig{{ end }}">Config</span>
            <span class="cfg-meta">{{ .Name }}{{ if .HostName }} · {{ .HostName }}{{ end }}</span>
            <div class="row-actions">
              <button class="btn sm copy" type="button" data-text="{{ .Config }}" data-i-title="copy"><span class="anticon">{{ index $.Icons "CopyOutlined" }}</span></button>
              <a class="btn sm" href="?device={{ .ID }}{{ if .HostID }}&host={{ .HostID }}{{ end }}" download="{{ .Filename }}" data-i-title="download"><span class="anticon">{{ index $.Icons "DownloadOutlined" }}</span></a>
              {{ if .QR }}<button class="btn sm qr" type="button" title="QR"><span class="anticon">{{ index $.Icons "QrcodeOutlined" }}</span></button>
              <div class="pop"><div class="pop-card"><span class="tag qr-tag">{{ .Name }}</span><img src="{{ .QR }}" width="220" height="220" alt="QR"><span class="pop-hint" data-i="tapToClose">Tap outside to close</span></div></div>{{ end }}
            </div>
          </div>
          <div class="cfg-body"><code class="cfg-text">{{ .Config }}</code></div>
        </div>
        {{ end }}
      </div>
      {{ end }}

      <div class="apps">
        <div class="app">
          <button class="btn lg primary" type="button" data-menu="android"><span class="anticon">{{ index .Icons "AndroidOutlined" }}</span> Android <span class="anticon">{{ index .Icons "DownOutlined" }}</span></button>
          <div class="menu" id="menu-android">
            {{ if eq .Page.Protocol "wireguard" }}
            <a href="https://play.google.com/store/apps/details?id=com.wireguard.android" target="_blank" rel="noopener noreferrer">WireGuard</a>
            <a href="https://play.google.com/store/apps/details?id=org.amnezia.awg" target="_blank" rel="noopener noreferrer">AmneziaWG</a>
            {{ else }}
            <a href="https://play.google.com/store/apps/details?id=net.openvpn.openvpn" target="_blank" rel="noopener noreferrer">OpenVPN Connect</a>
            {{ end }}
            {{ range .Devices }}<a href="?device={{ .ID }}{{ if .HostID }}&host={{ .HostID }}{{ end }}" download="{{ .Filename }}"><span class="anticon">{{ index $.Icons "DownloadOutlined" }}</span>{{ .Filename }}</a>{{ end }}
          </div>
        </div>
        <div class="app">
          <button class="btn lg primary" type="button" data-menu="ios"><span class="anticon">{{ index .Icons "AppleOutlined" }}</span> iOS <span class="anticon">{{ index .Icons "DownOutlined" }}</span></button>
          <div class="menu" id="menu-ios">
            {{ if eq .Page.Protocol "wireguard" }}
            <a href="https://apps.apple.com/app/wireguard/id1441195209" target="_blank" rel="noopener noreferrer">WireGuard</a>
            <a href="https://apps.apple.com/app/amneziawg/id6478942365" target="_blank" rel="noopener noreferrer">AmneziaWG</a>
            {{ else }}
            <a href="https://apps.apple.com/app/openvpn-connect/id590379981" target="_blank" rel="noopener noreferrer">OpenVPN Connect</a>
            {{ end }}
            {{ range .Devices }}<a href="?device={{ .ID }}{{ if .HostID }}&host={{ .HostID }}{{ end }}" download="{{ .Filename }}"><span class="anticon">{{ index $.Icons "DownloadOutlined" }}</span>{{ .Filename }}</a>{{ end }}
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
  var THEMES = ['dark', 'ultra', 'light'];
  function theme() { try { var t = localStorage.getItem('wui.sub.theme'); return THEMES.indexOf(t) >= 0 ? t : 'dark'; } catch (e) { return 'dark'; } }
  function applyTheme(t) {
    if (t === 'dark') html.removeAttribute('data-theme'); else html.setAttribute('data-theme', t);
    $('[data-theme-icon]').forEach(function (el) { el.classList.toggle('hidden', el.getAttribute('data-theme-icon') !== t); });
    try { localStorage.setItem('wui.sub.theme', t); } catch (e) {}
  }
  applyTheme(theme());
  document.getElementById('theme').addEventListener('click', function () { applyTheme(THEMES[(THEMES.indexOf(theme()) + 1) % 3]); });

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
</script>
</body>
</html>
`))
