// Package backup will hold the Go port of routes/backup.js — scoped/full
// backup export (legacy self-contained JSON and the zip shape), restore
// (including dry-run preview, per-section apply, and passphrase-encrypted
// secrets via lib/backup-crypto.js's AES-256-GCM-scrypt scheme), and the
// image/path-traversal validation restore depends on.
//
// Phase 0 placeholder only. See openapi.yaml's Backup tag for the frozen
// contract this package must satisfy. No implementation yet.
package backup
