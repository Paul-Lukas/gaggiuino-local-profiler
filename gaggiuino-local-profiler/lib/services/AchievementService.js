// #812: evaluates the achievements registry (lib/achievements/registry.js)
// against a fresh context snapshot (lib/achievements/context.js) and
// persists newly-crossed badges (lib/repositories/AchievementRepository.js).
//
// Called from two places only, both wired in lib/events.js/server.js:
//   - bus event listeners (shot saved, bean changed, maintenance
//     acknowledged, order completed, profile saved, backup exported) --
//     evaluateAll({ type, payload }).
//   - server.js at boot -- evaluateAll() with no event, which is both the
//     "retroactive evaluation on first run after the update" pass and
//     every subsequent restart's idempotent re-check (see registry.js's
//     header for which badges only ever unlock on that specific live event
//     vs. which get full retroactive credit here).
// Never a timer/interval -- see lib/events.js's EVENTS header comment.
const repo = require('../repositories/AchievementRepository');
const { buildContext } = require('../achievements/context');
const { BADGES, isRetired, getSecretCopy } = require('../achievements/registry');
const { log } = require('../helpers');
const { bus, EVENTS } = require('../events');

// The bus event types that trigger a re-evaluation pass. SYNC_PROGRESS/
// LIVE_SNAPSHOT/PREHEAT_UPDATE etc. are deliberately excluded -- those fire
// far more often (once per live poll tick) and carry no achievement-
// relevant state change themselves (SHOT_SAVED already fires once the shot
// they summarize is actually persisted).
const TRIGGER_EVENTS = [
    EVENTS.SHOT_SAVED, EVENTS.BEAN_CHANGED, EVENTS.MAINTENANCE_ACKNOWLEDGED,
    EVENTS.ORDER_COMPLETED, EVENTS.PROFILE_SAVED, EVENTS.BACKUP_EXPORTED,
];

class AchievementService {
    // Subscribes evaluateAll() to every trigger event above. Called once
    // from server.js at boot -- idempotent to call twice only in the sense
    // that a second call would double-register listeners, so it must not be
    // called more than once per process (tests construct their own bus
    // listeners directly instead of calling this).
    init() {
        for (const type of TRIGGER_EVENTS) {
            bus.on(type, payload => this.evaluateAll({ type, payload }));
        }
    }

    // Runs every non-retired badge's check() against a fresh context.
    // Skips check()/progress() entirely for badges already unlocked -- once
    // a row has unlocked_at set, nothing about it can change, so there's no
    // reason to keep recomputing (often the more expensive part, e.g.
    // scoring every shot) for badges that are done. Returns the list of ids
    // newly unlocked by this pass (empty on every ordinary call once a
    // household's history is mostly stamped).
    evaluateAll(event = null) {
        const existing = repo.getAll();
        const stillLocked = BADGES.filter(b => !isRetired(b) && !existing[b.id]?.unlockedAt);
        if (!stillLocked.length) return [];

        const ctx = buildContext(event);
        const nowSec = Math.floor(ctx.now / 1000);
        const newlyUnlocked = [];

        for (const badge of stillLocked) {
            let unlocked;
            try {
                unlocked = !!badge.check(ctx);
            } catch (e) {
                log(`Achievement check failed for "${badge.id}": ${e.message}`, true);
                continue;
            }
            if (unlocked) {
                repo.unlock(badge.id, nowSec, badge.progressTarget ?? null);
                newlyUnlocked.push(badge.id);
                continue;
            }
            if (badge.progress) {
                try {
                    repo.setProgress(badge.id, badge.progress(ctx));
                } catch (e) {
                    log(`Achievement progress failed for "${badge.id}": ${e.message}`, true);
                }
            }
        }

        if (newlyUnlocked.length) log(`Achievements unlocked: ${newlyUnlocked.join(', ')}`);
        return newlyUnlocked;
    }

    // Shapes the full catalogue + DB state for routes/achievements.js.
    // Secret badges only carry stamp/name/description once unlocked --
    // before that, only id/card/secret/unlocked (false) are ever sent, so
    // the browser has nothing to leak.
    getState(lang) {
        const existing = repo.getAll();
        return BADGES.filter(b => !isRetired(b)).map(badge => {
            const row = existing[badge.id];
            const unlocked = !!row?.unlockedAt;
            const base = {
                id: badge.id,
                card: badge.card,
                secret: !!badge.secret,
                unlocked,
                unlockedAt: unlocked ? row.unlockedAt : null,
            };
            if (badge.progressTarget && !unlocked) {
                base.progress = { current: row?.progress ?? 0, target: badge.progressTarget };
            }
            if (!badge.secret) {
                base.stamp = badge.stamp;
            } else if (unlocked) {
                const copy = getSecretCopy(badge.id, lang);
                if (copy) Object.assign(base, copy);
            }
            return base;
        });
    }
}

module.exports = new AchievementService();
