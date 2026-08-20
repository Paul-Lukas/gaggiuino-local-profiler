// Package machines will hold the Go port of the machine-registry and
// machine-control domain: routes/machines.js (CRUD + reachability probe),
// routes/machine-control.js (Gaggiuino settings/opmode/tare/service-test/
// firmware proxy), routes/system.js's /api/machine/* handlers, and the
// lib/machines/* adapter layer (Gaggiuino WebSocket/protobuf client,
// GaggiMate adapter, registry.resolveMachine convention).
//
// The settings/control proxy's 501="adapter unsupported" status code
// (requireSettingsProxySupport in routes/machine-control.js, returned when
// an adapter's capabilities().settingsProxy is false, e.g. GaggiMate) must
// be preserved exactly. This package has no "feature disabled" 404 case of
// its own — that pattern (isOrdersEnabled) belongs to the orders package;
// see go/internal/orders/doc.go. The Gaggiuino firmware protobuf schema
// this package's WS client will need is tracked as a separate research
// spike — see go/RESEARCH.md.
//
// Phase 0 placeholder only. No implementation yet.
package machines
