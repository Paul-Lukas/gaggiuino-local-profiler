package orders

import (
	"sync"
	"time"
)

// keyedRateLimiter ports lib/helpers.js's rateLimit(key, maxPerMinute): a
// fixed one-minute window counter per arbitrary string key (unlike
// internal/ratelimit's token-bucket Limiter, which is keyed only by socket
// address for the app-wide backstop — this is the per-feature limiter
// routes/orders.js's POST /api/orders uses, keyed "orders:<ip>"). Entries
// older than 2 windows are swept lazily on Allow rather than a background
// ticker (Node's setInterval(...,120_000) has no direct equivalent worth
// adding a goroutine for at this scale).
type keyedRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*rlWindow
}

type rlWindow struct {
	start time.Time
	count int
}

func newKeyedRateLimiter() *keyedRateLimiter {
	return &keyedRateLimiter{windows: make(map[string]*rlWindow)}
}

// Allow ports rateLimit(key, maxPerMinute): increments key's counter,
// resetting it if more than 60s have elapsed since the window started, and
// reports whether the incremented count is still within maxPerMinute.
func (l *keyedRateLimiter) Allow(key string, maxPerMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) > time.Minute {
		w = &rlWindow{start: now}
		l.windows[key] = w
	}
	w.count++
	if len(l.windows) > 10000 {
		cutoff := now.Add(-2 * time.Minute)
		for k, v := range l.windows {
			if v.start.Before(cutoff) {
				delete(l.windows, k)
			}
		}
	}
	return w.count <= maxPerMinute
}
