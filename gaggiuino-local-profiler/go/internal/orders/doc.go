// Package orders will hold the Go port of the barista-orders domain:
// routes/orders.js (menu, orders CRUD/lifecycle, milk stock, queue ETA,
// stats, notify mapping, shop-open/closed broadcast) and
// lib/services/OrderService.js's queue-ETA and order-lifecycle logic.
//
// glp-integration's orders_api.py and glp-order-card are the binding
// consumers of this contract — field names like machineReachable, isLive,
// apiToken, targetAt and the bool-as-string quirks in
// /api/machine/settings/{category} (machines package, not this one) must
// stay exact. See the migration plan at
// ~/.claude/plans/folgendes-m-chte-ich-als-shimmying-hartmanis.md.
//
// Phase 0 placeholder only. No implementation yet.
package orders
