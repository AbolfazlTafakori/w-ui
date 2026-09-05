package api

import "net/http"

// SecureHeaders sets what a browser needs to be told about this page.
//
// The panel is an authenticated application served to a browser, and everything
// it loads it serves itself — no CDN, no external font, no analytics. That makes
// a strict policy cheap to hold, and there is no reason not to.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// Everything comes from this origin. Injected markup that tries to
		// reach anywhere else is refused by the browser rather than trusted.
		// 'unsafe-inline' covers the style attributes Vue writes for meters and
		// sparklines; scripts get no such exemption, which is the half that
		// matters for injection.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self'; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'")

		// The panel must not be framed. An admin clicking what looks like
		// something else, on a page that has framed this one, is a real way to
		// have a customer deleted.
		h.Set("X-Frame-Options", "DENY")

		// A response the browser decides is a script because of its contents
		// rather than its declared type is a way to turn stored data into code.
		h.Set("X-Content-Type-Options", "nosniff")

		// Paths here contain customer and interface identifiers, and there is
		// no reason to hand those to whatever an operator clicks through to.
		h.Set("Referrer-Policy", "no-referrer")

		// None of these are used, and saying so stops a page that finds a way
		// to ask from being able to.
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")

		// Only over TLS. Sending it over plain HTTP would tell a browser to
		// refuse the panel entirely on a server that has not set TLS up yet,
		// which is an operator locked out by a header.
		if clientScheme(r) == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}
