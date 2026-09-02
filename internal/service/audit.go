package service

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Warning is one thing about this installation that is worth saying out loud.
type Warning struct {
	// ID is stable so a page can key on it and an operator can be told which
	// one they dismissed.
	ID string `json:"id"`
	// Severity is "high" for something being exploited right now if anybody is
	// looking, "medium" for a weakness that needs another mistake to matter.
	Severity string `json:"severity"`
	// Title is the problem in a few words.
	Title string `json:"title"`
	// Detail says what is actually wrong.
	Detail string `json:"detail"`
	// Fix says what to do about it, in the operator's own terms.
	Fix string `json:"fix"`
	// Where links to the page that fixes it, so the warning is one click from
	// being actionable rather than a thing to go and hunt for.
	Where string `json:"where,omitempty"`
}

// Audit looks over the installation and reports what an attacker would notice.
//
// This exists because the dangerous states are all states that look fine. A
// panel on its default port with a guessable subscription path and no
// two-factor is working perfectly and is one scan away from being somebody
// else's. Nothing here changes anything: it says what is true and where to go.
type Audit struct {
	db     *gorm.DB
	subs   *Subscriptions
	listen string
}

func NewAudit(db *gorm.DB, subs *Subscriptions, listen string) *Audit {
	return &Audit{db: db, subs: subs, listen: listen}
}

// Run collects the warnings that currently apply.
func (a *Audit) Run(ctx context.Context) []Warning {
	var out []Warning

	out = append(out, a.checkAdmins(ctx)...)
	out = append(out, a.checkSubscription(ctx)...)
	out = append(out, a.checkExposure()...)
	out = append(out, a.checkTokens(ctx)...)
	out = append(out, a.checkNodes(ctx)...)

	return out
}

// checkAdmins looks at who can sign in.
func (a *Audit) checkAdmins(ctx context.Context) []Warning {
	var out []Warning

	var admins []model.Admin
	if err := a.db.WithContext(ctx).Find(&admins).Error; err != nil {
		return nil
	}

	for _, ad := range admins {
		// The name that every scanner tries first. Guessing a password is much
		// easier when half of the credential is already known.
		if strings.EqualFold(ad.Username, "admin") {
			out = append(out, Warning{
				ID:       "admin-username",
				Severity: "medium",
				Title:    "The administrator is still called \"admin\"",
				Detail: "Half of the credential is a name anybody would try first, " +
					"which is what makes an automated attempt worth someone's time.",
				Fix:   "Change the username under Settings, Security.",
				Where: "/settings/security",
			})
		}
		if ad.TOTPSecret == "" {
			out = append(out, Warning{
				ID:       "no-totp",
				Severity: "medium",
				Title:    "Two-factor authentication is off",
				Detail: "A password that leaks — reused, phished, or read from a " +
					"backup — is the whole of the way in.",
				Fix:   "Turn on two-factor authentication under Settings, Security.",
				Where: "/settings/security",
			})
		}
		// A password that has never been changed since the installer wrote it
		// is a password that has been in a terminal's scrollback ever since.
		if ad.LastLoginAt == nil {
			out = append(out, Warning{
				ID:       "never-signed-in",
				Severity: "medium",
				Title:    "The generated password has never been changed",
				Detail: "The installer printed it once. It is still in whatever " +
					"terminal history and log that session produced.",
				Fix:   "Sign in and set a password of your own.",
				Where: "/settings/security",
			})
		}
	}
	return out
}

