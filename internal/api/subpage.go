package api

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/devguide"
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

// subLogo is the mark: a file of the page's own, fetched once and kept
// (subassets.go), where it was written into every page twice.
var subLogo = template.URL(registerSubAsset("logo", ".png", "image/png", subLogoPNG))

type subPageView struct {
	Logo  template.URL
	Page  *service.SubPage
	Nonce string
	// Reveal is the mark the page's copy buttons send for an L2TP or PPTP
	// sign-in, which the page itself never carries (subreveal.go).
	Reveal  string
	SubID   string
	Devices []subPageDevice
	// Groups is the files by tunnel: a plan for one shows one row per
	// tunnel with the actions on it; a plan for several shows one row per
	// tunnel that opens on the users, each with their own actions.
	Groups []subGroup
	// Usage is who spent what on which tunnel: a row per user, a column
	// per tunnel, a total at the end of each. For people sharing a plan
	// and its cost.
	Usage *subUsage
	// UsageSince is when the usage table starts, when that is later than the
	// start of the customer's current period: shown with the table, so a
	// table that covers less than the usage above it says how much less.
	UsageSince string
	HasWG      bool // any device on a WireGuard tunnel: its apps are offered
	HasOVPN    bool // any on OpenVPN
	HasL2TP    bool // any on L2TP, which needs no app: the device has it built in
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
	SubQR      string // subQR
	// CSSPath and StringsPath are the page's styles and its words in both
	// languages, for the language button: files of their own (subassets.go).
	CSSPath     string
	StringsPath string
	// AnySetup is whether any file on the page has its setup card; each is
	// in the file's own row (subPageDevice.Setup).
	AnySetup bool
	Lang     string
	Icons    map[string]template.HTML
	// Template is the look, one of service.SubTemplates; Preview marks a
	// page rendered from sample figures for the settings page.
	Template string
	Preview  bool
}

