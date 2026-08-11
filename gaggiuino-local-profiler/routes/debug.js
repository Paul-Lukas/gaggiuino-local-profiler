// #722: dev-channel-only raw SQLite export for troubleshooting live user bug
// reports (see #721, where support had to reconstruct DB state from a text
// log alone because there was no way to just grab the DB). The existing
// GET/POST /api/backup is a curated, sectioned, secrets-redacted export --
// this route is deliberately the opposite: the entire raw glp.db file,
// unfiltered, meant only for a maintainer troubleshooting a dev-channel
// install, never for a real user's production install.
//
// Safety mechanism: gated on process.env.GLP_DEV_BUILD, the same flag
// routes/system.js's /api/status devBuild field and server.js's startup log
// suffix already use (only ever set by .github/workflows/build-dev.yaml's
// Docker build-arg for the dev-channel image -- never set for a real
// install, even once this code reaches `main` at the next release, since
// `dev` merges fully into `main` and keeping it out of that history is not
// itself a safety mechanism). The check is the first thing the handler does,
// unconditionally, before the database is touched at all.
'use strict';
const express = require('express');
const router  = express.Router();
const fs      = require('fs');
const path    = require('path');

const { getDb, DB_PATH } = require('../lib/db');
const { log } = require('../lib/helpers');

const SQLITE_MAGIC = Buffer.from('SQLite format 3\0');

// Mirrors routes/backup.js's own backupTimestamp() (kept as a separate copy
// rather than a shared helper, same reasoning that file documents for its
// browser-side twin in backup-modal.js).
function exportTimestamp() {
    const d = new Date();
    const pad = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}_${pad(d.getHours())}-${pad(d.getMinutes())}-${pad(d.getSeconds())}`;
}

router.get('/api/debug/export-db', (req, res) => {
    // Must stay first and unconditional: a real install never sets
    // GLP_DEV_BUILD, so this 404s exactly as if the route didn't exist at
    // all -- no distinguishing response that would leak a gated route exists.
    if (!process.env.GLP_DEV_BUILD) return res.status(404).end();

    try {
        // WAL mode (lib/db.js) means recent writes can still be sitting in
        // the -wal file rather than glp.db itself -- checkpoint first so the
        // downloaded file actually reflects everything committed so far.
        getDb().pragma('wal_checkpoint(TRUNCATE)');
        const filename = `glp-db-export-${exportTimestamp()}.db`;
        res.download(DB_PATH, filename, (err) => {
            if (err) log(`DB export download failed: ${err.message}`, true);
        });
    } catch (err) {
        log(`DB export failed: ${err.message}`, true);
        res.status(500).json({ error: err.message });
    }
});

// #755: counterpart to GET /api/debug/export-db above -- replaces the whole
// glp.db file 1:1 with an uploaded one. Gated identically (GLP_DEV_BUILD,
// checked first and unconditionally, same 404-not-400 "route doesn't exist
// on a real install" behavior as the export route).
//
// Written to a temp file then renamed into place (not written directly to
// DB_PATH) so the currently-running process's already-open database handle
// -- which keeps its file descriptor pinned to the OLD inode regardless of
// what the path now points to, standard POSIX rename() semantics -- is
// never touched mid-request; only a restart re-opens DB_PATH and picks up
// the new file. The old file's -wal/-shm sidecars are removed for the same
// reason lib/db.js checkpoints before migrating: a WAL file left over from
// the OLD database doesn't correspond to the NEWLY imported one's contents,
// and SQLite would try to replay it against a mismatched main file on the
// next startup otherwise.
router.post('/api/debug/import-db', (req, res) => {
    if (!process.env.GLP_DEV_BUILD) return res.status(404).end();

    try {
        const buf = req.body;
        // CodeQL's js/type-confusion-through-parameter-tampering doesn't
        // recognise Buffer.isBuffer() alone as a type-narrowing barrier for
        // an HTTP body value (same false-positive class as v2.30.0's release
        // notes -- CodeQL wants Array.isArray() explicitly ruled out, not
        // just Buffer.isBuffer() asserted) -- express.raw() only ever hands
        // this route a real Buffer or leaves req.body untouched (the {}
        // default from the global express.json() parser earlier in
        // server.js), so this is already impossible at runtime; the
        // Array.isArray() check documents that explicitly for the analyzer.
        if (Array.isArray(buf) || !Buffer.isBuffer(buf) || buf.length === 0) {
            return res.status(400).json({ error: 'No database file uploaded' });
        }
        if (!buf.subarray(0, SQLITE_MAGIC.length).equals(SQLITE_MAGIC)) {
            return res.status(400).json({ error: 'Not a SQLite database file' });
        }

        // Checkpoint + safety-copy of the CURRENT database before replacing
        // it -- same pre-change-backup pattern lib/db.js's
        // migrateMachineColumns() uses before its own destructive step.
        getDb().pragma('wal_checkpoint(TRUNCATE)');
        const backupPath = path.join(path.dirname(DB_PATH), `pre-import-backup-${Date.now()}.db`);
        fs.copyFileSync(DB_PATH, backupPath);

        const tmpPath = `${DB_PATH}.importing-${Date.now()}`;
        fs.writeFileSync(tmpPath, buf);
        fs.renameSync(tmpPath, DB_PATH);
        for (const suffix of ['-wal', '-shm']) {
            const sidecarPath = DB_PATH + suffix;
            if (fs.existsSync(sidecarPath)) fs.unlinkSync(sidecarPath);
        }

        log(`DB import: replaced glp.db (${buf.length} bytes, previous version backed up to ${backupPath}) -- restart required to load it`);
        res.json({ ok: true, restartRequired: true, backupPath: path.basename(backupPath) });
    } catch (err) {
        log(`DB import failed: ${err.message}`, true);
        res.status(500).json({ error: err.message });
    }
});

module.exports = router;
