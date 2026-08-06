// Reported by Max: opening the restore modal and toggling a couple of
// section checkboxes hit "Rate limit exceeded" before he ever clicked
// Restore. Root cause: POST /api/restore's dry-run preview (fired on every
// checkbox toggle and debounced passphrase keystroke by
// public-src/components/backup-modal.js) shared the SAME rate-limit bucket
// as a real, destructive restore -- 3 requests/minute. A handful of normal
// UI interactions exhausted that budget on read-only preview traffic alone.
//
// test/backup.test.js patches lib/helpers.js's rateLimit() to always return
// true for its whole file (its own test count would otherwise exceed the
// real limit), so it can't prove the limiter itself works. This file uses
// the *real* rateLimit() against the real route to prove the fix: dry runs
// and real restores now draw from independent budgets.
import { describe, it, expect, beforeEach, afterAll } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const Database = require('better-sqlite3');
const dbPath   = require.resolve('../lib/db');
const realDb   = require(dbPath);
const memDb    = new Database(':memory:');
realDb.initSchema(memDb);
require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

const express      = require('express');
const backupRouter = require('../routes/backup');

function makeApp() {
    const app = express();
    // Test-only: server.js deliberately does NOT trust X-Forwarded-For in
    // production (see lib/middleware/rateLimit.js's comment on why) — this
    // is purely so the test harness can vary req.ip per test case without
    // opening real sockets on different addresses, to prove per-IP rate-limit
    // isolation. Never mirror this in the actual app.
    app.set('trust proxy', true);
    app.use(express.json({ limit: '50mb' }));
    app.use(backupRouter);
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ error: err.message }));
    return app;
}

let server, baseUrl;

// A fresh in-memory-map key per test (via a distinct fake source IP) so
// tests never share a rate-limit window with each other.
let ipCounter = 0;
function nextIp() { return `10.0.0.${++ipCounter}`; }

beforeEach(async () => {
    server = makeApp().listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    baseUrl = `http://127.0.0.1:${server.address().port}`;
});

afterAll(() => server?.close());

async function restore(body, ip) {
    return fetch(`${baseUrl}/api/restore`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Forwarded-For': ip },
        body: JSON.stringify(body),
    });
}

const MINIMAL_BACKUP = { glp_backup: true, shots: [] };

describe('POST /api/restore dry-run and real-restore rate limits are independent', () => {
    it('allows well more than 3 dry-run requests per minute — the exact reported symptom', async () => {
        const ip = nextIp();
        const results = [];
        for (let i = 0; i < 10; i++) {
            const r = await restore({ ...MINIMAL_BACKUP, dryRun: true }, ip);
            results.push(r.status);
        }
        expect(results.every(s => s === 200)).toBe(true);
    });

    it('still caps real (non-dry-run) restores at 3 per minute', async () => {
        const ip = nextIp();
        const statuses = [];
        for (let i = 0; i < 5; i++) {
            const r = await restore(MINIMAL_BACKUP, ip);
            statuses.push(r.status);
        }
        expect(statuses.slice(0, 3).every(s => s === 200)).toBe(true);
        expect(statuses.slice(3)).toEqual([429, 429]);
    });

    it('dry-run traffic does not consume the real-restore budget', async () => {
        const ip = nextIp();
        // Ten dry runs first -- would have exhausted a shared 3/min budget
        // many times over under the old behavior.
        for (let i = 0; i < 10; i++) {
            const r = await restore({ ...MINIMAL_BACKUP, dryRun: true }, ip);
            expect(r.status).toBe(200);
        }
        // The real restore's own budget must still be fully available.
        const real1 = await restore(MINIMAL_BACKUP, ip);
        const real2 = await restore(MINIMAL_BACKUP, ip);
        const real3 = await restore(MINIMAL_BACKUP, ip);
        expect([real1.status, real2.status, real3.status]).toEqual([200, 200, 200]);
    });

    it('a throttled real restore does not block subsequent dry-run previews', async () => {
        const ip = nextIp();
        for (let i = 0; i < 3; i++) await restore(MINIMAL_BACKUP, ip);
        const throttled = await restore(MINIMAL_BACKUP, ip);
        expect(throttled.status).toBe(429);

        const preview = await restore({ ...MINIMAL_BACKUP, dryRun: true }, ip);
        expect(preview.status).toBe(200);
    });
});
