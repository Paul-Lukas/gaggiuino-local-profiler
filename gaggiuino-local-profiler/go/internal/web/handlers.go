package web

import (
	"html"
	"log"
	"net/http"
	"strconv"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/httputil"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/web/templates"
)

// Handlers wires shots.Service into the HTML handlers below — the same
// service internal/shots' own JSON handlers call, per this package's own
// doc comment above.
type Handlers struct {
	shots *shots.Service
}

// NewHandlers builds Handlers around svc.
func NewHandlers(svc *shots.Service) *Handlers {
	return &Handlers{shots: svc}
}

// RegisterRoutes registers this package's page and static-asset routes
// onto mux. Unlike every REST domain package's RegisterRoutes (see e.g.
// shots.Handlers.RegisterRoutes), these routes are NOT prefixed with
// /api/ — see this package's doc comment for why that's the auth-relevant
// choice, not an incidental one.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /web/static/", staticHandler())
	mux.HandleFunc("GET /{$}", h.rootRedirect)
	mux.HandleFunc("GET /shots", h.listPage)
	mux.HandleFunc("POST /shots/{id}/trash", h.trashAction)
	mux.HandleFunc("POST /shots/{id}/restore", h.restoreAction)
}

// rootRedirect ports the fix for #901's live-blocking bug: nothing in
// internal/web (or any other domain package) ever registered a route for
// GET / itself, only the individual subpages (/shots, /library, /machines,
// ...). HA Ingress always proxies a freshly-opened add-on panel to GET / on
// the container, so with no handler there the bare ingress base URL 404'd
// before auth/middleware even ran — the very case the Dockerfile
// HEALTHCHECK sidestepped by probing /web/static/style.css instead of /
// (see go/Dockerfile's own comment), but whose actual user-facing
// consequence was never fixed until now. "/{$}" (not "/") is deliberate:
// "/" alone is ServeMux's catch-all for any unmatched path, which would
// silently redirect genuine 404s (typos, probes) to /shots instead of
// reporting them; "/{$}" matches only the exact root path.
//
// The redirect target is a relative Location header, set directly rather
// than via http.Redirect: http.Redirect resolves a relative target against
// r.URL.Path — the path this app itself sees, which is always "/" here
// regardless of the browser's real address, because HA Ingress strips its
// per-session prefix (/api/hassio_ingress/<token>) before forwarding to
// the container (see internal/auth.HAIngressPrefix's doc comment) — so it
// would turn "shots" into the root-absolute "/shots" (path.Split("/") ==
// ("/", ""), then olddir+url == "/shots"; verified against
// net/http/server.go's Redirect). A root-absolute Location resolves in the
// BROWSER against its own visible URL, which still has the ingress
// prefix — landing on the origin root instead of the add-on and 404ing
// there, exactly the bug class go/internal/web/static/glp-token.js's own
// doc comment already fixed once for its token fetch. Writing the header
// directly keeps "shots" genuinely relative, so the browser resolves it
// against its own address bar (".../<ingress-prefix>/"), landing on
// ".../<ingress-prefix>/shots" — the add-on's own page.
func (h *Handlers) rootRedirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Location", "shots")
	w.WriteHeader(http.StatusFound)
}

// listPage ports GET /shots: the full page, live shots plus any trashed
// ones — the templ equivalent of loadData()+loadTrashData() in
// public-src/views/shots/index.js, minus the chart/annotation panel this
// list page doesn't render.
func (h *Handlers) listPage(w http.ResponseWriter, r *http.Request) {
	live, err := h.shots.GetAll()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	trashed, err := h.shots.GetTrash()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}

	rows := make([]templates.ShotRow, len(live))
	for i, shot := range live {
		rows[i] = toShotRow(shot, h.shots.ComputeScore(shot))
	}
	trashRows := make([]templates.ShotRow, len(trashed))
	for i, shot := range trashed {
		trashRows[i] = toShotRow(shot, h.shots.ComputeScore(shot))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ShotsPage(rows, trashRows).Render(r.Context(), w); err != nil {
		// Render can only fail after writing has already started (a
		// broken client connection, mid-stream), so there's no valid
		// status code left to send — log and stop, matching net/http's
		// own convention for a write-time failure. httputil.InternalError
		// would be wrong here (a #901 code-review finding): it calls
		// WriteHeader(500) and writes a JSON error body, which after a
		// partial HTML write only produces a "superfluous WriteHeader"
		// warning plus a JSON blob appended straight after truncated HTML.
		log.Printf("web: rendering /shots: %v", err)
	}
}

// trashAction ports the htmx `hx-post="/shots/{id}/trash"` interaction:
// trashes the shot and, on success, answers an empty 200 body so htmx's
// `hx-swap="outerHTML"` removes the row element entirely (see
// templates/shots.templ's shotRowActive) — no JSON envelope needed since
// nothing but htmx itself consumes this response.
func (h *Handlers) trashAction(w http.ResponseWriter, r *http.Request) {
	id, ok := parseShotID(r.PathValue("id"))
	if !ok {
		writeFragmentError(w, http.StatusBadRequest, "Invalid shot ID")
		return
	}
	if err := h.shots.TrashShot(id); err != nil {
		if err == shots.ErrShotNotFound {
			writeFragmentError(w, http.StatusNotFound, "Shot not found")
			return
		}
		httputil.InternalError(w, "web", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// restoreAction ports the htmx `hx-post="/shots/{id}/restore"` interaction
// — same empty-body-on-success pattern as trashAction, removing the row
// from the trash section.
func (h *Handlers) restoreAction(w http.ResponseWriter, r *http.Request) {
	id, ok := parseShotID(r.PathValue("id"))
	if !ok {
		writeFragmentError(w, http.StatusBadRequest, "Invalid shot ID")
		return
	}
	if err := h.shots.RestoreShot(id); err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// parseShotID enforces the same positive-integer-within-MaxShotID bound
// internal/shots' own handlers.go's parseID does, using strconv directly
// rather than importing that package's unexported parseID/jsParseInt (this
// route doesn't need parseId()'s JS-parseInt leading-garbage tolerance —
// a path segment htmx itself always builds from row.ID, never user free
// text, so plain strconv.ParseInt's stricter all-digits-or-reject behavior
// is fine here).
func parseShotID(param string) (int64, bool) {
	id, err := strconv.ParseInt(param, 10, 64)
	if err != nil || id < 1 || id > shots.MaxShotID {
		return 0, false
	}
	return id, true
}

// writeFragmentError answers a small HTML fragment (not JSON — this
// route's only consumer is htmx, which swaps the response body straight
// into the DOM) at status, styled as a shot-row so it drops into the same
// hx-target the success path would have emptied.
func writeFragmentError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<div class="shot-row"><span class="fragment-error" style="color:var(--err)">` + html.EscapeString(message) + `</span></div>`))
}