type subPageDevice struct {
	service.SubPageDevice
	// SignIn is an L2TP file's sign-in, shown in its row: what the setup
	// card's fields are filled with. CopyIcon is the button's, for the row's
	// own template, which cannot reach the page's.
	SignIn   []devguide.Row
	CopyIcon template.HTML
	// Files are an IKEv2 file's certificates, to download.
	Files []ikev2File
	// AmneziaQR is the AmneziaVPN key as a QR code, for an AmneziaWG file.
	AmneziaQR string
	// Ref names this file to the copy buttons: "ID.HostID".
	Ref string
	// Setup is, for an L2TP or PPTP file, the guide that is its row: the
	// customer's device, and that device's fields filled in with this
	// file's sign-in. The sign-in is shown there and nowhere else.
	Setup *setupCard
	// PPP is an L2TP or PPTP file, whose text carries the sign-in: the page
	// never shows or copies that text, whatever else it has to show.
	PPP bool
	QR  string // subQR, "" for a file no app scans
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
	// A copy button asking for one L2TP or PPTP sign-in value.
	if view == "reveal" {
		s.serveSubReveal(w, r, token)
		return true
	}
	// An IKEv2 file: the tunnel's authority, or a device's certificate,
	// which is useless without the password the page withholds.
	if view == "ikev2" {
		id, err := strconv.ParseUint(r.URL.Query().Get("device"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		name, body, err := s.subs.IKEv2FileFor(r.Context(), token, uint(id), r.URL.Query().Get("file"))
		if err != nil {
			s.subFailed(w, r, err)
			return true
		}
		ct := "application/x-pkcs12"
		if strings.HasSuffix(name, ".crt") {
			ct = "application/x-x509-ca-cert"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
		return true
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
			s.subFailed(w, r, err)
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
		s.subFailed(w, r, err)
		return true
	}

	s.renderSubPage(w, page, token, false, r.UserAgent())
	return true
}

// renderSubPage writes the page for one customer -- or, for a preview, for
// nobody in particular. userAgent picks the device an L2TP sign-in opens on.
func (s *Server) renderSubPage(w http.ResponseWriter, page *service.SubPage, token string, preview bool, userAgent string) {
	v := newSubView(page, token, preview, userAgent)
	if !preview && v.HasL2TP {
		v.Reveal = s.reveals.issue(token, time.Now())
	}

	var buf bytes.Buffer
	if err := subPageTemplate.Execute(&buf, v); err != nil {
		s.log.Error("could not render the subscription page", "error", err)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	h := w.Header()
	// This page's own policy, replacing the panel's. Its scripts -- its own,
	// and its words -- are named by nonce rather than by opening the door to
	// every script on the page; its styles are a file of the page's own
	// address. Nothing may be loaded from anywhere else at all -- except that
	// address, which it asks for fresh figures.
	h.Set("Content-Security-Policy",
		"default-src 'none'; "+
			"img-src 'self' data:; "+
			"font-src 'self'; "+
			"connect-src 'self'; "+
			"style-src 'self' 'nonce-"+v.Nonce+"'; "+
			"script-src 'nonce-"+v.Nonce+"'; "+
			"form-action 'none'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// A customer's page is theirs alone: no index, no archive, no preview.
	h.Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet")
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
	v := newSubView(page, token, false, "")
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
func newSubView(page *service.SubPage, token string, preview bool, userAgent string) subPageView {
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
	// Each QR is its pattern, drawn by the page when it is opened (subqr.go).
	v.SubQR = subQR(page.SubURL)
	for _, d := range page.Devices {
		entry := subPageDevice{SubPageDevice: d}
		ppp := d.Protocol == "l2tp" || d.Protocol == "pptp" || d.Protocol == "ikev2" || d.Protocol == "openconnect"
		entry.Ref = fileRef(d)
		entry.PPP = ppp
		if d.Guide != nil && ppp {
			entry.SignIn = l2tpSignIn(*d.Guide)
			entry.CopyIcon = v.Icons["CopyOutlined"]
			entry.Files = ikev2Files(d)
		}
		// An L2TP or PPTP sign-in is typed into the device's own settings; no app
		// scans it, so a QR of it would only be something to puzzle over.
		entry.QR = subQR(qrText(d, ""))
		entry.AmneziaQR = subQR(qrText(d, "amnezia"))
		v.Devices = append(v.Devices, entry)
		switch d.Protocol {
		case "openvpn":
			v.HasOVPN = true
		case "l2tp", "pptp", "ikev2", "openconnect":
			v.HasL2TP = true
		default:
			v.HasWG = true
		}
	}
	v.Groups = groupDevices(v.Devices, page.Name)
	v.Usage = usageTable(v.Devices, page.Name)
	detected := devguide.Detect(userAgent)
	// Each file's own guide, in its own row: the device, then that
	// device's steps for this file. One card under every file, rather than
	// one at the foot of the page asking which file again.
	for i := range v.Groups {
		for j := range v.Groups[i].Devices {
			d := &v.Groups[i].Devices[j]
			if d.Setup = newSetupCard([]subPageDevice{*d}, detected, v.Icons["CopyOutlined"]); d.Setup != nil {
				v.AnySetup = true
			}
		}
	}
	if page.FilesSince != nil {
		v.UsageSince = page.FilesSince.Local().Format("2006-01-02 15:04")
	}
	v.CSSPath = subCSSPath
	v.StringsPath = subStringsPath(v.HasL2TP, v.AnySetup)
	return v
}

// subPageStrings are the page's words in both languages, keyed as the classic panel's
// subscription strings are, so the language button can swap them in place.
// subPageL2TPStrings are added only to a page with an L2TP sign-in on it, so
// that every other customer's page is, to the byte, what it was before L2TP.
var subPageL2TPStrings = map[string]map[string]string{
	"en": {"l2tpConfig": "L2TP/IPsec sign-in", "pptpConfig": "PPTP sign-in", "ikev2Config": "IKEv2 sign-in", "ocConfig": "OpenConnect (Cisco) sign-in", "l2tpBuiltIn": "L2TP/IPsec: built in, under Settings → VPN",
		"revealExpired": "This page has been open a while: reload it, then copy again.",
		"revealBusy":    "Too many copies in a row: wait a minute.",
		"revealFailed":  "Could not copy it; try again."},
	"fa": {"l2tpConfig": "ورود L2TP/IPsec", "pptpConfig": "ورود PPTP", "ikev2Config": "ورود IKEv2", "ocConfig": "ورود OpenConnect (سیسکو)", "l2tpBuiltIn": "L2TP/IPsec: داخل خود دستگاه، در تنظیمات ← VPN",
		"revealExpired": "این صفحه مدتی باز مانده: یک بار تازه‌اش کنید و دوباره کپی کنید.",
		"revealBusy":    "کپی‌های پشت سر هم زیاد شد: یک دقیقه صبر کنید.",
		"revealFailed":  "کپی نشد؛ دوباره امتحان کنید."},
}

// pageStrings is the dictionary a page is given: the guides' words on a page
// with a setup card, the L2TP row's on a page with an L2TP sign-in, and on
// any other page exactly what it was.
func pageStrings(l2tp, setup bool) map[string]map[string]string {
	if !l2tp && !setup {
		return subPageStrings
	}
	out := make(map[string]map[string]string, len(subPageStrings))
	var guides map[string]map[string]string
	if setup {
		guides = devguide.Strings()
	}
	for lang, words := range subPageStrings {
		merged := make(map[string]string, len(words)+len(subPageL2TPStrings[lang])+len(guides[lang]))
		for k, v := range words {
			merged[k] = v
		}
		if l2tp {
			for k, v := range subPageL2TPStrings[lang] {
				merged[k] = v
			}
		}
		for k, v := range guides[lang] {
			merged[k] = v
		}
		out[lang] = merged
	}
	return out
}

// l2tpSignIn is an L2TP file's own four values, for its row.
func l2tpSignIn(v devguide.Values) []devguide.Row {
	rows := []devguide.Row{{Label: "Server", Value: v.Server, Copy: true}}
	if v.Protocol == "openconnect" {
		// What the device dials, whole: the camouflage secret is part of it.
		rows = []devguide.Row{{Label: "Server address", Value: v.OC.URL, Copy: true}}
	}
	if v.Protocol == "ikev2" && v.ServerID != "" && v.ServerID != v.Server {
		rows = append(rows, devguide.Row{Label: "Remote ID", Value: v.ServerID, Copy: true})
	}
	if v.OwnPort != 0 {
		// Only for a client with a port setting; the built-in ones dial
		// the standard port, which stays open.
		rows = append(rows, devguide.Row{Label: "Port (apps with a port setting)", Value: strconv.Itoa(v.OwnPort), Copy: true})
	}
	// PPTP has no key of its own: the sign-in is all there is. The rest is
	// masked: the page copies it on a click, and never carries it.
	if v.PSK != "" {
		rows = append(rows, devguide.Row{Label: "Pre-shared key", Value: devguide.Mask, Copy: true, Secret: devguide.SecretPSK})
	}
	return append(rows,
		devguide.Row{Label: "Username", Value: devguide.Mask, Copy: true, Secret: devguide.SecretUsername},
		devguide.Row{Label: "Password", Value: devguide.Mask, Copy: true, Secret: devguide.SecretPassword})
}

// ikev2File is a file an IKEv2 row offers for download.
type ikev2File struct{ Name, Href string }

// ikev2Files are an IKEv2 file's certificates, as links on its row.
func ikev2Files(d service.SubPageDevice) []ikev2File {
	if d.Guide == nil || d.Protocol != "ikev2" {
		return nil
	}
	id := strconv.FormatUint(uint64(d.ID), 10)
	var out []ikev2File
	if f := d.Guide.IKEv2.CAFile; f != "" {
		out = append(out, ikev2File{Name: f, Href: "?view=ikev2&file=" + service.IKEv2FileCA + "&device=" + id})
	}
	if f := d.Guide.IKEv2.CertFile; f != "" {
		out = append(out, ikev2File{Name: f, Href: "?view=ikev2&file=" + service.IKEv2FileCert + "&device=" + id})
	}
	return out
}

// fileRef names a file to the copy buttons.
func fileRef(d service.SubPageDevice) string {
	return strconv.FormatUint(uint64(d.ID), 10) + "." + strconv.FormatUint(uint64(d.HostID), 10)
}

// maskRows replaces every withheld value with the mask.
func maskRows(rows []devguide.Row) []devguide.Row {
	out := make([]devguide.Row, len(rows))
	for i, r := range rows {
		if r.Secret != "" {
			r.Value = devguide.Mask
		}
		out[i] = r
	}
	return out
}

// setupCard is the device picker and every guide it can show.
type setupCard struct {
	Devices  []setupDevice
	Files    []setupFile
	Views    []setupView
	CopyIcon template.HTML
}

// setupDevice is one device in the picker.
type setupDevice struct {
	ID, Name, NameKey string
	// Head and HeadText are the heading of the list this device opens, on
	// the first device of each.
	Head, HeadText string
	Selected       bool
	// Supports is the protocols the device dials, for the line that says so
	// when the customer's plan has none of them.
	Supports []setupProtocol
	Notes    []guideNote
}

type setupProtocol struct{ Key, Name string }

// setupFile is one of the customer's files, as the connection picker names
// it: its protocol, and which file when the plan has several of that one.
type setupFile struct {
	Index       int
	Name, Key   string
	Suffix      string
	Others      string
	ProtocolKey string
}

// setupView is one device's guide for one file.
type setupView struct {
	Device string
	File   int
	// Ref names the file to the copy buttons; Secret marks a view whose
	// command carries the sign-in, so it is shown masked and copied on a
	// click.
	Ref      string
	Secret   bool
	Protocol string
	Weak     bool
	Path     string
	Rows     []devguide.Row
	Notes    []guideNote
	Command  string
}

type guideNote struct{ Key, Text string }

// newSetupCard is the card for a page's files: nothing for a page with none.
// The device the browser is opens it, or Windows where the browser is a
// device the catalogue does not list.
func newSetupCard(devices []subPageDevice, detected string, icon template.HTML) *setupCard {
	if len(devices) == 0 {
		return nil
	}
	words := devguide.Strings()["en"]
	card := &setupCard{CopyIcon: icon}
	count := map[string]int{}
	for _, d := range devices {
		if d.Guide != nil {
			for _, p := range d.Guide.Served() {
				count[string(p)]++
			}
		}
	}
	for i, d := range devices {
		if d.Guide == nil {
			continue
		}
		for _, p := range d.Guide.Served() {
			f := setupFile{Index: i, Name: devguide.ProtocolNames[p].EN, Key: devguide.ProtocolKey(p), ProtocolKey: string(p)}
			if count[string(p)] > 1 {
				f.Suffix = d.Label
				if f.Suffix == "" {
					f.Suffix = d.Filename
				}
			}
			if p == devguide.L2TP || p == devguide.L2TPPlain || p == devguide.PPTP {
				f.Others = strings.Join(d.Guide.Others, ", ")
			}
			card.Files = append(card.Files, f)
		}
		views := devguide.Views(*d.Guide)
		// A sign-in the page withholds is withheld from the commands too:
		// they are shown as the masked values make them.
		var masked []devguide.View
		if d.Guide.Withholds() {
			masked = devguide.Views(d.Guide.Masked())
		}
		for n, view := range views {
			sv := setupView{Device: view.Device, File: i, Ref: fileRef(d.SubPageDevice), Protocol: string(view.Protocol), Weak: view.Weak,
				Path: view.Path, Rows: maskRows(view.Rows), Command: view.Command}
			if masked != nil && n < len(masked) && masked[n].Device == view.Device && masked[n].Protocol == view.Protocol &&
				masked[n].Command != view.Command {
				sv.Command, sv.Secret = masked[n].Command, true
			}
			for n, note := range view.Notes {
				sv.Notes = append(sv.Notes, guideNote{Key: devguide.NoteKey(view.Device, view.Protocol, n), Text: note.EN})
			}
			card.Views = append(card.Views, sv)
		}
	}
	if len(card.Files) == 0 {
		return nil
	}
	heads := map[devguide.Group]string{
		devguide.GroupDevice: "dgGroupDevice",
		devguide.GroupRouter: "dgGroupRouter",
		devguide.GroupNone:   "dgGroupNone",
	}
	var last devguide.Group
	found := false
	// The list is the devices that can make one of this card's connections:
	// an Android 12 phone has no L2TP, and offering it on an L2TP row is a
	// choice that leads nowhere. The models with no VPN client stay, at the
	// end: choosing one says what to do instead.
	dials := map[string]bool{}
	for _, view := range card.Views {
		dials[view.Device] = true
	}
	listed := func(g devguide.Guide) bool { return dials[g.ID] || devguide.GroupOf(g) == devguide.GroupNone }
	for _, g := range devguide.All() {
		if g.ID == detected && listed(g) {
			found = true
		}
	}
	if !found {
		// The device this is, when it cannot make these connections, or
		// is not known: the first that can.
		detected = ""
		for _, g := range devguide.All() {
			if dials[g.ID] {
				detected = g.ID
				break
			}
		}
	}
	for _, g := range devguide.All() {
		if !listed(g) {
			continue
		}
		group := devguide.GroupOf(g)
		d := setupDevice{ID: g.ID, Name: g.Name.EN, NameKey: devguide.NameKey(g.ID), Selected: g.ID == detected}
		if group != last {
			d.Head, d.HeadText = heads[group], words[heads[group]]
			last = group
		}
		for _, p := range g.Supports() {
			d.Supports = append(d.Supports, setupProtocol{Key: devguide.ProtocolKey(p), Name: devguide.ProtocolNames[p].EN})
		}
		for n, note := range g.Notes {
			d.Notes = append(d.Notes, guideNote{Key: devguide.NoteKey(g.ID, "", n), Text: note.EN})
		}
		card.Devices = append(card.Devices, d)
	}
	return card
}

// setupTemplate is the card: the two pickers, and every guide, one shown.
const setupTemplate = `{{ $icon := .CopyIcon }}<div class="setup">
<label class="guide-pick"><span data-i="dgDevice">Your device</span>
<select class="guide-select setup-device" dir="ltr">{{ range .Devices }}{{ if .Head }}<option disabled data-i="{{ .Head }}" data-en="{{ .HeadText }}">{{ .HeadText }}</option>{{ end }}<option value="{{ .ID }}"{{ if .Selected }} selected{{ end }} data-i="{{ .NameKey }}" data-en="{{ .Name }}">{{ .Name }}</option>{{ end }}</select>
</label>
<label class="guide-pick setup-file-pick"><span data-i="dgProtocol">Connection</span>
<select class="guide-select setup-file"></select>
</label>
<div class="setup-files" hidden>{{ range .Files }}<span data-file="{{ .Index }}" data-protocol="{{ .ProtocolKey }}" data-key="{{ .Key }}" data-suffix="{{ .Suffix }}" data-others="{{ .Others }}">{{ .Name }}</span>{{ end }}</div>
<div class="guide-others" hidden><span data-i="dgOthers">If it does not answer, use instead</span>: <span class="others" dir="ltr"></span></div>
{{ range .Devices }}<div class="setup-device-notes" data-device="{{ .ID }}" hidden>{{ range .Notes }}<p class="guide-note" data-i="{{ .Key }}">{{ .Text }}</p>{{ end }}{{ if .Supports }}<div class="setup-none"><p class="guide-note" data-i="dgNoneOfPlan">This device can dial none of the connections on your plan. It can dial:</p><p class="guide-note setup-supports">{{ range $i, $p := .Supports }}{{ if $i }} · {{ end }}<span data-i="{{ $p.Key }}">{{ $p.Name }}</span>{{ end }}</p></div>{{ end }}</div>{{ end }}
{{ range .Views }}{{ $ref := .Ref }}<div class="guide" data-device="{{ .Device }}" data-file="{{ .File }}" data-protocol="{{ .Protocol }}" hidden>{{ if .Weak }}
<p class="guide-warn" data-i="dgWeak">This connection is not encrypted, or only with a cipher long since broken. Use it only where nothing else will do.</p>{{ end }}{{ if .Path }}
<div class="guide-path" dir="ltr">{{ .Path }}</div>{{ end }}{{ if .Rows }}
<dl class="guide-fields">{{ range .Rows }}<div class="guide-row"><dt dir="ltr">{{ .Label }}</dt><dd>{{ if .Key }}<span data-i="{{ .Key }}">{{ .Value }}</span>{{ else if .Secret }}<code dir="ltr" class="secret">{{ .Value }}</code>{{ else }}<code dir="ltr">{{ .Value }}</code>{{ end }}{{ if .Secret }}<button class="btn sm copy" type="button" data-ref="{{ $ref }}" data-secret="{{ .Secret }}" data-i-title="copy"><span class="anticon">{{ $icon }}</span></button>{{ else if .Copy }}<button class="btn sm copy" type="button" data-text="{{ .Value }}" data-i-title="copy"><span class="anticon">{{ $icon }}</span></button>{{ end }}</dd></div>{{ end }}</dl>{{ end }}{{ range .Notes }}
<p class="guide-note" data-i="{{ .Key }}">{{ .Text }}</p>{{ end }}{{ if .Command }}
<div class="guide-cmd"><code dir="ltr">{{ .Command }}</code>{{ if .Secret }}<button class="btn sm copy" type="button" data-ref="{{ $ref }}" data-secret="cmd" data-device="{{ .Device }}" data-protocol="{{ .Protocol }}" data-i-title="copy">{{ else }}<button class="btn sm copy" type="button" data-text="{{ .Command }}" data-i-title="copy">{{ end }}<span class="anticon">{{ $icon }}</span></button></div>{{ end }}
</div>{{ end }}
</div>`

// signInTemplate is an L2TP row's four values.
const signInTemplate = `{{ $icon := .CopyIcon }}{{ $ref := .Ref }}<dl class="guide-fields">{{ range .SignIn }}<div class="guide-row"><dt>{{ .Label }}</dt><dd>{{ if .Secret }}<code dir="ltr" class="secret">{{ .Value }}</code><button class="btn sm copy" type="button" data-ref="{{ $ref }}" data-secret="{{ .Secret }}" data-i-title="copy">{{ else }}<code dir="ltr">{{ .Value }}</code><button class="btn sm copy" type="button" data-text="{{ .Value }}" data-i-title="copy">{{ end }}<span class="anticon">{{ $icon }}</span></button></dd></div>{{ end }}</dl>`

// Both are templates of the page's own, added before it is ever run.
var (
	_ = template.Must(subPageTemplate.New("setup").Parse(setupTemplate))
	_ = template.Must(subPageTemplate.New("signIn").Parse(signInTemplate))
)

var subPageStrings = map[string]map[string]string{
	"en": {
		"title": "Subscription info", "subId": "Subscription ID", "email": "Email", "status": "Status",
		"active": "Active", "inactive": "Inactive", "unlimited": "Unlimited",
		"downloaded": "Downloaded", "uploaded": "Uploaded", "usage": "Usage", "totalQuota": "Total Quota",
		"remained": "Remaining", "lastOnline": "Last Online", "expiry": "Expiry", "noExpiry": "No expiry",
		"expired": "Expired", "copy": "Copy", "copied": "Copied", "download": "Download",
		"copyLink": "Copy URL", "copyAll": "Copy all configs", "copyAllDone": "All configs copied",
		"config": "WireGuard config", "ovpnConfig": "OpenVPN config", "xrayConfig": "V2Ray link", "theme": "Theme", "language": "Language", "users": "users", "user": "User", "show": "Show", "oneUser": "1 user", "usageTable": "Usage by user and tunnel", "usageSince": "Split by user since", "total": "Total", "allUsers": "All users", "tunnel": "Tunnel",
		"live": "Live", "online": "Online", "idle": "Idle", "offline": "Off",
		"subSettings": "Subscription", "tapToClose": "Tap outside to close", "amneziaKeyHint": "For the AmneziaVPN app: copy the key, or scan it.",
	},
	"fa": {
		"title": "اطلاعات سابسکریپشن", "subId": "شناسه اشتراک", "email": "ایمیل", "status": "وضعیت",
		"active": "فعال", "inactive": "غیرفعال", "unlimited": "نامحدود",
		"downloaded": "دانلود شده", "uploaded": "آپلود شده", "usage": "مصرف", "totalQuota": "حجم کل",
		"remained": "باقی‌مانده", "lastOnline": "آخرین فعالیت", "expiry": "انقضا", "noExpiry": "بدون انقضا",
		"expired": "منقضی", "copy": "کپی", "copied": "کپی شد", "download": "دانلود",
		"copyLink": "کپی لینک", "copyAll": "کپی همه کانفیگ‌ها", "copyAllDone": "همه کانفیگ‌ها کپی شد",
		"config": "پیکربندی WireGuard", "ovpnConfig": "پیکربندی OpenVPN", "xrayConfig": "لینک V2Ray", "theme": "تم", "language": "زبان", "users": "کاربر", "user": "کاربر", "show": "نمایش", "oneUser": "۱ کاربر", "usageTable": "مصرف هر کاربر روی هر تانل", "usageSince": "تفکیک کاربرها از", "total": "جمع", "allUsers": "همهٔ کاربران", "tunnel": "تانل",
		"live": "زنده", "online": "آنلاین", "idle": "بی‌کار", "offline": "خاموش",
		"subSettings": "اشتراک", "tapToClose": "برای بستن بیرون بزنید", "amneziaKeyHint": "برای اپ AmneziaVPN: کلید را کپی کنید یا اسکن کنید.",
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
		s.subFailed(w, r, err)
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
	// Two decimals from GiB up, so a figure moves by about ten megabytes
	// rather than a hundred; one below, where a tenth is already fine.
	if exp >= 2 {
		return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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
<link rel="stylesheet" href="{{ .CSSPath }}">
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
        <div class="cfg-body usage-body">{{ if .UsageSince }}
          <p class="usage-note"><span data-i="usageSince">Split by user since</span> <span dir="ltr">{{ .UsageSince }}</span></p>{{ end }}
          <div class="usage-scroll">
            <table class="usage-grid">
              <thead><tr>
                <th class="usage-corner"><span data-i="user">User</span></th>
                {{ range .Usage.Tunnels }}<th><span class="usage-dot {{ if eq .Protocol "openvpn" }}orange{{ else if or (eq .Protocol "l2tp") (eq .Protocol "pptp") (eq .Protocol "ikev2") (eq .Protocol "openconnect") }}purple{{ else }}cyan{{ end }}"></span>{{ .Name }}</th>{{ end }}
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
            <div class="pop"><div class="pop-card"><span class="tag green qr-tag" data-i="subSettings">Subscription</span><img data-qr="{{ .SubQR }}" width="240" height="240" alt="QR"><span class="pop-hint" data-i="tapToClose">Tap outside to close</span></div></div>{{ end }}
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
        <div class="cfg cfg-group{{ if or (eq .Protocol "l2tp") (eq .Protocol "pptp") (eq .Protocol "ikev2") (eq .Protocol "openconnect") }} open{{ end }}">
          <div class="cfg-head">
            <span class="anticon caret">{{ index $.Icons "RightOutlined" }}</span>
            <span class="tag tag-config {{ if eq .Protocol "openvpn" }}orange{{ else if or (eq .Protocol "l2tp") (eq .Protocol "pptp") (eq .Protocol "ikev2") (eq .Protocol "openconnect") }}purple{{ else }}cyan{{ end }}" data-i="{{ if eq .Protocol "openvpn" }}ovpnConfig{{ else if eq .Protocol "l2tp" }}l2tpConfig{{ else if eq .Protocol "pptp" }}pptpConfig{{ else if eq .Protocol "ikev2" }}ikev2Config{{ else if eq .Protocol "openconnect" }}ocConfig{{ else if eq .Protocol "xray" }}xrayConfig{{ else }}config{{ end }}">Config</span>
            {{ if .Tunnel }}<span class="cfg-meta">{{ .Tunnel }}</span>{{ end }}
            <span class="cfg-count">{{ if gt (len .Devices) 1 }}<span dir="ltr">{{ len .Devices }}</span> <span data-i="users">users</span>{{ else }}<span data-i="oneUser">1 user</span>{{ end }}</span>
          </div>
          <div class="cfg-body cfg-users">
            {{ range .Devices }}
            <div class="cfg-user{{ if or .SignIn (and .PPP .Setup) }} open{{ end }}">
              <div class="cfg-user-head">
                <span class="cfg-user-name">{{ if .User }}<span data-i="user">User</span> <span dir="ltr">{{ .User }}</span>{{ else }}{{ .Row }}{{ end }}{{ if .HostName }} · {{ .HostName }}{{ end }}{{ if and (eq .Protocol "openvpn") .Username }} <span class="cfg-user-login" dir="ltr">{{ .Username }}</span>{{ end }}</span>
                <div class="row-actions">
                  {{ if not .PPP }}<button class="btn sm copy" type="button" data-text="{{ .Config }}" data-i-title="copy"><span class="anticon">{{ index $.Icons "CopyOutlined" }}</span></button>{{ end }}
                  <a class="btn sm" href="?device={{ .ID }}{{ if .HostID }}&host={{ .HostID }}{{ end }}" download="{{ .Filename }}" data-i-title="download"><span class="anticon">{{ index $.Icons "DownloadOutlined" }}</span></a>
                  {{ if .QR }}<button class="btn sm qr" type="button" title="QR"><span class="anticon">{{ index $.Icons "QrcodeOutlined" }}</span></button>
                  <div class="pop"><div class="pop-card"><span class="tag qr-tag">{{ .Label }}</span><img data-qr="{{ .QR }}" width="220" height="220" alt="QR"><span class="pop-hint" data-i="tapToClose">Tap outside to close</span></div></div>{{ end }}
                  <button class="btn sm show" type="button" data-i-title="show"><span class="anticon">{{ index $.Icons "RightOutlined" }}</span></button>
                </div>
              </div>
              <div class="cfg-user-body">{{ if .PPP }}{{ if .Setup }}{{ template "setup" .Setup }}{{ else if .SignIn }}{{ template "signIn" . }}{{ end }}{{ else }}<code class="cfg-text">{{ .Config }}</code>{{ if .Setup }}<div class="divider"><span data-i="dgSetup">Set up a device</span></div>{{ template "setup" .Setup }}{{ end }}{{ end }}{{ if .AmneziaKey }}<div class="awg-key"><span class="tag" dir="ltr">AmneziaVPN</span><span class="awg-key-text" data-i="amneziaKeyHint">For the AmneziaVPN app: copy the key, or scan it.</span><button class="btn sm copy" type="button" data-text="{{ .AmneziaKey }}" data-i-title="copy"><span class="anticon">{{ index $.Icons "CopyOutlined" }}</span></button>{{ if .AmneziaQR }}<button class="btn sm qr" type="button" title="QR"><span class="anticon">{{ index $.Icons "QrcodeOutlined" }}</span></button><div class="pop"><div class="pop-card"><span class="tag qr-tag" dir="ltr">AmneziaVPN</span><img data-qr="{{ .AmneziaQR }}" width="220" height="220" alt="QR"><span class="pop-hint" data-i="tapToClose">Tap outside to close</span></div></div>{{ end }}</div>{{ end }}{{ with .Files }}<div class="ike-files">{{ range . }}<a class="btn sm" href="{{ .Href }}" download="{{ .Name }}" dir="ltr">{{ .Name }}</a>{{ end }}</div>{{ end }}</div>
            </div>
            {{ end }}
          </div>
        </div>
        {{ end }}
      </div>
      {{ end }}

      <!-- The apps only: the files are handed out per user in the rows
           above, and repeating them here made the menu a second list. A
           plan that is only L2TP needs none: the device has it built in. -->
      {{ if or .HasWG .HasOVPN (not .HasL2TP) }}<div class="apps">
        <div class="app">
          <button class="btn lg primary" type="button" data-menu="android"><span class="anticon">{{ index .Icons "AndroidOutlined" }}</span> Android <span class="anticon">{{ index .Icons "DownOutlined" }}</span></button>
          <div class="menu" id="menu-android">
            {{ if .HasWG }}
            <a href="https://play.google.com/store/apps/details?id=com.wireguard.android" target="_blank" rel="noopener noreferrer">WireGuard</a>
            <a href="https://play.google.com/store/apps/details?id=org.amnezia.awg" target="_blank" rel="noopener noreferrer">AmneziaWG</a>
            {{ end }}{{ if .HasOVPN }}
            <a href="https://play.google.com/store/apps/details?id=net.openvpn.openvpn" target="_blank" rel="noopener noreferrer">OpenVPN Connect</a>
            {{ end }}{{ if .HasL2TP }}
            <span class="menu-note" data-i="l2tpBuiltIn">L2TP/IPsec: built in, under Settings → VPN</span>
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
            {{ end }}{{ if .HasL2TP }}
            <span class="menu-note" data-i="l2tpBuiltIn">L2TP/IPsec: built in, under Settings → VPN</span>
            {{ end }}
          </div>
        </div>
      </div>{{ end }}
    </div>
  </div>
</div></div>
<div class="toast" id="toast"></div>

<script nonce="{{ .Nonce }}" src="{{ .StringsPath }}"></script>
<script nonce="{{ .Nonce }}">
(function () {
  var S = window.__wuiStrings || { en: {} };
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
    $('[data-i-ph]').forEach(function (el) { var k = el.getAttribute('data-i-ph'); if (d[k]) { el.placeholder = d[k]; el.setAttribute('aria-label', d[k]); } });
    $('#menu-lang button').forEach(function (b) { b.classList.toggle('on', b.getAttribute('data-lang') === l); });
    try { localStorage.setItem('wui.sub.lang', l); } catch (e) {}{{ if .AnySetup }}
    document.dispatchEvent(new Event('wui:lang'));{{ end }}
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
  $('button.copy').forEach(function (b) { b.addEventListener('click', function (e) {
    e.stopPropagation();
    if (b.hasAttribute('data-secret')) { copySecret(b); return; }
    copyText(b.getAttribute('data-text') || '');
  }); });
{{ if .Reveal }}
  // A withheld sign-in: asked of the server on the click, put on the
  // clipboard, and dropped. It is never written into the page, so nothing
  // reading the page finds it. The request is this page's alone: a POST from
  // its own origin, with a header and a mark another site cannot send.
  var REVEAL = '{{ .Reveal }}';
  function revealBody(b) {
    return JSON.stringify({ mark: REVEAL, ref: b.getAttribute('data-ref'), what: b.getAttribute('data-secret'),
      device: b.getAttribute('data-device') || '', protocol: b.getAttribute('data-protocol') || '' });
  }
  var revealURL = location.pathname + '?view=reveal';
  function revealSay(status) {
    var d = S[lang()] || S.en;
    say(status === 409 ? d.revealExpired : status === 429 ? d.revealBusy : d.revealFailed);
  }
  function copySecret(b) {
    var d = S[lang()] || S.en;
    if (!(navigator.clipboard && window.isSecureContext)) {
      // Plain http: the clipboard API is not there, and the old way only
      // copies inside the click itself, so the value is asked for in it.
      var x = new XMLHttpRequest();
      try {
        x.open('POST', revealURL, false);
        x.setRequestHeader('Content-Type', 'application/json');
        x.setRequestHeader('X-Wui-Reveal', '1');
        x.send(revealBody(b));
      } catch (e) { revealSay(0); return; }
      if (x.status !== 200) { revealSay(x.status); return; }
      var v = ''; try { v = JSON.parse(x.responseText).value || ''; } catch (e) {}
      fallback(v); v = '';
      return;
    }
    var asked = fetch(revealURL, { method: 'POST', credentials: 'same-origin', cache: 'no-store', referrerPolicy: 'no-referrer',
      headers: { 'Content-Type': 'application/json', 'X-Wui-Reveal': '1' }, body: revealBody(b) })
      .then(function (r) { if (!r.ok) { var err = new Error('reveal'); err.status = r.status; throw err; } return r.json(); })
      .then(function (j) { return j.value || ''; });
    // Safari copies only inside the click: it is handed the value still on
    // its way, which it waits for. The others write it once it is here.
    if (window.ClipboardItem && navigator.clipboard.write) {
      var item;
      try { item = new ClipboardItem({ 'text/plain': asked.then(function (v) { return new Blob([v], { type: 'text/plain' }); }) }); } catch (e) { item = null; }
      if (item) {
        navigator.clipboard.write([item]).then(function () { say(d.copied); }, function () {
          asked.then(function (v) { return navigator.clipboard.writeText(v); }).then(function () { say(d.copied); }, function (e) { revealSay(e && e.status); });
        });
        return;
      }
    }
    asked.then(function (v) { return navigator.clipboard.writeText(v); }).then(function () { say(d.copied); }, function (e) { revealSay(e && e.status); });
  }
{{ else }}
  function copySecret() {}
{{ end }}
  var all = [];
  $('.cfg-text').forEach(function (c) { all.push(c.textContent); });
  $('button.copy-all').forEach(function (b) { b.addEventListener('click', function () { copyText(all.join('\n'), (S[lang()] || S.en).copyAllDone); }); });

  function closeMenus() { $('.menu.open, .pop.open').forEach(function (m) { m.classList.remove('open'); }); }
  $('[data-menu]').forEach(function (b) { b.addEventListener('click', function (e) { e.stopPropagation(); var m = document.getElementById('menu-' + b.getAttribute('data-menu')); var was = m.classList.contains('open'); closeMenus(); if (!was) m.classList.add('open'); }); });
  // A QR comes as its pattern (subqr.go): its width in dots, then its dots
  // a bit each, row by row. Drawn here when it is opened, with the quiet
  // margin of four dots a scanner looks for, and made an image so it can be
  // saved like one.
  function drawQR(code) {
    var at = code.indexOf(':'), n = +code.slice(0, at), raw = atob(code.slice(at + 1).replace(/-/g, '+').replace(/_/g, '/'));
    var m = Math.max(2, Math.ceil(480 / (n + 8))), c = document.createElement('canvas');
    c.width = c.height = (n + 8) * m;
    var g = c.getContext('2d'); g.fillStyle = '#fff'; g.fillRect(0, 0, c.width, c.height); g.fillStyle = '#000';
    for (var i = 0; i < n * n; i++) { if (raw.charCodeAt(i >> 3) & (0x80 >> (i & 7))) g.fillRect((i % n + 4) * m, ((i / n | 0) + 4) * m, m, m); }
    return c.toDataURL('image/png');
  }
  $('button.qr').forEach(function (b) { b.addEventListener('click', function (e) { e.stopPropagation(); var p = b.nextElementSibling; var was = p.classList.contains('open'); closeMenus(); if (!was) { var img = p.querySelector('img[data-qr]'); if (img) { try { img.src = drawQR(img.getAttribute('data-qr')); } catch (err) {} img.removeAttribute('data-qr'); } p.classList.add('open'); } }); });
  $('.cfg-head').forEach(function (h) { h.addEventListener('click', function () { h.parentNode.classList.toggle('open'); }); });
  $('.cfg-head .row-actions').forEach(function (a) { a.addEventListener('click', function (e) { e.stopPropagation(); }); });
  $('.cfg-user .btn.show').forEach(function (b) { b.addEventListener('click', function () { b.closest('.cfg-user').classList.toggle('open'); }); });
  $('.pop').forEach(function (p) { p.addEventListener('click', function (e) { if (e.target === p) closeMenus(); }); });
  $('.pop-card').forEach(function (c) { c.addEventListener('click', function (e) { e.stopPropagation(); }); });
  document.addEventListener('click', closeMenus);
{{ if .AnySetup }}
  // The setup card: a device, then the files of the plan that device can
  // dial, then that device's fields for the file chosen. The device is
  // remembered, so a customer who picked their router sees it again.
  $('.setup').forEach(function (box) {
    var dev = box.querySelector('.setup-device'), file = box.querySelector('.setup-file');
    var pick = box.querySelector('.setup-file-pick'), others = box.querySelector('.guide-others');
    var files = [].slice.call(box.querySelectorAll('.setup-files [data-file]'));
    function word(key, fallback) { var d = window.__wuiDict || {}, l = document.documentElement.lang === 'fa' ? 'fa' : 'en'; return (d[l] && d[l][key]) || fallback; }
    var every = [].slice.call(dev.options);
    // Persian and Arabic letters typed either way, digits either way, and
    // no case: what a customer types finds what the list says.
    function norm(t) {
      return String(t || '').toLowerCase().replace(/\u200c/g, '')
        .replace(/[\u064a\u0649]/g, '\u06cc').replace(/\u0643/g, '\u06a9')
        .replace(/[\u06f0-\u06f9]/g, function (c) { return String(c.charCodeAt(0) - 0x6f0); })
        .replace(/[\u0660-\u0669]/g, function (c) { return String(c.charCodeAt(0) - 0x660); })
        .replace(/[\s\-_/().]+/g, ' ').trim();
    }
    function label(o) { return word(o.getAttribute('data-i'), o.getAttribute('data-en') || o.textContent); }
    // What a customer types in Persian for a brand or a kind of device, and
    // the English that is in the names: each device is found by either.
    var FA = [['tp-link', '\u062a\u06cc\u200c\u067e\u06cc\u200c\u0644\u06cc\u0646\u06a9 \u062a\u06cc \u067e\u06cc \u0644\u06cc\u0646\u06a9 \u062a\u06cc\u067e\u06cc\u0644\u06cc\u0646\u06a9'],
      ['mikrotik', '\u0645\u06cc\u06a9\u0631\u0648\u062a\u06cc\u06a9'], ['xiaomi', '\u0634\u06cc\u0627\u0626\u0648\u0645\u06cc \u0634\u06cc\u0627\u0648\u0645\u06cc \u0634\u06cc\u0627\u0645\u06cc'],
      ['d-link', '\u062f\u06cc\u200c\u0644\u06cc\u0646\u06a9 \u062f\u06cc \u0644\u06cc\u0646\u06a9 \u062f\u06cc\u0644\u06cc\u0646\u06a9'], ['huawei', '\u0647\u0648\u0627\u0648\u06cc'],
      ['irancell', '\u0627\u06cc\u0631\u0627\u0646\u0633\u0644'], ['zte', '\u0632\u062f\u062a\u06cc\u0627\u06cc \u0632\u062f\u200c\u062a\u06cc\u200c\u0627\u06cc'],
      ['iphone', '\u0622\u06cc\u0641\u0648\u0646 \u0627\u06cc\u0641\u0648\u0646'], ['ipad', '\u0622\u06cc\u067e\u062f \u0627\u06cc\u067e\u062f'],
      ['android', '\u0627\u0646\u062f\u0631\u0648\u06cc\u062f'], ['windows', '\u0648\u06cc\u0646\u062f\u0648\u0632'], ['macos', '\u0645\u06a9 \u0645\u06a9\u200c\u0628\u0648\u06a9'],
      ['linux', '\u0644\u06cc\u0646\u0648\u06a9\u0633'], ['cisco', '\u0633\u06cc\u0633\u06a9\u0648'], ['keenetic', '\u06a9\u06cc\u0646\u062a\u06cc\u06a9'],
      ['mercusys', '\u0645\u0631\u06a9\u0648\u0633\u06cc\u0633'], ['tenda', '\u062a\u0646\u062f\u0627'], ['netis', '\u0646\u062a\u06cc\u0633'],
      ['totolink', '\u062a\u0648\u062a\u0648\u0644\u06cc\u0646\u06a9'], ['ubiquiti', '\u06cc\u0648\u0628\u06cc\u06a9\u06cc\u0648\u062a\u06cc \u06cc\u0648\u0628\u06cc\u06a9\u0648\u06cc\u062a\u06cc'],
      ['omada', '\u0627\u0648\u0645\u0627\u062f\u0627'], ['draytek', '\u062f\u0631\u0627\u06cc\u062a\u06a9'], ['archer', '\u0622\u0631\u0686\u0631 \u0627\u0631\u0686\u0631'],
      ['extender', '\u062a\u0642\u0648\u06cc\u062a \u06a9\u0646\u0646\u062f\u0647 \u0631\u06cc\u067e\u06cc\u062a\u0631'], ['4g', '\u0633\u06cc\u0645\u200c\u06a9\u0627\u0631\u062a\u06cc \u0633\u06cc\u0645 \u06a9\u0627\u0631\u062a\u06cc'],
      ['modem', '\u0645\u0648\u062f\u0645'], ['router', '\u0631\u0648\u062a\u0631']];
    function aliases(en) {
      var e = norm(en), out = '';
      FA.forEach(function (p) { if (e.indexOf(norm(p[0])) >= 0) out += ' ' + norm(p[1]); });
      return out;
    }
    // The device list as a dropdown with its own search: the button shows
    // the device chosen; open, it has the search above the devices. The
    // select stays underneath -- it is what a page without script uses, and
    // what everything else here reads.
    var combo = document.createElement('div'), btn = document.createElement('button');
    var btnText = document.createElement('span'), pop = document.createElement('div');
    var search = document.createElement('input'), count = document.createElement('span');
    var list = document.createElement('div'), none = document.createElement('div');
    combo.className = 'dsel';
    btn.type = 'button'; btn.className = 'guide-select dsel-btn';
    btn.setAttribute('aria-haspopup', 'listbox'); btn.setAttribute('aria-expanded', 'false');
    btnText.className = 'dsel-value'; btn.appendChild(btnText);
    var caretEl = document.createElement('span'); caretEl.className = 'dsel-caret'; caretEl.setAttribute('aria-hidden', 'true'); btn.appendChild(caretEl);
    pop.className = 'dsel-pop'; pop.hidden = true;
    var sbox = document.createElement('div'); sbox.className = 'dsel-search-box';
    search.type = 'search'; search.className = 'dsel-search'; search.dir = 'auto';
    search.autocomplete = 'off'; search.spellcheck = false;
    search.setAttribute('autocapitalize', 'off'); search.setAttribute('enterkeyhint', 'search');
    search.setAttribute('data-i-ph', 'dgSearch');
    search.placeholder = word('dgSearch', 'Search for your device or router');
    search.setAttribute('aria-label', search.placeholder);
    count.className = 'dsel-count'; count.setAttribute('aria-live', 'polite');
    sbox.appendChild(search); sbox.appendChild(count);
    list.className = 'dsel-list'; list.setAttribute('role', 'listbox');
    none.className = 'dsel-none'; none.setAttribute('data-i', 'dgNoMatch'); none.hidden = true;
    none.textContent = word('dgNoMatch', 'No device matches.');
    var head = document.createElement('div'), title = document.createElement('span'), shut = document.createElement('button');
    head.className = 'dsel-sheet-head';
    title.setAttribute('data-i', 'dgDevice'); title.textContent = word('dgDevice', 'Your device');
    shut.type = 'button'; shut.className = 'dsel-shut'; shut.setAttribute('aria-label', 'Close'); shut.textContent = '\u00d7';
    head.appendChild(title); head.appendChild(shut);
    var backdrop = document.createElement('div'); backdrop.className = 'dsel-backdrop'; backdrop.hidden = true;
    document.body.appendChild(backdrop);
    pop.appendChild(head); pop.appendChild(sbox); pop.appendChild(list); pop.appendChild(none);
    combo.appendChild(btn); combo.appendChild(pop);
    // Out of the label the select sat in: a click anywhere in a label is
    // handed to the first button in it -- this one -- which would open the
    // list again the moment a device in it was chosen.
    var lab = dev.parentNode;
    if (lab.tagName === 'LABEL') {
      var holder = document.createElement('div');
      holder.className = lab.className;
      while (lab.firstChild) holder.appendChild(lab.firstChild);
      lab.parentNode.replaceChild(holder, lab);
    }
    dev.parentNode.insertBefore(combo, dev);
    dev.hidden = true; dev.tabIndex = -1; dev.setAttribute('aria-hidden', 'true');

    var items = [], active = -1;
    function setBtn() {
      var o = dev.options[dev.selectedIndex];
      btnText.textContent = o ? label(o) : '';
    }
    function mark(i) {
      if (active >= 0 && items[active]) items[active].classList.remove('active');
      active = i;
      if (active >= 0 && items[active]) {
        var it = items[active];
        it.classList.add('active');
        // Within the list only: scrolling it into view would move the page.
        if (it.offsetTop < list.scrollTop) list.scrollTop = it.offsetTop - 28;
        else if (it.offsetTop + it.offsetHeight > list.scrollTop + list.clientHeight) list.scrollTop = it.offsetTop + it.offsetHeight - list.clientHeight + 4;
      }
    }
    function render() {
      var q = norm(search.value), head = null, headShown = false, n = 0;
      list.innerHTML = ''; items = []; active = -1;
      every.forEach(function (o) {
        if (o.disabled) { head = o; headShown = false; return; }
        var hay = o._hay || (o._hay = norm(o.getAttribute('data-en')) + ' ' + norm(o.value) + aliases(o.getAttribute('data-en') + ' ' + o.value));
        var name = label(o);
        if (q && !q.split(' ').every(function (w) { return (hay + ' ' + norm(name)).indexOf(w) >= 0; })) return;
        if (head && !headShown) {
          var h = document.createElement('div'); h.className = 'dsel-head'; h.textContent = label(head);
          list.appendChild(h); headShown = true;
        }
        var it = document.createElement('div');
        it.className = 'dsel-item'; it.setAttribute('role', 'option'); it.setAttribute('data-v', o.value);
        it.textContent = name;
        var on = o.value === dev.value;
        it.setAttribute('aria-selected', on ? 'true' : 'false');
        if (on) it.classList.add('chosen');
        it.addEventListener('mousedown', function (e) { e.preventDefault(); });
        it.addEventListener('click', function () { choose(o.value); });
        list.appendChild(it); items.push(it); n++;
      });
      none.hidden = !(q && n === 0);
      count.textContent = q && n ? word('dgMatches', '{n} found').replace('{n}', n) : '';
      var at = -1;
      items.forEach(function (it, i) { if (it.getAttribute('data-v') === dev.value) at = i; });
      mark(q ? (items.length ? 0 : -1) : at);
    }
    var phone = window.matchMedia('(max-width: 640px)');
    function place() {
      combo.classList.remove('up');
      list.style.maxHeight = '';
      if (phone.matches) return;
      var r = btn.getBoundingClientRect();
      var below = window.innerHeight - r.bottom - 12, above = r.top - 12;
      var up = below < 300 && above > below;
      combo.classList.toggle('up', up);
      // Room for the list: the side it opens on, less the search above it.
      list.style.maxHeight = Math.max(150, Math.min(340, (up ? above : below) - 76)) + 'px';
    }
    function open() {
      var sheet = phone.matches;
      combo.classList.toggle('sheet', sheet);
      pop.classList.toggle('sheet', sheet);
      // A sheet is the page's, not the card's: anything around the card that
      // makes a layer of its own would hold a fixed sheet inside it.
      if (sheet) document.body.appendChild(pop);
      pop.hidden = false; combo.classList.add('open'); btn.setAttribute('aria-expanded', 'true');
      backdrop.hidden = !sheet;
      document.documentElement.classList.toggle('dsel-lock', sheet);
      search.value = ''; render(); place();
      // On a phone the keyboard would cover the list it opens on; the
      // search is a tap away. Elsewhere typing starts at once.
      if (!sheet) search.focus({ preventScroll: true });
    }
    function close(back) {
      if (pop.hidden) return;
      pop.hidden = true; combo.classList.remove('open'); btn.setAttribute('aria-expanded', 'false');
      if (pop.parentNode !== combo) combo.appendChild(pop);
      pop.classList.remove('sheet');
      backdrop.hidden = true;
      document.documentElement.classList.remove('dsel-lock');
      if (back && !phone.matches) btn.focus({ preventScroll: true });
    }
    // The chosen device's fields, brought into view when they are below it.
    function reveal() {
      var g = [].filter.call(box.querySelectorAll('.guide'), function (x) { return !x.hidden; })[0] || box;
      var top = g.getBoundingClientRect().top;
      if (top > window.innerHeight * 0.55) {
        window.scrollBy({ top: combo.getBoundingClientRect().top - 72, behavior: 'smooth' });
      }
    }
    function choose(v) {
      if (dev.value !== v) { dev.value = v; dev.dispatchEvent(new Event('change')); }
      setBtn(); close(true); reveal();
    }
    shut.addEventListener('click', function () { close(true); });
    backdrop.addEventListener('click', function () { close(false); });
    window.addEventListener('resize', function () { if (!pop.hidden && !combo.classList.contains('sheet')) place(); });
    btn.addEventListener('click', function (e) { e.stopPropagation(); if (pop.hidden) open(); else close(true); });
    pop.addEventListener('click', function (e) { e.stopPropagation(); });
    document.addEventListener('click', function () { close(false); });
    search.addEventListener('input', render);
    search.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown') { e.preventDefault(); if (items.length) mark(Math.min(active + 1, items.length - 1)); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); if (items.length) mark(Math.max(active - 1, 0)); }
      else if (e.key === 'Enter') { e.preventDefault(); if (items[active]) choose(items[active].getAttribute('data-v')); }
      else if (e.key === 'Escape') { e.preventDefault(); close(true); }
      else if (e.key === 'Tab') { close(false); }
    });
    btn.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); open(); }
    });
    function show() {
      var id = dev.value, f = file.value;
      box.querySelectorAll('.guide').forEach(function (g) { g.hidden = !(g.getAttribute('data-device') === id && g.getAttribute('data-file') + '/' + g.getAttribute('data-protocol') === f); });
      var o = files.filter(function (x) { return x.getAttribute('data-file') + '/' + x.getAttribute('data-protocol') === f; })[0];
      var list = o ? o.getAttribute('data-others') : '';
      others.hidden = !list;
      if (list) others.querySelector('.others').textContent = list;
    }
    function fill() {
      var id = dev.value, prev = file.value;
      file.innerHTML = '';
      files.forEach(function (x) {
        var key = x.getAttribute('data-file') + '/' + x.getAttribute('data-protocol');
        if (!box.querySelector('.guide[data-device="' + id + '"][data-file="' + x.getAttribute('data-file') + '"][data-protocol="' + x.getAttribute('data-protocol') + '"]')) return;
        var opt = document.createElement('option');
        opt.value = key;
        var suffix = x.getAttribute('data-suffix');
        opt.textContent = word(x.getAttribute('data-key'), x.textContent) + (suffix ? ' · ' + suffix : '');
        file.appendChild(opt);
      });
      if ([].some.call(file.options, function (o) { return o.value === prev; })) file.value = prev;
      pick.hidden = file.options.length <= 1;
      // The device's own notes, and when the plan has nothing it dials,
      // what it does dial; nothing at all when there is neither.
      box.querySelectorAll('.setup-device-notes').forEach(function (n) {
        var none = n.querySelector('.setup-none'), dials = file.options.length > 0;
        if (none) none.hidden = dials;
        n.hidden = n.getAttribute('data-device') !== id || (dials && !n.querySelector(':scope > .guide-note'));
      });
      show();
    }
    try {
      var saved = localStorage.getItem('wui.sub.device') || localStorage.getItem('wui.sub.l2tpDevice');
      if (saved && [].some.call(dev.options, function (o) { return o.value === saved && !o.disabled; })) dev.value = saved;
    } catch (e) {}
    dev.addEventListener('change', function () { setBtn(); fill(); try { localStorage.setItem('wui.sub.device', dev.value); } catch (e) {} });
    file.addEventListener('change', show);
    document.addEventListener('wui:lang', function () { setBtn(); if (!pop.hidden) render(); fill(); });
    setBtn();
    fill();
  });
{{ end }}
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
	case ".txt":
		ctype = "text/plain; charset=utf-8"
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
func usageTable(entries []subPageDevice, customer string) *subUsage {
	// A file is on the page once for every address it can be handed out
	// with -- the spare hosts an operator keeps -- but it carried its bytes
	// once. Counted per entry, a tunnel with two spare hosts showed every
	// user three times what they used.
	var devices []subPageDevice
	counted := map[uint]bool{}
	for _, d := range entries {
		if counted[d.ID] {
			continue
		}
		counted[d.ID] = true
		devices = append(devices, d)
	}
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

// subFailed answers a customer's page or file that could not be made. What
// is not there -- a token, a device, a file that never existed -- is not
// found, as for a token that never existed. Anything else is this server's
// fault, said as one and logged: answered not found, it looked to the
// customer like a link that had died, and to the operator like nothing at
// all.
func (s *Server) subFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrInvalid) {
		http.NotFound(w, r)
		return
	}
	s.log.Error("a customer's page could not be made", "view", r.URL.Query().Get("view"), "error", err)
	w.Header().Set("Retry-After", "5")
	http.Error(w, "temporarily unavailable; try again in a moment", http.StatusServiceUnavailable)
}
