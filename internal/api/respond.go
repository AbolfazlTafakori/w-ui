package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, fmt.Sprintf("malformed request body: %v", err))
		return false
	}
	return true
}
