# GLP App — Go rewrite (in progress)

This directory holds the future Go implementation of the Gaggiuino Local
Profiler backend and frontend. It exists **parallel to** the current
Express/Node app (`server.js`, `lib/`, `routes/`, `public-src/`) at the repo
root, which remains the shipping, stable implementation. Nothing under `go/`
is wired into the Docker image, CI, or the running add-on yet.

## Status: Phase 3b (system domain + background polling + the two bootstrap-critical endpoints Phase 1g had deferred — every REST domain package from the migration plan now exists and is routed)

Phase 0 was scaffolding only. Phase 1a ported the first two foundational
packages everything else builds on. Phase 1b added a real, listening HTTP
server plus the `/api/events` SSE endpoint. Phase 1c (issue #901) ports
`routes/shots.js` — the first REST domain to go the full HTTP-request →
handler → `internal/db` → response path, establishing the pattern every
later domain package follows. Phase 1d (issue #901) ports the coffee
library domain on top of that same pattern:

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
- `internal/library` (Phase 1d, new) — the full coffee-library REST domain:
  `GET /api/library` (grinders enriched with a computed `wear` field),
  `GET /api/library/beans-info`, full CRUD + bag/frozen-portion lifecycle +
  known-grind + image upload/serve for beans, full CRUD + burr-wear/reset +
  image for grinders, full CRUD + image for baskets/puck screens, full CRUD
  + stock-deduct for milks, full CRUD for recipes, and the SSRF-guarded
  `GET /api/library/scan/:barcode` Open Food Facts proxy — `LibraryService`'s
  `getBeansInfo`/`computeGrinderWearStats`/`upsertKnownGrindSetting`/
  `setBeanImage`, `LibraryRepository`'s `getLibrary`/`saveLibrary`, and
  `lib/ssrf-guard.js`'s `assertPublicHost` DNS-rebinding guard, all ported.
  Deliberately NOT ported: the five one-time `migrateX()` startup
  migrations (data already migrated on any install this binary can run
  against — none turned out to be live business logic on inspection);
  `geocodeBean` (external geocoding provider, out of this phase's scope);
  and the maintenance-domain cross-call grinder delete would otherwise make
  (`internal/maintenance` is still Phase 0) — see `internal/library/doc.go`
  for exactly what each deferral does and doesn't change, including the one
  genuine (if minor) behavior gap: deleting a grinder through the Go server
  doesn't clean up its stale `maintenance` table row the way Node does.
- `cmd/server` — `main.go` opens the DB, loads/creates the API token, and
  wires the above into a real `net/http` server listening on port 8099
  (same port as Node), with the same middleware order server.js actually
  registers (security headers → rate limiter → token auth), verified by
  manually booting the binary and curling it end-to-end (401
  unauthenticated; working `/api/events` stream and the full `/api/shots/*`
  + `/api/library/*` surface with a valid token).

- `internal/machines` (Phase 1e, new) — the full machine-registry +
  machine-control + machine-profile domain: `GET|POST /api/machines`,
  `PUT|DELETE /api/machines/:id`, `.../default`, `.../test`; the #597
  Gaggiuino settings/control proxy (`/api/machine/{settings,
  settings/save, settings/:category, opmode, tare, service-test,
  profile/save, firmware/progress, firmware/update, firmware/version,
  live}`); and the machine-profile CRUD (`GET /api/machine/profiles`,
  `POST /api/machine/profile/set`, `GET|POST|PUT|DELETE
  /api/machine/profile[/:id]`). Ports `lib/machines/registry.js`
  (`Registry`), the `adapter-base.js` contract as a real Go interface with
  two implementations (`GaggiuinoAdapter`, `GaggiMateAdapter`), and both
  machines' WebSocket clients — Gaggiuino's binary protobuf protocol
  (`internal/machines/proto`, a from-scratch hand-written wire codec since
  no `.proto` sources exist anywhere for this firmware, cross-validated
  field-for-field against `lib/gaggiuino-proto.js`'s real
  `@protobuf-ts/runtime` output) over `nhooyr.io/websocket`, plus
  GaggiMate's JSON WebSocket protocol. `live.go`'s persistent Gaggiuino WS
  session caches every live sensor/system-state push, read (not
  re-polled) by Phase 1g's `internal/system.Poller` — this phase's own
  original design had `live.go` publishing those pushes directly onto
  `internal/sse.Hub` as a stand-in `EventLiveSnapshot` producer; Phase 1g
  reconciled that into the real `lib/poll.js`-equivalent producer (see
  `internal/system/doc.go`). The Gaggiuino REST API's settings
  bool-as-string quirk (some boolean fields are JSON
  *strings* `"true"`/`"false"`, not real booleans — see
  `internal/machines/doc.go`) is preserved byte-for-byte end to end by
  treating every settings payload as opaque bytes, never a typed struct.
  Deliberately NOT ported in this phase: `GET /api/machine/status` +
  `/api/preheat*` + `/api/live/data` (all four depend on `lib/poll.js`'s
  background polling loop — `system` domain; now ported in Phase 1g); the
  default machine's on-disk profiles-cache persistence; GaggiMate's binary shot-
  history parsing; MQTT live-data transport; and backup/restore — see
  `internal/machines/doc.go` for the full list and rationale. A standalone
  CLI, `cmd/gaggiuino-ws-probe`, exists so the protobuf decoder can be
  verified against a real machine's live traffic once one is reachable
  (no network access to real hardware was available while this package
  was built).
- `cmd/gaggiuino-ws-probe` — manual verification tool for
  `internal/machines/proto`'s decoder: connects to a real machine and
  dumps every decoded WS frame, or replays one recorded hex frame offline.
  Not part of the server binary or any test suite.

- `internal/orders` (Phase 1f, new) — the full barista-orders REST domain:
  menu CRUD, orders settings, queue ETA, milk stock, order placement +
  accept/complete/decline lifecycle, notify mapping, and stats. Ports
  `OrderService.js`'s `resolveMachineId`/`resolveBeanId`/`computeQueueEta`/
  lifecycle methods and `OrderRepository.js`'s DB access. Every path
  `glp-integration`'s `orders_api.py` proxy allowlists is covered and
  contract-tested (`handlers_test.go`'s `TestProxiedPaths_Answer200`), as
  is the `X-GLP-HA-User-ID` header's precedence over both the body field
  and the `mine` endpoint's query parameter (#547). `GetActiveBeans`/
  `GetActiveMilks`/`DeductMilkByName`/`ComputeBeanRemaining` — deferred out
  of Phase 1d's scope — are now ported too, in
  `internal/library/orders_support.go`. Deliberately NOT ported: the
  shop-open/shop-closed HA-notify broadcast `POST /api/orders/settings`
  triggers (needs the default machine's live runtime state from the
  still-unported `system` domain) — settings themselves persist correctly,
  only that notification side effect is missing — see
  `internal/orders/doc.go`.
- `internal/maintenance` (Phase 1f, new) — the full maintenance-tracking
  REST domain: per-task/per-grinder due tracking with thresholds, the
  maintenance log, and the `machineId=all` aggregate view. Ports
  `LibraryService.js`'s `computeMaintenanceStats`/
  `computeAllMachinesMaintenance` and `LibraryRepository.js`'s
  maintenance-table methods, split into their own package (Node keeps them
  in the library domain's files; this rewrite doesn't). Closes a Phase 1d
  gap: deleting a grinder now also removes its `grinder_{id}` maintenance
  row, wired from `internal/library` via a callback
  (`SetOnGrinderDeleted`) rather than a direct import, since this package
  already imports `internal/library` the other way around (grinder
  existence checks, grinder names).
- `internal/backup` (Phase 1f, new) — the full backup/restore REST domain:
  `GET`/`POST /api/backup` (legacy self-contained JSON export and the zip
  export the app's UI actually uses, both with optional section scoping
  and passphrase-encrypted secrets), and `POST /api/restore` (dry-run
  preview, per-section apply, zip or legacy-JSON body). Ports
  `lib/backup-crypto.js` (AES-256-GCM-scrypt) verbatim, uses Go's stdlib
  `archive/zip` instead of porting `lib/zip.js`'s hand-rolled DEFLATE/CRC32
  implementation (no behavior difference — same ZIP format), and closes
  two more cross-domain gaps flagged deferred by earlier phases:
  `internal/machines/registry.go`'s `RestoreMachines` (flagged in
  Phase 1e) and `internal/library`'s whole-entity restore sanitizers
  (`SanitizeBeanFields` et al., flagged in Phase 1d — now in
  `internal/library/restore_sanitize.go`). **One known, deliberate gap**:
  a real restore is NOT wrapped in one all-or-nothing transaction the way
  Node's is — each of the six backup sections (shots, maintenance, orders,
  machines, settings, secrets) writes atomically on its own, but a failure
  partway through a multi-section restore leaves earlier sections applied
  and later ones not, unlike Node. `routes/debug.js`'s
  `export-db`/`import-db` (raw SQLite file dump/restore) are explicitly
  NOT part of this domain — see `internal/backup/doc.go` for the full
  reasoning on both.
- `internal/ha` (Phase 1f, extended in Phase 1g) — ports `lib/ha.js`:
  `SendNotify`, `GetNotifyServices`, `GetPersons` (Phase 1f, orders domain),
  plus `GetSwitchState`, `CallHaService`, `GetHaLanguage` (Phase 1g, the
  system domain's power-check/ready-by-preheat needs). Degrades to a
  no-op/empty-result (or, for `CallHaService`, an error) when no
  `SUPERVISOR_TOKEN`/`GLP_HA_URL` is configured, exactly like the Node
  original — HA integration is optional.
- `internal/system` (Phase 1g, new, #901) — the last REST domain package
  from the migration plan, plus the background polling mechanism the other
  domains depend on for live machine data: `GET /api/machine/status`,
  `GET /api/live/data`, `GET /api/preheat`, `POST /api/preheat/ready-by`,
  `GET /api/version`, `POST /api/demo/{seed,end}`, and `lib/poll.js`'s 1s
  polling loop (`checkAndApplyMachinePower`/`backgroundHaCheck`,
  `startLivePolling`/`stopLivePolling`, the `#655` `machineReachable`
  powered-off-vs-idle-but-reachable distinction) plus `lib/preheat.js`'s
  `buildPreheatResponse`/`SetReadyByTarget`/the ready-by auto turn-on
  watcher. Reconciles Phase 1e's `internal/machines/live.go`, which
  published its own WS-session-cache snapshots directly onto the SSE hub
  as a stand-in before this phase existed — that package's `live.go` no
  longer publishes directly; `internal/system`'s `Poller` is now the sole
  `live-snapshot` SSE producer, reading the same WS cache through
  `machines.Adapter`'s `GetLiveSensorSnapshot`/`GetLiveSystemState` (see
  `internal/system/doc.go`'s "Reconciling with Phase 1e's live.go"
  section). Also closes `internal/orders`' shop-open/shop-closed
  HA-notify-broadcast deferral flagged in Phase 1f, wired via a
  `PreheatInfoFunc` callback (not a direct import, which would close a
  package cycle against this domain's own still-deferred
  `_checkPreheatNotify`). Phase 3b (#901) added `GET /api/token` and
  `GET /api/status`, found missing when verifying a standalone Go backend
  against a real `glp-integration` install: `GET /api/token` is the only
  way any consumer (glp-integration's `GlpAuth`, the installable PWA) ever
  obtains a working `X-GLP-Token`, and `GET /api/status` is
  glp-integration's discovery probe and every `GlpDataCoordinator` poll's
  first call — Phase 1g's own "not required to make the endpoints above
  correct" scope cut had missed that both are load-bearing for every real
  client, not just this phase's own six endpoints. `GET /api/status`'s
  `lastSync`/`syncRetryCount`/`lastSyncError` fields stay permanently
  null/0 in this Go port, same reason as the next paragraph.
  Deliberately NOT ported: `lib/sync.js` entirely (the shot-history sync
  engine — its own future phase, and the reason for the three always-null
  fields above), `lib/connectivity-stats.js`'s debug-log summary,
  `_checkPreheatNotify` (the barista "preheat ready" push notification —
  needs a read dependency on `internal/orders`' settings this phase's
  budget didn't cover), `lib/machines/options-adoption.js`'s
  `adoptOptionChanges()` (so `GET /api/status`'s
  `legacyMachineOptionsPending` is a documented always-false stub), and a
  handful of `routes/system.js` routes not in any phase's endpoint list
  (`GET`/`POST /api/switch(/toggle)`, `POST /api/sync`,
  `GET /api/openapi.json`, `GET /api/debug/machine`) — see
  `internal/system/doc.go`'s "Scope" section for the full reasoning on
  each.

Every REST domain package named in the original migration plan now exists
and routes the endpoints its phase brief scoped it to, including the two
bootstrap-critical endpoints (`GET /api/token`, `GET /api/status`) Phase 1g
had originally deferred — see `internal/system/doc.go` for the small
number of `routes/system.js` routes that remain unrouted by design, none
of them depended on by anything any phase has built. `go build ./...`,
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
    server/                main.go — HTTP bootstrap: db + auth + sse + shots + library + machines + orders + maintenance + backup + system wiring
    gaggiuino-ws-probe/     manual protobuf-decoder verification tool (not part of the server binary)
  internal/
    db/                    lib/db.js — schema + migrations
    auth/                  server.js's ingress-trust + token-auth
    ratelimit/              lib/middleware/rateLimit.js — app-level rate limiter
    sse/                   routes/sse.js — /api/events (implemented, Phase 1b)
    shots/                 routes/shots.js + ShotService/ShotRepository (implemented, Phase 1c)
    library/               routes/library/*.js + LibraryService (implemented, Phase 1d)
    machines/              routes/machines.js + machine-control.js + lib/machines/* (implemented, Phase 1e)
    machines/proto/         Gaggiuino's binary protobuf schema (implemented, Phase 1e)
    orders/                routes/orders.js + OrderService (implemented, Phase 1f, extended Phase 1g)
    maintenance/           routes/maintenance.js + LibraryService/LibraryRepository's maintenance-table methods (implemented, Phase 1f)
    backup/                routes/backup.js + lib/backup-crypto.js (implemented, Phase 1f)
    ha/                    lib/ha.js — SendNotify/GetNotifyServices/GetPersons/GetSwitchState/CallHaService/GetHaLanguage (implemented, Phase 1f, extended Phase 1g)
    system/                routes/system.js's token/status/live/preheat/version/demo endpoints + lib/poll.js + lib/preheat.js (implemented, Phase 1g; token/status added Phase 3b)
```

Every package under `internal/` is now implemented — see
`go/internal/system/doc.go` for the small, deliberate set of
`routes/system.js` routes it doesn't route.

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
