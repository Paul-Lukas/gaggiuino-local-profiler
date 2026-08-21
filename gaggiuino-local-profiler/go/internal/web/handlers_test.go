package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/system"
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

// TestRootRedirect verifies #901's Ingress-base-URL fix: GET / (what HA
// Ingress always proxies a freshly-opened add-on panel to, per
// go/internal/auth.HAIngressPrefix's doc comment) redirects to "shots" via
// a genuinely RELATIVE Location header, not "/shots" — the same bug class
// go/internal/web/static/glp-token.js's own doc comment already fixed once
// for its token fetch (a root-absolute Location skips the browser's
// Ingress-prefix and 404s against HA Core's origin root instead of this
// add-on). Checked both with and without an X-Ingress-Path header set,
// since rootRedirect itself doesn't (and per this package's doc comment,
// shouldn't) special-case Ingress requests differently — see
// internal/auth.RequireToken's GET/HEAD bypass, which already lets this
// route through unauthenticated either way.
func TestRootRedirect(t *testing.T) {
	mux, _ := newTestServer(t)

	for _, tc := range []struct {
		name         string
		ingressPath  string
		supervisorIP string
	}{
		{name: "direct, no ingress headers"},
		{name: "ingress-style request", ingressPath: "/api/hassio_ingress/faketoken", supervisorIP: "172.30.32.1:12345"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.ingressPath != "" {
				req.Header.Set("X-Ingress-Path", tc.ingressPath)
			}
			if tc.supervisorIP != "" {
				req.RemoteAddr = tc.supervisorIP
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("GET /: status = %d, want %d", rec.Code, http.StatusFound)
			}
			if loc := rec.Header().Get("Location"); loc != "shots" {
				t.Errorf("GET / Location = %q, want relative %q (a leading slash would skip the Ingress prefix and 404 on HA Core's origin root)", loc, "shots")
			}
		})
	}
}

// failingResponseWriter simulates a client connection that breaks partway
// through the response: its Write only ever accepts half of what it's
// given before returning an error, mimicking templ's bufio-buffered
// Render flushing the fully-rendered HTML in one big underlying Write that
// itself only partially lands on the wire. It records every WriteHeader/
// Write call so a test can assert nothing was attempted after the failure.
type failingResponseWriter struct {
	header           http.Header
	writeHeaderCalls []int
	body             strings.Builder
	writeCalls       int
}

func (f *failingResponseWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *failingResponseWriter) WriteHeader(status int) {
	f.writeHeaderCalls = append(f.writeHeaderCalls, status)
}

func (f *failingResponseWriter) Write(p []byte) (int, error) {
	f.writeCalls++
	n := len(p) / 2
	f.body.Write(p[:n])
	return n, errors.New("simulated broken connection")
}

