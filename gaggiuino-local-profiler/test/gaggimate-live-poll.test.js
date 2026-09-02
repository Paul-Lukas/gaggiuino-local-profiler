// Tests for GaggiMate live-data path in poll.js:
// - startLivePolling() instantiates GaggiMateLiveClient
// - stopLivePolling() closes the client
// - pollViaGaggiuinoStatus() reads cached WS status → machineStatus + machineReachable
// - unreachable client → machineReachable false + LIVE_SNAPSHOT emitted
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const Database = require('better-sqlite3');

const dbPath       = require.resolve('../lib/db');
const realDb       = require(dbPath);
const registryPath = require.resolve('../lib/machines/registry');
const pollPath     = require.resolve('../lib/poll');
const wsClientPath = require.resolve('../lib/machines/gaggimate/ws-client');
const realWsClient = require(wsClientPath);
const statePath    = require.resolve('../lib/state');
const eventsPath   = require.resolve('../lib/events');

describe('GaggiMate live poll path', () => {
    let memDb, mockClientInstance, busEmitSpy;

    function makeMockClient(reachable = false, status = null) {
        return { status, reachable, closed: false, close: vi.fn() };
    }

    beforeEach(() => {
        memDb = new Database(':memory:');
        realDb.initSchema(memDb);
        require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

        mockClientInstance = makeMockClient();
        // Must use function (not arrow) so vi.fn() is callable as constructor
        // with `new`. A constructor that explicitly returns an object causes
        // `new` to yield that object, giving us a stable mockClientInstance ref.
        require.cache[wsClientPath].exports = {
            ...realWsClient,
            GaggiMateLiveClient: vi.fn(function () { return mockClientInstance; }),
        };

        delete require.cache[registryPath];
        delete require.cache[pollPath];

        require('../lib/machines/registry');
        memDb.prepare(
            `INSERT INTO machines (id, name, type, host, switch_entity, is_default, enabled, created_at)
             VALUES (1, 'GaggiMate', 'gaggimate', 'gaggimate.local', null, 1, 1, ?)`
        ).run(Date.now());

        const { bus } = require('../lib/events');
        busEmitSpy = vi.spyOn(bus, 'emit');
    });

    afterEach(() => {
        memDb.close();
        require.cache[dbPath].exports = realDb;
        require.cache[wsClientPath].exports = realWsClient;
        vi.restoreAllMocks();
    });

    it('startLivePolling() instantiates GaggiMateLiveClient with machine baseUrl', () => {
        const { startLivePolling, stopLivePolling } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        const runtime = new MachineRuntimeState();
        startLivePolling(runtime);
        const { GaggiMateLiveClient } = require('../lib/machines/gaggimate/ws-client');
        expect(GaggiMateLiveClient).toHaveBeenCalledWith('http://gaggimate.local');
        stopLivePolling(runtime);
    });

    it('stopLivePolling() calls close() on the live client', () => {
        const { startLivePolling, stopLivePolling } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        const runtime = new MachineRuntimeState();
        startLivePolling(runtime);
        stopLivePolling(runtime);
        expect(mockClientInstance.close).toHaveBeenCalled();
    });

    it('reachable client with status → machineReachable true and machineStatus populated', async () => {
        mockClientInstance.reachable = true;
        mockClientInstance.status = { ct: 93.5, tt: 94.0, pr: 0.5, m: 0, p: 'Default' };

        const { startLivePolling, stopLivePolling, pollViaGaggiuinoStatus } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        const state = require('../lib/state');
        const runtime = new MachineRuntimeState();

        startLivePolling(runtime);
        await pollViaGaggiuinoStatus(runtime);

        expect(state.machineReachable).toBe(true);
        expect(runtime.machineStatus.temperature).toBe(93.5);
        expect(runtime.machineStatus.targetTemperature).toBe(94.0);
        expect(runtime.machineStatus.pressure).toBe(0.5);
        expect(runtime.machineStatus.waterLevel).toBeNull();
        stopLivePolling(runtime);
    });

    it('reachable client → emits live-snapshot on each poll tick', async () => {
        mockClientInstance.reachable = true;
        mockClientInstance.status = { ct: 90.0, tt: 93.0, pr: 1.2, m: 0, p: '' };

        const { startLivePolling, stopLivePolling, pollViaGaggiuinoStatus } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        const runtime = new MachineRuntimeState();

        startLivePolling(runtime);
        const countBefore = busEmitSpy.mock.calls.filter(([ev]) => ev === 'live-snapshot').length;
        await pollViaGaggiuinoStatus(runtime);
        const countAfter = busEmitSpy.mock.calls.filter(([ev]) => ev === 'live-snapshot').length;

        expect(countAfter).toBeGreaterThan(countBefore);
        stopLivePolling(runtime);
    });

    it('unreachable client → machineReachable false and live-snapshot emitted', async () => {
        mockClientInstance.reachable = false;
        mockClientInstance.status = null;

        const { startLivePolling, stopLivePolling, pollViaGaggiuinoStatus } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');
        const state = require('../lib/state');
        const runtime = new MachineRuntimeState();

        state.machineReachable = true;
        startLivePolling(runtime);
        await pollViaGaggiuinoStatus(runtime);

        expect(state.machineReachable).toBe(false);
        const snaps = busEmitSpy.mock.calls.filter(([ev]) => ev === 'live-snapshot');
        expect(snaps.length).toBeGreaterThan(0);
        stopLivePolling(runtime);
    });

    it('client not started (pollViaGaggiuinoStatus without startLivePolling) → no crash, no HTTP call', async () => {
        const axiosPath = require.resolve('axios');
        const realAxios = require(axiosPath);
        const axiosMock = vi.fn().mockRejectedValue(new Error('network disabled'));
        require.cache[axiosPath].exports = { get: axiosMock };

        const { pollViaGaggiuinoStatus } = require('../lib/poll');
        const { MachineRuntimeState } = require('../lib/machine-runtime-state');

        await expect(pollViaGaggiuinoStatus(new MachineRuntimeState())).resolves.not.toThrow();
        expect(axiosMock).not.toHaveBeenCalled();

        require.cache[axiosPath].exports = realAxios;
    });
});
