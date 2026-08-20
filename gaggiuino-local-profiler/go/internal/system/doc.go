// Package system will hold the Go port of routes/system.js's general
// status/live/preheat endpoints (GET /api/status, /api/token, /api/switch,
// /api/preheat, POST /api/preheat/ready-by, GET /api/live/data, GET
// /api/version) plus lib/poll.js's polling/reachability state machine
// (buildLiveDataResponse, buildPreheatResponse) that backs them.
//
// glp-integration's orders_api.py and glp-order-card are binding consumers
// of this contract's field names — machineReachable (#655: distinguishes a
// powered-off machine from an idle-but-reachable one), isLive, apiToken,
// and targetAt all belong here, not to the orders package, even though
// orders' own consumers also read them. See openapi.yaml's System tag
// (Status, LiveData, PreheatStatus schemas) for the frozen contract.
//
// Phase 0 placeholder only. No implementation yet.
package system
