// Registry config facade (lib/machines/registry.js's hostFor/switchEntityFor/
// baseUrlFor/apiUrlFor, introduced to end #638/#641/#643/#648 -- four bugs
// with the same root cause: a consumer reading opts.machine_host/
// opts.switch_entity straight off options.json instead of the registry).
//
// Table-driven over the two registry-backed fields (host, switchEntity) and
// their consumers. Per CLAUDE.md's regression policy: a setting that's
// editable in the UI needs a test proving a *change* changes behavior, not
// just a test that saving succeeds -- that gap is exactly how the v2.29.0
// switch_entity bug (options.json edited after the initial seed never
// reaching the app) shipped unnoticed. Some consumers (lib/poll.js,
// lib/preheat.js, lib/sync.js's syncShots()) already have this proof in
// test/default-machine-{host,switch-entity}-live-sync.test.js; this file
// covers the facade itself plus the consumers that had no such proof yet:
// lib/sync.js's fetchMachineVersion() (unfixed until this round), and
// routes/system.js's GET /api/switch, POST /api/switch/toggle and GET
// /api/status.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const fs   = require('fs');
const os   = require('os');
const path = require('path');
const Database = require('better-sqlite3');

const dbPath        = require.resolve('../lib/db');
const realDb        = require(dbPath);
const constantsPath = require.resolve('../lib/constants');
const realConstants = require(constantsPath);
const axiosPath      = require.resolve('axios');
const realAxios      = require(axiosPath);
const registryPath  = require.resolve('../lib/machines/registry');
const dataPath      = require.resolve('../lib/data');
const syncPath      = require.resolve('../lib/sync');
const systemPath    = require.resolve('../routes/system');

// options.json is deliberately different from every registry value set below
// in every test -- if a consumer (or the facade) ever falls back to it while
// a default machine exists, these assertions catch it immediately.
const STALE_HOST   = 'options-stale.local';
const STALE_ENTITY = 'switch.options_stale';

