package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// newTestLibraryServer opens a throwaway on-disk SQLite DB (same pattern as
// newTestServer above and internal/library's own helpers_test.go) and wires
// it into a fresh web.LibraryHandlers routed through a real *http.ServeMux.
func newTestLibraryServer(t *testing.T) (*http.ServeMux, *library.Repository, *shots.Repository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	libRepo := library.NewRepository(sqlDB)
	shotsRepo := shots.NewRepository(sqlDB)
	h := NewLibraryHandlers(libRepo, shotsRepo)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, libRepo, shotsRepo
}

// ── Beans ──────────────────────────────────────────────────────────────

func TestBeansPage_RendersBeans(t *testing.T) {
	mux, libRepo, _ := newTestLibraryServer(t)
	lib := library.Library{
		Beans: []library.Entity{
			{
				"id": int64(1), "name": "Ethiopia Yirgacheffe", "roaster": "Roaster A",
				"category": "speciality", "stock_g": 250.0,
				"bags": []any{library.Entity{"roastDate": "2026-01-01"}},
			},
			{
				"id": int64(2), "name": "Decaf Blend", "decaf": true, "enabled": false,
			},
		},
	}
	if err := libRepo.SaveLibrary(lib); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	rec := doRequest(t, mux, "GET", "/beans")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /beans: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html prefix", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"Ethiopia Yirgacheffe",
		"Roaster A",
		"speciality",
		"roasted 2026-01-01",
		"250 g",
		"250 g left", // remaining == full stock, nothing consumed yet
		"Decaf Blend",
		"decaf",
		"disabled",
		`hx-post="/beans/1/toggle-active"`,
		`hx-post="/beans/2/toggle-active"`,
		"Disable", // bean 1: enabled -> offers Disable
		"Enable",  // bean 2: disabled -> offers Enable
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /beans body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestBeansPage_Empty(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "GET", "/beans")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /beans: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No beans yet.") {
		t.Errorf("GET /beans body missing empty-state message:\n%s", rec.Body.String())
	}
}

