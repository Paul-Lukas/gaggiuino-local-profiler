// Regression tests for GaggiMate as the default machine (#318 follow-up):
// - pollViaGaggiuinoStatus() must never call /api/system/status for GaggiMate
// - syncShots() must never call /api/shots/latest for GaggiMate
// - Gaggiuino default machine continues using those HTTP paths (no regression)
// - GaggiMateLiveClient disconnect while CONNECTING must not crash the process
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const Database = require('better-sqlite3');

const dbPath       = require.resolve('../lib/db');
const realDb       = require(dbPath);
const axiosPath    = require.resolve('axios');
const realAxios    = require(axiosPath);
const registryPath = require.resolve('../lib/machines/registry');
const pollPath     = require.resolve('../lib/poll');
const syncPath     = require.resolve('../lib/sync');
const machinesPath = require.resolve('../lib/machines');

describe('GaggiMate as default machine — Gaggiuino HTTP paths must not fire', () => {
    let memDb, axiosGetMock;

    beforeEach(() => {
        memDb = new Database(':memory:');
        realDb.initSchema(memDb);
        require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

        axiosGetMock = vi.fn().mockRejectedValue(new Error('network disabled in test'));
        require.cache[axiosPath].exports = { get: axiosGetMock };

        delete require.cache[registryPath];
        delete require.cache[pollPath];
        delete require.cache[syncPath];
        delete require.cache[machinesPath];
        delete require.cache[require.resolve('../lib/machines/gaggimate/adapter')];

        // Seed default machine as type gaggimate
        const registry = require('../lib/machines/registry');
        memDb.prepare(
            `INSERT INTO machines (id, name, type, host, switch_entity, is_default, enabled, created_at)
             VALUES (1, 'GaggiMate', 'gaggimate', 'gaggimate.local', null, 1, 1, ?)`
        ).run(Date.now());
    });

    afterEach(() => {
        memDb.close();
        require.cache[dbPath].exports = realDb;
        require.cache[axiosPath].exports = realAxios;
        vi.restoreAllMocks();
    });

    it('pollViaGaggiuinoStatus() does not call /api/system/status when default machine is GaggiMate', async () => {
        const { pollViaGaggiuinoStatus } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        await pollViaGaggiuinoStatus(new MachineRuntimeState());

        expect(axiosGetMock).not.toHaveBeenCalled();
    });

    it('syncShots() does not call /api/shots/latest when default machine is GaggiMate', async () => {
        // syncShots() delegates to syncMachineShots() for GaggiMate.
        // syncMachineShots() calls adapter.getLatestShotId() which hits index.bin.
        // Stub the adapter so we never need real network but can assert HTTP calls.
        const gaggiMateAdapterPath = require.resolve('../lib/machines/gaggimate/adapter');
        const realAdapter = require(gaggiMateAdapterPath);
        const adapterStub = { ...realAdapter, getLatestShotId: vi.fn().mockResolvedValue(null) };
        require.cache[gaggiMateAdapterPath].exports = adapterStub;

        delete require.cache[syncPath];
        const { syncShots } = require('../lib/sync');
        const ok = await syncShots({ machineOn: true });

        // No /api/shots/latest call — the adapter stub handled it
        const shotLatestCalls = axiosGetMock.mock.calls.filter(([url]) => url.includes('shots/latest') || url.includes('/latest'));
        expect(shotLatestCalls).toHaveLength(0);
        expect(ok).toBe(true); // adapter returned null (no shots yet) = already up to date
    });

    it('/api/system/status is not called even when startLivePolling() runs for GaggiMate', async () => {
        const { startLivePolling, stopLivePolling } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        const runtime = new MachineRuntimeState();

        startLivePolling(runtime);
        // Wait past one poll tick (1s interval)
        await new Promise(r => setTimeout(r, 1200));
        stopLivePolling(runtime);

        const statusCalls = axiosGetMock.mock.calls.filter(([url]) => url.includes('system/status'));
        expect(statusCalls).toHaveLength(0);
    }, 5000);
});

describe('Gaggiuino as default machine — HTTP paths must still fire (no regression)', () => {
    let memDb, axiosGetMock;

    beforeEach(() => {
        memDb = new Database(':memory:');
        realDb.initSchema(memDb);
        require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

        axiosGetMock = vi.fn().mockRejectedValue(new Error('network disabled in test'));
        require.cache[axiosPath].exports = { get: axiosGetMock };

        delete require.cache[registryPath];
        delete require.cache[pollPath];
        delete require.cache[syncPath];

        const registry = require('../lib/machines/registry');
        memDb.prepare(
            `INSERT INTO machines (id, name, type, host, switch_entity, is_default, enabled, created_at)
             VALUES (1, 'Gaggiuino', 'gaggiuino', 'gaggiuino.local', null, 1, 1, ?)`
        ).run(Date.now());
    });

    afterEach(() => {
        memDb.close();
        require.cache[dbPath].exports = realDb;
        require.cache[axiosPath].exports = realAxios;
        vi.restoreAllMocks();
    });

    it('pollViaGaggiuinoStatus() still calls /api/system/status for Gaggiuino default machine', async () => {
        const { pollViaGaggiuinoStatus } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        await pollViaGaggiuinoStatus(new MachineRuntimeState());

        expect(axiosGetMock).toHaveBeenCalledTimes(1);
        const [url] = axiosGetMock.mock.calls[0];
        expect(url).toBe('http://gaggiuino.local/api/system/status');
    });

    it('syncShots() still calls /api/shots/latest for Gaggiuino default machine', async () => {
        const { syncShots } = require('../lib/sync');
        const ok = await syncShots({ machineOn: true });

        expect(ok).toBe(false); // rejected by mock, confirming request was attempted
        expect(axiosGetMock).toHaveBeenCalledTimes(1);
        const [url] = axiosGetMock.mock.calls[0];
        expect(url).toBe('http://gaggiuino.local/api/shots/latest');
    });
});

describe('gaggiuino-live-client disconnect of a CONNECTING socket', () => {
    it('does not crash the process when terminate() is called on a socket still connecting', async () => {
        // Open a WS connection to a port with nothing listening — the socket
        // will stay in CONNECTING state (ECONNREFUSED fires asynchronously).
        // disconnect() must not let the 'error' event become an unhandled exception.
        const liveClient = require('../lib/gaggiuino-live-client');
        const unreachableUrl = 'http://127.0.0.1:1'; // port 1 never has anything listening

        // Prime a session (connect() is called lazily by getLiveSensorSnapshot)
        liveClient.getLiveSensorSnapshot(unreachableUrl);

        // Immediately disconnect while the socket is still trying to connect.
        // The error must be swallowed, not thrown.
        expect(() => liveClient.disconnect(unreachableUrl)).not.toThrow();

        // Allow any pending async error events to fire — if they're unhandled
        // they'd crash the process before this line.
        await new Promise(r => setTimeout(r, 200));
    });
});
