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

// humanMessage strips the prefixes that exist to locate an error in the code
// from a message that is going to be read by a person.
//
// "service: invalid input: listen port 99999 is out of range" tells an operator
// two things they cannot use and one they can.
func humanMessage(err error) string {
	msg := err.Error()
	for _, prefix := range []string{
		"service: invalid input: ",
		"service: ",
		"invalid input: ",
	} {
		msg = strings.TrimPrefix(msg, prefix)
	}
	if msg == "" {
		return err.Error()
	}
	// Capitalised, because it is now the start of a sentence rather than the
	// tail of a chain.
	return strings.ToUpper(msg[:1]) + msg[1:]
}
