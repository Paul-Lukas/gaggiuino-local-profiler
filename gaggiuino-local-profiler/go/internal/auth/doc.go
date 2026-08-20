// Package auth is the Go port of the app's ingress-trust and token-auth
// model:
//
//   - IsFromSupervisor/IsIngressRequest port server.js's
//     isFromSupervisor/isIngressRequest — trusting the HA Supervisor's
//     172.30.0.0/16 add-on network (plus loopback), and requiring both a
//     Supervisor-network source IP and an X-Ingress-Path header with the
//     HAIngressPrefix prefix before a request counts as genuine Ingress.
//   - IsTokenValid ports isTokenValid's constant-time X-GLP-Token comparison
//     (crypto.timingSafeEqual in Node, crypto/subtle.ConstantTimeCompare
//     here), including the Node original's same-length-required early exit.
//   - LoadOrCreateToken ports loadOrCreateApiToken's read-or-generate-and
//     -persist logic against /data/api_token.txt (DefaultTokenFile),
//     using the same tmp-file-then-rename atomic write as
//     lib/helpers.js's writeFileSafe.
//   - SecurityHeaders ports the security-header net/http middleware block
//     near the top of server.js — identical header names and values.
//
// This model is replicated 1:1, not "sinngemäß nachgebaut" — see
// go/README.md for the migration's security-parity requirement.
//
// lib/middleware/rateLimit.js's per-socket-address rate-limit bucket logic
// is NOT part of this package (Phase 1a scope was ingress-trust + token-auth
// + security headers only); it belongs here or in a sibling package once
// the HTTP server wiring that would actually exercise it exists.
package auth
