// Package sse will hold the Go port of routes/sse.js — the single
// /api/events Server-Sent Events endpoint multiplexing sync-progress,
// sync-complete, live-snapshot and preheat-update pushes over one
// connection.
//
// The documented HA-Ingress-buffering workarounds (leading 2048-byte padding
// comment, X-Accel-Buffering: no, TCP_NODELAY via setNoDelay, 20s :ping
// keepalive, query-param token for EventSource) must be exactly replicated,
// not approximated — see project_glp_sse_ingress_nginx_buffering in the
// operator's memory and the migration plan at
// ~/.claude/plans/folgendes-m-chte-ich-als-shimmying-hartmanis.md for why:
// this broke Live View in production once already.
//
// Phase 0 placeholder only. No implementation yet.
package sse
