package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/service"
)

// Previewing a subscription template.
//
// The page is served to customers without a session, and the panel's own
// session is a bearer token a new tab does not carry. So the settings page
// asks for a one-time link -- a random name good for a minute, opened
// once -- and the page it opens is rendered from sample figures, never
// from a real customer.

const previewTTL = time.Minute

type subPreview struct {
	template string
	expires  time.Time
}

type subPreviews struct {
	mu   sync.Mutex
	open map[string]subPreview
}

func (p *subPreviews) issue(template string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.open == nil {
		p.open = map[string]subPreview{}
	}
	now := time.Now()
	for k, v := range p.open {
		if now.After(v.expires) {
			delete(p.open, k)
		}
	}
	id := newNonce()
	p.open[id] = subPreview{template: template, expires: now.Add(previewTTL)}
	return id
}

func (p *subPreviews) take(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.open[id]
	if !ok {
		return "", false
	}
	delete(p.open, id)
	if time.Now().After(v.expires) {
		return "", false
	}
	return v.template, true
}

// handleSubPreviewLink hands the settings page a link to open.
func (s *Server) handleSubPreviewLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template string `json:"template"`
	}
	if !decode(w, r, &in) {
		return
	}
	tpl := strings.TrimSpace(in.Template)
	valid := false
	for _, t := range service.SubTemplates {
		if t == tpl {
			valid = true
		}
	}
	if !valid {
		fail(w, s.log, fmt.Errorf("%w: %q is not a template", service.ErrInvalid, tpl))
		return
	}
	id := s.previews.issue(tpl)
	writeJSON(w, http.StatusOK, map[string]string{"url": "/sub-preview/" + id})
}

// serveSubPreview renders the sample page for a link that was just issued.
func (s *Server) serveSubPreview(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/sub-preview/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	tpl, ok := s.previews.take(strings.TrimPrefix(r.URL.Path, prefix))
	if !ok {
		http.NotFound(w, r)
		return true
	}
	cfg, err := s.subs.Settings(r.Context())
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return true
	}
	page := previewPage(tpl)
	page.Title = cfg.Title
	page.SubURL = "https://" + r.Host + cfg.Path + "sampletoken"
	s.renderSubPage(w, page, "sampletoken", true)
	return true
}

// previewPage is the made-up customer a preview shows.
func previewPage(tpl string) *service.SubPage {
	exp := time.Now().Add(12*24*time.Hour + 4*time.Hour)
	seen := time.Now().Add(-3 * time.Hour)
	page := &service.SubPage{
		Title:      "W-UI",
		Template:   tpl,
		Name:       "sample@customer",
		Status:     "active",
		Protocol:   "wireguard",
		Locale:     "en",
		UpdatedAt:  time.Now(),
		QuotaBytes: 50 << 30,
		UsedBytes:  38 << 30,
		UpBytes:    8 << 30,
		DownBytes:  30 << 30,
		ExpiresAt:  &exp,
		LastOnline: &seen,
		SubURL:     "https://example.com/subscribe/sampletoken",
		Devices: []service.SubPageDevice{
			{ID: 0, Name: "Phone", Address: "10.66.0.2", Filename: "sample-phone.conf",
				Config: "[Interface]\nPrivateKey = (sample)\nAddress = 10.66.0.2/32\nDNS = 1.1.1.1\n\n[Peer]\nPublicKey = (sample)\nEndpoint = example.com:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25"},
			{ID: 0, Name: "Laptop", Address: "10.66.0.3", Filename: "sample-laptop.conf",
				Config: "[Interface]\nPrivateKey = (sample)\nAddress = 10.66.0.3/32\nDNS = 1.1.1.1\n\n[Peer]\nPublicKey = (sample)\nEndpoint = example.com:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25"},
		},
	}
	return page
}
