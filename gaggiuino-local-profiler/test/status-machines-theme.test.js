// #701: GET /api/status's `machines` array now carries each machine's
// `theme` (already stored by the registry, already returned by GET
// /api/machines) so glp-integration can forward it as an entity attribute
// and the Lovelace/Order cards can sync their accent color to the app's own
// Settings -> Machines theme picker instead of only their YAML-config
// fallback.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const Database = require('better-sqlite3');
const dbPath   = require.resolve('../lib/db');
const realDb   = require(dbPath);
const memDb    = new Database(':memory:');
realDb.initSchema(memDb);
require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema, getInstallId: () => 'test-install-id' };

const systemPath = require.resolve('../routes/system');
const express     = require('express');

function makeApp(systemRouter) {
    const app = express();
    app.use(express.json());
    app.use(systemRouter);
    return app;
}

let server, baseUrl;

beforeEach(async () => {
    memDb.exec('DELETE FROM machines;');
    delete require.cache[systemPath];
    const systemRouter = require('../routes/system');
    server = makeApp(systemRouter).listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    baseUrl = `http://127.0.0.1:${server.address().port}`;
});

afterEach(async () => {
    if (server) await new Promise(resolve => server.close(resolve));
    delete require.cache[systemPath];
});

describe('#701 GET /api/status machines[] includes each machine\'s theme', () => {
    it('reports null for a machine with no theme configured', async () => {
        const registry = require('../lib/machines/registry');
        registry.ensureDefaultMachine();

        const r = await fetch(`${baseUrl}/api/status`);
        const body = await r.json();
        expect(body.machines).toHaveLength(1);
        expect(body.machines[0].theme).toBeNull();
    });

    it('reports a preset theme set via the registry', async () => {
        const registry = require('../lib/machines/registry');
        registry.ensureDefaultMachine();
        const def = registry.getDefaultMachine();
        registry.updateMachine(def.id, { theme: { preset: 'amber-americano' } });

        const r = await fetch(`${baseUrl}/api/status`);
        const body = await r.json();
        expect(body.machines[0].theme).toEqual({ preset: 'amber-americano' });
    });

    it('reports a custom {a,b} theme for a second, non-default machine', async () => {
        const registry = require('../lib/machines/registry');
        registry.ensureDefaultMachine();
        const second = registry.createMachine({
            name: 'Kitchen GaggiMate', type: 'gaggimate', host: 'kitchen.local',
            theme: { a: '#111111', b: '#222222' },
        });

        const r = await fetch(`${baseUrl}/api/status`);
        const body = await r.json();
        const m2 = body.machines.find(m => m.id === second.id);
        expect(m2.theme).toEqual({ a: '#111111', b: '#222222' });
    });
});
