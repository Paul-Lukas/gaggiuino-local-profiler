package backup

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/maintenance"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/orders"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/ratelimit"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// This file ports routes/backup.js's Express router (GET/POST /api/backup,
// POST /api/restore) onto Go 1.22+'s method-and-wildcard http.ServeMux.

// restoreJSONBodyLimit/restoreZipBodyLimit mirror server.js's
// `app.use('/api/restore', express.json({ limit: '50mb' }))` /
// `express.raw({ type: 'application/zip', limit: '50mb' })`.
const (
	restoreJSONBodyLimit = 50 * 1024 * 1024
	restoreZipBodyLimit  = 50 * 1024 * 1024
	postBackupBodyLimit  = 16 * 1024 // POST /api/backup's own body is tiny (sections+passphrase) — server.js's global express.json({limit:'16kb'}) default applies.
)

// restoreUnzipEntryLimit/restoreUnzipTotalLimit bound how much
// *decompressed* data readZip will accept out of a restore zip — a
// dimension restoreZipBodyLimit above does NOT cover, since it only caps
// the compressed request body. Without this, a small, highly-compressible
// zip entry (a "zip bomb") can inflate to many GB via io.ReadAll and
// OOM-kill the process before any JSON validation on backup.json's
// contents ever runs (#901 code review). Sized generously above
// restoreJSONBodyLimit — a legitimate backup.json is never bigger
// uncompressed than that same content would be as the legacy non-zip JSON
// restore body — to leave headroom for the largest single image entry; the
// total cap bounds the sum across every entry in the archive, so many
// merely-large-but-not-bomb-sized entries can't add up past a sane ceiling
// either. Package-level vars (not consts), same testing seam pattern as
// machines/registry.go's machineHostGuard, so tests can shrink them to
// avoid actually allocating/deflating hundreds of MB per test run.
var (
	restoreUnzipEntryLimit int64 = 100 * 1024 * 1024
	restoreUnzipTotalLimit int64 = 300 * 1024 * 1024
)

// Dependencies wires every cross-domain repository this package's export/
// restore need — one *sql.DB-backed dependency per domain this rewrite has
// split its own routes/*.js file into, plus the two ports never got a
// domain package of their own (see kv.go).
type Dependencies struct {
	DB              *sql.DB
	ShotsRepo       *shots.Repository
	LibRepo         *library.Repository
	OrdersRepo      *orders.Repository
	MaintenanceRepo *maintenance.Repository
	Registry        *machines.Registry
	// Token is the API token this server process is currently enforcing
	// (see cmd/server/main.go's auth.LoadOrCreateToken call). Included,
	// passphrase-encrypted, in a backup's `secrets` block when requested.
	// TokenFile is where a restored token is persisted — see restore.go's
	// applyRestoredToken doc comment for why writing it here does NOT take
	// effect in this already-running process until a restart (a real,
	// deliberate gap from Node's live state.apiToken — internal/auth's
	// RequireToken middleware closes over a fixed string at startup, with
	// no mutable/live token source to swap into, and building one is out
	// of this phase's scope).
	Token     string
	TokenFile string
}

// Handlers wires Dependencies into net/http handlers.
type Handlers struct {
	deps Dependencies
	rl   *ratelimit.KeyedLimiter
}

// NewHandlers builds Handlers around deps. TokenFile defaults to
// auth.DefaultTokenFile if unset.
func NewHandlers(deps Dependencies) *Handlers {
	if deps.TokenFile == "" {
		deps.TokenFile = auth.DefaultTokenFile
	}
	return &Handlers{deps: deps, rl: ratelimit.NewKeyed()}
}

// RegisterRoutes registers /api/backup and /api/restore onto mux.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backup", h.getBackup)
	mux.HandleFunc("POST /api/backup", h.postBackup)
	mux.HandleFunc("POST /api/restore", h.postRestore)
}

// ── response helpers ────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"Internal server error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func internalError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "Internal server error")
}

// backupTimestamp ports routes/backup.js's backupTimestamp(): a filename-
// safe local-time timestamp, e.g. "2026-08-06_08-32-05".
func backupTimestamp() string {
	return time.Now().Format("2006-01-02_15-04-05")
}

// ── GET /api/backup ──────────────────────────────────────────────────────

