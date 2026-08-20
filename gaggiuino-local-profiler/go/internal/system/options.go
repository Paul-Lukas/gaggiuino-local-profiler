package system

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"
)

// This mirrors internal/orders/options.go's isOrdersEnabled(): a narrow,
// single-field read of /data/options.json (written by the Supervisor),
// rather than a full loadOptions() facade — see that file's doc comment
// for the trade-off reasoning, which applies identically here.

// preheatMinutesCache caches loadPreheatMinutes()'s parsed result, keyed on
// options.json's mtime. #901 code review: loadPreheatMinutes() used to
// re-read and re-parse the file on every call, including pollTick's 1s hot
// path (it's also read on every preheat-update SSE push and every
// preheat-state change) — for a value the Supervisor only ever rewrites
// when the user edits the add-on's config in HA's UI, a handful of times a
// year at most. A changed file is still picked up promptly: every call
// still os.Stat's the file (cheap relative to the ReadFile+Unmarshal this
// now skips on a cache hit) and re-parses the moment the mtime moves.
var preheatMinutesCache struct {
	mu      sync.Mutex
	valid   bool // false until the first os.Stat succeeds
	mtime   time.Time
	minutes int
}

// loadPreheatMinutes ports `Math.max(1, parseInt(opts.preheat_time) || 20)`,
// used by buildPreheatResponse/_checkReadyByPreheat/_checkPreheatNotify.
// Falls back to GLP_PREHEAT_TIME (#764, standalone Docker with no
// Supervisor) when options.json doesn't exist/parse, then to 20, matching
// loadOptions()'s own fallback chain.
func loadPreheatMinutes() int {
	preheatMinutesCache.mu.Lock()
	defer preheatMinutesCache.mu.Unlock()

	info, statErr := os.Stat(defaultOptionsFile)
	if statErr == nil && preheatMinutesCache.valid && info.ModTime().Equal(preheatMinutesCache.mtime) {
		return preheatMinutesCache.minutes
	}

	minutes := parsePreheatMinutesFile()
	preheatMinutesCache.valid = statErr == nil
	if statErr == nil {
		preheatMinutesCache.mtime = info.ModTime()
	}
	preheatMinutesCache.minutes = minutes
	return minutes
}

// parsePreheatMinutesFile does loadPreheatMinutes()'s actual read+parse+
// fallback chain — split out so loadPreheatMinutes itself only holds the
// cache-check/cache-store logic.
func parsePreheatMinutesFile() int {
	if data, err := os.ReadFile(defaultOptionsFile); err == nil {
		var opts struct {
			PreheatTime json.Number `json:"preheat_time"`
		}
		if err := json.Unmarshal(data, &opts); err == nil {
			if n, err := opts.PreheatTime.Int64(); err == nil && n > 0 {
				return int(n)
			}
		}
		return 20
	}
	if n, err := strconv.Atoi(os.Getenv("GLP_PREHEAT_TIME")); err == nil && n > 0 {
		return n
	}
	return 20
}

// isOrdersEnabled duplicates internal/orders' own isOrdersEnabled() verbatim
// (see that package's options.go for the reasoning): a narrow,
// single-field read rather than an import of internal/orders, which this
// package doesn't otherwise depend on. GET /api/status's ordersFeature
// field is this copy's only caller.
func isOrdersEnabled() bool {
	data, err := os.ReadFile(defaultOptionsFile)
	if err != nil {
		return os.Getenv("GLP_ENABLE_ORDERS") == "true"
	}
	var opts struct {
		EnableOrders bool `json:"enable_orders"`
	}
	if err := json.Unmarshal(data, &opts); err != nil {
		return os.Getenv("GLP_ENABLE_ORDERS") == "true"
	}
	return opts.EnableOrders
}

// isApiPortExposed ports lib/data.js's isApiPortExposed() /
// loadOptions().expose_api_port !== false. Opposite default from
// isOrdersEnabled/loadPreheatMinutes above: a missing/unparseable
// options.json, or a valid options.json that simply doesn't have this key
// yet (an install predating #803), must resolve to true — only an
// explicit JSON `false` turns it off. See routes/system.js's GET
// /api/token doc comment (ported verbatim in handlers.go's getToken) for
// why the default can't be closed.
func isApiPortExposed() bool {
	data, err := os.ReadFile(defaultOptionsFile)
	if err != nil {
		return os.Getenv("GLP_EXPOSE_API_PORT") != "false"
	}
	var opts struct {
		ExposeAPIPort *bool `json:"expose_api_port"`
	}
	if err := json.Unmarshal(data, &opts); err != nil {
		return os.Getenv("GLP_EXPOSE_API_PORT") != "false"
	}
	if opts.ExposeAPIPort == nil {
		return true // key absent from an otherwise-valid options.json -- undefined !== false
	}
	return *opts.ExposeAPIPort
}

// loadSyncIntervalMinutes ports `opts.sync_interval || 5` — GET
// /api/status's syncInterval field. A missing/unparseable options.json
// falls back to GLP_SYNC_INTERVAL (#764) then 5; a valid options.json that
// simply lacks (or has a non-positive) sync_interval falls straight to 5,
// matching loadOptions()'s own per-branch fallback chain (the env var is
// only ever consulted on the *whole-file-missing* path, never merged in
// field-by-field against an otherwise-present file — same distinction
// isApiPortExposed's key-absent-vs-file-absent branches draw above).
func loadSyncIntervalMinutes() int {
	data, err := os.ReadFile(defaultOptionsFile)
	if err != nil {
		if n, err := strconv.Atoi(os.Getenv("GLP_SYNC_INTERVAL")); err == nil && n > 0 {
			return n
		}
		return 5
	}
	var opts struct {
		SyncInterval json.Number `json:"sync_interval"`
	}
	if err := json.Unmarshal(data, &opts); err != nil {
		if n, err := strconv.Atoi(os.Getenv("GLP_SYNC_INTERVAL")); err == nil && n > 0 {
			return n
		}
		return 5
	}
	if n, err := opts.SyncInterval.Int64(); err == nil && n > 0 {
		return int(n)
	}
	return 5
}
