// Package system is the Go port of Phase 1g (#901): the last REST domain
// from the migration plan, plus the background polling mechanism the other
// domains depend on for live machine data.
//
// # Scope
//
// Ported: GET /api/machine/status, GET /api/live/data, GET /api/preheat,
// POST /api/preheat/ready-by, GET /api/version, POST /api/demo/seed, POST
// /api/demo/end, and lib/poll.js's background polling loop
// (startLivePolling/stopLivePolling/pollLive/pollViaGaggiuinoStatus,
// checkAndApplyMachinePower/backgroundHaCheck) plus lib/preheat.js's
// buildPreheatResponse/setReadyByTarget/isTempStable/save-load state and
// the ready-by auto turn-on watcher (_checkReadyByPreheat).
//
// Explicitly out of this phase's scope (not in the task brief's endpoint
// list, and NOT required to make the endpoints above correct) —
// GET /api/status, GET /api/token, GET/POST /api/switch(/toggle),
// POST /api/sync, GET /api/openapi.json, GET /api/menu (that one is
// internal/orders' GetMenu already), and the H2 debug-only
// GET /api/debug/machine. These remain unrouted; a future pass can add
// them without touching anything this phase built, since none of the
// endpoints ported here depend on them. go/README.md's status section
// reflects this precisely — "every REST domain package now exists and
// routes the endpoints its phase brief scoped it to," not "every single
// route in routes/system.js is ported."
//
// # File layout
//
//	runtime.go   RuntimeState — lib/machine-runtime-state.js's
//	             MachineRuntimeState, mutex-guarded (Node's single-
//	             threaded event loop needed no lock; Go's 1s/30s/30s
//	             tickers plus concurrent HTTP reads do).
//	derive.go    deriveMachineState/isStillWarm — lib/machine-state.js,
//	             pure functions, unit-tested without any I/O.
//	poll.go      Poller — lib/poll.js's polling loop + checkAndApplyMachinePower/
//	             backgroundHaCheck, plus lib/state.js's module-level
//	             fields this package needs (pollGlobalState).
//	preheat.go   lib/preheat.js: buildPreheatResponse, SetReadyByTarget,
//	             checkReadyByPreheat, save/load preheat_state.json.
//	options.go   loadPreheatMinutes() — a narrow options.json read, same
//	             pattern as internal/orders/options.go's isOrdersEnabled().
//	version.go   lib/version-check.js — GET /api/version's GitHub-release
//	             check.
//	demo.go      lib/services/DemoService.js + lib/demo-seed.js — POST
//	             /api/demo/{seed,end}.
//	handlers.go  routes/system.js's REST surface for everything above.
//
// # Reconciling with Phase 1e's live.go
//
// Phase 1e's internal/machines/live.go published every WS
// sensor-snap/sys-state push directly onto the shared internal/sse.Hub as
// EventLiveSnapshot, explicitly as a stand-in ("closing the loop the task
// brief asked for... Reconciling the two into one payload shape is
// system-domain work, out of this package's scope" — see that file's own
// header comment as it stood before this phase). That payload shape
// ({machineHost, sensorSnap} / {machineHost, sysState}) never matched
// openapi.yaml's LiveData schema (isLive/profileName/datapoints/seq/
// machineReachable) the live-snapshot SSE event and GET /api/live/data are
// both bound to — and Node's own architecture agrees: only lib/poll.js's
// emitLiveSnapshot() ever publishes LIVE_SNAPSHOT there; the WS client
// (lib/gaggiuino-live-client.js) only ever updates a cache lib/poll.js
// reads from via lib/live-transport.js, never publishes itself.
//
// This phase removes machines/live.go's direct Hub.Publish calls (see its
// updated header comment) and makes this package's Poller.emitLiveSnapshot
// the sole live-snapshot producer, reading the same WS cache through
// machines.Adapter's GetLiveSensorSnapshot/GetLiveSystemState — exactly
// Node's live-transport.js dispatch seam, minus the MQTT branch (see
// poll.go's header comment on what else lib/poll.js/lib/live-transport.js
// this package does not port).
//
// One deliberate simplification versus Node: #708's optimization (an
// immediate SSE push the instant a fresh WS/MQTT sample arrives, via an
// event-emitter bridge, on top of the 1s poll tick) is NOT ported — every
// live-snapshot push here is tick-driven only, adding up to ~1s of extra
// latency before a fresh sensor reading reaches an open SSE connection.
// The #655 correctness fix this event-bridge sits on top of (distinguishing
// a powered-off machine from an idle-but-reachable one via
// machineReachable) IS fully ported and is the one that actually matters
// for glp-integration/glp-order-card's correctness — #708 is pure latency
// polish. Wiring the bridge would mean exposing an event stream from
// internal/machines' live client, which doesn't exist yet; tracked as a
// follow-up, not attempted in this already-large phase.
//
// # Deliberately not ported (and why)
//
//   - lib/sync.js entirely — syncShots()/syncAfterBrew()/scheduleNextSync()/
//     fetchMachineVersion(). Three call sites in lib/poll.js reference it:
//     the #725 reachability-recovery catch-up sync, the brew-finished
//     setTimeout(syncAfterBrew, 3000), and backgroundHaCheck's
//     `if (!cachedMachineVersion) fetchMachineVersion()` fallback (this
//     package's own pollViaGaggiuinoStatus already opportunistically
//     captures cachedMachineVersion from every successful status poll, the
//     same field Node's inline capture in lib/poll.js also fills — Node's
//     fetchMachineVersion is a *fallback path* for when polling itself
//     isn't running, e.g. switch off). None of them are reachable from any
//     endpoint this phase ports; the shot-history sync engine is its own
//     future phase.
//   - lib/connectivity-stats.js's rolling-window debug-log summary
//     (recordConnectivity/summarizeConnectivity) — pure logging
//     diagnostics, not part of any response contract.
//   - lib/live-transport.js's MQTT branch — already out of scope per
//     internal/machines/doc.go (Phase 1e); this package's poll.go calls
//     machines.Adapter.GetLiveSensorSnapshot/GetLiveSystemState directly,
//     which is always the WS-backed cache (GaggiuinoAdapter), matching
//     live-transport.js's behavior for every install that hasn't opted
//     into the MQTT Settings toggle.
//   - lib/preheat.js's _checkPreheatNotify (the barista "preheat ready" HA
//     push, gated by orders settings' notify_preheat_ready/
//     baristaNotifyService plus lib/notify-i18n.js's localized text) — see
//     preheat.go's header comment: wiring it needs a read dependency on
//     internal/orders' settings, and internal/orders already depends on
//     this package (see below) for its own shop-broadcast — adding the
//     reverse dependency too, in the same phase, would need a second round
//     of callback plumbing this phase's budget didn't cover. Tracked as a
//     follow-up alongside GET /api/status/GET /api/switch above.
//   - GET /api/status, GET /api/token, GET/POST /api/switch(/toggle),
//     POST /api/sync, GET /api/openapi.json, GET /api/debug/machine — see
//     "Scope" above.
//
// # internal/orders' shop-broadcast (closed in this phase)
//
// internal/orders/doc.go flagged its POST /api/orders/settings shop-open/
// shop-closed HA-notify broadcast as deferred pending "the default
// machine's live runtime state from the still-unported system domain."
// That dependency is now resolvable: internal/orders/handlers.go takes a
// PreheatInfoFunc callback (not a direct import of this package, which
// would close a cycle — this package's own preheat-ready-notify would need
// to import internal/orders right back for its settings, see above) that
// cmd/server wires to Poller.PreheatInfo, closing that gap. See
// internal/orders/handlers.go's own comment on _broadcastShopState for the
// port itself.
package system
