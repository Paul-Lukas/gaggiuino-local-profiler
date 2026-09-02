package debug

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
)

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open(%s): %v", path, err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func insertShot(t *testing.T, sqlDB *sql.DB, id int64, profile string) {
	t.Helper()
	if _, err := sqlDB.Exec(
		`INSERT INTO shots (id, timestamp, duration, profile_name, data, machine_id) VALUES (?,?,?,?,?,1)`,
		id, 1000, 300, profile, "{}",
	); err != nil {
		t.Fatalf("inserting shot %d: %v", id, err)
	}
}

func countShots(t *testing.T, path string) int {
	t.Helper()
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopening %s: %v", path, err)
	}
	defer sqlDB.Close()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM shots`).Scan(&n); err != nil {
		t.Fatalf("counting shots: %v", err)
	}
	return n
}

func newMux(h *Handlers) *http.ServeMux {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

// TestExportImportRoundTrip: export a DB with two shots, mutate it, then
// re-import the exported bytes and confirm the mutation is gone.
func TestExportImportRoundTrip(t *testing.T) {
	t.Setenv("GLP_DEV_BUILD", "dev")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "glp.db")
	sqlDB := openTestDB(t, dbPath)
	insertShot(t, sqlDB, 1, "V60")
	insertShot(t, sqlDB, 2, "Espresso")

	h := NewHandlers(sqlDB, dbPath, nil)
	mux := newMux(h)

	// Export.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/debug/export-db", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("export Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !bytes.Contains([]byte(cd), []byte("attachment; filename=\"glp-db-export-")) {
		t.Errorf("export Content-Disposition = %q", cd)
	}
	exported := append([]byte(nil), rec.Body.Bytes()...)
	if !bytes.HasPrefix(exported, sqliteMagic) {
		t.Fatalf("exported bytes are not a SQLite file")
	}

	// Mutate the live DB.
	insertShot(t, sqlDB, 3, "Extra")
	if got := countShots(t, dbPath); got != 3 {
		t.Fatalf("pre-import shot count = %d, want 3", got)
	}

	// Import the earlier export back.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/debug/import-db", bytes.NewReader(exported))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("import response: %v", err)
	}
	if resp["ok"] != true || resp["restartRequired"] != true {
		t.Errorf("import response = %v", resp)
	}
	bp, _ := resp["backupPath"].(string)
	if bp == "" || filepath.Dir(bp) != "." {
		t.Errorf("backupPath = %q, want a bare filename", bp)
	}
	if _, err := os.Stat(filepath.Join(dir, bp)); err != nil {
		t.Errorf("backup file %s missing: %v", bp, err)
	}

	// The mutation is gone; the -wal/-shm sidecars of the old DB were removed.
	if got := countShots(t, dbPath); got != 2 {
		t.Errorf("post-import shot count = %d, want 2 (import should have restored the 2-shot export)", got)
	}
	for _, sfx := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + sfx); err == nil {
			t.Errorf("stale %s sidecar left behind after import", sfx)
		}
	}
}

func TestImportDB_RejectsNonSQLiteBody(t *testing.T) {
	t.Setenv("GLP_DEV_BUILD", "dev")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "glp.db")
	sqlDB := openTestDB(t, dbPath)
	mux := newMux(NewHandlers(sqlDB, dbPath, nil))

	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"empty", nil, "No database file uploaded"},
		{"garbage", []byte("this is definitely not a sqlite database file, it is just text"), "Not a SQLite database file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/debug/import-db", bytes.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			var resp map[string]string
			json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp["error"] != tc.want {
				t.Errorf("error = %q, want %q", resp["error"], tc.want)
			}
		})
	}
}

func TestImportDB_RejectsOversizedBody(t *testing.T) {
	t.Setenv("GLP_DEV_BUILD", "dev")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "glp.db")
	sqlDB := openTestDB(t, dbPath)
	mux := newMux(NewHandlers(sqlDB, dbPath, nil))

	orig := importDBMaxBytes
	importDBMaxBytes = 32
	defer func() { importDBMaxBytes = orig }()

	body := append([]byte(nil), sqliteMagic...)
	body = append(body, bytes.Repeat([]byte("A"), 1024)...) // well over 32

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/debug/import-db", bytes.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDebugRoutes_404WhenNotDevBuild(t *testing.T) {
	t.Setenv("GLP_DEV_BUILD", "")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "glp.db")
	sqlDB := openTestDB(t, dbPath)
	mux := newMux(NewHandlers(sqlDB, dbPath, nil))

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/debug/export-db"},
		{http.MethodPost, "/api/debug/import-db"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, bytes.NewReader(sqliteMagic)))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s %s: body = %q, want empty", tc.method, tc.path, rec.Body.String())
		}
	}
}

func TestDebugMachine_NotRegisteredInProduction(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "glp.db")
	sqlDB := openTestDB(t, dbPath)
	mux := newMux(NewHandlers(sqlDB, dbPath, nil))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/debug/machine", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route not registered under NODE_ENV=production)", rec.Code)
	}
}

func TestDebugMachine_ReportsUnreachableMachine(t *testing.T) {
	t.Setenv("NODE_ENV", "development")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "glp.db")
	sqlDB := openTestDB(t, dbPath)

	h := NewHandlers(sqlDB, dbPath, nil)
	h.httpGet = func(_ context.Context, _ string) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}
	// registry is nil -> defaultMachineBaseURL errors before httpGet, so
	// this still exercises the always-200 { ok:false } branch.
	mux := newMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/debug/machine", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (machine debug always answers 200)", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["ok"] != false {
		t.Errorf("ok = %v, want false", resp["ok"])
	}
}
