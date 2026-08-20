// Package machines will hold the Go port of the machine-registry and
// machine-control domain: routes/machines.js (CRUD + reachability probe),
// routes/machine-control.js (Gaggiuino settings/opmode/tare/service-test/
// firmware proxy), routes/system.js's /api/machine/* handlers, and the
// lib/machines/* adapter layer (Gaggiuino WebSocket/protobuf client,
// GaggiMate adapter, registry.resolveMachine convention).
//
// The 404="feature disabled" vs. 501="adapter unsupported" status-code
// semantics for the settings/control proxy must be preserved exactly — see
// the migration plan's Sicherheits-Parität section at
// ~/.claude/plans/folgendes-m-chte-ich-als-shimmying-hartmanis.md. The
// Gaggiuino firmware protobuf schema this package's WS client will need is
// tracked as a separate research spike — see go/RESEARCH.md.
//
// Phase 0 placeholder only. No implementation yet.
package machines
