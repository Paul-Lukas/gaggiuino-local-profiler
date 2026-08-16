// #812: builds the single read snapshot every badge's `check()` runs
// against. Built once per evaluation pass (one bus event, or the boot/
// retroactive sweep) and handed to every registry entry -- so evaluating 54
// badges costs one pass over each table, not 54.
//
// Per-install, not per-machine (see registry.js's header comment for why):
// every collection below is gathered across ALL machines, never scoped to
// the default machine or machineId 1. That's the one thing to double-check
// when adding a new check() -- pulling from a single-machine-scoped
// repository call here would silently make a badge blind to a second
// machine's activity.
const shotRepo = require('../repositories/ShotRepository');
const shotService = require('../services/ShotService');
const libraryService = require('../services/LibraryService');
const orderRepo = require('../repositories/OrderRepository');
const registry = require('../machines/registry');
const { STATIC_MAINTENANCE_TASKS, DEFAULT_MENU } = require('../constants');
const versionCheck = require('../version-check');
const { isDemoShot, isDemoLibraryId } = require('./helpers');

function buildContext(event = null) {
    const machines = registry.listMachines();

    const shots = shotRepo.findAllExcludingTrash()
        .filter(s => !isDemoShot(s))
        .map(s => ({ ...s, score: shotService.computeScoreDetail(s).score }));

    const lib = libraryService.getLibrary();
    const beans = (lib.beans || []).filter(b => !isDemoLibraryId(b.id));

    // Same remaining-grams math the library view itself uses (LibraryService.
    // computeBeanRemaining) -- precomputed once here so the bean_empty check
    // doesn't need its own copy of the consumption math.
    const annotatedDoses = shotRepo.getAnnotatedDoses();
    const beanRemaining = {};
    for (const b of beans) beanRemaining[b.id] = libraryService.computeBeanRemaining(b, annotatedDoses, beans);

    const orders = orderRepo.findAll();
    const menu = orderRepo.getMenu();

    const maintenanceLogs = libraryService.getMaintenanceLog(); // all machines
    const maintenanceConfigByMachine = {};
    const maintenanceStatsByMachine = {};
    for (const m of machines) {
        const conf = libraryService.getMaintenance(m.id);
        maintenanceConfigByMachine[m.id] = conf;
        maintenanceStatsByMachine[m.id] = libraryService.computeMaintenanceStats(conf, m.id);
    }

    return {
        now: Date.now(),
        event,
        shots,
        beans,
        beanRemaining,
        menu,
        defaultMenuIds: new Set(DEFAULT_MENU.map(i => i.id)),
        orders,
        machines,
        maintenanceLogs,
        maintenanceConfigByMachine,
        maintenanceStatsByMachine,
        staticMaintenanceTasks: STATIC_MAINTENANCE_TASKS,
        version: versionCheck.getCached(),
    };
}

module.exports = { buildContext };
