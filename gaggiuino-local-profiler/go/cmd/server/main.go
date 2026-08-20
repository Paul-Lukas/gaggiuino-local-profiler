// Command server is the Go rewrite's HTTP bootstrap: it wires internal/db,
// internal/auth, internal/ratelimit, internal/sse, internal/shots (Phase
// 1c), internal/library (Phase 1d), internal/machines (Phase 1e),
// internal/orders, internal/maintenance, internal/backup, internal/ha
// (Phase 1f), and internal/system (Phase 1g, issue #901) together into a
// real net/http server, in the same middleware order server.js actually
// registers its own (read that file, not a paraphrase of it — see the
// comment on the handler chain below).
//
// Every REST domain package the original Migrationsplan named now exists
// and is registered: GET /api/events (Phase 1b), /shots.json + /api/shots/*
// (Phase 1c), /api/library/* (Phase 1d), the machine-registry +
// machine-control + machine-profile domain (Phase 1e), /api/orders/*,
// /api/maintenance/*, GET/POST /api/backup + POST /api/restore (Phase 1f),
// and internal/system's GET /api/machine/status, GET /api/live/data,
// GET/POST /api/preheat*, GET /api/version, POST /api/demo/{seed,end}
// plus the background polling loop that backs them (Phase 1g). A handful
// of routes/system.js routes remain unrouted by design — see
// go/internal/system/doc.go's "Scope" section for exactly which and why
// (none of them are depended on by anything this phase ported). This
// binary is not wired into the Docker image, CI, or the running add-on;
// the Node app (server.js) remains the sole shipping entrypoint until the
// rollout plan in go/README.md says otherwise.
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/backup"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/ha"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/maintenance"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/orders"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/ratelimit"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/sse"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/system"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/web"
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
	// Prime is wired below, once poller exists — routes/sse.js primes a
	// newly-connected client with the current preheat-update/live-snapshot
	// snapshot (buildPreheatResponse()/buildLiveDataResponse(), both
	// synchronous reads) before subscribing it to the Hub. The
	// sync-progress priming loop Node also does has no Go equivalent yet
	// (state.syncProgress isn't ported — see internal/system/doc.go).

	mux := http.NewServeMux()
	mux.Handle("/api/events", sseHandler)

	shotsRepo := shots.NewRepository(sqlDB)
	shotsHandlers := shots.NewHandlers(shotsRepo)
	shotsHandlers.RegisterRoutes(mux)

	// Phase 2a (#901): the Go frontend foundation — GET /shots plus its two
	// htmx trash/restore actions, built on the same shots.Service the JSON
	// API above uses. Not yet reachable in production (this binary isn't
	// wired into the Docker image/CI — see go/README.md). Registered
	// outside /api/ so the read-only GET falls through auth.RequireToken's
	// static-asset bypass; the two POST actions do NOT get that bypass
	// (RequireToken scopes it to GET/HEAD) and require the same
	// token/Ingress trust the JSON API does — see internal/web/doc.go's
	// "Auth model" section.
	webHandlers := web.NewHandlers(shots.NewService(shotsRepo))
	webHandlers.RegisterRoutes(mux)

	libRepo := library.NewRepository(sqlDB)
	libraryHandlers := library.NewHandlers(libRepo, shotsRepo)
	libraryHandlers.RegisterRoutes(mux)

	// Phase 2b (#901): the Library domain's Go frontend pages — Beans (plus
	// its one htmx write action, toggle-active) and read-only lists for
	// Grinders/Baskets/Puck Screens/Milks/Recipes, built on the same
	// library.Repository/shots.Repository the JSON API above uses. Same
	// registration-outside-/api/ auth model as webHandlers above — see
	// internal/web/doc.go's "Auth model" section.
	webLibraryHandlers := web.NewLibraryHandlers(libRepo, shotsRepo)
	webLibraryHandlers.RegisterRoutes(mux)

	registry := machines.NewRegistry(sqlDB)
	machinesHandlers := machines.NewHandlers(registry, hub)
	machinesHandlers.RegisterRoutes(mux)

	haClient := ha.NewClientFromEnv()
	ordersRepo := orders.NewRepository(sqlDB)
	ordersHandlers := orders.NewHandlers(ordersRepo, shotsRepo, libRepo, registry, haClient)
	ordersHandlers.RegisterRoutes(mux)

	// Phase 2d (#901): the Orders domain's Go frontend pages — the barista
	// queue (GET /orders, with accept/complete/decline htmx actions) and the
	// customer ordering form (GET /menu, with its one write action) — built
	// on the same orders.Repository/Service dependencies the JSON API above
	// uses, via its own *orders.Service instance (see
	// internal/web/handlers_orders.go's own doc comment for why a second
	// instance, not ordersHandlers' internal one). Same
	// registration-outside-/api/ auth model as every other web.*Handlers.
	webOrdersHandlers := web.NewOrdersHandlers(ordersRepo, shotsRepo, libRepo, registry, haClient)
	webOrdersHandlers.RegisterRoutes(mux)

	// Phase 1g (#901): the background polling loop that backs
	// GET /api/machine/status, GET /api/live/data, GET/POST /api/preheat*,
	// and the live-snapshot/preheat-update SSE events — see
	// internal/system/doc.go for the full scope and what it deliberately
	// doesn't port. poller.Start launches its own 30s HA-check/preheat
	// tickers bound to ctx; the process runs until the OS kills it (no
	// graceful-shutdown signal handling exists in this binary yet, same as
	// every other domain package here), so ctx is background — cancelling
	// it would only matter for a future clean-shutdown path.
	poller := system.NewPoller(registry, machinesHandlers, hub, haClient)
	poller.Start(context.Background())
	// Closes internal/orders' shop-broadcast deferral (see
	// internal/orders/doc.go and internal/system/doc.go's "internal/orders'
	// shop-broadcast" section for why this is a callback, not an import).
	ordersHandlers.SetPreheatInfoProvider(poller.PreheatInfo)

	demoService := system.NewDemoService(sqlDB, shotsRepo, libRepo)
	systemHandlers := system.NewHandlers(poller, demoService, token)
	systemHandlers.RegisterRoutes(mux)

	// Phase 2c (#901): the Machines domain's Go frontend pages — the
	// machines list (default/reachable badges, set-default and delete htmx
	// actions) plus GET /live, the live shot chart page whose actual chart
	// is a standalone vanilla-JS SSE consumer (static/live.js), not an htmx
	// fragment page — see internal/web/handlers_machines.go and
	// templates/live.templ's own doc comments. poller is passed so the
	// machines list can show the default machine's live reachable status
	// (internal/system.Poller.StatusInfo) and GET /live can name the
	// current default machine. Same registration-outside-/api/ auth model
	// as every other web.*Handlers above.
	webMachinesHandlers := web.NewMachinesHandlers(registry, poller)
	webMachinesHandlers.RegisterRoutes(mux)

	// routes/sse.js primes a newly-connected client with the current
	// preheat/live snapshot before subscribing it to future pushes — see
	// the Prime field's doc comment above.
	sseHandler.Prime = func() []sse.Event {
		return []sse.Event{
			{Type: sse.EventPreheatUpdate, Data: poller.PreheatStatus()},
			{Type: sse.EventLiveSnapshot, Data: poller.LiveData()},
		}
	}

	maintenanceRepo := maintenance.NewRepository(sqlDB, libRepo)
	maintenanceHandlers := maintenance.NewHandlers(maintenanceRepo, shotsRepo, libRepo, registry)
	maintenanceHandlers.RegisterRoutes(mux)
	// #901 (Phase 1f): closes the Phase 1d gap flagged in
	// internal/library/doc.go — deleting a grinder now also removes its
	// `grinder_{id}` maintenance-table row, via a callback (not a direct
	// import) since internal/maintenance already imports internal/library.
	libraryHandlers.SetOnGrinderDeleted(maintenanceRepo.DeleteGrinderTask)

	backupHandlers := backup.NewHandlers(backup.Dependencies{
		DB:              sqlDB,
		ShotsRepo:       shotsRepo,
		LibRepo:         libRepo,
		OrdersRepo:      ordersRepo,
		MaintenanceRepo: maintenanceRepo,
		Registry:        registry,
		// Token/TokenFile: a restored API token is persisted to
		// tokenPath but does NOT take effect in this already-running
		// process — see backup.Dependencies.Token's doc comment.
		Token:     token,
		TokenFile: tokenPath,
	})
	backupHandlers.RegisterRoutes(mux)

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
