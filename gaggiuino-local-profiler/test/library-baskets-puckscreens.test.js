import { describe, it, expect, beforeEach, afterAll } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

// Same in-memory DB swap as db-routes.test.js: patch the require cache for
// lib/db.js before any route/repository is required, so every consumer
// shares the memory DB.
const Database  = require('better-sqlite3');
const dbPath    = require.resolve('../lib/db');
const realDb    = require(dbPath);
const memDb     = new Database(':memory:');
realDb.initSchema(memDb);
require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

// Image upload/download is network-free here (direct buffer upload, unlike
// bean's URL-fetch path) but still touches the filesystem — mocked the same
// way db-routes.test.js mocks bean image handling, so these tests exercise
// route wiring/validation, not disk I/O.
const imageServicePath = require.resolve('../lib/services/ImageService');
const realImageService = require(imageServicePath);
const saveUploadedImageMock = (prefix, id, buffer, contentType) =>
    contentType === 'image/jpeg' || contentType === 'image/png' ? 'jpg' : null;
require.cache[imageServicePath].exports = {
    ...realImageService, saveUploadedImage: saveUploadedImageMock, deleteImage: () => {},
};

const express       = require('express');
const libraryRouter = require('../routes/library');
const { saveLibrary } = require('../lib/data');
const { getDb }        = require('../lib/db');

// Image routes register their own express.raw() middleware per-route (see
// routes/library/baskets.js, mirroring grinders.js) — no global raw parser
// needed here, just express.json() for the rest.
function makeApp() {
    const app = express();
    app.use(express.json());
    app.use(libraryRouter);
    app.use((err, req, res, _next) => res.status(err.status || 500).json({ error: err.message }));
    return app;
}

let server, baseUrl;

beforeEach(async () => {
    getDb().exec('DELETE FROM library;');
    server = makeApp().listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    baseUrl = `http://127.0.0.1:${server.address().port}`;
});

afterAll(() => server?.close());

describe('GET /api/library', () => {
    it('includes empty baskets/puckScreens arrays by default (#635)', async () => {
        const lib = await (await fetch(`${baseUrl}/api/library`)).json();
        expect(lib.baskets).toEqual([]);
        expect(lib.puckScreens).toEqual([]);
    });
});

describe('Basket CRUD (#635)', () => {
    it('rejects a basket without a name', async () => {
        const r = await fetch(`${baseUrl}/api/library/basket`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({}),
        });
        expect(r.status).toBe(400);
    });

    it('rejects an out-of-whitelist wallType', async () => {
        const r = await fetch(`${baseUrl}/api/library/basket`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: 'IMS Precision', wallType: 'not-a-real-type' }),
        });
        expect(r.status).toBe(400);
    });

    it('creates, lists, updates and deletes a basket', async () => {
        const createR = await fetch(`${baseUrl}/api/library/basket`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: 'IMS Precision', doseCapacity: '18-20g', wallType: 'precision-machined', shape: 'straight', holeCount: '~600 holes', notes: 'great basket' }),
        });
        expect(createR.status).toBe(200);
        const basket = await createR.json();
        expect(basket.name).toBe('IMS Precision');
        expect(basket.wallType).toBe('precision-machined');
        expect(basket.shape).toBe('straight');

        const listR = await fetch(`${baseUrl}/api/library/baskets`);
        expect((await listR.json()).map(b => b.id)).toEqual([basket.id]);

        const updateR = await fetch(`${baseUrl}/api/library/basket/${basket.id}`, {
            method: 'PUT', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ wallType: 'high-flow' }),
        });
        expect(updateR.status).toBe(200);
        expect((await updateR.json()).wallType).toBe('high-flow');

        const deleteR = await fetch(`${baseUrl}/api/library/basket/${basket.id}`, { method: 'DELETE' });
        expect(deleteR.status).toBe(200);
        const afterDelete = await (await fetch(`${baseUrl}/api/library/baskets`)).json();
        expect(afterDelete).toEqual([]);
    });

    it('uploads and serves a basket photo, same content-type whitelist as grinder photos', async () => {
        saveLibrary({ beans: [], grinders: [], recipes: [], baskets: [{ id: 1, name: 'IMS Precision' }] });
        const uploadR = await fetch(`${baseUrl}/api/library/basket/1/image`, {
            method: 'POST', headers: { 'Content-Type': 'image/jpeg' }, body: Buffer.from([1, 2, 3]),
        });
        expect(uploadR.status).toBe(200);
        expect((await uploadR.json()).image).toBe('jpg');

        const rejectR = await fetch(`${baseUrl}/api/library/basket/1/image`, {
            method: 'POST', headers: { 'Content-Type': 'image/svg+xml' }, body: Buffer.from([1, 2, 3]),
        });
        expect(rejectR.status).toBe(400);
    });
});

describe('Puck screen CRUD (#635)', () => {
    it('rejects a puck screen without a name', async () => {
        const r = await fetch(`${baseUrl}/api/library/puckscreen`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({}),
        });
        expect(r.status).toBe(400);
    });

    it('rejects an out-of-whitelist thickness', async () => {
        const r = await fetch(`${baseUrl}/api/library/puckscreen`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: 'Slayer mesh', thickness: 'ultra-thick' }),
        });
        expect(r.status).toBe(400);
    });

    it('creates, lists, updates and deletes a puck screen', async () => {
        const createR = await fetch(`${baseUrl}/api/library/puckscreen`, {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: 'Slayer mesh', thickness: 'thin', material: 'Porous Sintered Mesh', notes: 'nice' }),
        });
        expect(createR.status).toBe(200);
        const puckScreen = await createR.json();
        expect(puckScreen.name).toBe('Slayer mesh');
        expect(puckScreen.thickness).toBe('thin');

        const listR = await fetch(`${baseUrl}/api/library/puckscreens`);
        expect((await listR.json()).map(p => p.id)).toEqual([puckScreen.id]);

        const updateR = await fetch(`${baseUrl}/api/library/puckscreen/${puckScreen.id}`, {
            method: 'PUT', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ thickness: 'thick' }),
        });
        expect(updateR.status).toBe(200);
        expect((await updateR.json()).thickness).toBe('thick');

        const deleteR = await fetch(`${baseUrl}/api/library/puckscreen/${puckScreen.id}`, { method: 'DELETE' });
        expect(deleteR.status).toBe(200);
        const afterDelete = await (await fetch(`${baseUrl}/api/library/puckscreens`)).json();
        expect(afterDelete).toEqual([]);
    });

    it('uploads and serves a puck screen photo', async () => {
        saveLibrary({ beans: [], grinders: [], recipes: [], puckScreens: [{ id: 1, name: 'Slayer mesh' }] });
        const uploadR = await fetch(`${baseUrl}/api/library/puckscreen/1/image`, {
            method: 'POST', headers: { 'Content-Type': 'image/png' }, body: Buffer.from([1, 2, 3]),
        });
        expect(uploadR.status).toBe(200);
        expect((await uploadR.json()).image).toBe('jpg');
    });
});
