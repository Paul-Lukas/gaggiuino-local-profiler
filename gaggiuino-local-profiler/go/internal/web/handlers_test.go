package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// newTestServer opens a throwaway on-disk SQLite DB (same pattern as
// internal/shots' own helpers_test.go's newTestHandlers) and wires it into
// a fresh web.Handlers routed through a real *http.ServeMux, so
// r.PathValue("id") is populated the same way it would be in cmd/server.
func newTestServer(t *testing.T) (*http.ServeMux, *shots.Repository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	repo := shots.NewRepository(sqlDB)
	h := NewHandlers(shots.NewService(repo))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, repo
}

func upsertTestShot(t *testing.T, repo *shots.Repository, id, timestamp int64, profileName string, annotation map[string]any) {
	t.Helper()
	shot := shots.Shot{
		"id":          id,
		"timestamp":   timestamp,
		"profileName": profileName,
		"machineId":   int64(1),
	}
	if annotation != nil {
		shot["annotation"] = annotation
	}
	if err := repo.Upsert(shot); err != nil {
		t.Fatalf("repo.Upsert(%d): %v", id, err)
	}
}

func doRequest(t *testing.T, mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestListPage_RendersShots verifies GET /shots renders the expected
// structural content — profile names, coffee/dose, and a trash action per
// row — not a pixel-exact snapshot (per the dispatch brief's "nicht
// pixelgenau, aber strukturell" requirement).
func TestListPage_RendersShots(t *testing.T) {
	mux, repo := newTestServer(t)
	upsertTestShot(t, repo, 1, 1_700_000_000, "Espresso Classic", map[string]any{
		"coffee": "Ethiopia Yirgacheffe",
		"dose":   18.2,
		"rating": 4,
	})
	upsertTestShot(t, repo, 2, 1_700_000_100, "Filter", nil)

	rec := doRequest(t, mux, "GET", "/shots")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shots: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html prefix", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"Espresso Classic",
		"Filter",
		"Ethiopia Yirgacheffe",
		"18.2 g",
		`hx-post="/shots/1/trash"`,
		`hx-post="/shots/2/trash"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /shots body missing %q\nbody:\n%s", want, body)
		}
	}
	if strings.Contains(body, "trash-section") {
		t.Errorf("GET /shots body has a trash section with nothing trashed yet")
	}
}

// TestListPage_Empty verifies the empty-state branch when no shots exist.
func TestListPage_Empty(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := doRequest(t, mux, "GET", "/shots")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shots: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No shots yet.") {
		t.Errorf("GET /shots body missing empty-state message:\n%s", rec.Body.String())
	}
}

// TestTrashAndRestore_RoundTrip drives the two htmx actions end to end:
// trashing a shot moves it out of the live list and into the trash
// section, and restoring it moves it back — exercising the same
// shots.Service.TrashShot/RestoreShot the JSON API's own handlers call.
func TestTrashAndRestore_RoundTrip(t *testing.T) {
	mux, repo := newTestServer(t)
	upsertTestShot(t, repo, 5, 1_700_000_000, "Espresso Classic", nil)

	trashRec := doRequest(t, mux, "POST", "/shots/5/trash")
	if trashRec.Code != http.StatusOK {
		t.Fatalf("POST /shots/5/trash: status = %d, body = %s", trashRec.Code, trashRec.Body.String())
	}
	if trashRec.Body.Len() != 0 {
		t.Errorf("POST /shots/5/trash: body = %q, want empty (htmx outerHTML-removes the row)", trashRec.Body.String())
	}

	afterTrash := doRequest(t, mux, "GET", "/shots").Body.String()
	if !strings.Contains(afterTrash, `hx-post="/shots/5/restore"`) {
		t.Errorf("GET /shots after trash: missing restore action for shot 5\nbody:\n%s", afterTrash)
	}
	if strings.Contains(afterTrash, `hx-post="/shots/5/trash"`) {
		t.Errorf("GET /shots after trash: shot 5 still in the live list\nbody:\n%s", afterTrash)
	}

	restoreRec := doRequest(t, mux, "POST", "/shots/5/restore")
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("POST /shots/5/restore: status = %d, body = %s", restoreRec.Code, restoreRec.Body.String())
	}

	afterRestore := doRequest(t, mux, "GET", "/shots").Body.String()
	if !strings.Contains(afterRestore, `hx-post="/shots/5/trash"`) {
		t.Errorf("GET /shots after restore: shot 5 not back in the live list\nbody:\n%s", afterRestore)
	}
	if strings.Contains(afterRestore, "trash-section") {
		t.Errorf("GET /shots after restore: trash section should be gone (nothing trashed)\nbody:\n%s", afterRestore)
	}
}

// TestTrashAction_InvalidID verifies the same 400 boundary internal/shots'
// own handlers enforce for a malformed id.
func TestTrashAction_InvalidID(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := doRequest(t, mux, "POST", "/shots/not-a-number/trash")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /shots/not-a-number/trash: status = %d, want 400", rec.Code)
	}
}

// TestTrashAction_NotFound verifies the 404 branch when the shot doesn't
// exist, answered as an HTML fragment (not JSON) since htmx is the only
// consumer of this route.
func TestTrashAction_NotFound(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := doRequest(t, mux, "POST", "/shots/999/trash")
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /shots/999/trash: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Shot not found") {
		t.Errorf("POST /shots/999/trash body = %q, want it to mention 'Shot not found'", rec.Body.String())
	}
}

// TestStaticAssets_Served verifies the vendored htmx/Alpine files are
// reachable at /web/static/ — a build-time embed.FS wiring bug would 404
// here even though `go build` itself stays green.
func TestStaticAssets_Served(t *testing.T) {
	mux, _ := newTestServer(t)
	for _, path := range []string{
		"/web/static/style.css",
		"/web/static/vendor/htmx-2.0.10.min.js",
		"/web/static/vendor/alpine-csp-3.16.2.min.js",
	} {
		rec := doRequest(t, mux, "GET", path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, rec.Code)
		}
	}
}
