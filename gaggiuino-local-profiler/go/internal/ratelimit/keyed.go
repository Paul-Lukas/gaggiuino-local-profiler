package ratelimit

import (
	"sync"
	"time"
)

// KeyedLimiter ports lib/helpers.js's rateLimit(key, maxPerMinute): a fixed
// one-minute window counter per arbitrary string key, distinct from
// Limiter's app-wide, socket-address-only token bucket above. Callers pick
// their own key prefix/scope (e.g. orders' "orders:<ip>" for POST
// /api/orders, backup's "restore:<ip>"/"restore-preview:<ip>" for
// POST /api/restore) and their own maxPerMinute per call to Allow — one
// KeyedLimiter instance is shared across every such feature-level limit a
// package needs. Originally duplicated verbatim between
// internal/orders/ratelimit.go and internal/backup/ratelimit.go (#901 code
// review); consolidated here since both domains need the identical
// behavior. Entries older than 2 windows are swept lazily on Allow rather
// than a background ticker (Node's setInterval(...,120_000) has no direct
// equivalent worth adding a goroutine for at this scale).
type KeyedLimiter struct {
	mu      sync.Mutex
	windows map[string]*keyedWindow
}

type keyedWindow struct {
	start time.Time
	count int
}

// NewKeyed creates an empty KeyedLimiter.
func NewKeyed() *KeyedLimiter {
	return &KeyedLimiter{windows: make(map[string]*keyedWindow)}
}

// Allow ports rateLimit(key, maxPerMinute): increments key's counter,
// resetting it if more than 60s have elapsed since the window started, and
// reports whether the incremented count is still within maxPerMinute.
func (l *KeyedLimiter) Allow(key string, maxPerMinute int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) > time.Minute {
		w = &keyedWindow{start: now}
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
