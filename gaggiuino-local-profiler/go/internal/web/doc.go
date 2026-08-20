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
// HTML pages under this package are deliberately NOT gated by
// internal/auth.RequireToken's X-GLP-Token check — same as every static
// asset the Node app serves today. That middleware's own bypass list
// already carves this out: `if !strings.HasPrefix(r.URL.Path, "/api/") &&
// r.URL.Path != "/shots.json" { next.ServeHTTP(w, r); return }` (see
// internal/auth/auth.go) — the token gate only ever covered the JSON API
// surface, never the SPA's static HTML/JS/CSS, because server.js's
// public-src frontend fetches its own token via GET /api/token and attaches
// it to its own XHR calls (see public-src/api.js's initToken()); the
// static shell that bootstraps that fetch was never itself gated. Routes
// under /shots and /web/static/ replicate exactly that: the pages (and the
// htmx actions they trigger, since neither is prefixed with /api/) reach
// this package's handlers unauthenticated, protected the same way the rest
// of the static frontend already is — HA Ingress's own auth in front of
// the add-on, or physical/LAN access to the exposed port in standalone
// mode. This is a pragmatic decision for this first template page, not a
// new auth scheme: it reuses the one Node already ships, rather than
// inventing a session/cookie model the dispatch brief explicitly said not
// to invent. A future page that needs the JSON API's stricter guarantee
// can still call through fetch() with a token the way the SPA does today;
// nothing here forecloses that.
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
