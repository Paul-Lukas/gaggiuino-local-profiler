// Command server is the Go rewrite's minimal HTTP bootstrap (Phase 1b,
// issue #901): it wires internal/db, internal/auth, internal/ratelimit and
// internal/sse together into a real net/http server, in the same
// middleware order server.js actually registers its own (read that file,
// not a paraphrase of it — see the comment on the handler chain below).
//
// No REST domain routes are registered yet — only GET /api/events. Those
// come in Phase 1c. This binary is not wired into the Docker image, CI, or
// the running add-on; the Node app (server.js) remains the sole shipping
// entrypoint until the rollout plan in go/README.md says otherwise.
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/ratelimit"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/sse"
)

// defaultPort matches lib/constants.js's DEFAULT_PORT (8099) — the port the
// Node app listens on today, confirmed against config.yaml's exposed add-on
// port. Overridable via GLP_PORT for local/dev runs of this binary outside
// the add-on container, same pattern as dbPath/tokenPath below.
const defaultPort = "8099"

func main() {
	dbPath := getEnv("GLP_DB_PATH", db.DefaultPath)
	tokenPath := getEnv("GLP_TOKEN_FILE", auth.DefaultTokenFile)
	port := getEnv("GLP_PORT", defaultPort)
	rateLimitWindow := time.Duration(getEnvNumber("GLP_RATE_LIMIT_WINDOW_MS", float64(ratelimit.DefaultWindow/time.Millisecond))) * time.Millisecond
	rateLimitMax := int(getEnvNumber("GLP_RATE_LIMIT_MAX", float64(ratelimit.DefaultMax)))

	sqlDB, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("opening database at %s: %v", dbPath, err)
	}
	defer sqlDB.Close()

	token, err := auth.LoadOrCreateToken(tokenPath)
	if err != nil {
		log.Fatalf("loading API token from %s: %v", tokenPath, err)
	}

	hub := sse.NewHub()
	sseHandler := &sse.Handler{Hub: hub}
	// Prime is deliberately left nil: the sync-progress/preheat/live-
	// snapshot state it would read (lib/state.js, lib/preheat.js,
	// lib/poll.js) belongs to Phase 1c's domain packages, which don't
	// exist yet — see internal/sse/doc.go.

	mux := http.NewServeMux()
	mux.Handle("/api/events", sseHandler)

	limiter := ratelimit.New(rateLimitWindow, rateLimitMax)

	// server.js's ACTUAL app.use() order — security headers (lines ~83-98),
	// then the app-level rate limiter (line 104, deliberately ahead of auth
	// so it also caps unauthenticated login/token-probing traffic, per
	// lib/middleware/rateLimit.js's own comment), then token auth
	// (lines ~144-173). Read from the innermost handler outward, this chain
	// applies auth first, rate-limit second, security headers last, which
	// is the correct nesting to make requests experience them in that
	// server.js order.
	//
	// server.js's body-parser step (lines ~178-193) has no Go equivalent to
	// slot in here: net/http reads a request body lazily per-handler, not
	// through a chained global middleware, so there is nothing to add yet.
	// Phase 1c's handlers will each bound their own request body size
	// per-route the way routes/backup.js's /api/restore and
	// routes/debug.js's /api/debug/import-db use route-scoped
	// express.json()/express.raw() limits today.
	handler := auth.SecurityHeaders(
		limiter.Middleware(
			auth.RequireToken(token)(mux),
		),
	)

	addr := ":" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listening on %s: %v", addr, err)
	}

	log.Printf("GLP Go server listening on port %s", port)
	srv := &http.Server{Handler: handler}
	if err := srv.Serve(tcpNoDelayListener{ln}); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// getEnvNumber ports lib/middleware/rateLimit.js's
// `Number(process.env.X) || default` pattern for GLP_RATE_LIMIT_WINDOW_MS/
// GLP_RATE_LIMIT_MAX: an unset env var, one that fails to parse as a number
// (JS's Number() returns NaN, which is falsy), or one that parses to 0
// (also falsy in JS) all fall back to def — matching the Node original's
// behavior exactly, including that a literal "0" override is treated the
// same as no override.
func getEnvNumber(name string, def float64) float64 {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || n == 0 {
		return def
	}
	return n
}

// tcpNoDelayListener explicitly disables Nagle's algorithm on every
// accepted connection. routes/sse.js's #740 fix (res.socket.setNoDelay(true))
// has no real equivalent to port here: Go's net.TCPConn already defaults
// NoDelay to true for every connection Go's own net package creates (see
// net.TCPConn.SetNoDelay's doc comment) — Node's net.Socket defaults the
// other way, which is the only reason that explicit call exists there. This
// wrapper is defense-in-depth that makes the guarantee explicit at the
// listener level for every connection this process accepts, rather than a
// port of Node's per-connection workaround (see internal/sse/doc.go).
type tcpNoDelayListener struct{ net.Listener }

func (l tcpNoDelayListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return conn, err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	return conn, nil
}
