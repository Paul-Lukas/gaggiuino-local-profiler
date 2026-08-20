# GLP App — Go rewrite (in progress)

This directory holds the future Go implementation of the Gaggiuino Local
Profiler backend and frontend. It exists **parallel to** the current
Express/Node app (`server.js`, `lib/`, `routes/`, `public-src/`) at the repo
root, which remains the shipping, stable implementation. Nothing under `go/`
is wired into the Docker image, CI, or the running add-on yet.

## Status: Phase 1c (shots domain — first full REST domain)

Phase 0 was scaffolding only. Phase 1a ported the first two foundational
packages everything else builds on. Phase 1b added a real, listening HTTP
server plus the `/api/events` SSE endpoint. Phase 1c (issue #901) ports
`routes/shots.js` — the first REST domain to go the full HTTP-request →
handler → `internal/db` → response path, establishing the pattern every
later domain package follows:

- `internal/db` — real SQLite schema init + migrations on
  `modernc.org/sqlite`, verified against a fixture generated from
  `lib/db.js`'s own code (see `internal/db/doc.go`).
- `internal/auth` — real ingress-trust checks, constant-time token
  comparison, token file persistence, the security-header middleware, and
  `RequireToken`, the full API-token-auth middleware ported from
  server.js's `app.use` block (see `internal/auth/doc.go`).
- `internal/ratelimit` — the app-level rate limiter ported from
  `lib/middleware/rateLimit.js` onto `golang.org/x/time/rate`: 600 req/min
  per socket address, `/assets/*` exempt (see `internal/ratelimit/doc.go`).
- `internal/sse` — the `/api/events` Server-Sent Events endpoint ported
  from `routes/sse.js`: same headers, same 2048-byte Ingress-buffering
  padding comment, same connect-time priming, same 20s keepalive, same
  event multiplexing, plus a Go-channel pub/sub (`Hub`) domain packages
  publish onto (see `internal/sse/doc.go`).
- `internal/shots` (Phase 1c, new) — the full shot-history REST domain:
  `/shots.json`, `/api/shots/last`, `/api/shots/defaults` (GET+POST),
  `/api/shots/:id`, `/api/shots/:id/annotate`, `/api/shots/:id/{trash,
  restore,delete}`, and `/api/shots/:id/image` (GET+POST+DELETE) —
  `lib/score.js`'s scoring, `ShotService`'s annotation/trash/blocklist
  logic, and `ShotRepository`'s + `ShotDefaultsRepository`'s DB access, all
  ported. `GET /api/shots/:id/card` (share-card PNG) is deliberately
  stubbed at 501 — see `internal/shots/doc.go` for exactly what that
  endpoint and the `#450`/`#456` library-dependent scoring/notification
  paths defer to the not-yet-ported Library phase.
- `cmd/server` — `main.go` opens the DB, loads/creates the API token, and
  wires the above into a real `net/http` server listening on port 8099
  (same port as Node), with the same middleware order server.js actually
  registers (security headers → rate limiter → token auth), verified by
  manually booting the binary and curling it end-to-end (401
  unauthenticated; working `/api/events` stream and the full `/api/shots/*`
  surface with a valid token).

Every other package under `internal/` (`system`, `library`, `machines`,
`orders`, `maintenance`, `backup`) is still a Phase 0 `doc.go` placeholder —
no REST routes exist for those domains yet. `go build ./...`,
`go vet ./...`, `gofmt -l .`, and `go test ./...` (including `-race`) are
all green.

## Why

Replace Node/Express + better-sqlite3 with a single static Go binary
(`net/http` + `modernc.org/sqlite`, no CGo) to eliminate the multi-arch
`better-sqlite3` rebuild pain on Home Assistant's ARM hardware, cut the
resource footprint, and remove the npm supply-chain surface.

This is a rollout, not a rewrite-and-flip: the plan is to ship the Go
binary first on the dev channel as an opt-in beta alongside the existing
Node image, promote it to the stable/main add-on only once it's proven
itself there, and keep Node as the fallback until then — no big-bang cutover.
Two things anchor that compatibility bar:

- `openapi.yaml` at the repo root is the frozen contract — every Go endpoint
  must match paths, methods, status codes, and response shapes exactly, so
  `glp-integration`, `glp-lovelace-card`, and `glp-order-card` don't need to
  care which binary answers a request.
- The existing `/data/glp.db` SQLite file must keep opening unchanged — no
  data migration, only schema compatibility (see `internal/db/doc.go`).

Security parity with the Node app's ingress-trust model (HA Ingress vs.
direct-port trust boundary, `X-GLP-Token` auth, SSRF guards on machine
hosts, rate limiting) is non-negotiable and must be replicated 1:1, not
approximated — see `internal/auth/doc.go`.

## Layout

```
go/
  go.mod
  README.md              — this file
  RESEARCH.md             — Phase 0 research spikes (protobuf sources, image/QR libs)
  cmd/
    server/                main.go — HTTP bootstrap: db + auth + sse + shots wiring
  internal/
    db/                    lib/db.js — schema + migrations
    auth/                  server.js's ingress-trust + token-auth
    ratelimit/              lib/middleware/rateLimit.js — app-level rate limiter
    system/                routes/system.js's status/live/preheat endpoints + lib/poll.js
    sse/                   routes/sse.js — /api/events (implemented, Phase 1b)
    shots/                 routes/shots.js + ShotService/ShotRepository (implemented, Phase 1c)
    library/               routes/library/*.js + LibraryService
    machines/              routes/machines.js + machine-control.js + lib/machines/*
    orders/                routes/orders.js + OrderService
    maintenance/           routes/maintenance.js
    backup/                routes/backup.js
```

The still-unimplemented packages (`system`, `library`, `machines`,
`orders`, `maintenance`, `backup`) currently contain only a `doc.go`
package comment pointing at their Node source of truth. See each one for
exactly which file(s) it will absorb.

## Contract

`openapi.yaml` at the repo root (kept in sync with the Node app's actual
routes as of this package's creation) is the frozen reference contract for
this rewrite — every Go endpoint must match it exactly (paths, methods,
status codes, response shapes) before it's considered done, verified via
contract tests against recorded Node traffic (Phase 0/1, not yet built).

## Building

```
cd go
go build ./...
```