// checkSubscription looks at what is reachable without signing in.
func (a *Audit) checkSubscription(ctx context.Context) []Warning {
	cfg, err := a.subs.Settings(ctx)
	if err != nil || !cfg.Enabled {
		return nil
	}

	var out []Warning

	// The paths every panel of this kind has used. A path that is guessable
	// turns "you need the link" into "you need to have looked".
	for _, known := range []string{"/sub/", "/subscription/", "/s/"} {
		if strings.EqualFold(cfg.Path, known) {
			out = append(out, Warning{
				ID:       "well-known-sub-path",
				Severity: "high",
				Title:    fmt.Sprintf("The subscription path %q is well known", cfg.Path),
				Detail: "Anyone scanning for panels tries this first. A customer's " +
					"configuration is served to whoever holds the link, so a " +
					"guessable path is worth guessing.",
				Fix:   "Change it to something of your own under Settings, Subscription.",
				Where: "/settings/subscription",
			})
			break
		}
	}

	if len(strings.Trim(cfg.Path, "/")) < 6 {
		out = append(out, Warning{
			ID:       "short-sub-path",
			Severity: "medium",
			Title:    "The subscription path is short enough to stumble onto",
			Detail:   "A short path narrows the search for anybody looking for one.",
			Fix:      "Make it longer and less obvious under Settings, Subscription.",
			Where:    "/settings/subscription",
		})
	}
	return out
}

// checkExposure looks at how the panel is reachable.
func (a *Audit) checkExposure() []Warning {
	var out []Warning

	host, port, err := net.SplitHostPort(a.listen)
	if err != nil {
		return nil
	}

	// Bound to everything. Correct for most installs and worth naming, because
	// the operator who meant to put it behind a proxy usually has not noticed.
	if host == "" || host == "0.0.0.0" || host == "::" {
		out = append(out, Warning{
			ID:       "listening-everywhere",
			Severity: "medium",
			Title:    "The panel is reachable from any address",
			Detail: fmt.Sprintf(
				"It is listening on every interface on port %s, so the sign-in "+
					"page is on the public internet.", port),
			Fix: "If it sits behind a proxy or a tunnel, bind it to 127.0.0.1 " +
				"with WUI_LISTEN and let the proxy be the only way in.",
		})
	}
	return out
}

// checkTokens looks at machine credentials.
func (a *Audit) checkTokens(ctx context.Context) []Warning {
	var out []Warning

	var tokens []model.APIToken
	if err := a.db.WithContext(ctx).Find(&tokens).Error; err != nil {
		return nil
	}

	// A token nobody has used since it was made is either forgotten or was
	// issued for something that never happened. Either way it is a key to the
	// whole API sitting in a drawer.
	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	stale := 0
	for _, t := range tokens {
		used := t.LastUsedAt
		if used == nil && t.CreatedAt.Before(cutoff) {
			stale++
			continue
		}
		if used != nil && used.Before(cutoff) {
			stale++
		}
	}
	if stale > 0 {
		out = append(out, Warning{
			ID:       "stale-tokens",
			Severity: "medium",
			Title:    fmt.Sprintf("%d access token(s) have not been used in three months", stale),
			Detail: "Each one is full access to this panel's API for whoever " +
				"still has a copy of it.",
			Fix:   "Revoke the ones nothing is using, under Nodes.",
			Where: "/nodes",
		})
	}
	return out
}

// checkNodes looks at how this panel talks to the others.
func (a *Audit) checkNodes(ctx context.Context) []Warning {
	var out []Warning

	var nodes []model.Node
	if err := a.db.WithContext(ctx).Where("kind = ?", model.KindRemote).
		Find(&nodes).Error; err != nil {
		return nil
	}

	var plain []string
	for _, n := range nodes {
		if strings.HasPrefix(n.Address, "http://") && !isLoopback(hostOf(n.Address)) {
			plain = append(plain, n.Name)
		}
	}
	if len(plain) > 0 {
		out = append(out, Warning{
			ID:       "node-plain-http",
			Severity: "high",
			Title:    fmt.Sprintf("%d node(s) are reached over plain HTTP", len(plain)),
			Detail: fmt.Sprintf(
				"The access token for %s travels unencrypted, and that token is "+
					"full control of the panel at the far end.",
				strings.Join(plain, ", ")),
			Fix:   "Give those panels a certificate and use https:// addresses.",
			Where: "/nodes",
		})
	}
	return out
}

func hostOf(addr string) string {
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	if i := strings.IndexAny(addr, "/:"); i > 0 {
		addr = addr[:i]
	}
	return addr
}
