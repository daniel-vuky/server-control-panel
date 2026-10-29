package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxBody caps a request body. A track request lists every row of a scan, so
// it can hold a few hundred entries; 1 MiB is far above that.
const maxBody = 1 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeFieldError writes a 422 FieldError: msg is shown under the form field.
func writeFieldError(w http.ResponseWriter, field, code, msg string) {
	writeJSON(w, http.StatusUnprocessableEntity, FieldError{Error: msg, Field: field, Code: code})
}

// readJSON decodes a JSON request body of at most maxBody bytes into v. On
// failure it writes a 400 and returns false.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return false
	}
	return true
}
