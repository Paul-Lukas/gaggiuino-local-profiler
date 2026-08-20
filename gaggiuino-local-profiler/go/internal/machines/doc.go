// Package machines is the Go port of the machine-registry and machine-
// control domain (#901, Phase 1e — the third and largest REST domain
// after shots/library, and the first to also run a background WebSocket
// client): routes/machines.js (registry CRUD + reachability probe),
// routes/machine-control.js (Gaggiuino settings/opmode/tare/service-test/
// firmware proxy), the machine-profile/live-status portion of
// routes/system.js, and the lib/machines/* adapter layer (Gaggiuino
// WebSocket/protobuf client, GaggiMate JSON WebSocket client, registry
// facade).
//
// # Layout
//
//   - model.go / theme_presets.go — Machine/MachineInput/Theme, ported from
//     registry.js's row shape + machineSchema + theme-presets.js.
//   - ssrf.go — assertMachineHost, the narrower loopback/link-local-only
//     SSRF guard (a real machine legitimately lives in RFC1918 space,
//     unlike internal/library's assertPublicHost).
//   - registry.go — the `machines` table CRUD (already created by
//     internal/db — see that package's schema), resolveMachine, and
//     BaseURLFor (consolidates the identical baseUrlFor() Node defines
//     twice, once per adapter).
//   - adapter.go — the Adapter interface (ports adapter-base.js's
//     documented per-machine-type contract, extended with the #597
//     settings-proxy methods) and per-machine-type dispatch.
//   - validation.go — profile-input types (phaseSchema et al.) and
//     ToWireProfile(), the Go port of gaggiuino-ws-client.js's
//     toWireProfile().
//   - proto/ — the reconstructed Gaggiuino binary protobuf schema (see its
//     own doc.go for the full story: no `.proto` sources exist anywhere,
//     hand-written Go structs + wire codec, cross-validated against
//     lib/gaggiuino-proto.js's real runtime output).
//   - ws.go / live.go — lib/gaggiuino-ws-client.js's short-lived-
//     connection-per-request client (profile CRUD, #597 commands) and
//     lib/gaggiuino-live-client.js's persistent auto-reconnecting session
//     (cached d_sensor_snap/d_sys_state pushes), both on nhooyr.io/websocket.
//   - gaggiuino_adapter.go / http.go — the Gaggiuino Adapter implementation:
//     REST calls (net/http) plus ws.go/live.go for what has no REST
//     equivalent.
//   - gaggimate_ws.go / gaggimate_profiles.go / gaggimate_adapter.go — the
//     GaggiMate Adapter implementation: a short-lived JSON WebSocket
//     client (ws-client.js's request()/waitForStatus() only — see
//     gaggimate_ws.go's header comment for what's deliberately not
//     ported) plus profile pass-throughs.
//   - firmware_check.go — the #620 GitHub-releases update-availability
//     check.
//   - handlers.go / handlers_registry.go / handlers_control.go /
//     handlers_profiles.go — the REST surface, split the same way
//     internal/library splits its handler files by sub-domain.
//
// # The settings bool-as-string quirk
//
// The Gaggiuino REST API's settings endpoints encode several boolean
// fields as the JSON *strings* `"true"`/`"false"` instead of real JSON
// booleans: boiler.brewDeltaState/dreamSteamState, display.lcdDarkMode,
// scales.forcePredictive/hwScalesEnabled/btScalesEnabled, and
// led.state/disco (verified against glp-integration's
// custom_components/gaggiuino_profiler/gaggiuino_bool.py, the consumer
// this quirk exists for — it coerces exactly this field list on read and
// re-encodes matching the original representation on write). Node's own
// gaggiuino/adapter.js never normalizes these — getSettings/updateSettings
// are thin axios passthroughs, forwarding whatever JSON shape the machine
// itself returns/expects.
//
// This package replicates that passthrough exactly by treating a settings
// payload as opaque bytes end-to-end, never as a typed Go struct with real
// bool fields: GetSettings/UpdateSettings both move json.RawMessage/raw
// []byte only (see http.go's httpGetBytes/httpPostBytes and
// gaggiuino_adapter.go's doc comments), and handlers_control.go's
// updateSettings handler forwards the exact client request bytes it read
// off the wire, not a JSON-decoded-then-re-encoded value. See
// gaggiuino_adapter_test.go's TestGaggiuinoAdapter_SettingsQuirkPassthrough
// and handlers_test.go's TestHandlers_SettingsQuirkPassthrough_EndToEnd
// for the tests that verify this field-by-field, at both the adapter and
// full-HTTP-handler layers.
//
// # 501 = adapter unsupported
//
// The settings/control proxy's 501="adapter unsupported" status code
// (requireSettingsProxySupport in routes/machine-control.js, returned when
// an adapter's capabilities().settingsProxy is false — only GaggiMate)
// is ported as requireSettingsProxySupport in handlers.go, used by every
// handler in handlers_control.go. requireProfileEditSupport (also 501,
// gated on capabilities().profileEdit) is the same pattern for the
// profile-write routes in handlers_profiles.go. This package has no
// "feature disabled" 404 case of its own — that pattern (isOrdersEnabled)
// belongs to the orders package; see go/internal/orders/doc.go.
//
// # What this phase deliberately does NOT port (and why)
//
//   - GET /api/machine/status, GET /api/preheat, POST /api/preheat/ready-by,
//     GET /api/live/data — all four depend on lib/poll.js's hard-single-
//     machine 1s background polling loop (defaultRuntime.machineStatus,
//     preheat scheduling, sync-after-brew triggers), which is `system`
//     domain and still a Phase 0 placeholder (go/internal/system). The
//     original task brief for this phase listed GET /api/machine/status
//     among this package's endpoints, inherited from an earlier explore
//     report that didn't distinguish it from the genuinely machine-
//     adapter-scoped GET /api/machine/live (which IS ported here, backed
//     by live.go's own WS session cache, not poll.js's). Porting the
//     polling loop itself is squarely internal/system's job.
//   - GET /api/machine/profiles' default-machine special case, which reads
//     defaultRuntime.machineStatus (the same poll.js cache) instead of
//     calling adapter.GetStatus() directly for the default machine only.
//     handlers_profiles.go's listMachineProfiles calls adapter.GetStatus()
//     unconditionally for every machine, default or not — functionally
//     equivalent (same response shape) but one extra round trip to the
//     machine for the default machine's profile list specifically, until
//     internal/system exists and this can wire into its cache instead.
//   - The default machine's on-disk profiles-cache persistence
//     (PROFILES_CACHE_FILE, survives a Node process restart). handlers.go's
//     profilesCache is in-memory only for every machine including the
//     default one — acceptable since this binary isn't wired into a
//     running add-on process yet (go/README.md); real for cutover, not for
//     this phase.
//   - lib/live-transport.js's MQTT live-data transport (an alternative to
//     the WebSocket path this package's live.go ports, toggled by a
//     Settings-page option, default-machine-only). Not requested by this
//     phase's scope and not reachable from any endpoint listed in it.
//   - lib/machines/gaggimate/history.js's index.bin/.slog binary shot-
//     history parsing (GaggiMateAdapter's GetLatestShotId/GetShot in
//     Node). These back lib/sync.js's background shot-history sync, not
//     any REST endpoint in this phase's scope — the shots-sync domain
//     itself (routes/machines.js's #729/#731 catch-up sync included, see
//     handlers_registry.go's header comment) doesn't run as a background
//     process in this Go binary yet either.
//   - ws-client.js's GaggiMateLiveClient (a third, persistent-connection
//     pattern, for lib/poll.js's multi-machine live-poll loop only) — see
//     live.go's header comment: every REST endpoint that would read its
//     cached data is gated by requireSettingsProxySupport, and GaggiMate
//     reports capabilities().settingsProxy == false, so it's unreachable
//     from this phase's REST surface regardless.
//   - lib/machines/registry.js's restoreMachines() and
//     options-adoption.js's post-restore reconciliation hook — both
//     routes/backup.js-triggered, backup domain, still Phase 0
//     (go/internal/backup) — same deferral internal/library made for its
//     own backup-only repository methods.
//
// # Protobuf verification status
//
// proto/messages.go's hand-written wire codec is cross-validated against
// lib/gaggiuino-proto.js's real @protobuf-ts/runtime output
// (proto/node_vectors_test.go) — genuine ground truth, since that's the
// same code talking to real machines in production today. It has NOT been
// verified against a live machine directly: no network access to real
// hardware was available while this package was built (go/RESEARCH.md's
// documented blocker). cmd/gaggiuino-ws-probe is the tool this package
// ships specifically so that step is a `go run` away once real hardware is
// reachable — see its own doc comment.
package machines
