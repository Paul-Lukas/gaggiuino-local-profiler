import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createRequire } from 'module';
import dns from 'dns';
const require = createRequire(import.meta.url);

// Same in-memory DB swap pattern as other library route tests (library
// router requires a working DB even though this route itself is DB-free).
const Database  = require('better-sqlite3');
const dbPath    = require.resolve('../lib/db');
const realDb    = require(dbPath);
const memDb     = new Database(':memory:');
realDb.initSchema(memDb);
require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

// Same require-cache axios mock as import-route.test.js — no real network
// calls to Open Food Facts from this suite.
const axiosPath = require.resolve('axios');
const axiosGet  = vi.fn();
require.cache[axiosPath] = { exports: { get: axiosGet, default: { get: axiosGet } } };

const express       = require('express');
const libraryRouter = require('../routes/library');

// `dns` is a core singleton — monkeypatch its `.promises.lookup` in place,
// same as import-route.test.js. Public by default so assertPublicHost()
// doesn't block the (mocked) world.openfoodfacts.org lookup.
const PUBLIC_ADDR = { address: '203.0.113.10', family: 4 };
let dnsLookup;

function makeApp() {
    const app = express();
    app.use(express.json());
    app.use(libraryRouter);
    return app;
}

let server, baseUrl;

beforeEach(async () => {
    axiosGet.mockReset();
    dnsLookup = vi.spyOn(dns.promises, 'lookup').mockResolvedValue([PUBLIC_ADDR]);
    server = makeApp().listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    baseUrl = `http://127.0.0.1:${server.address().port}`;
});

afterEach(() => { server?.close(); dnsLookup.mockRestore(); });

describe('GET /api/library/scan/:barcode', () => {
    it('rejects a non-numeric barcode before any outbound fetch', async () => {
        const r = await fetch(`${baseUrl}/api/library/scan/not-a-barcode`);
        expect(r.status).toBe(400);
        expect(axiosGet).not.toHaveBeenCalled();
    });

    it('rejects an implausible-length barcode', async () => {
        const r = await fetch(`${baseUrl}/api/library/scan/123`);
        expect(r.status).toBe(400);
        expect(axiosGet).not.toHaveBeenCalled();
    });

    it('returns name/roaster/notes reduced from the Open Food Facts payload for a valid EAN-13', async () => {
        axiosGet.mockResolvedValue({ status: 200, data: {
            product: {
                product_name: 'Test Bohnen',
                brands: 'Test Roastery',
                categories_tags: ['en:coffees', 'en:roasted-coffees'],
                labels: 'Organic',
            },
        }});
        const r = await fetch(`${baseUrl}/api/library/scan/4006381333931`);
        expect(r.status).toBe(200);
        const data = await r.json();
        expect(data).toEqual({ name: 'Test Bohnen', roaster: 'Test Roastery', notes: 'coffees, Organic' });
        expect(axiosGet).toHaveBeenCalledWith(
            'https://world.openfoodfacts.org/api/v3/product/4006381333931.json',
            expect.any(Object),
        );
    });

    // The frontend used to show the same generic scan_error
    // for "no such product" as for a real fetch failure — the route must
    // make these distinguishable via status code.
    it('responds 404 (not the generic error) when Open Food Facts has no product for the barcode', async () => {
        axiosGet.mockResolvedValue({ status: 200, data: { status: 0 } });
        const r = await fetch(`${baseUrl}/api/library/scan/00000000`);
        expect(r.status).toBe(404);
        const data = await r.json();
        expect(data.error).toBe('not_found');
    });

    it('responds 404 when Open Food Facts itself 404s', async () => {
        axiosGet.mockResolvedValue({ status: 404, data: {} });
        const r = await fetch(`${baseUrl}/api/library/scan/00000000`);
        expect(r.status).toBe(404);
    });

    it('responds 502 with a distinct error code when the fetch itself fails', async () => {
        axiosGet.mockRejectedValue(new Error('timeout'));
        const r = await fetch(`${baseUrl}/api/library/scan/4006381333931`);
        expect(r.status).toBe(502);
        const data = await r.json();
        expect(data.error).toBe('lookup_failed');
    });

    it('sends Cache-Control: no-store', async () => {
        axiosGet.mockResolvedValue({ status: 200, data: { product: { product_name: 'X' } } });
        const r = await fetch(`${baseUrl}/api/library/scan/4006381333931`);
        expect(r.headers.get('cache-control')).toBe('no-store');
    });
});