// getBackup ports GET /api/backup: always the unscoped, all-sections,
// secrets-free legacy JSON export.
func (h *Handlers) getBackup(w http.ResponseWriter, r *http.Request) {
	g, err := h.deps.gatherBackupData("", nil)
	if err != nil {
		internalError(w, err)
		return
	}
	bundle := g.bundle
	if g.imagesRequested {
		images := map[string]string{}
		for _, f := range g.imageFiles {
			images[f.filename] = base64.StdEncoding.EncodeToString(f.data)
		}
		bundle["images"] = images
	}
	b, err := json.Marshal(bundle)
	if err != nil {
		internalError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="glp-backup-%s.json"`, backupTimestamp()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// ── POST /api/backup ─────────────────────────────────────────────────────

func (h *Handlers) postBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, postBackupBodyLimit)
	var body struct {
		Passphrase string `json:"passphrase"`
		Sections   any    `json:"sections"`
	}
	// An empty/absent body is valid (full, secrets-free export) — only a
	// malformed non-empty body is an error, mirroring express.json()'s own
	// leniency (routes/backup.js reads req.body?.passphrase/req.body?.sections
	// with optional chaining, never requiring a body at all).
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, "request entity too large")
			} else {
				writeError(w, http.StatusBadRequest, "Invalid JSON body")
			}
			return
		}
	}
	sec := normaliseSections(body.Sections)
	g, err := h.deps.gatherBackupData(body.Passphrase, sec)
	if err != nil {
		internalError(w, err)
		return
	}
	zipBytes, err := buildZip(g)
	if err != nil {
		internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="glp-backup-%s.zip"`, backupTimestamp()))
	w.WriteHeader(http.StatusOK)
	w.Write(zipBytes)
}

// buildZip ports buildBackupZip's zip-assembly half: backup.json (no
// embedded image bytes) plus one images/<filename> entry per photo. Uses
// Go's stdlib archive/zip rather than porting lib/zip.js's hand-rolled
// DEFLATE/CRC32 reader-writer — archive/zip already implements the exact
// same ZIP format (APPNOTE.TXT, DEFLATE method) that hand-rolled version
// targets, so there is nothing lib/zip.js's own approach adds here.
func buildZip(g gatheredBundle) ([]byte, error) {
	backupJSON, err := json.Marshal(g.bundle)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("backup.json")
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(backupJSON); err != nil {
		return nil, err
	}
	if g.imagesRequested {
		for _, f := range g.imageFiles {
			iw, err := zw.Create("images/" + f.filename)
			if err != nil {
				return nil, err
			}
			if _, err := iw.Write(f.data); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readZip unpacks a zip archive's entries into backup.json's raw bytes
// (error if absent) plus an images/<filename> -> bytes map. Each entry's
// decompressed size is bounded (both individually and cumulatively across
// the whole archive — see restoreUnzipEntryLimit/restoreUnzipTotalLimit's
// doc comment) so a zip-bomb entry fails cleanly here instead of exhausting
// memory.
func readZip(data []byte) (backupJSON []byte, images map[string][]byte, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid zip file: %w", err)
	}
	images = map[string][]byte{}
	var totalUnzipped int64
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("invalid zip file: %w", err)
		}
		content, err := readZipEntry(rc, &totalUnzipped)
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		if f.Name == "backup.json" {
			backupJSON = content
		} else if name, ok := strings.CutPrefix(f.Name, "images/"); ok {
			images[name] = content
		}
	}
	if backupJSON == nil {
		return nil, nil, errors.New("invalid backup file (no backup.json in zip)")
	}
	return backupJSON, images, nil
}

// readZipEntry reads one zip entry's decompressed bytes, rejecting it once
// either restoreUnzipEntryLimit (this entry alone) or restoreUnzipTotalLimit
// (summed via *total across every entry read so far) is exceeded. The
// io.LimitReader cap is set one byte above the limit so an entry that lands
// exactly on the limit is still distinguishable from one that overflows it.
func readZipEntry(rc io.Reader, total *int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(rc, restoreUnzipEntryLimit+1))
	if err != nil {
		return nil, fmt.Errorf("invalid zip file: %w", err)
	}
	if int64(len(content)) > restoreUnzipEntryLimit {
		return nil, fmt.Errorf("zip entry too large uncompressed (max %d bytes)", restoreUnzipEntryLimit)
	}
	*total += int64(len(content))
	if *total > restoreUnzipTotalLimit {
		return nil, fmt.Errorf("zip archive too large uncompressed (max %d bytes total)", restoreUnzipTotalLimit)
	}
	return content, nil
}
