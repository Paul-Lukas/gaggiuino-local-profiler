// Package orders will hold the Go port of the barista-orders domain:
// routes/orders.js (menu, orders CRUD/lifecycle, milk stock, queue ETA,
// stats, notify mapping, shop-open/closed broadcast) and
// lib/services/OrderService.js's queue-ETA and order-lifecycle logic.
//
// glp-integration's orders_api.py and glp-order-card are the binding
// consumers of this contract — see openapi.yaml's Orders tag for the fields
// (order status enum, queue ETA, milk stock counts, notify mapping, etc.)
// that must stay exact. Machine/live-status fields like machineReachable,
// isLive, apiToken and targetAt belong to the system package's contract,
// not this one — see go/internal/system/doc.go. The isOrdersEnabled 404
// ("orders feature not enabled") is this package's own feature-disabled
// gate, distinct from the settings-proxy's 501 in the machines package —
// see go/internal/machines/doc.go.
//
// Phase 0 placeholder only. No implementation yet.
package orders
