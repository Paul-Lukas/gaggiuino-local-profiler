// Package auth will hold the Go port of the app's trust/auth model:
// server.js's isFromSupervisor/isIngressRequest ingress-trust checks,
// isTokenValid's constant-time X-GLP-Token comparison (crypto.timingSafeEqual
// in Node, crypto/subtle.ConstantTimeCompare in Go), the security-header
// middleware, and lib/middleware/rateLimit.js's per-socket-address bucket
// logic.
//
// Phase 0 placeholder only. This model must be replicated 1:1, not
// "sinngemäß nachgebaut" — see the Sicherheits-Parität section of the
// migration plan at
// ~/.claude/plans/folgendes-m-chte-ich-als-shimmying-hartmanis.md.
// No implementation yet.
package auth
