const { getDb }            = require('../db');
const { MAINTENANCE_DEFAULTS, isGlobalMaintenanceTask } = require('../constants');

// waterfilter/grinder_* rows always live under this sentinel machine_id (#338)
// since that equipment is shared across machines — see isGlobalMaintenanceTask().
const GLOBAL_MAINTENANCE_MACHINE_ID = 1;

class LibraryRepository {
    _lastLogId = 0;

    getLibrary() {
        const db  = getDb();
        const row = db.prepare("SELECT data FROM library WHERE key = 'main'").get();
        const lib = row ? JSON.parse(row.data) : {};
        return {
            beans:       lib.beans       ?? [],
            grinders:    lib.grinders    ?? [],
            recipes:     lib.recipes     ?? [],
            milks:       lib.milks       ?? [],
            baskets:     lib.baskets     ?? [],
            puckScreens: lib.puckScreens ?? [],
        };
    }

    saveLibrary(lib) {
        getDb().prepare("INSERT OR REPLACE INTO library (key, data) VALUES ('main', ?)").run(JSON.stringify(lib));
    }

    getMaintenance(machineId = 1) {
        const db      = getDb();
        // Fetch rows for both the requested machine and the global sentinel,
        // then keep each key from the set it actually belongs to — avoids
        // per-machine descaling/backflush/etc. rows leaking across machines
        // while still surfacing the shared waterfilter/grinder_* rows (#338).
        const rows    = db.prepare('SELECT key, data, machine_id FROM maintenance WHERE machine_id IN (?, ?)')
            .all(machineId, GLOBAL_MAINTENANCE_MACHINE_ID)
            .filter(r => isGlobalMaintenanceTask(r.key) ? r.machine_id === GLOBAL_MAINTENANCE_MACHINE_ID : r.machine_id === machineId);
        const saved   = Object.fromEntries(rows.map(r => [r.key, JSON.parse(r.data)]));
        const result  = JSON.parse(JSON.stringify(MAINTENANCE_DEFAULTS));
        for (const key of Object.keys(result)) {
            if (saved[key]) Object.assign(result[key], saved[key]);
        }
        const { grinders } = this.getLibrary();
        for (const grinder of grinders) {
            const key = `grinder_${grinder.id}`;
            const s   = saved[key] || {};
            result[key] = {
                lastDate:        s.lastDate        ?? null,
                threshold_shots: 'threshold_shots' in s ? s.threshold_shots : 200,
                threshold_days:  'threshold_days'  in s ? s.threshold_days  : null,
                grinderName:     grinder.name,
            };
        }
        return result;
    }

    saveMaintenance(data, machineId = 1) {
        const db  = getDb();
        const ins = db.prepare('INSERT OR REPLACE INTO maintenance (machine_id, key, data) VALUES (?,?,?)');
        db.transaction(() => {
            for (const [key, val] of Object.entries(data)) {
                const targetMachineId = isGlobalMaintenanceTask(key) ? GLOBAL_MAINTENANCE_MACHINE_ID : machineId;
                ins.run(targetMachineId, key, JSON.stringify(val));
            }
        })();
    }

    // machineId (#393): a finite integer scopes to that machine's log rows;
    // undefined (omitted) keeps the pre-existing unfiltered behavior — every
    // entry, across every machine — so older cached frontend clients that
    // never send machineId at all still get exactly what they got before.
    getMaintenanceLog(machineId) {
        const db  = getDb();
        const rows = Number.isFinite(machineId)
            ? db.prepare('SELECT * FROM maintenance_log WHERE machine_id = ? ORDER BY ts DESC').all(machineId)
            : db.prepare('SELECT * FROM maintenance_log ORDER BY ts DESC').all();
        const grinderNames = new Map(this.getLibrary().grinders.map(g => [`grinder_${g.id}`, g.name]));
        return rows.map(r => ({
            id:             r.id,
            ts:             r.ts,
            date:           r.date,
            task:           r.task,
            machine:        r.machine,
            machineId:      r.machine_id,
            shotCountAtTime: r.shot_count,
            notes:          r.notes,
            ...(grinderNames.has(r.task) ? { grinderName: grinderNames.get(r.task) } : {}),
        }));
    }