describe('settings take effect: registry-backed machine config reaches every consumer', () => {
    let memDb, tmpFile, axiosGetMock;

    beforeEach(() => {
        memDb = new Database(':memory:');
        realDb.initSchema(memDb);
        require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

        tmpFile = path.join(os.tmpdir(), `glp-test-settings-take-effect-${Date.now()}-${Math.random().toString(36).slice(2)}.json`);
        fs.writeFileSync(tmpFile, JSON.stringify({ machine_host: STALE_HOST, switch_entity: STALE_ENTITY }));
        require.cache[constantsPath].exports = { ...realConstants, OPTIONS_FILE: tmpFile };

        axiosGetMock = vi.fn().mockResolvedValue({ data: {} });
        require.cache[axiosPath].exports = { ...realAxios, get: axiosGetMock };

        delete require.cache[registryPath];
        delete require.cache[dataPath];
        delete require.cache[syncPath];
        delete require.cache[systemPath];

        const registry = require('../lib/machines/registry');
        registry.ensureDefaultMachine(); // seeds machine #1 from options.json's stale values
    });

    afterEach(() => {
        memDb.close();
        require.cache[dbPath].exports = realDb;
        require.cache[constantsPath].exports = realConstants;
        require.cache[axiosPath].exports = realAxios;
        fs.rmSync(tmpFile, { force: true });
    });

    describe('registry.js facade', () => {
        it('hostFor()/baseUrlFor()/apiUrlFor() prefer the registry host over options.json', () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { host: 'app-set-host.local' });

            expect(registry.hostFor()).toBe('app-set-host.local');
            expect(registry.baseUrlFor()).toBe('http://app-set-host.local');
            expect(registry.apiUrlFor()).toBe('http://app-set-host.local/api/shots');
        });

        it('hostFor()/baseUrlFor()/apiUrlFor() fall back to options.json only when there is no default machine', () => {
            const registry = require('../lib/machines/registry');
            const spy = vi.spyOn(registry, 'getDefaultMachine').mockReturnValue(null);

            expect(registry.hostFor()).toBe(STALE_HOST);
            expect(registry.baseUrlFor()).toBe(`http://${STALE_HOST}`);
            expect(registry.apiUrlFor()).toBe(`http://${STALE_HOST}/api/shots`);

            spy.mockRestore();
        });

        it('switchEntityFor() prefers the registry value, including an explicitly-set one', () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { switchEntity: 'switch.app_set' });

            expect(registry.switchEntityFor()).toBe('switch.app_set');
        });

        // #643, at the facade layer: this is the semantics that a naive
        // read-time fallback (`registry value || opts.switch_entity`) would
        // silently violate -- a cleared field must stay cleared, never
        // resurrect the stale options.json value it was seeded from.
        it('switchEntityFor() returns null for an explicitly-cleared registry value -- does NOT fall back to options.json (#643)', () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { switchEntity: '' });

            expect(registry.switchEntityFor()).toBe(null);
            expect(registry.switchEntityFor()).not.toBe(STALE_ENTITY);
        });

        it('switchEntityFor() falls back to options.json only when there is no default machine at all', () => {
            const registry = require('../lib/machines/registry');
            const spy = vi.spyOn(registry, 'getDefaultMachine').mockReturnValue(null);

            expect(registry.switchEntityFor()).toBe(STALE_ENTITY);

            spy.mockRestore();
        });

        it('an explicit machineId resolves that machine\'s own host/switchEntity, not the default machine\'s', () => {
            const registry = require('../lib/machines/registry');
            const other = registry.createMachine({ name: 'Second', type: 'gaggiuino', host: 'second.local', switchEntity: 'switch.second' });

            expect(registry.hostFor(other.id)).toBe('second.local');
            expect(registry.switchEntityFor(other.id)).toBe('switch.second');
            // Unaffected by the default machine's (still options.json-seeded) values.
            expect(registry.hostFor()).toBe(STALE_HOST);
        });
    });

    describe('consumers use the live registry value, not the stale options.json one', () => {
        it('lib/sync.js fetchMachineVersion() polls the registry\'s current host', async () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { host: 'updated-host.local' });

            axiosGetMock.mockResolvedValueOnce({ data: { version: '1.2.3' } });

            const { fetchMachineVersion } = require('../lib/sync');
            await fetchMachineVersion();

            expect(axiosGetMock).toHaveBeenCalledWith('http://updated-host.local/api/system/info', expect.anything());
            const calledUrls = axiosGetMock.mock.calls.map(c => c[0]);
            expect(calledUrls.every(u => !u.includes(STALE_HOST))).toBe(true);
        });

        it('GET /api/switch reports the registry\'s current switch entity, not the stale options.json one', async () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { switchEntity: 'switch.updated' });

            const express = require('express');
            const systemRouter = require('../routes/system');
            const app = express();
            app.use(express.json());
            app.use(systemRouter);
            const server = app.listen(0);
            await new Promise(resolve => server.once('listening', resolve));
            const baseUrl = `http://127.0.0.1:${server.address().port}`;

            try {
                const body = await (await fetch(`${baseUrl}/api/switch`)).json();
                expect(body.entity).toBe('switch.updated');
                expect(body.entity).not.toBe(STALE_ENTITY);
            } finally {
                await new Promise(resolve => server.close(resolve));
            }
        });

        // #643 semantics through a real route: clearing the entity in
        // Settings must gate off the toggle even though options.json still
        // has the stale value it was seeded from.
        it('POST /api/switch/toggle rejects once the registry switch entity is cleared, even with a stale options.json value present', async () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { switchEntity: '' });

            const express = require('express');
            const systemRouter = require('../routes/system');
            const app = express();
            app.use(express.json());
            app.use(systemRouter);
            const server = app.listen(0);
            await new Promise(resolve => server.once('listening', resolve));
            const baseUrl = `http://127.0.0.1:${server.address().port}`;

            try {
                const res = await fetch(`${baseUrl}/api/switch/toggle`, { method: 'POST' });
                expect(res.status).toBe(400);
            } finally {
                await new Promise(resolve => server.close(resolve));
            }
        });

        it('GET /api/status reports the registry\'s current switchEntity and machineUrl, not the stale options.json ones', async () => {
            const registry = require('../lib/machines/registry');
            registry.updateMachine(1, { host: 'status-host.local', switchEntity: 'switch.status_updated' });

            const express = require('express');
            const systemRouter = require('../routes/system');
            const app = express();
            app.use(express.json());
            app.use((req, res, next) => { req.glpAuthenticated = true; next(); }); // sensitive fields require this
            app.use(systemRouter);
            const server = app.listen(0);
            await new Promise(resolve => server.once('listening', resolve));
            const baseUrl = `http://127.0.0.1:${server.address().port}`;

            try {
                const body = await (await fetch(`${baseUrl}/api/status`)).json();
                expect(body.switchEntity).toBe('switch.status_updated');
                expect(body.machineUrl).toContain('status-host.local');
                expect(body.machineUrl).not.toContain(STALE_HOST);
            } finally {
                await new Promise(resolve => server.close(resolve));
            }
        });
    });
});
