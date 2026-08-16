'use strict';
// #735: minimal backend pub/sub feeding routes/sse.js's single multiplexed
// SSE endpoint (GET /api/events). A plain EventEmitter is enough -- no
// external broker needed for a single-process add-on. setMaxListeners(50)
// because every open browser tab registers its own listener per event type
// it cares about (see routes/sse.js), and Node's default cap of 10 would log
// a MaxListenersExceededWarning long before that's an actual problem.
const EventEmitter = require('events');

const bus = new EventEmitter();
bus.setMaxListeners(50);

// SYNC_PROGRESS/SYNC_COMPLETE: emitted by lib/sync.js's backfill loop (#735).
// LIVE_SNAPSHOT/PREHEAT_UPDATE: emitted by lib/poll.js/lib/preheat.js (#736).
//
// #812 (achievements): SHOT_SAVED/BEAN_CHANGED/MAINTENANCE_ACKNOWLEDGED/
// ORDER_COMPLETED are the four state changes lib/achievements/service.js
// re-evaluates the badge registry on -- emitted from the single service-layer
// choke point each action already funnels through (ShotService.upsertShot,
// routes/library/beans.js, LibraryService.addMaintenanceLogEntry,
// OrderService.completeOrder), never from a timer/interval.
//
// PROFILE_SAVED/BACKUP_EXPORTED are two narrow additions beyond that
// four-event list: "a profile was created/edited" and "a backup was
// exported" are facts GLP has nowhere else to observe or reconstruct after
// the fact (profiles live on the machine, not in GLP's own DB; a past export
// leaves no row anywhere) -- unlike every other badge, there is no snapshot
// to retroactively evaluate at startup. Without a live hook here, the
// first_profile/profile_edit/backup badges could never unlock at all. Same
// bus, same non-polling discipline, just two more one-shot action events.
const EVENTS = {
    SYNC_PROGRESS: 'sync-progress',
    SYNC_COMPLETE: 'sync-complete',
    LIVE_SNAPSHOT: 'live-snapshot',
    PREHEAT_UPDATE: 'preheat-update',
    SHOT_SAVED: 'shot-saved',
    BEAN_CHANGED: 'bean-changed',
    MAINTENANCE_ACKNOWLEDGED: 'maintenance-acknowledged',
    ORDER_COMPLETED: 'order-completed',
    PROFILE_SAVED: 'profile-saved',
    BACKUP_EXPORTED: 'backup-exported',
};

module.exports = { bus, EVENTS };
