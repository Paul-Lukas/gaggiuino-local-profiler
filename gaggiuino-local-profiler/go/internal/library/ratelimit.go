package library

import (
	"sync"
	"time"
)

// rateLimiter ports lib/helpers.js's rateLimit(key, maxPerMinute): a fixed
// 60s window per key, reset (not slid) once it expires — distinct from
// internal/ratelimit's token-bucket app-level limiter (that one gates every
// request by socket address; this one additionally rate-limits specific
// library create/scan routes by `lib:<ip>`/`scan:<ip>` keys, exactly
// mirroring the Node original's own second, route-scoped limiter).
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*rlWindow
}

type rlWindow struct {
	t time.Time
	n int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windows: make(map[string]*rlWindow)}
}

// allow ports the `++e.n <= maxPerMinute` check.
func (rl *rateLimiter) allow(key string, maxPerMinute int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	e, ok := rl.windows[key]
	if !ok || now.Sub(e.t) > 60*time.Second {
		e = &rlWindow{t: now, n: 0}
		rl.windows[key] = e
	}
	e.n++
	return e.n <= maxPerMinute
}
