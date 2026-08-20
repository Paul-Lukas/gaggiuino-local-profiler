package system

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetPreheatMinutesCacheForTest isolates a test's use of the
// package-level preheatMinutesCache/defaultOptionsFile (both process-wide
// state, shared with every other test in this package and with
// production code) and restores both on cleanup.
func resetPreheatMinutesCacheForTest(t *testing.T) {
	t.Helper()
	origFile := defaultOptionsFile
	preheatMinutesCache.mu.Lock()
	origValid, origMtime, origMinutes := preheatMinutesCache.valid, preheatMinutesCache.mtime, preheatMinutesCache.minutes
	preheatMinutesCache.valid = false
	preheatMinutesCache.mu.Unlock()
	t.Cleanup(func() {
		defaultOptionsFile = origFile
		preheatMinutesCache.mu.Lock()
		preheatMinutesCache.valid = origValid
		preheatMinutesCache.mtime = origMtime
		preheatMinutesCache.minutes = origMinutes
		preheatMinutesCache.mu.Unlock()
	})
}

// TestLoadPreheatMinutes_CachesUntilFileChanges is the #901 code-review
// regression test for loadPreheatMinutes()'s caching: a cache hit (mtime
// unchanged) must keep serving the previously-parsed value even if the
// file's *content* changed underneath it without a new mtime, proving this
// isn't silently re-reading on every call — and a genuine mtime bump must
// still take effect on the very next call, proving the cache isn't stale
// forever either.
func TestLoadPreheatMinutes_CachesUntilFileChanges(t *testing.T) {
	resetPreheatMinutesCacheForTest(t)

	path := filepath.Join(t.TempDir(), "options.json")
	defaultOptionsFile = path

	writeOptionsAt := func(minutes int, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"preheat_time":%d}`, minutes)), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	base := time.Now().Truncate(time.Second)
	writeOptionsAt(15, base)
	if got := loadPreheatMinutes(); got != 15 {
		t.Fatalf("loadPreheatMinutes() = %d, want 15", got)
	}

	// Rewrite the content but hold the mtime fixed -- a cache hit must
	// keep returning the stale-but-cached 15, not re-parse.
	if err := os.WriteFile(path, []byte(`{"preheat_time":99}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(path, base, base); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if got := loadPreheatMinutes(); got != 15 {
		t.Fatalf("loadPreheatMinutes() = %d, want 15 (cached, mtime unchanged)", got)
	}

	// Now bump the mtime -- the new value must take effect on this very
	// next call, not lag behind.
	writeOptionsAt(25, base.Add(time.Second))
	if got := loadPreheatMinutes(); got != 25 {
		t.Fatalf("loadPreheatMinutes() = %d, want 25 (mtime changed, cache invalidated)", got)
	}
}

// TestLoadPreheatMinutes_FallsBackWhenFileMissing ports the pre-existing
// no-file/env-var/default-20 fallback chain, now routed through the cache
// path too (a missing file never becomes "valid", so every call re-checks
// rather than latching onto a stale fallback forever).
func TestLoadPreheatMinutes_FallsBackWhenFileMissing(t *testing.T) {
	resetPreheatMinutesCacheForTest(t)
	defaultOptionsFile = filepath.Join(t.TempDir(), "does-not-exist.json")

	t.Setenv("GLP_PREHEAT_TIME", "")
	if got := loadPreheatMinutes(); got != 20 {
		t.Fatalf("loadPreheatMinutes() = %d, want 20 (default)", got)
	}

	t.Setenv("GLP_PREHEAT_TIME", "12")
	if got := loadPreheatMinutes(); got != 12 {
		t.Fatalf("loadPreheatMinutes() = %d, want 12 (GLP_PREHEAT_TIME)", got)
	}
}
