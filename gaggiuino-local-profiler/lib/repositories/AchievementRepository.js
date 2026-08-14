// #812: raw DB access for the achievements table (lib/db.js). Deliberately
// thin -- the actual badge conditions live in lib/achievements/registry.js,
// this only persists/reads the (id, unlocked_at, progress) rows the
// evaluator decides on.
const { getDb } = require('../db');

class AchievementRepository {
    // Map of id -> { id, unlockedAt, progress } for every row ever written.
    // An id with no row at all (never evaluated true, never had progress)
    // simply isn't a key in this map -- callers treat that as locked/0.
    getAll() {
        const rows = getDb().prepare('SELECT id, unlocked_at, progress FROM achievements').all();
        const out = {};
        for (const r of rows) out[r.id] = { id: r.id, unlockedAt: r.unlocked_at, progress: r.progress };
        return out;
    }

    // Unlocks a badge at `unlockedAt` (Unix seconds). Idempotent: INSERT OR
    // IGNORE means a badge that's already unlocked keeps its original
    // unlocked_at forever, even if evaluateAll() runs again later (retroactive
    // re-run, or another event firing) -- re-evaluation must never re-stamp.
    unlock(id, unlockedAt, progress = null) {
        getDb().prepare(
            'INSERT OR IGNORE INTO achievements (id, unlocked_at, progress) VALUES (?, ?, ?)'
        ).run(id, unlockedAt, progress);
    }

    // Updates progress on a still-locked badge (e.g. "7 of 10"). Never touches
    // unlocked_at -- once a row is unlocked, unlock() above is the only writer
    // that's still allowed to touch it (and it no-ops via INSERT OR IGNORE).
    setProgress(id, progress) {
        getDb().prepare(
            `INSERT INTO achievements (id, unlocked_at, progress) VALUES (?, NULL, ?)
             ON CONFLICT(id) DO UPDATE SET progress = excluded.progress
             WHERE achievements.unlocked_at IS NULL`
        ).run(id, progress);
    }
}

module.exports = new AchievementRepository();
