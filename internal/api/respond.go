package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/abolfazl/w-ui/internal/routing"
	"github.com/abolfazl/w-ui/internal/service"
)

// errorBody is the shape of every failed response, so the frontend has one
// thing to parse rather than a mix of JSON and plain text. Code is a fixed
// name for what went wrong (codeOf), for the page and for a script to tell
// cases apart without reading the sentence, which may change.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// The codes a failed response can carry. Fixed: a script may match on them.
const (
	codeInvalid              = "invalid"
	codeNotFound             = "notFound"
	codeCapacityInsufficient = "capacity.insufficient"
	codeTokensNone           = "tokens.none"
	codeTokensInsufficient   = "tokens.insufficient"
	codeTokensResellerIssues = "tokens.resellerCannotIssue"
	codeOperatorPaused       = "operator.paused"
	codeDeviceLimit          = "client.deviceLimit"
	codeAddressesExhausted   = "interface.addressesExhausted"
	codeDatabaseBusy         = "database.busy"
	codeInternal             = "internal"
)

// codeOf is the fixed code for a service error.
func codeOf(err error) string {
	switch {
	case errors.Is(err, service.ErrNoUsers):
		return codeCapacityInsufficient
	case errors.Is(err, service.ErrBarred):
		return codeOperatorPaused
	case errors.Is(err, service.ErrDeviceLimit):
		return codeDeviceLimit
	case errors.Is(err, service.ErrPoolExhausted):
		return codeAddressesExhausted
	case errors.Is(err, service.ErrNotFound):
		return codeNotFound
	case service.FieldOf(err) == "tokens":
		return codeTokensInsufficient
	}
	return codeInvalid
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

// writeErrorCode is writeError with the fixed code for the case.
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: msg, Code: code})
}

// fail maps a service error to a status code.
//
// Validation failures are the caller's problem and say what is wrong; anything
// unrecognised is logged in full and reported as a generic 500, so internal
// details never reach the browser.
func fail(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		writeErrorCode(w, http.StatusNotFound, codeNotFound, humanMessage(err))
	case errors.Is(err, service.ErrInvalid),
		errors.Is(err, service.ErrDeviceLimit),
		errors.Is(err, service.ErrPoolExhausted),
		// Raised deeper down, by the code that parses an operator's address and
		// port lists. Without this a typo in one entry of a list is reported as
		// an internal error, which tells the operator nothing and suggests the
		// panel is broken rather than the input.
		errors.Is(err, routing.ErrInvalidPolicy):
		// The field travels with the message so the page can put it under the
		// input it is about rather than in a toast that names nothing.
		if field := service.FieldOf(err); field != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": humanMessage(err),
				"field": field,
				"code":  codeOf(err),
			})
			return
		}
		writeErrorCode(w, http.StatusBadRequest, codeOf(err), humanMessage(err))
	case service.IsBusy(err):
		// Another change held the database past the wait: this one was not
		// made, and is worth asking for again -- not an unexpected fault.
		log.Warn("a change waited for the database and was not made", "error", err)
		w.Header().Set("Retry-After", "5")
		writeErrorCode(w, http.StatusServiceUnavailable, codeDatabaseBusy,
			"the panel's database was busy writing another change and did not free up in time, so nothing was changed. Try again in a few seconds")
	default:
		// What went wrong stays in the log, in full, under a reference the
		// answer gives: the operator is told the panel failed, not how --
		// a path or a query in a browser is a leak -- and where to look.
		ref := faultRef()
		log.Error("request failed", "ref", ref, "error", err)
		writeErrorCode(w, http.StatusInternalServerError, codeInternal, fmt.Sprintf(
			"the panel could not finish this: an unexpected fault, logged under reference %s. "+
				"Try again; if it happens again, the panel's log says what failed under that reference", ref))
	}
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeWith(w, r, dst, true)
}

// decodeLenient reads a body and lets unknown fields pass. For what one
// panel sends another: a panel updated first must still be able to talk to
// a node that has not been, and a field the node does not know yet is
// nothing it can act on anyway.
func decodeLenient(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeWith(w, r, dst, false)
}

func decodeWith(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if strict {
		dec.DisallowUnknownFields()
	}

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
		"not found",
		"invalid policy":
		return true
	}
	for _, r := range w {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// faultRef is a short reference for one unexpected fault, to find it in
// the log by.
func faultRef() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
