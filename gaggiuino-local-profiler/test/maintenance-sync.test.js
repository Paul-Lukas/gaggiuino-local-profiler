// lib/maintenance-sync.js (#578) — auto-syncs GLP's own descaling/backflush
// "done" status from newer Gaggiuino firmware's native Service Log
// (adapter.getNativeMaintenanceLog()), scoped correctly per machine in a
// multi-machine setup. In-memory DB swap, same pattern as
// test/bean-id-migration.test.js; the gaggiuino adapter's
// getNativeMaintenanceLog()/capabilities() are spied rather than hitting a
// real machine.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const Database = require('better-sqlite3');
const dbPath    = require.resolve('../lib/db');
const realDb    = require(dbPath);
const memDb     = new Database(':memory:');
realDb.initSchema(memDb);
require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

const gaggiuinoAdapter = require('../lib/machines/gaggiuino/adapter');
const libraryService = require('../lib/services/LibraryService');
const { syncNativeMaintenance } = require('../lib/maintenance-sync');

beforeEach(() => {
    memDb.exec('DELETE FROM machines; DELETE FROM maintenance; DELETE FROM maintenance_log; DELETE FROM library;');
    libraryService.saveLibrary({ beans: [], grinders: [], recipes: [] });
    vi.restoreAllMocks();
});

function addMachine({ id, type = 'gaggiuino', enabled = 1, isDefault = 0 }) {
    memDb.prepare(
        `INSERT INTO machines (id, name, type, host, switch_entity, is_default, enabled, created_at)
         VALUES (?, ?, ?, ?, NULL, ?, ?, ?)`
    ).run(id, `Machine ${id}`, type, `machine${id}.local`, isDefault, enabled, Date.now());
}

describe('syncNativeMaintenance (#578)', () => {
    it('marks descaling/backflush done for a machine when the native timestamp is newer', async () => {
        addMachine({ id: 1, isDefault: 1 });
        vi.spyOn(gaggiuinoAdapter, 'getNativeMaintenanceLog').mockResolvedValue({
            lastDescaleTimestamp: 1700000000, shotsSinceDescale: 3,
            lastBackflushTimestamp: 1700001000, shotsSinceBackflush: 1,
        });

        await syncNativeMaintenance();

        const maint = libraryService.getMaintenance(1);
        expect(maint.descaling.machineSyncedAt).toBe(1700000000);
        expect(maint.descaling.lastDate).toBe(new Date(1700000000 * 1000).toISOString());
        expect(maint.backflush.machineSyncedAt).toBe(1700001000);

        const logEntries = libraryService.getMaintenanceLog(1);
        expect(logEntries.filter(e => e.notes === 'Auto-synced from machine')).toHaveLength(2);
    });

    it('does not re-trigger a sync/log entry for an already-applied machine timestamp', async () => {
        addMachine({ id: 1, isDefault: 1 });
        vi.spyOn(gaggiuinoAdapter, 'getNativeMaintenanceLog').mockResolvedValue({
            lastDescaleTimestamp: 1700000000, shotsSinceDescale: 3,
            lastBackflushTimestamp: 0, shotsSinceBackflush: 0,
        });

        await syncNativeMaintenance();
        await syncNativeMaintenance(); // same timestamp again

        const logEntries = libraryService.getMaintenanceLog(1);
        expect(logEntries.filter(e => e.notes === 'Auto-synced from machine')).toHaveLength(1);
        // backflush never reported (0 = "never" on the machine) — untouched
        expect(libraryService.getMaintenance(1).backflush.machineSyncedAt).toBeNull();
    });

    it('syncs each machine only into its own maintenance record (#578 multi-machine)', async () => {
        addMachine({ id: 1, isDefault: 1 });
        addMachine({ id: 2 });
        vi.spyOn(gaggiuinoAdapter, 'getNativeMaintenanceLog').mockImplementation(async (machine) => (
            machine.id === 1
                ? { lastDescaleTimestamp: 1700000000, shotsSinceDescale: 1, lastBackflushTimestamp: 0, shotsSinceBackflush: 0 }
                : { lastDescaleTimestamp: 1800000000, shotsSinceDescale: 5, lastBackflushTimestamp: 0, shotsSinceBackflush: 0 }
        ));

        await syncNativeMaintenance();

        expect(libraryService.getMaintenance(1).descaling.machineSyncedAt).toBe(1700000000);
        expect(libraryService.getMaintenance(2).descaling.machineSyncedAt).toBe(1800000000);
    });

    it('skips disabled machines and machine types without the capability', async () => {
        addMachine({ id: 1, isDefault: 1, enabled: 0 });
        addMachine({ id: 2, type: 'gaggimate' });
        const spy = vi.spyOn(gaggiuinoAdapter, 'getNativeMaintenanceLog');

        await syncNativeMaintenance();

        expect(spy).not.toHaveBeenCalled();
    });

    it('a machine sync failure does not stop other machines from syncing', async () => {
        addMachine({ id: 1, isDefault: 1 });
        addMachine({ id: 2 });
        vi.spyOn(gaggiuinoAdapter, 'getNativeMaintenanceLog').mockImplementation(async (machine) => {
            if (machine.id === 1) throw new Error('machine unreachable');
            return { lastDescaleTimestamp: 1700000000, shotsSinceDescale: 1, lastBackflushTimestamp: 0, shotsSinceBackflush: 0 };
        });

        await expect(syncNativeMaintenance()).resolves.not.toThrow();
        expect(libraryService.getMaintenance(2).descaling.machineSyncedAt).toBe(1700000000);
    });
});
