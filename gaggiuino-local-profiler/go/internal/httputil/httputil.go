// Package httputil holds the tiny JSON-response helpers every REST domain
// package (orders, backup, maintenance, machines, shots, library, system)
// had copy-pasted verbatim across Phases 1c-1g — WriteJSON/WriteError were
// byte-for-byte identical in all seven handlers.go files, and InternalError
// differed only in whether/how it logged (orders and system domain-prefixed
// their log line, backup/maintenance/machines/library either swallowed the
// error silently or discarded it with a `_ = err` comment solely to satisfy
// the compiler). Extracted per the Phase 1g code-review's finding #5 (#901):
// one implementation, every domain package's own internalError now a
// one-line wrapper passing its own domain prefix.
package httputil

import (
	"encoding/json"
	"log"
	"net/http"
)

// WriteJSON marshals v as the response body at status. A marshal failure
// (a caller passing an unmarshalable value, which should never happen for
// any of this codebase's own response types) degrades to a hardcoded 500
// body rather than a panic or a half-written response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"Internal server error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// WriteError writes {"error": message} at status.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}

// InternalError logs err with domain's prefix (matching this codebase's
// "<package>: <what>: %v" log convention) and writes the generic 500 body —
// never the raw error text, which could leak internal details (file paths,
// SQL, a machine host) to the client.
func InternalError(w http.ResponseWriter, domain string, err error) {
	log.Printf("%s: internal error: %v", domain, err)
	WriteError(w, http.StatusInternalServerError, "Internal server error")
}
