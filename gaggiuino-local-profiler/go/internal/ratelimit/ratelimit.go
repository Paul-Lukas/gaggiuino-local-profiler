package ratelimit

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
)

// DefaultWindow and DefaultMax port lib/middleware/rateLimit.js's
// RATE_LIMIT_WINDOW_MS/RATE_LIMIT_MAX defaults (60s / 600 requests).
const (
	DefaultWindow = 60 * time.Second
	DefaultMax    = 600
)

// Limiter holds one token bucket per client key, created lazily on first
// use and never evicted — bounded in practice by the number of distinct
// socket addresses (LAN clients + the single shared Supervisor-Ingress
// address) that ever reach this process, same unbounded-but-small-in-
// practice memory profile as express-rate-limit's default in-memory store.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	rate    rate.Limit
	burst   int
}

// New creates a Limiter allowing up to max requests per window, per key.
func New(window time.Duration, max int) *Limiter {
	return &Limiter{
		buckets: make(map[string]*rate.Limiter),
		rate:    rate.Every(window / time.Duration(max)),
		burst:   max,
	}
}

func (l *Limiter) bucket(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = rate.NewLimiter(l.rate, l.burst)
		l.buckets[key] = b
	}
	return b
}

// Allow reports whether one more request for key is permitted right now,
// consuming one token from its bucket if so.
func (l *Limiter) Allow(key string) bool {
	return l.bucket(key).Allow()
}

// Middleware ports server.js's app.use(createApiRateLimiter()) registration:
// same key (auth.RemoteIP — raw socket address only, see doc.go), same
// /assets/* exemption, same 429 JSON error shape.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		if !l.Allow(auth.RemoteIP(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "Too many requests"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
