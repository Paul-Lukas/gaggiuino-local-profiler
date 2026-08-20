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
//	doc.go            this file
//	assets.go         embed.FS for static/ (vendored htmx/Alpine, style.css)
//	view.go            shots.Shot -> templates.ShotRow projection
//	handlers.go        GET /shots + the two htmx trash/restore actions
//	templates/         .templ sources (own package; see templates/layout.templ)
//	static/            vendored JS/CSS served at /web/static/*
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
// The two htmx write actions (POST /shots/{id}/trash, POST
// /shots/{id}/restore) are NOT part of that carve-out: RequireToken's
// bypass in internal/auth/auth.go is scoped to GET/HEAD requests
// specifically (a #901 code-review fix — it originally matched any
// non-/api/ path regardless of method, which let any third-party page in
// the user's browser trigger these writes with a plain unauthenticated
// POST, no token or custom header required — a CSRF hole). A POST here now
// has to either arrive through genuine HA Ingress (RequireToken's
// IsIngressRequest bypass, which applies before the GET/HEAD check and
// covers the add-on's primary access path unconditionally) or carry a
// valid X-GLP-Token header, exactly like the JSON API. Neither templ
// template attaches that header today (see templates/shots.templ's
// hx-post buttons) — standalone-mode (non-Ingress) use of the Trash/Restore
// buttons will 401 until a future page wires the token into htmx (e.g. via
// hx-headers sourced from GET /api/token, the way public-src/api.js's
// initToken() does for the SPA's own fetches). Not fixed here: this
// binary isn't wired into production yet (see cmd/server/main.go and
// go/README.md), and Ingress — the primary intended access path — is
// unaffected.
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
