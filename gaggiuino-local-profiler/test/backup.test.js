import { describe, it, expect, beforeEach, afterAll } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

// The backend is CommonJS (require-based), so vi.mock's ESM interception doesn't
// reach it here. Patch the require cache for lib/db.js directly instead — every
// consumer (routes/backup.js, ShotRepository.js, ...) resolves the same relative
// path to the same absolute file and shares Node's module cache, so overwriting
// the cached exports object before anything else is required swaps in an
// in-memory database for the whole test file.
const Database  = require('better-sqlite3');
const dbPath    = require.resolve('../lib/db');
const realDb    = require(dbPath);
const memDb     = new Database(':memory:');
realDb.initSchema(memDb);
require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

// #635: the restore route rate-limits at 3/min per IP — plenty for real
// usage, but this file's own test count exceeds it (same 127.0.0.1 for every
// call). Patch it permissive here, same require-cache-swap trick as the db
// patch above, rather than trim test coverage to fit the limit.
const helpersPath = require.resolve('../lib/helpers');
const realHelpers = require(helpersPath);
require.cache[helpersPath].exports = { ...realHelpers, rateLimit: () => true };

const express      = require('express');
const backupRouter = require('../routes/backup');
const shotRepo      = require('../lib/repositories/ShotRepository');
const libService    = require('../lib/services/LibraryService');
const { getDb }     = require('../lib/db');

function makeApp() {
    const app = express();
    app.use(express.json({ limit: '50mb' }));
    app.use(backupRouter);
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ error: err.message }));
    return app;
}

let server, baseUrl;

beforeEach(async () => {
    getDb().exec('DELETE FROM shots; DELETE FROM annotations; DELETE FROM trash; DELETE FROM blocklist;');
    server = makeApp().listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    baseUrl = `http://127.0.0.1:${server.address().port}`;
});

afterAll(() => server?.close());

function seedOneShot() {
    shotRepo.upsert({ id: 1, timestamp: 1700000000, duration: 250, profile_name: 'Test Profile' });
}

describe('POST /api/restore', () => {
    it('rejects a shot with a missing timestamp and leaves existing data intact', async () => {
        seedOneShot();
        const bad = {
            glp_backup: true,
            shots: [{ id: 2, duration: 100 }], // no timestamp
        };
        const r = await fetch(`${baseUrl}/api/restore`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(bad),
        });
        expect(r.status).toBe(400);
        const body = await r.json();
        expect(body.error).toMatch(/shot #0.*timestamp/i);

        // Core regression check: the wipe must not have committed before the failure.
        const remaining = shotRepo.findAll();
        expect(remaining).toHaveLength(1);
        expect(remaining[0].id).toBe(1);
    });

    it('rejects a shot with an invalid id and names the offending shot', async () => {
        seedOneShot();
        const bad = {
            glp_backup: true,
            shots: [
                { id: 1, timestamp: 1700000000 },
                { id: 0, timestamp: 1700000001 },
            ],
        };
        const r = await fetch(`${baseUrl}/api/restore`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(bad),
        });
        expect(r.status).toBe(400);
        const body = await r.json();
        expect(body.error).toMatch(/shot #1.*id/i);
        expect(shotRepo.findAll()).toHaveLength(1);
    });

    it('restores successfully with a fully valid backup', async () => {
        seedOneShot();
        const good = {
            glp_backup: true,
            shots: [
                { id: 5, timestamp: 1700000100, duration: 200, profile_name: 'Restored' },
                { id: 6, timestamp: 1700000200, duration: 220, profile_name: 'Restored 2' },
            ],
            annotations: {}, blocklist: [],
        };
        const r = await fetch(`${baseUrl}/api/restore`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(good),
        });
        expect(r.status).toBe(200);
        const body = await r.json();
        expect(body.ok).toBe(true);
        expect(body.shots).toBe(2);

        const remaining = shotRepo.findAll().map(s => s.id).sort();
        expect(remaining).toEqual([5, 6]);
    });

    // #635: milks used to be the one library entity NOT sanitized on restore
    // (bug/inconsistency — beans/grinders/recipes already were); fixed
    // alongside adding baskets/puckScreens sanitization.
    it('sanitizes milks, baskets and puckScreens the same way beans/grinders/recipes already are', async () => {
        seedOneShot();
        const backup = {
            glp_backup: true,
            shots: [{ id: 5, timestamp: 1700000100, duration: 200 }],
            coffee_library: {
                milks: [{ id: 1, name: '  Oat  '.padEnd(150, 'x'), emoji: 'not-an-emoji-way-too-long', stockMl: -50 }],
                baskets: [{ id: 2, name: 'IMS Precision', wallType: 'not-a-real-type', shape: 'also-fake', notes: 'x'.repeat(2000) }],
                puckScreens: [{ id: 3, name: 'Slayer mesh', thickness: 'ultra-thick', material: 'x'.repeat(300) }],
            },
        };
        const r = await fetch(`${baseUrl}/api/restore`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(backup),
        });
        expect(r.status).toBe(200);

        const lib = libService.getLibrary();
        expect(lib.milks[0].name.length).toBeLessThanOrEqual(100);
        expect(lib.milks[0].stockMl).toBe(0); // negative value rejected, falls back to 0
        expect(lib.baskets[0].wallType).toBe(''); // out-of-whitelist value dropped
        expect(lib.baskets[0].shape).toBe('');
        expect(lib.baskets[0].notes.length).toBeLessThanOrEqual(1000);
        expect(lib.puckScreens[0].thickness).toBe(''); // out-of-whitelist value dropped
        expect(lib.puckScreens[0].material.length).toBeLessThanOrEqual(200);
    });
});

describe('ShotRepository.upsert', () => {
    it('fails with a clean NOT NULL constraint error, not an opaque TypeError, on a missing timestamp', () => {
        // shots.timestamp is NOT NULL, so this is still expected to throw — the point of the
        // `?? null` guard is only to turn an unhandled "bind undefined" TypeError into a clean,
        // diagnosable SQLite constraint error for any code path that bypasses route validation
        // (e.g. the legacy JSON migration in lib/db.js).
        expect(() => shotRepo.upsert({ id: 99 })).toThrowError(/NOT NULL constraint failed/);
    });
});
