// Package backup is the Go port of routes/backup.js (Phase 1f, issue
// #901): scoped/full backup export (the legacy self-contained JSON shape
// GET /api/backup always returns, and the zip shape POST /api/backup
// produces), restore (POST /api/restore — dry-run preview, per-section
// apply, passphrase-encrypted secrets via AES-256-GCM-scrypt), and the
// image path-traversal/integrity validation restore depends on.
//
// File layout:
//
//	model.go     BACKUP_SECTIONS / SECTION_BUNDLE_KEYS /
//	              SECTION_PRESENCE_BUNDLE_KEYS / normaliseSections(raw)
//	kv.go         MqttSettingsRepository.js/ImportSettingsRepository.js's
//	              get/save round trip — narrowly, just what the `kv` block
//	              needs (see its own doc comment)
//	crypto.go     lib/backup-crypto.js — AES-256-GCM-scrypt secrets encryption
//	image.go      ImageService.js's filename/path helpers (duplicated, same
//	              precedent as internal/shots' own copy) + the restore-time
//	              path-traversal/magic-bytes image validation guard
//	sanitize.go   the restore-time row sanitizers (maintenance/
//	              maintenance_log/order rows) — the coffee_library restore
//	              sanitizer itself lives in internal/library/
//	              restore_sanitize.go, since only that package can export it
//	              without an import cycle
//	ratelimit.go  lib/helpers.js's rateLimit(key, maxPerMinute) — same
//	              duplication precedent as internal/orders' own copy
//	bundle.go     gatherBackupData / buildBackupZip — the export half
//	restore.go    the POST /api/restore handler and everything it composes
//	              — the largest file in this package, deliberately split
//	              from handlers.go given its size
//	handlers.go   GET/POST /api/backup, route registration, zip build/read
//
// # Cross-domain dependencies this phase closed
//
// Two backup-only gaps flagged as deferred by earlier phases are closed
// here:
//
//   - internal/machines/registry.go's RestoreMachines (flagged deferred in
//     that package's own doc.go through Phase 1e) is now ported. NOT
//     included: evictLiveSession(oldHost) for every host that existed
//     before the restore (a stale WS session reconnects/fails naturally
//     against a host nothing identifies anymore, rather than being torn
//     down immediately — cosmetic timing, not data correctness) and
//     options-adoption.js's reconcileAfterRestore() (ties a restored
//     machine's stale host/switchEntity back to the current legacy add-on
//     options.json — no options.json facade exists in this Go port yet).
//     See RestoreMachines' own doc comment.
//   - internal/library's whole-entity restore sanitizers
//     (sanitizeBeanFields et al., flagged deferred in that package's
//     sanitize.go through Phase 1d) are now ported, in
//     internal/library/restore_sanitize.go, and called from this
//     package's mapToLibrary.
//
// # Deliberately deferred in this phase
//
//   - Atomicity: routes/backup.js wraps every restore write in one
//     getDb().transaction(...). This Go port does NOT reproduce that —
//     see restore.go's header comment for the full rationale (every
//     Repository across five packages would need a shared-Tx-accepting
//     variant of every write method, a real architecture change out of
//     this task's scope). applyRestore instead writes each of the six
//     sections sequentially, each internally atomic on its own; a failure
//     partway through leaves earlier sections applied and later ones not,
//     unlike Node's all-or-nothing guarantee. This is the one genuine
//     behavior gap in this domain's contract and is flagged again in
//     go/README.md's status section.
//   - A restored API token (POST /api/restore's decrypted secrets.apiToken)
//     is persisted to TOKEN_FILE on disk, but does NOT take effect in the
//     already-running Go server process: internal/auth.RequireToken closes
//     over a fixed token string at startup (cmd/server/main.go), with no
//     mutable/live token source the way Node's state.apiToken is. A
//     restarted process picks up the new token correctly (LoadOrCreateToken
//     reads the file); a not-yet-restarted one keeps accepting the old
//     token. See Dependencies.Token's doc comment.
//   - routes/debug.js's GET /api/debug/export-db / POST /api/debug/import-db
//     (raw SQLite file dump/restore, ~500MB body limit) are NOT part of
//     this package: they're closely related to backup/restore in spirit
//     but are a separate route file Node itself never merged into
//     routes/backup.js, and are a materially different mechanism
//     (whole-file SQLite blob, not a structured JSON bundle). Phase 2e
//     (#901) ported them into their own package, internal/debug.
//   - lib/zip.js's hand-rolled DEFLATE/CRC32 ZIP reader-writer is not
//     ported at all: Go's stdlib archive/zip already implements the same
//     ZIP format (APPNOTE.TXT, DEFLATE) that hand-rolled version targets,
//     so buildZip/readZip in handlers.go use it directly instead.
//
// See openapi.yaml's Backup tag for the frozen contract this package
// satisfies.
package backup
