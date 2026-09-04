package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/abolfazl/w-ui/internal/service"
)

// handleSubscription serves a customer their own configuration.
//
// This is the only route on the panel that is reached without signing in, and
// the token in the path is the whole of the authorisation. That is deliberate
// and it is what every subscription client can actually do — they fetch a URL
// and nothing else — but it means the handler has to be careful in ways the
// rest of the API does not:
//
//   - the answer for a wrong token is the same as for one that never existed,
//     so the endpoint cannot be used to find out which tokens are real;
//   - nothing derived from the customer's own text reaches a header unescaped;
//   - the panel's own session cookie is never read here, so a customer's
//     browser following this link cannot act as whoever is signed in.
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.subs.Settings(r.Context())
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if !cfg.Enabled {
		http.NotFound(w, r)
		return
	}

	token := strings.TrimPrefix(r.URL.Path, cfg.Path)
	// A token with a slash in it is a path traversal attempt or a broken link;
	// either way it is not one we issued.
	if token == "" || strings.ContainsAny(token, "/\\") {
		http.NotFound(w, r)
		return
	}

	// One file, for a customer downloading a single device from their page.
	if s.maybeServeSubDevice(w, r, token) {
		return
	}
	// A browser gets a page about the plan; a client app gets the configuration.
	// Decided on what the caller says it accepts, so an app fetching this URL on
	// a schedule keeps receiving exactly what it did before.
	if s.maybeServeSubPage(w, r, token) {
		return
	}

	format := r.URL.Query().Get("format")
	bundle, err := s.subs.Serve(r.Context(), token, format)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, service.ErrInvalid) {
			// A real customer with no devices yet. Their app should not keep
			// retrying, and the operator should see this in the panel rather
			// than in a support message.
			http.Error(w, "this subscription has no devices on it yet", http.StatusNotFound)
			return
		}
		// Anything else is this server's fault, and a retry may well work.
		s.log.Error("subscription failed", "error", err)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	h := w.Header()
	h.Set("Content-Type", bundle.ContentType)
	// The three headers subscription clients read. Profile-update-interval is
	// in hours and profile-title is base64 so a title in any script survives a
	// header, which is latin-1 by specification.
	h.Set("Subscription-Userinfo", bundle.UserInfo)
	h.Set("Profile-Update-Interval", fmt.Sprint(bundle.UpdateHours))
	h.Set("Profile-Title", "base64:"+b64(bundle.Title))
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", bundle.Filename))
	// A link a customer's app polls should not be cached by anything between
	// them and here, or a customer who has just been given a new device keeps
	// getting yesterday's answer.
	h.Set("Cache-Control", "no-store")
	// This is not a page and nothing should frame it or sniff it.
	h.Set("X-Content-Type-Options", "nosniff")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle.Body)
}

// ── the authenticated side ───────────────────────────────────────────────────

func (s *Server) handleGetSubSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.subs.Settings(r.Context())
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSaveSubSettings(w http.ResponseWriter, r *http.Request) {
	var in service.SubSettings
	if !decode(w, r, &in) {
		return
	}
	out, err := s.subs.SaveSettings(r.Context(), in)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleClientSubscription hands the operator one customer's link.
func (s *Server) handleClientSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	token, err := s.subs.EnsureToken(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	link, err := s.subs.LinkFor(r.Context(), token, r.Host)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": link, "token": token})
}

// handleRotateSubscription invalidates the old link and issues a new one.
func (s *Server) handleRotateSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	token, err := s.subs.RotateToken(r.Context(), id)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	link, err := s.subs.LinkFor(r.Context(), token, r.Host)
	if err != nil {
		fail(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": link, "token": token})
}

// SubscriptionRouter puts the subscription service in front of everything else.
//
// The path is configurable, so it cannot be registered on a mux once at
// startup: an operator who moves it would have to restart the panel, and the
// restart is exactly what they are trying to avoid by having a settings page.
// Checking here costs one string comparison per request and lets the change
// take effect on the next one.
func (s *Server) SubscriptionRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only GET and HEAD. A subscription is a read, and answering a POST
		// here would give a cross-site form somewhere a way to reach it.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		// Never shadow the panel's own routes, whatever the path is set to.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}

		cfg, err := s.subs.Settings(r.Context())
		if err != nil || !cfg.Enabled || cfg.Path == "" || cfg.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, cfg.Path) {
			next.ServeHTTP(w, r)
			return
		}

		s.handleSubscription(w, r)
	})
}

// b64 encodes a header value that may contain any script.
//
// HTTP header values are latin-1, so a Persian or Chinese profile title put in
// one raw is mangled or rejected depending on the client. Encoding it is what
// the subscription clients expect and is why the value is prefixed.
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// handleSecurityWarnings reports what an attacker would notice about this
// installation.
func (s *Server) handleSecurityWarnings(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusOK, map[string]any{"warnings": []any{}})
		return
	}
	warnings := s.audit.Run(r.Context())
	if warnings == nil {
		warnings = []service.Warning{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings})
}
