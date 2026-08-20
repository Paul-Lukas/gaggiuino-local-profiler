package library

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// newTestHandlers opens a throwaway on-disk SQLite DB via internal/db.Open
// (same fixture pattern as shots/helpers_test.go) and wires it into a fresh
// Handlers/Repository pair.
func newTestHandlers(t *testing.T) (*Handlers, *Repository, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	repo := NewRepository(sqlDB)
	h := NewHandlers(repo, shots.NewRepository(sqlDB))
	// DefaultImageDir ("/data/bean-images") isn't writable by a test
	// process — point image uploads at a throwaway dir instead.
	h.imageDir = t.TempDir()
	return h, repo, sqlDB
}

func newMux(h *Handlers) *http.ServeMux {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decoding response body %q: %v", body, err)
	}
	return m
}

func decodeBodyArray(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var m []map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decoding response array %q: %v", body, err)
	}
	return m
}
