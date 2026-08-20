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

Full context — motivation, target architecture, migration strategy phase by
phase, and the non-negotiable security-parity requirements (ingress trust
model, token auth, SSRF guards, rate limiting, the 404/501 status-code
semantics, bool-as-string quirks glp-integration depends on) — lives in the
migration plan:

`~/.claude/plans/folgendes-m-chte-ich-als-shimmying-hartmanis.md`

In short: replace Node/Express + better-sqlite3 with a single static Go
binary (`net/http` + `modernc.org/sqlite`, no CGo) to eliminate the
multi-arch `better-sqlite3` rebuild pain on Home Assistant's ARM hardware,
cut the resource footprint, and remove the npm supply-chain surface — without
breaking `glp-integration`, `glp-lovelace-card`, `glp-order-card`, HA
Ingress, standalone Docker, or any existing `/data/glp.db`.

## Layout

```
go/
  go.mod
  README.md              — this file
  RESEARCH.md             — Phase 0 research spikes (protobuf sources, image/QR libs)
  internal/
    db/                    lib/db.js — schema + migrations
    auth/                  server.js's ingress-trust + token-auth + rate-limit
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
