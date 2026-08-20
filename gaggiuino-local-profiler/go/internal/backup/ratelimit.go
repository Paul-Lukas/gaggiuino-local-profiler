package backup

import (
	"sync"
	"time"
)

// keyedRateLimiter ports lib/helpers.js's rateLimit(key, maxPerMinute) —
// same duplication precedent internal/orders/ratelimit.go already set
// (see that file's doc comment for the full rationale: a fixed one-minute
// window per string key, distinct from internal/ratelimit's app-wide
// socket-address-only token bucket). POST /api/restore uses this at two
// different keys/limits: "restore:<ip>" (3/min, a real restore) and
// "restore-preview:<ip>" (30/min, dry-run traffic the modal fires on every
// section-checkbox toggle).
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
