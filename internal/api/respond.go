package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/abolfazl/w-ui/internal/service"
)

// errorBody is the shape of every failed response, so the frontend has one
// thing to parse rather than a mix of JSON and plain text.
type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// fail maps a service error to a status code.
//
// Validation failures are the caller's problem and say what is wrong; anything
// unrecognised is logged in full and reported as a generic 500, so internal
// details never reach the browser.
func fail(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, service.ErrInvalid),
		errors.Is(err, service.ErrDeviceLimit),
		errors.Is(err, service.ErrPoolExhausted):
		// The field travels with the message so the page can put it under the
		// input it is about rather than in a toast that names nothing.
		if field := service.FieldOf(err); field != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": humanMessage(err),
				"field": field,
			})
			return
		}
		writeError(w, http.StatusBadRequest, humanMessage(err))
	default:
		log.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		// Go's own wording here names its decoder, not anything the caller did.
		// The unknown-field case is worth translating because it is what a
		// mismatched client version looks like, and "unknown field" alone sends
		// somebody looking for a typo they did not make.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "unknown field"):
			field := strings.Trim(strings.TrimPrefix(msg, "json: unknown field "), `"`)
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"this request carried a field this server does not know: %s. "+
					"The panel and its interface are probably different versions", field))
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "the request had no body")
		case strings.Contains(msg, "request body too large"):
			writeError(w, http.StatusRequestEntityTooLarge, "that request is too large")
		default:
			writeError(w, http.StatusBadRequest, "the request body could not be read as JSON")
		}
		return false
	}
	return true
}

// humanMessage turns an error chain into a sentence for a person.
//
// Go errors are wrapped with the package that raised them, so by the time one
// reaches an operator it reads "wgdriver: WireGuard is only available on Linux".
// The prefix is how a developer finds the code; it is noise to everyone else,
// and capitalising it produces "Wgdriver", which is worse than leaving it.
func humanMessage(err error) string {
	msg := err.Error()

	// Strip leading "package: " segments, of which there may be several. Only a
	// bare lowercase identifier counts, so a message that happens to contain a
	// colon — a URL, or a quoted value — keeps it.
	for {
		i := strings.Index(msg, ": ")
		// Long enough for the longest sentinel below. The length is only a cheap
		// first filter; what actually keeps a real sentence intact is the strict
		// check that follows.
		if i <= 0 || i > 40 {
			break
		}
		head := msg[:i]
		if !isPackageWord(head) {
			break
		}
		msg = msg[i+2:]
	}

	if msg == "" {
		return err.Error()
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

// isPackageWord reports whether a prefix looks like a package or layer name
// rather than part of the sentence.
func isPackageWord(w string) bool {
	if w == "" {
		return false
	}
	// Our own sentinels. Each is a marker for code to match on with errors.Is,
	// and the message that follows already says what it means — "Device limit
	// reached: Roya already has 1 of 1 devices" says it twice.
	switch w {
	case "invalid input",
		"device limit reached",
		"no addresses left on this interface",
		"not found":
		return true
	}
	for _, r := range w {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
