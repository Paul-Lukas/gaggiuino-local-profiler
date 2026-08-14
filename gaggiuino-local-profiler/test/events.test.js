// lib/events.js (#735): minimal backend pub/sub feeding routes/sse.js's
// single multiplexed SSE endpoint. Just an EventEmitter singleton plus a
// fixed set of event-name constants -- covers the two properties that
// actually matter: it really is a singleton (every require() sees the same
// bus, so a listener registered by one route stays registered when another
// module emits), and setMaxListeners(50) is high enough that several
// concurrently open browser tabs (each registering its own listener per
// event type via routes/sse.js) never trip Node's default-10
// MaxListenersExceededWarning.
import { describe, it, expect } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

describe('lib/events.js', () => {
    it('exposes the expected event-name constants', () => {
        const { EVENTS } = require('../lib/events');
        // #812 added the six domain events the achievement evaluation hangs
        // off. Kept as an exact match rather than a subset check: this list is
        // a contract other modules subscribe to by name, and a typo'd or
        // silently renamed event would otherwise just stop firing.
        expect(EVENTS).toEqual({
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
        });
    });

    it('bus is a singleton -- every require() returns the same instance', () => {
        const a = require('../lib/events');
        const b = require('../lib/events');
        expect(a.bus).toBe(b.bus);
    });

    it('bus.emit() reaches a listener registered via a separate require()', () => {
        const { bus: busA, EVENTS } = require('../lib/events');
        const { bus: busB } = require('../lib/events');

        let received = null;
        busA.on(EVENTS.SYNC_PROGRESS, payload => { received = payload; });
        busB.emit(EVENTS.SYNC_PROGRESS, { machineId: 1, current: 2, total: 5 });

        expect(received).toEqual({ machineId: 1, current: 2, total: 5 });
        busA.removeAllListeners(EVENTS.SYNC_PROGRESS);
    });

    it('raises maxListeners to 50, so many concurrently open tabs never trigger the default-10 warning', () => {
        const { bus } = require('../lib/events');
        expect(bus.getMaxListeners()).toBe(50);
    });
});