// TestToggleBeanActiveAction_RoundTrip drives the one write action Beans
// gets in this phase end to end: toggling flips the enabled flag (persisted
// via library.ToggleBeanActive, the same function
// internal/library/handlers_beans.go's REST handler now also calls) and the
// returned fragment reflects the new state.
func TestToggleBeanActiveAction_RoundTrip(t *testing.T) {
	mux, libRepo, _ := newTestLibraryServer(t)
	if err := libRepo.SaveLibrary(library.Library{
		Beans: []library.Entity{{"id": int64(10), "name": "Kenya AA"}},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	rec := doRequest(t, mux, "POST", "/beans/10/toggle-active")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /beans/10/toggle-active: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="bean-row-10"`) {
		t.Errorf("toggle response missing bean-row-10 fragment\nbody:\n%s", body)
	}
	if !strings.Contains(body, "disabled") || !strings.Contains(body, "Enable") {
		t.Errorf("toggle response should show the bean as disabled with an Enable action\nbody:\n%s", body)
	}

	lib, err := libRepo.GetLibrary()
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if enabled, _ := lib.Beans[0]["enabled"].(bool); enabled {
		t.Errorf("bean 10: enabled = true after toggle, want false")
	}

	// Toggle back.
	rec = doRequest(t, mux, "POST", "/beans/10/toggle-active")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /beans/10/toggle-active (2nd): status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Disable") {
		t.Errorf("toggle-back response should show the bean as enabled with a Disable action\nbody:\n%s", rec.Body.String())
	}
}

func TestToggleBeanActiveAction_InvalidID(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "POST", "/beans/not-a-number/toggle-active")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /beans/not-a-number/toggle-active: status = %d, want 400", rec.Code)
	}
}

func TestToggleBeanActiveAction_NotFound(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "POST", "/beans/999/toggle-active")
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST /beans/999/toggle-active: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Bean not found") {
		t.Errorf("POST /beans/999/toggle-active body = %q, want it to mention 'Bean not found'", rec.Body.String())
	}
}

// ── Grinders ───────────────────────────────────────────────────────────

func TestGrindersPage_RendersGrinders(t *testing.T) {
	mux, libRepo, shotsRepo := newTestLibraryServer(t)
	if err := libRepo.SaveLibrary(library.Library{
		Grinders: []library.Entity{
			{"id": int64(1), "name": "Niche Zero", "burrType": "conical", "purchaseDate": "2025-01-01"},
		},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}
	upsertTestShot(t, shotsRepo, 1, 1_700_000_000, "Espresso", map[string]any{
		"grinder": "Niche Zero",
		"dose":    18.0,
	})

	rec := doRequest(t, mux, "GET", "/grinders")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /grinders: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Niche Zero",
		"conical",
		"purchased 2025-01-01",
		"1 shots / 18 g since burr swap",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /grinders body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestGrindersPage_Empty(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "GET", "/grinders")
	if !strings.Contains(rec.Body.String(), "No grinders yet.") {
		t.Errorf("GET /grinders body missing empty-state message:\n%s", rec.Body.String())
	}
}

// ── Baskets ────────────────────────────────────────────────────────────

func TestBasketsPage_RendersBaskets(t *testing.T) {
	mux, libRepo, _ := newTestLibraryServer(t)
	if err := libRepo.SaveLibrary(library.Library{
		Baskets: []library.Entity{
			{"id": int64(1), "name": "VST 18g", "wallType": "straight", "shape": "round", "doseCapacity": "18g", "holeCount": "20"},
		},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	rec := doRequest(t, mux, "GET", "/baskets")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /baskets: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"VST 18g", "straight", "round", "18g", "20 holes"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /baskets body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestBasketsPage_Empty(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "GET", "/baskets")
	if !strings.Contains(rec.Body.String(), "No baskets yet.") {
		t.Errorf("GET /baskets body missing empty-state message:\n%s", rec.Body.String())
	}
}

// ── Puck screens ───────────────────────────────────────────────────────

func TestPuckScreensPage_RendersPuckScreens(t *testing.T) {
	mux, libRepo, _ := newTestLibraryServer(t)
	if err := libRepo.SaveLibrary(library.Library{
		PuckScreens: []library.Entity{
			{"id": int64(1), "name": "IMS puck screen", "thickness": "1.4mm", "material": "stainless"},
		},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	rec := doRequest(t, mux, "GET", "/puckscreens")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /puckscreens: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"IMS puck screen", "1.4mm", "stainless"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /puckscreens body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestPuckScreensPage_Empty(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "GET", "/puckscreens")
	if !strings.Contains(rec.Body.String(), "No puck screens yet.") {
		t.Errorf("GET /puckscreens body missing empty-state message:\n%s", rec.Body.String())
	}
}

// ── Milks ──────────────────────────────────────────────────────────────

func TestMilksPage_RendersMilks(t *testing.T) {
	mux, libRepo, _ := newTestLibraryServer(t)
	if err := libRepo.SaveLibrary(library.Library{
		Milks: []library.Entity{
			{"id": int64(1), "name": "Oat Milk", "emoji": "🌾", "stockMl": 500.0},
			{"id": int64(2), "name": "Whole Milk", "stockMl": 1000.0}, // no emoji -> defaults to 🥛
		},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	rec := doRequest(t, mux, "GET", "/milks")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /milks: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Oat Milk", "🌾", "500 ml", "Whole Milk", "🥛", "1000 ml"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /milks body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestMilksPage_Empty(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "GET", "/milks")
	if !strings.Contains(rec.Body.String(), "No milks yet.") {
		t.Errorf("GET /milks body missing empty-state message:\n%s", rec.Body.String())
	}
}

// ── Recipes ────────────────────────────────────────────────────────────

func TestRecipesPage_RendersRecipes(t *testing.T) {
	mux, libRepo, _ := newTestLibraryServer(t)
	if err := libRepo.SaveLibrary(library.Library{
		Recipes: []library.Entity{
			{"id": int64(1), "name": "V60", "brewMethod": "pourover", "drinkType": "filter"},
		},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	rec := doRequest(t, mux, "GET", "/recipes")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /recipes: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"V60", "pourover", "filter"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /recipes body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestRecipesPage_Empty(t *testing.T) {
	mux, _, _ := newTestLibraryServer(t)
	rec := doRequest(t, mux, "GET", "/recipes")
	if !strings.Contains(rec.Body.String(), "No recipes yet.") {
		t.Errorf("GET /recipes body missing empty-state message:\n%s", rec.Body.String())
	}
}

// ── Auth ───────────────────────────────────────────────────────────────

// TestLibraryPagesRequireAuthBehindRequireToken wires this package's Library
// routes behind auth.RequireToken the same way cmd/server actually does
// (mirroring handlers_test.go's TestTrashRestore_RequireAuthBehindRequireToken
// for the Shots page) and confirms every GET /beans, /grinders, /baskets,
// /puckscreens, /milks, /recipes page stays reachable without a token, while
// the one write action this phase adds — POST /beans/{id}/toggle-active —
// requires either genuine HA Ingress or a valid X-GLP-Token, exactly like
// the Shots page's trash/restore actions. This is the check #901's own
// dispatch brief calls out: Phase 2a shipped with exactly this class of bug
// (a write route missing RequireToken's CSRF-relevant GET/HEAD scoping)
// before it was caught in review, so this page's write action is verified
// the same way from the start.
func TestLibraryPagesRequireAuthBehindRequireToken(t *testing.T) {
	const testToken = "test-fixture-token-not-a-real-secret"

	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	libRepo := library.NewRepository(sqlDB)
	shotsRepo := shots.NewRepository(sqlDB)
	if err := libRepo.SaveLibrary(library.Library{
		Beans: []library.Entity{{"id": int64(1), "name": "Kenya AA"}},
	}); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}

	h := NewLibraryHandlers(libRepo, shotsRepo)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	handler := auth.RequireToken(testToken)(mux)

	doAuthedRequest := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "192.168.1.50:1234" // LAN, not Ingress/Supervisor
		if token != "" {
			req.Header.Set("X-GLP-Token", token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, path := range []string{"/beans", "/grinders", "/baskets", "/puckscreens", "/milks", "/recipes"} {
		if rec := doAuthedRequest("GET", path, ""); rec.Code != http.StatusOK {
			t.Errorf("GET %s without a token: status = %d, want 200", path, rec.Code)
		}
	}

	if rec := doAuthedRequest("POST", "/beans/1/toggle-active", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /beans/1/toggle-active without a token: status = %d, want 401", rec.Code)
	}
	if rec := doAuthedRequest("POST", "/beans/1/toggle-active", testToken); rec.Code != http.StatusOK {
		t.Errorf("POST /beans/1/toggle-active with a valid token: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}
