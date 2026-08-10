// #755: counterpart to test/debug-export-db.test.js -- proves POST
// /api/debug/import-db's dev-build gate actually gates (same reasoning as
// the export test: a data-modifying path reaching a real production install
// would be far worse than exfiltration), and that a valid upload actually
// replaces the on-disk file via rename (not an in-place write) plus a
// timestamped backup, while a non-SQLite upload is rejected outright.
import { describe, it, expect, afterAll, afterEach } from 'vitest';
import { createRequire } from 'module';
import { mkdtempSync, rmSync, readdirSync, readFileSync, writeFileSync, existsSync } from 'fs';
import { tmpdir } from 'os';
import path from 'path';
const require = createRequire(import.meta.url);

// Same DATA_DIR override pattern as test/debug-export-db.test.js -- this
// route touches DB_PATH directly on disk (rename/copy/unlink), so it can't
// run against an in-memory database.
const tmpDataDir = mkdtempSync(path.join(tmpdir(), 'glp-debug-import-db-'));
const constantsPath = require.resolve('../lib/constants');
const realConstants = require(constantsPath);
require.cache[constantsPath].exports = { ...realConstants, DATA_DIR: tmpDataDir };

const dbPath = require.resolve('../lib/db');
const { getDb, DB_PATH } = require(dbPath);
getDb(); // creates the real glp.db file + schema in tmpDataDir

const express = require('express');
const debugRouter = require('../routes/debug');
const realFetch = globalThis.fetch;

async function startServer() {
    const app = express();
    app.use(express.raw({ type: 'application/octet-stream', limit: '50mb' }));
    app.use(debugRouter);
    const server = app.listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    return { server, baseUrl: `http://127.0.0.1:${server.address().port}` };
}

const VALID_SQLITE_BYTES = Buffer.concat([Buffer.from('SQLite format 3\0'), Buffer.from('fake-imported-content')]);

describe('#755 POST /api/debug/import-db dev-build gate', () => {
    let server;

    afterEach(async () => {
        const s = server;
        server = undefined;
        if (s) await new Promise(resolve => s.close(resolve));
        delete process.env.GLP_DEV_BUILD;
    });

    afterAll(() => {
        delete require.cache[constantsPath];
        delete require.cache[dbPath];
        globalThis.fetch = realFetch;
        rmSync(tmpDataDir, { recursive: true, force: true });
    });

    it('returns 404 when GLP_DEV_BUILD is unset, without touching the database file', async () => {
        delete process.env.GLP_DEV_BUILD;
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        const before = readFileSync(DB_PATH);
        const res = await realFetch(`${baseUrl}/api/debug/import-db`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/octet-stream' },
            body: VALID_SQLITE_BYTES,
        });
        expect(res.status).toBe(404);
        expect(readFileSync(DB_PATH).equals(before)).toBe(true);
    });

    it('rejects an upload that is not a SQLite file, even with GLP_DEV_BUILD set', async () => {
        process.env.GLP_DEV_BUILD = 'dev-20260810_0800';
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        const before = readFileSync(DB_PATH);
        const res = await realFetch(`${baseUrl}/api/debug/import-db`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/octet-stream' },
            body: Buffer.from('not a database'),
        });
        expect(res.status).toBe(400);
        expect(readFileSync(DB_PATH).equals(before)).toBe(true);
    });

    it('rejects an empty body', async () => {
        process.env.GLP_DEV_BUILD = 'dev-20260810_0800';
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        const res = await realFetch(`${baseUrl}/api/debug/import-db`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/octet-stream' },
            body: Buffer.alloc(0),
        });
        expect(res.status).toBe(400);
    });

    it('replaces the database file and keeps a timestamped backup of the old one', async () => {
        process.env.GLP_DEV_BUILD = 'dev-20260810_0800';
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        // Settle the live connection's on-disk state first (the route does
        // its own checkpoint too, but that's a no-op on an already-checked-
        // pointed DB) -- reading before that could see stale/incomplete WAL
        // content that wouldn't match what actually gets backed up.
        getDb().pragma('wal_checkpoint(TRUNCATE)');
        const originalBytes = readFileSync(DB_PATH);
        const res = await realFetch(`${baseUrl}/api/debug/import-db`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/octet-stream' },
            body: VALID_SQLITE_BYTES,
        });
        expect(res.status).toBe(200);
        const body = await res.json();
        expect(body.restartRequired).toBe(true);

        expect(readFileSync(DB_PATH).equals(VALID_SQLITE_BYTES)).toBe(true);

        const backupFiles = readdirSync(tmpDataDir).filter(f => f.startsWith('pre-import-backup-'));
        expect(backupFiles.length).toBeGreaterThan(0);
        expect(readFileSync(path.join(tmpDataDir, backupFiles[0])).equals(originalBytes)).toBe(true);
    });

    it('removes stale -wal/-shm sidecar files so a mismatched WAL is never replayed against the new DB', async () => {
        process.env.GLP_DEV_BUILD = 'dev-20260810_0800';
        writeFileSync(`${DB_PATH}-wal`, 'stale-wal-content');
        writeFileSync(`${DB_PATH}-shm`, 'stale-shm-content');
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        const res = await realFetch(`${baseUrl}/api/debug/import-db`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/octet-stream' },
            body: VALID_SQLITE_BYTES,
        });
        expect(res.status).toBe(200);
        expect(existsSync(`${DB_PATH}-wal`)).toBe(false);
        expect(existsSync(`${DB_PATH}-shm`)).toBe(false);
    });
});
