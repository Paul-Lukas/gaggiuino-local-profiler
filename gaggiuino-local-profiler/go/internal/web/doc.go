// Package web is the Go frontend foundation (Phase 2a, #901): templ+htmx+
// Alpine tooling, the shared page layout, and the first fully working
// page — a shots list — built on top of internal/shots' existing Phase 1c
// service layer, the same way internal/shots itself was the first REST
// domain package establishing the pattern every later one followed (see
// go/README.md's Status section). No REST-API handler in internal/shots is
// reused directly for HTML rendering here — this package calls
// shots.Service, the same dependency internal/shots' own handlers.go
// calls, and renders HTML instead of JSON from it.
//
// File layout:
//
//	doc.go              this file
//	assets.go           embed.FS for static/ (vendored htmx/Alpine, style.css,
//	                    glp-token.js)
//	view.go             shots.Shot -> templates.ShotRow projection
//	handlers.go         GET /shots + the two htmx trash/restore actions
//	view_library.go     (Phase 2b) library.Entity -> templates' *Row projections
//	handlers_library.go (Phase 2b) GET /beans,/grinders,/baskets,/puckscreens,
//	                    /milks,/recipes + the toggle-active htmx action
//	view_machines.go     (Phase 2c) machines.Machine -> templates.MachineRow
//	handlers_machines.go (Phase 2c) GET /machines + set-default/delete htmx
//	                    actions, plus GET /live (static chrome only — the
//	                    live chart itself is static/live.js, not server-
//	                    rendered; see that file's own doc comment)
//	templates/          .templ sources (own package; see templates/layout.templ)
//	static/             vendored + first-party JS/CSS served at /web/static/*
//	                    (Phase 2c adds live.js and vendored Chart.js)
//
// # Auth model
//
// GET /shots and /web/static/* are deliberately NOT gated by
// internal/auth.RequireToken's X-GLP-Token check — same as every static
// asset the Node app serves today. server.js's public-src frontend fetches
// its own token via GET /api/token and attaches it to its own XHR calls
// (see public-src/api.js's initToken()); the static shell that bootstraps
// that fetch was never itself gated, and neither is this package's read-only
// page. Protected the same way the rest of the static frontend already is
// — HA Ingress's own auth in front of the add-on, or physical/LAN access to
// the exposed port in standalone mode.
//
// The htmx write actions — POST /shots/{id}/trash, POST /shots/{id}/restore,
// (Phase 2b) POST /beans/{id}/toggle-active in handlers_library.go, and
// (Phase 2c) POST /machines/{id}/default and POST /machines/{id}/delete in
// handlers_machines.go — are NOT part of that carve-out: RequireToken's
// bypass in
// internal/auth/auth.go is scoped to GET/HEAD requests specifically (a #901
// code-review fix — it originally matched any non-/api/ path regardless of
// method, which let any third-party page in the user's browser trigger
// these writes with a plain unauthenticated POST, no token or custom header
// required — a CSRF hole). A POST here now has to either arrive through
// genuine HA Ingress (RequireToken's IsIngressRequest bypass, which applies
// before the GET/HEAD check and covers the add-on's primary access path
// unconditionally) or carry a valid X-GLP-Token header, exactly like the
// JSON API.
//
// That header is wired in structurally, for every current and future
// Phase-2 page, not per-button: templates/layout.templ loads
// static/glp-token.js once, globally, in <head>. That script fetches the
// token from the already-public GET /api/token (mirroring
// public-src/api.js's initToken() for the existing SPA) and attaches it as
// X-GLP-Token to every htmx request via htmx's htmx:configRequest event —
// see glp-token.js's own doc comment for the full mechanism and why
// fetch-and-attach was chosen over baking the token into GET /shots' own
// HTML (that page is itself unauthenticated per the paragraph above, so an
// SSR-embedded token would hand it to a caller who couldn't otherwise get
// it — GET /api/token carries no such risk, since it's already exactly as
// reachable to that same caller). Standalone mode with expose_api_port
// explicitly set to false denies GET /api/token to a non-Ingress caller
// (see internal/system/handlers.go's getToken), so glp-token.js's fetch
// comes back empty and the Trash/Restore buttons 401 in that
// configuration — not a new failure mode, the same one
// isApiPortBlocked()/api.js's initToken() already describe for the SPA
// today, and out of scope for this package to change. This is the ONLY
// caller glp-token.js's fetch is expected to fail for — genuine HA Ingress
// access (the primary path) reaches GET /api/token fine, because the
// fetch is relative ("api/token"), resolving against the Ingress-prefixed
// page URL the same way public-src/api.js's initToken() does, not against
// the origin root (a #901 code-review fix: a root-absolute fetch would
// have skipped the Ingress prefix entirely and missed the add-on).
// TestBrowserFlow_FetchedTokenAuthorizesTrash in handlers_test.go drives
// this end to end through the real auth.RequireToken stack: fetch GET
// /api/token, then use the token it returns to authorize
// POST /shots/{id}/trash.
//
// # CSP
//
// internal/auth.SecurityHeaders's Content-Security-Policy (`script-src
// 'self'`, no `'unsafe-inline'`/`'unsafe-eval'`) is not relaxed for this
// package — every template avoids inline <script>/<style> and event-handler
// attributes, and static/vendor/ ships the CSP-safe Alpine build
// (@alpinejs/csp, not plain alpinejs) specifically because core Alpine's
// expression evaluator needs 'unsafe-eval'. See static/vendor/NOTICE.md.
package web