// TestListPage_RenderFailureOnlyLogs pins the #901 code-review fix: when
// templates.ShotsPage.Render fails after output has already started
// (this handler's own comment says exactly that — a broken client
// connection mid-stream), the handler must only log, never attempt a
// WriteHeader/Write afterward. The previous code called
// httputil.InternalError there, which did both — producing a "superfluous
// WriteHeader" plus a JSON error blob appended straight after the
// truncated HTML, contradicting its own comment.
func TestListPage_RenderFailureOnlyLogs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	repo := shots.NewRepository(sqlDB)
	upsertTestShot(t, repo, 1, 1_700_000_000, "Espresso Classic", nil)

	h := NewHandlers(shots.NewService(repo))
	fw := &failingResponseWriter{}
	req := httptest.NewRequest(http.MethodGet, "/shots", nil)

	h.listPage(fw, req)

	if len(fw.writeHeaderCalls) != 0 {
		t.Errorf("WriteHeader called %d time(s) after a render failure, want 0: %v", len(fw.writeHeaderCalls), fw.writeHeaderCalls)
	}
	if fw.writeCalls != 1 {
		t.Errorf("Write called %d time(s) after a render failure, want exactly 1 (the failing flush itself)", fw.writeCalls)
	}
	if strings.Contains(fw.body.String(), `"error"`) {
		t.Errorf("response body contains a JSON error blob appended after partial HTML: %q", fw.body.String())
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

// TestTrashRestore_RequireAuthBehindRequireToken wires this package's
// routes behind auth.RequireToken the same way cmd/server actually does
// (unlike newTestServer's bare mux above, which never applies auth
// middleware and so can't exercise this) and confirms the #901 code-review
// CSRF fix end to end: the two write actions 401 without a token, while
// GET /shots stays reachable without one — see internal/web/doc.go's
// "Auth model" section.
func TestTrashRestore_RequireAuthBehindRequireToken(t *testing.T) {
	const testToken = "test-fixture-token-not-a-real-secret"

	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	repo := shots.NewRepository(sqlDB)
	upsertTestShot(t, repo, 1, 1_700_000_000, "Espresso Classic", nil)

	h := NewHandlers(shots.NewService(repo))
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

	if rec := doAuthedRequest("GET", "/shots", ""); rec.Code != http.StatusOK {
		t.Errorf("GET /shots without a token: status = %d, want 200", rec.Code)
	}
	if rec := doAuthedRequest("POST", "/shots/1/trash", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /shots/1/trash without a token: status = %d, want 401", rec.Code)
	}
	if rec := doAuthedRequest("POST", "/shots/1/restore", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /shots/1/restore without a token: status = %d, want 401", rec.Code)
	}
	if rec := doAuthedRequest("POST", "/shots/1/trash", testToken); rec.Code != http.StatusOK {
		t.Errorf("POST /shots/1/trash with a valid token: status = %d, want 200", rec.Code)
	}
	if rec := doAuthedRequest("POST", "/shots/1/restore", testToken); rec.Code != http.StatusOK {
		t.Errorf("POST /shots/1/restore with a valid token: status = %d, want 200", rec.Code)
	}
}

// TestListPage_LoadsTokenScript pins that GET /shots actually ships
// glp-token.js — the follow-up fix to the CSRF gap
// TestTrashRestore_RequireAuthBehindRequireToken above pins server-side.
// Without this <script> tag present in the rendered page, a real browser
// would never run the code that fetches a token and attaches it to htmx's
// write requests, and the Trash/Restore buttons would 401 exactly like
// they did before this fix (see static/glp-token.js's own doc comment and
// templates/layout.templ).
func TestListPage_LoadsTokenScript(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := doRequest(t, mux, "GET", "/shots")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shots: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `src="/web/static/glp-token.js"`) {
		t.Errorf("GET /shots body missing glp-token.js <script> tag\nbody:\n%s", rec.Body.String())
	}
}

// TestGlpTokenJS_UsesRelativeTokenFetchForIngress pins the #901 code-review
// fix for a root-absolute fetch("/api/token") silently breaking token
// fetching under HA Ingress: every route this package registers sits at a
// per-session Ingress prefix (/api/hassio_ingress/<token>/...), and a
// root-absolute fetch resolves against the origin root instead of that
// prefix — missing the add-on's own GET /api/token handler entirely (a
// 404 against HA Core's root, swallowed by fetchToken()'s .catch(), token
// stays null forever). The served script must fetch the relative
// "api/token" instead, exactly like public-src/api.js's initToken()
// already does for the SPA — see glp-token.js's own doc comment for the
// full reasoning. A full browser/Ingress-proxy round trip isn't exercised
// here (no headless browser in this test suite); this instead pins the
// exact served source text, which is what actually determines how the
// browser resolves the fetch.
func TestGlpTokenJS_UsesRelativeTokenFetchForIngress(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := doRequest(t, mux, "GET", "/web/static/glp-token.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /web/static/glp-token.js: status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `fetch("/api/token"`) {
		t.Errorf("glp-token.js fetches the root-absolute \"/api/token\" — breaks under HA Ingress's session-prefixed URLs\nbody:\n%s", body)
	}
	if !strings.Contains(body, `fetch("api/token"`) {
		t.Errorf("glp-token.js does not fetch the relative \"api/token\" (mirroring public-src/api.js's initToken())\nbody:\n%s", body)
	}
}

// TestGlpTokenJS_WaitsForTokenBeforeIssuingHtmxRequests pins the #901
// code-review fix for the click-before-fetch-resolves race: a Trash/
// Restore click landing before fetchToken()'s GET /api/token settled used
// to fire immediately with no X-GLP-Token header attached and 401, even
// though the fetch would have succeeded moments later. The fix defers
// htmx's actual request dispatch via the async htmx:confirm/issueRequest
// pattern (see htmx-2.0.10.min.js's confirm-event dispatch) until the
// token fetch has settled. Like the sibling test above, this pins the
// served source rather than driving a real browser/timing race, which
// this test suite has no infrastructure for.
func TestGlpTokenJS_WaitsForTokenBeforeIssuingHtmxRequests(t *testing.T) {
	mux, _ := newTestServer(t)
	rec := doRequest(t, mux, "GET", "/web/static/glp-token.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /web/static/glp-token.js: status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"htmx:confirm"`) {
		t.Errorf("glp-token.js does not listen for htmx:confirm — nothing defers a click until the token fetch settles\nbody:\n%s", body)
	}
	if !strings.Contains(body, "evt.detail.issueRequest") {
		t.Errorf("glp-token.js does not call evt.detail.issueRequest — htmx request would never actually be issued after the wait\nbody:\n%s", body)
	}
}

