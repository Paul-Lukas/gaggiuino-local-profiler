# GLP App — Go rewrite (in progress)

This directory holds the future Go implementation of the Gaggiuino Local
Profiler backend and frontend. It exists **parallel to** the current
Express/Node app (`server.js`, `lib/`, `routes/`, `public-src/`) at the repo
root, which remains the shipping, stable implementation. Nothing under `go/`
is wired into the Docker image, CI, or the running add-on yet.

## Status: Phase 1b (SSE + rate limiting + server bootstrap)

Phase 0 was scaffolding only. Phase 1a ported the first two foundational
packages everything else builds on. Phase 1b adds a real, listening HTTP
server on top of them, with one working endpoint:

- `internal/db` — real SQLite schema init + migrations on
  `modernc.org/sqlite`, verified against a fixture generated from
  `lib/db.js`'s own code (see `internal/db/doc.go`).
- `internal/auth` — real ingress-trust checks, constant-time token
  comparison, token file persistence, the security-header middleware, and
  (Phase 1b) `RequireToken`, the full API-token-auth middleware ported from
  server.js's `app.use` block (see `internal/auth/doc.go`).
- `internal/ratelimit` (Phase 1b, new package) — the app-level rate limiter
  ported from `lib/middleware/rateLimit.js` onto `golang.org/x/time/rate`:
  600 req/min per socket address, `/assets/*` exempt (see
  `internal/ratelimit/doc.go`).
- `internal/sse` (Phase 1b) — the `/api/events` Server-Sent Events endpoint
  ported from `routes/sse.js`: same headers, same 2048-byte Ingress-buffering
  padding comment, same connect-time priming, same 20s keepalive, same
  event multiplexing, plus a Go-channel pub/sub (`Hub`) Phase 1c's domain
  packages will publish onto (see `internal/sse/doc.go`).
- `cmd/server` (Phase 1b, new) — a minimal `main.go` that opens the DB,
  loads/creates the API token, and wires the above into a real `net/http`
  server listening on port 8099 (same port as Node), with the same
  middleware order server.js actually registers (security headers → rate
  limiter → token auth), verified by manually booting the binary and
  curling it end-to-end (401 unauthenticated, working `/api/events` stream
  with a valid token).

Every other package under `internal/` (`system`, `shots`, `library`,
`machines`, `orders`, `maintenance`, `backup`) is still a Phase 0 `doc.go`
placeholder — **no REST domain routes exist yet**, only `/api/events`.
`go build ./...`, `go vet ./...`, and `go test ./...` (including `-race`)
are all green.

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
    server/                main.go — HTTP bootstrap (Phase 1b): db + auth + sse wiring, no domain routes yet
  internal/
    db/                    lib/db.js — schema + migrations
    auth/                  server.js's ingress-trust + token-auth
    ratelimit/              lib/middleware/rateLimit.js — app-level rate limiter
    system/                routes/system.js's status/live/preheat endpoints + lib/poll.js
    sse/                   routes/sse.js — /api/events (implemented, Phase 1b)
    shots/                 routes/shots.js + ShotService/ShotRepository
    library/               routes/library/*.js + LibraryService
    machines/              routes/machines.js + machine-control.js + lib/machines/*
    orders/                routes/orders.js + OrderService
    maintenance/           routes/maintenance.js
    backup/                routes/backup.js
```

Each `internal/*` package currently contains only a `doc.go` package
comment pointing at its Node source of truth. See each one for exactly
which file(s) it will absorb.

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