    addMaintenanceLogEntry(task, notes, machine, shotCount, machineId = 1) {
        const db    = getDb();
        // #578: Date.now() alone collides when this is called more than once
        // in the same millisecond — never happened with the previous sole
        // caller (one HTTP request at a time), but lib/maintenance-sync.js's
        // multi-machine/multi-task sync loop can legitimately do exactly
        // that. _lastLogId keeps ids monotonic even across a same-ms burst,
        // without changing the column type or existing entries' meaning.
        const id = Math.max(Date.now(), this._lastLogId + 1);
        this._lastLogId = id;
        const entry = {
            id,
            ts:      Math.floor(Date.now() / 1000),
            date:    new Date().toISOString().split('T')[0],
            task,
            machine: machine || '',
            shot_count: shotCount ?? 0,
            notes:   notes || '',
            machine_id: machineId,
        };
        db.prepare(
            'INSERT INTO maintenance_log (id, ts, date, task, machine, shot_count, notes, machine_id) VALUES (?,?,?,?,?,?,?,?)'
        ).run(entry.id, entry.ts, entry.date, entry.task, entry.machine, entry.shot_count, entry.notes, entry.machine_id);
        db.prepare('DELETE FROM maintenance_log WHERE id NOT IN (SELECT id FROM maintenance_log ORDER BY ts DESC LIMIT 500)').run();
        return { ...entry, shotCountAtTime: entry.shot_count };
    }

    // Deletes a single manual log entry (#553) — the "Not found" check stays
    // in routes/maintenance.js (it already reads the log via
    // getMaintenanceLog() to check existence before calling this).
    deleteMaintenanceLog(id) {
        getDb().prepare('DELETE FROM maintenance_log WHERE id = ?').run(id);
    }

    // ── Raw backup/restore ──────────────────────────────────────────────────
    // Unlike getMaintenance()/getMaintenanceLog() above (computed/display-
    // shaped views that fill in MAINTENANCE_DEFAULTS or rename columns), these
    // round-trip the tables' actual rows verbatim — what a full backup export
    // needs, since the computed view would silently invent defaults for keys
    // that were never actually set.
    getAllMaintenanceRaw() {
        return getDb().prepare('SELECT machine_id, key, data FROM maintenance').all()
            .map(r => ({ machineId: r.machine_id, key: r.key, data: JSON.parse(r.data) }));
    }

    restoreMaintenanceRaw(rows) {
        const db  = getDb();
        const ins = db.prepare('INSERT OR REPLACE INTO maintenance (machine_id, key, data) VALUES (?,?,?)');
        db.transaction(() => {
            db.prepare('DELETE FROM maintenance').run();
            for (const r of rows) ins.run(r.machineId, r.key, JSON.stringify(r.data));
        })();
    }

    getAllMaintenanceLogRaw() {
        return getDb().prepare('SELECT id, ts, date, task, machine, shot_count, notes, machine_id FROM maintenance_log').all()
            .map(r => ({
                id: r.id, ts: r.ts, date: r.date, task: r.task, machine: r.machine,
                shotCount: r.shot_count, notes: r.notes, machineId: r.machine_id,
            }));
    }

    // True round-trip: preserves id/ts exactly rather than minting new ones,
    // unlike addMaintenanceLogEntry() (the live-logging path).
    restoreMaintenanceLogRaw(rows) {
        const db  = getDb();
        const ins = db.prepare(
            'INSERT OR REPLACE INTO maintenance_log (id, ts, date, task, machine, shot_count, notes, machine_id) VALUES (?,?,?,?,?,?,?,?)'
        );
        db.transaction(() => {
            db.prepare('DELETE FROM maintenance_log').run();
            for (const r of rows) ins.run(r.id, r.ts, r.date, r.task, r.machine ?? '', r.shotCount ?? 0, r.notes ?? '', r.machineId ?? 1);
        })();
    }
}

module.exports = new LibraryRepository();
