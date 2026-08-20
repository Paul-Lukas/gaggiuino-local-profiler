// Package sse will hold the Go port of routes/sse.js — the single
// /api/events Server-Sent Events endpoint multiplexing sync-progress,
// sync-complete, live-snapshot and preheat-update pushes over one
// connection.
//
// The documented HA-Ingress-buffering workarounds (leading 2048-byte padding
// comment, X-Accel-Buffering: no, TCP_NODELAY via setNoDelay, 20s :ping
// keepalive) must be exactly replicated, not approximated — see
// project_glp_sse_ingress_nginx_buffering in the operator's memory for why:
// this broke Live View in production once already.
//
// The ?token= query-param auth fallback for EventSource does NOT live in
// routes/sse.js — routes/sse.js has no auth logic of its own. It's a
// special case in server.js's global auth middleware (checked only when
// req.path === '/api/events'), which this package's route registration
// must go through unchanged rather than reimplementing.
//
// Phase 0 placeholder only. No implementation yet.
package sse
