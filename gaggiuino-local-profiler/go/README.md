# GLP App — Go rewrite (in progress)

This directory holds the future Go implementation of the Gaggiuino Local
Profiler backend and frontend. It exists **parallel to** the current
Express/Node app (`server.js`, `lib/`, `routes/`, `public-src/`) at the repo
root, which remains the shipping, stable implementation. Nothing under `go/`
is wired into the Docker image, CI, or the running add-on yet.

## Status: Phase 0 (skeleton)

This is scaffolding only — a Go module with one placeholder package per
future domain, each with a `doc.go` explaining what will move here and from
which Node file. **No behavior has been ported.** `go build ./...` is green,
but there is nothing here that talks to a database, a machine, or an HTTP
client yet.

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
  internal/
    db/                    lib/db.js — schema + migrations
    auth/                  server.js's ingress-trust + token-auth + rate-limit
    system/                routes/system.js's status/live/preheat endpoints + lib/poll.js
    sse/                   routes/sse.js — /api/events
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
