// Auto-sync GLP's own descaling/backflush "done" status from newer
// Gaggiuino firmware's native Service Log (#578) — a separate, machine-side
// accounting system (adapter.getNativeMaintenanceLog(), build 7889b7d+)
// from GLP's own maintenance tracking (5 tasks, user thresholds, own shot
// history — see lib/services/LibraryService.js's computeMaintenanceStats()).
// Only the two overlapping tasks sync; grouphead/gaskets/waterfilter have no
// machine-side counterpart and are untouched.
//
// Called from lib/sync.js's syncAllMachines() so it rides the existing
// multi-machine sync cycle/interval instead of adding a second timer.
'use strict';
const { log } = require('./helpers');
const registry = require('./machines/registry');
const { getAdapter } = require('./machines');
const libraryService = require('./services/LibraryService');

const TASKS = [
    ['descaling', 'lastDescaleTimestamp'],
    ['backflush', 'lastBackflushTimestamp'],
];

async function syncMachineNativeMaintenance(machine) {
    const adapter = getAdapter(machine);
    if (!adapter.capabilities().nativeMaintenanceLog) return;

    let native;
    try { native = await adapter.getNativeMaintenanceLog(machine); }
    catch (err) { log(`Native maintenance sync skipped (${machine.name}): ${err.message}`, true); return; }

    const maint = libraryService.getMaintenance(machine.id);
    let changed = false;
    for (const [task, tsField] of TASKS) {
        const machineTs = native?.[tsField];
        // 0/missing means "never done yet" on the machine (verified live) —
        // and an unchanged timestamp means this exact event was already
        // applied, so neither should re-trigger a sync.
        if (!machineTs || machineTs === maint[task].machineSyncedAt) continue;
        maint[task].lastDate        = new Date(machineTs * 1000).toISOString();
        maint[task].machineSyncedAt = machineTs;
        libraryService.addMaintenanceLogEntry(task, 'Auto-synced from machine', machine.name, machine.id);
        changed = true;
    }
    if (changed) libraryService.saveMaintenance(maint, machine.id);
}

// Every enabled machine, not just the default one (#578) — each machine's
// native log only ever updates that same machine's own maintenance record.
async function syncNativeMaintenance() {
    const machines = registry.listMachines().filter(m => m.enabled);
    for (const machine of machines) {
        await syncMachineNativeMaintenance(machine);
    }
}

module.exports = { syncNativeMaintenance, syncMachineNativeMaintenance };