// TestBrowserFlow_FetchedTokenAuthorizesTrash simulates the actual browser
// sequence glp-token.js drives end to end through the real
// auth.RequireToken middleware stack, the same pattern
// TestTrashRestore_RequireAuthBehindRequireToken above established but
// carried one step further: instead of a token the test already knows,
// this fetches GET /api/token — the exact request glp-token.js's
// fetchToken() issues on page load — through internal/system's real
// handler, and then uses whatever token that endpoint actually returned to
// authorize the htmx:configRequest-attached POST /shots/{id}/trash — the
// exact request glp-token.js's htmx:configRequest listener produces for a
// browser's Trash click. If RegisterRoutes ever registered a route under a
// different token, or getToken and RequireToken ever fell out of sync,
// this (unlike a test with a hardcoded shared token) would catch it.
func TestBrowserFlow_FetchedTokenAuthorizesTrash(t *testing.T) {
	const testToken = "test-fixture-token-not-a-real-secret"
	const remoteAddr = "192.168.1.50:1234" // LAN, not Ingress/Supervisor

	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	repo := shots.NewRepository(sqlDB)
	upsertTestShot(t, repo, 1, 1_700_000_000, "Espresso Classic", nil)

	mux := http.NewServeMux()
	NewHandlers(shots.NewService(repo)).RegisterRoutes(mux)
	// getToken (called below) only reads h.token/h.rl, never poller/demo —
	// nil is safe for a token-only test, and keeps this test from having
	// to fake an HA adapter just to exercise an unrelated handler.
	system.NewHandlers(nil, nil, testToken).RegisterRoutes(mux)
	handler := auth.RequireToken(testToken)(mux)

	// Step 1: page load. A real browser would run glp-token.js from here
	// (TestListPage_LoadsTokenScript above pins that it's actually linked).
	pageReq := httptest.NewRequest(http.MethodGet, "/shots", nil)
	pageReq.RemoteAddr = remoteAddr
	pageRec := httptest.NewRecorder()
	handler.ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("GET /shots: status = %d, want 200", pageRec.Code)
	}

	// Step 2: glp-token.js's fetchToken() — GET /api/token, no header yet
	// (fresh page load, no token cached).
	tokenReq := httptest.NewRequest(http.MethodGet, "/api/token", nil)
	tokenReq.RemoteAddr = remoteAddr
	tokenRec := httptest.NewRecorder()
	handler.ServeHTTP(tokenRec, tokenReq)
	if tokenRec.Code != http.StatusOK {
		t.Fatalf("GET /api/token: status = %d, want 200, body = %s", tokenRec.Code, tokenRec.Body.String())
	}
	var tokenBody struct {
		APIToken string `json:"apiToken"`
	}
	if err := json.Unmarshal(tokenRec.Body.Bytes(), &tokenBody); err != nil {
		t.Fatalf("decoding GET /api/token body: %v (body = %s)", err, tokenRec.Body.String())
	}
	if tokenBody.APIToken == "" {
		t.Fatalf("GET /api/token returned an empty apiToken")
	}

	// Step 3: htmx:configRequest attaches the fetched token as
	// X-GLP-Token to the Trash button's POST.
	trashReq := httptest.NewRequest(http.MethodPost, "/shots/1/trash", nil)
	trashReq.RemoteAddr = remoteAddr
	trashReq.Header.Set("X-GLP-Token", tokenBody.APIToken)
	trashRec := httptest.NewRecorder()
	handler.ServeHTTP(trashRec, trashReq)
	if trashRec.Code != http.StatusOK {
		t.Errorf("POST /shots/1/trash with the fetched token: status = %d, want 200, body = %s", trashRec.Code, trashRec.Body.String())
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
		"/web/static/glp-token.js",
	} {
		rec := doRequest(t, mux, "GET", path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, rec.Code)
		}
	}
}
