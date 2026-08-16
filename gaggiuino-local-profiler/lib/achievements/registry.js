// #812: the achievements ("stamp card") catalogue. Ported from the
// redesign-2026-08/achievements-catalog.py prototype's data list -- same
// 48 open + 6 secret badges across 7 categories, corrected per the
// prototype's own subject-matter pass (see PLAN.md section 5's "Vorher /
// Befund / Jetzt" table: rest-time direction, PID-realistic temperature
// tolerance, altitude threshold, process taxonomy, pressure-plateau
// tolerance).
//
// Registry contract (verbatim from issue #812):
//   id       stable, immutable (it's the DB key). Withdrawn -> retired:
//            true, entry stays (never delete a row -- see isRetired below).
//   card     category key, one of the 7 CARD_KEYS.
//   stamp    motif key OR plain text ('90', '1:2', '30d') -- text stamps
//            need no artwork.
//   check    condition, backend only. Threshold-on-metric or predicate-on-
//            event -- every entry below is one or the other.
//   progress optional (progressTarget + progress(ctx)), yields "7 of 10".
//   secret   name/description reach the browser only after unlocking (see
//            lib/achievements/secrets.js + routes/achievements.js).
//
// Per-install, not per-machine: the DB schema itself has no machine_id
// column (lib/db.js's achievements table is a flat id -> unlocked_at/
// progress map), so every check() below is written to aggregate across
// ALL machines via lib/achievements/context.js's ctx -- never scoped to
// the default machine or machineId 1. This app supports multiple
// concurrent machines (see CLAUDE.md's regression policy), and the
// precedent it warns about is exactly the trap here: a check() that
// silently only looked at machine 1 would work fine for every single-
// machine install and quietly under-count for anyone with two. The
// collection is a single shared keepsake for the install, the same way
// "Coffee Library" is one shared shelf regardless of how many machines
// brew from it.
//
// Adding a badge is one entry in BADGES below (or, for a whole new
// category, one entry in CARD_KEYS) -- nothing else needs to change.
const {
    stddev, detectPreinfusionSeconds, hasPressurePlateau,
    shotRatio, resolveBeanForShot,
    currentDayStreak, textMatchesAny, maintenanceCleanStreakDays, bagFirstUseAgesDays,
} = require('./helpers');
const { getSecretCopy, SECRET_IDS } = require('./secrets');

const CARD_KEYS = ['basics', 'craft', 'beans', 'endurance', 'care', 'house', 'secret'];

// ── Craft-card math thresholds, each with a source (per the catalogue rule:
// "every badge with a numeric threshold needs a source or a measurement
// from real shots") ──────────────────────────────────────────────────────
// +-1.5 C over the whole shot: real Gaggiuino PID performance is roughly
// +-2 C (single boiler) / +-3-4 C (dual boiler) per live-verified profiles
// referenced in PLAN.md's fact-check table -- +-0.3 C from the original
// draft was unreachable on real hardware, +-1.5 C is tight but attainable.
const TEMP_STABLE_TOLERANCE_C = 1.5;
// +-0.3 bar over 10s: loosened from +-0.2 bar/15s, which is tight for a
// vibration pump's natural ripple; 10s is long enough to rule out a blip.
const PRESSURE_PLATEAU_TOLERANCE_BAR = 0.3;
const PRESSURE_PLATEAU_WINDOW_S = 10;
// >1500m: the recognised "Strictly High Grown" threshold (1200m = "High
// Grown", 1500m = "Strictly High Grown" in standard green-coffee grading).
const ALTITUDE_SHG_M = 1500;
// 7-21 days: espresso needs 5-10 days minimum rest (CO2 outgassing) before
// it's not sour/uneven, and stays in its usable window out to about 3
// weeks before it's past peak -- see PLAN.md's fact-check table for why
// the original "week 1" / "14-21 day" drafts were wrong in each direction.
const PATIENT_MIN_REST_DAYS = 7;
const RESTED_WINDOW_MIN_DAYS = 10;
const RESTED_WINDOW_MAX_DAYS = 21;
// EUR/shot: no settings surface for a user-configurable threshold exists
// yet (see this module's own header note in the PR/report -- adding one
// would be exactly the "new field" the catalogue says shouldn't be needed).
// 0.35 EUR/shot is a conservative "home roaster bag, not a supermarket
// discount blend" estimate -- a documented default, not a measurement.
const PRICE_LOW_THRESHOLD_EUR = 0.35;

const PROCESS_KEYWORDS = {
    washed: ['washed', 'wet process', 'gewaschen', 'nassaufbereitet'],
    natural: ['natural', 'dry process', 'natur', 'trockenaufbereitet'],
    honey: ['honey', 'honig', 'pulped natural', 'miel'],
};
const EXPERIMENTAL_KEYWORDS = [
    'anaerob', 'anaerobic', 'carbonic maceration', 'kohlensäuremazeration',
    'co-ferment', 'coferment', 'koji', 'thermal shock', 'thermoshock',
    'extended fermentation', 'wine process', 'wein-verarbeitet',
    'double fermentation', 'lactic fermentation',
];

function isPalindrome(n) {
    const s = String(n);
    return s === s.split('').reverse().join('');
}

// Local calendar-date parts for a Unix-seconds timestamp -- server-local
// time, same convention already used elsewhere in this app for "which day"
// groupings (e.g. maintenance_log's own `date` column).
function localParts(unixSeconds) {
    const d = new Date(unixSeconds * 1000);
    return {
        year: d.getFullYear(), month: d.getMonth(), day: d.getDate(),
        hour: d.getHours(), minute: d.getMinutes(), weekday: d.getDay(),
    };
}

const BADGES = [
    // ── basics: what everyone does for the first time ──────────────────
    { id: 'first_connect', card: 'basics', stamp: 'link',
        check: ctx => ctx.machines.some(m => !!m.host) },
    { id: 'first_bean', card: 'basics', stamp: 'bean',
        check: ctx => ctx.beans.length > 0 },
    { id: 'first_shot', card: 'basics', stamp: 'cup',
        check: ctx => ctx.shots.length > 0 },
    // Not retroactively knowable: GLP has no persisted record of profile
    // creation, only a live moment routes/system.js observes (see
    // lib/events.js's EVENTS header comment).
    { id: 'first_profile', card: 'basics', stamp: 'slider',
        check: ctx => ctx.event?.type === 'profile-saved' && ctx.event.payload?.action === 'create' },
    { id: 'first_rating', card: 'basics', stamp: 'star',
        check: ctx => ctx.shots.some(s => s.annotation?.rating != null) },
    { id: 'first_milk', card: 'basics', stamp: 'jug',
        check: ctx => ctx.orders.some(o => o.status === 'done' && o.variant) },
    { id: 'shots_10', card: 'basics', stamp: '10',
        check: ctx => ctx.shots.length >= 10,
        progressTarget: 10, progress: ctx => ctx.shots.length },
    { id: 'first_maint', card: 'basics', stamp: 'wrench',
        check: ctx => ctx.maintenanceLogs.length > 0 },

    // ── craft: what takes a little practice ─────────────────────────────
    { id: 'score_90', card: 'craft', stamp: '90',
        check: ctx => ctx.shots.some(s => s.score != null && s.score >= 90) },
    { id: 'score_95', card: 'craft', stamp: '95',
        check: ctx => ctx.shots.some(s => s.score != null && s.score >= 95) },
    { id: 'ratio_exact', card: 'craft', stamp: '1:2',
        check: ctx => ctx.shots.some(s => { const r = shotRatio(s); return r != null && Math.abs(r - 2) < 0.005; }) },
    { id: 'dialed_in', card: 'craft', stamp: 'target',
        // 3 consecutive shots (in that bean's own chronological sequence,
        // not calendar-consecutive days) scoring >85 on the same bean.
        check: ctx => {
            const byBean = new Map();
            for (const shot of ctx.shots) {
                const bean = resolveBeanForShot(shot, ctx.beans);
                if (!bean) continue;
                if (!byBean.has(bean.id)) byBean.set(bean.id, []);
                byBean.get(bean.id).push(shot);
            }
            for (const shots of byBean.values()) {
                let streak = 0;
                for (const s of shots) {
                    streak = (s.score != null && s.score > 85) ? streak + 1 : 0;
                    if (streak >= 3) return true;
                }
            }
            return false;
        } },
    // Not retroactively knowable, same reasoning as first_profile.
    { id: 'profile_edit', card: 'craft', stamp: 'book',
        check: ctx => ctx.event?.type === 'profile-saved' && ctx.event.payload?.action === 'update' },
    { id: 'temp_stable', card: 'craft', stamp: 'shield',
        check: ctx => ctx.shots.some(s => {
            const t = (s.datapoints?.temperature || []).map(v => v / 10);
            return t.length > 5 && stddev(t) <= TEMP_STABLE_TOLERANCE_C;
        }) },
    { id: 'pressure_flat', card: 'craft', stamp: 'bolt',
        check: ctx => ctx.shots.some(s => {
            const d = s.datapoints || {};
            const times = (d.timeInShot || []).map(v => v / 10);
            const pressures = (d.pressure || []).map(v => v / 10);
            return hasPressurePlateau(times, pressures, PRESSURE_PLATEAU_WINDOW_S, PRESSURE_PLATEAU_TOLERANCE_BAR);
        }) },
    { id: 'blooming', card: 'craft', stamp: 'drop',
        check: ctx => ctx.shots.some(s => {
            if (s.score == null || s.score < 88) return false;
            const d = s.datapoints || {};
            const times = (d.timeInShot || []).map(v => v / 10);
            const pressures = (d.pressure || []).map(v => v / 10);
            const pre = detectPreinfusionSeconds(times, pressures);
            return pre != null && pre > 10;
        }) },

    // ── beans: what accumulates in the library ──────────────────────────
    { id: 'countries_10', card: 'beans', stamp: 'globe',
        check: ctx => _countryCodes(ctx.beans).size >= 10,
        progressTarget: 10, progress: ctx => Math.min(_countryCodes(ctx.beans).size, 10) },
    { id: 'roasters_10', card: 'beans', stamp: 'roast',
        check: ctx => _roasterNames(ctx.beans).size >= 10,
        progressTarget: 10, progress: ctx => Math.min(_roasterNames(ctx.beans).size, 10) },
    { id: 'processes_3', card: 'beans', stamp: 'leaf',
        check: ctx => _processesCovered(ctx.beans).size >= 3,
        progressTarget: 3, progress: ctx => _processesCovered(ctx.beans).size },
    { id: 'experimental', card: 'beans', stamp: 'bolt',
        check: ctx => ctx.beans.some(b => textMatchesAny(b.process, EXPERIMENTAL_KEYWORDS)) },
    { id: 'patient', card: 'beans', stamp: 'clock',
        check: ctx => bagFirstUseAgesDays(ctx.shots, ctx.beans).some(age => age >= PATIENT_MIN_REST_DAYS) },
    { id: 'rested', card: 'beans', stamp: 'moon',
        check: ctx => bagFirstUseAgesDays(ctx.shots, ctx.beans)
            .some(age => age >= RESTED_WINDOW_MIN_DAYS && age <= RESTED_WINDOW_MAX_DAYS) },
    { id: 'altitude', card: 'beans', stamp: 'map',
        check: ctx => ctx.beans.some(b => b.altitude_m > ALTITUDE_SHG_M) },
    { id: 'bean_empty', card: 'beans', stamp: 'cup',
        // ctx.beanRemaining is precomputed in context.js via
        // LibraryService.computeBeanRemaining -- same math the library view
        // itself uses to show "X g left".
        check: ctx => ctx.beans.some(b => {
            const remaining = ctx.beanRemaining[b.id];
            return remaining !== undefined && remaining !== null && remaining <= 0;
        }) },

    // ── endurance: what only settles in over time ───────────────────────
    { id: 'shots_100', card: 'endurance', stamp: '100',
        check: ctx => ctx.shots.length >= 100,
        progressTarget: 100, progress: ctx => ctx.shots.length },
    { id: 'shots_500', card: 'endurance', stamp: '500',
        check: ctx => ctx.shots.length >= 500,
        progressTarget: 500, progress: ctx => ctx.shots.length },
    { id: 'shots_1000', card: 'endurance', stamp: '1000',
        check: ctx => ctx.shots.length >= 1000,
        progressTarget: 1000, progress: ctx => ctx.shots.length },
    { id: 'streak_7', card: 'endurance', stamp: 'flame',
        check: ctx => currentDayStreak(_shotDaySet(ctx.shots), ctx.now) >= 7,
        progressTarget: 7, progress: ctx => Math.min(currentDayStreak(_shotDaySet(ctx.shots), ctx.now), 7) },
    { id: 'streak_30', card: 'endurance', stamp: '30d',
        check: ctx => currentDayStreak(_shotDaySet(ctx.shots), ctx.now) >= 30,
        progressTarget: 30, progress: ctx => Math.min(currentDayStreak(_shotDaySet(ctx.shots), ctx.now), 30) },
    { id: 'marathon', card: 'endurance', stamp: '5x',
        check: ctx => {
            const perDay = new Map();
            for (const s of ctx.shots) {
                const key = `${s.timestamp}`.length ? new Date(s.timestamp * 1000).toDateString() : null;
                if (!key) continue;
                perDay.set(key, (perDay.get(key) || 0) + 1);
            }
            return [...perDay.values()].some(n => n >= 5);
        } },
    { id: 'night', card: 'endurance', stamp: '23',
        check: ctx => ctx.shots.some(s => localParts(s.timestamp).hour >= 23) },
    { id: 'early', card: 'endurance', stamp: 'sun',
        check: ctx => ctx.shots.some(s => localParts(s.timestamp).hour < 6) },

    // ── care: what the machine itself needs back ────────────────────────
    { id: 'maint_30', card: 'care', stamp: '30d',
        // See helpers.js's maintenanceCleanStreakDays doc comment for the
        // "reconstructed from log history + current thresholds" approximation.
        check: ctx => maintenanceCleanStreakDays(ctx) >= 30,
        progressTarget: 30, progress: ctx => Math.min(maintenanceCleanStreakDays(ctx), 30) },
    { id: 'maint_all', card: 'care', stamp: 'wrench',
        check: ctx => _maintenanceTasksDone(ctx).size >= ctx.staticMaintenanceTasks.size,
        progressTarget: 5, progress: ctx => _maintenanceTasksDone(ctx).size },
    { id: 'backflush_10', card: 'care', stamp: 'drop',
        check: ctx => ctx.maintenanceLogs.filter(l => l.task === 'backflush').length >= 10,
        progressTarget: 10, progress: ctx => Math.min(ctx.maintenanceLogs.filter(l => l.task === 'backflush').length, 10) },
    { id: 'descale', card: 'care', stamp: 'leaf',
        check: ctx => ctx.maintenanceLogs.some(l => l.task === 'descaling') },
    // Not retroactively knowable -- no past export leaves any trace anywhere.
    { id: 'backup', card: 'care', stamp: 'shield',
        check: ctx => ctx.event?.type === 'backup-exported' },
    { id: 'two_machines', card: 'care', stamp: '2',
        check: ctx => ctx.machines.length >= 2 },
    { id: 'gaggimate', card: 'care', stamp: 'gear',
        check: ctx => ctx.machines.some(m => m.type === 'gaggimate') },
    { id: 'up_to_date', card: 'care', stamp: 'bolt',
        check: ctx => !!ctx.version.latest && ctx.version.updateAvailable === false },

    // ── house: orders, stock, and everything shared ─────────────────────
    { id: 'orders_10', card: 'house', stamp: '10',
        check: ctx => ctx.orders.filter(o => o.status === 'done').length >= 10,
        progressTarget: 10, progress: ctx => Math.min(ctx.orders.filter(o => o.status === 'done').length, 10) },
    { id: 'orders_50', card: 'house', stamp: 'jug',
        check: ctx => ctx.orders.filter(o => o.status === 'done' && o.variant).length >= 50,
        progressTarget: 50, progress: ctx => Math.min(ctx.orders.filter(o => o.status === 'done' && o.variant).length, 50) },
    { id: 'menu_custom', card: 'house', stamp: 'book',
        check: ctx => ctx.menu.some(item => typeof item.id === 'string' && item.id.startsWith('m_')) },
    { id: 'guest', card: 'house', stamp: 'star',
        // "Hosted" someone else, in aggregate: more than one distinct
        // identified requester has had an order fulfilled. GLP has no
        // account system, so haUserId (the HA person who placed the order)
        // is the only identity signal available -- see this module's
        // report for why a stricter "not the app owner" reading isn't
        // computable from data GLP has.
        check: ctx => new Set(ctx.orders.filter(o => o.status === 'done' && o.haUserId).map(o => o.haUserId)).size >= 2 },
    { id: 'price_low', card: 'house', stamp: 'scale',
        check: ctx => ctx.shots.some(s => {
            const bean = resolveBeanForShot(s, ctx.beans);
            const dose = s.annotation?.dose;
            if (!bean || !(bean.price_eur > 0) || !(bean.weight > 0) || !(dose > 0)) return false;
            return (bean.price_eur / bean.weight) * dose < PRICE_LOW_THRESHOLD_EUR;
        }) },
    { id: 'restock', card: 'house', stamp: 'bean',
        check: ctx => ctx.event?.type === 'bean-changed' && ctx.event.payload?.reason === 'restock' && !!ctx.event.payload?.wasEmpty },
    { id: 'flavor_10', card: 'house', stamp: 'target',
        // Adapted from the prototype's "filled the flavor wheel for ten
        // shots" -- the flavor wheel (public-src/components/flavor-wheel.js)
        // is a display over a BEAN's flavors[] tags, not a per-shot action;
        // there is no per-shot "wheel filled" field anywhere. This counts
        // real shots brewed from a bean that actually has flavor notes
        // recorded, which is the closest fully-existing-data equivalent.
        check: ctx => _flavoredShots(ctx).length >= 10,
        progressTarget: 10, progress: ctx => Math.min(_flavoredShots(ctx).length, 10) },
    { id: 'share', card: 'house', stamp: 'map',
        // Adapted: GLP can't observe an outbound share (Web Share sheet /
        // download) once the response leaves the server -- the closest
        // observable, already-stored proxy is "attached a photo to a shot".
        check: ctx => ctx.shots.some(s => !!s.image) },

    // ── secret: found, not chased ────────────────────────────────────────
    { id: 'secret_leap_day', card: 'secret', secret: true,
        check: ctx => ctx.shots.some(s => { const p = localParts(s.timestamp); return p.month === 1 && p.day === 29; }) },
    { id: 'secret_friday_13', card: 'secret', secret: true,
        check: ctx => ctx.shots.some(s => { const p = localParts(s.timestamp); return p.weekday === 5 && p.day === 13; }) },
    { id: 'secret_witching_hour', card: 'secret', secret: true,
        check: ctx => ctx.shots.some(s => { const p = localParts(s.timestamp); return p.hour === 3 && p.minute === 33; }) },
    { id: 'secret_new_year', card: 'secret', secret: true,
        check: ctx => ctx.shots.some(s => { const p = localParts(s.timestamp); return p.month === 0 && p.day === 1 && p.hour === 0 && p.minute === 0; }) },
    { id: 'secret_palindrome_id', card: 'secret', secret: true,
        check: ctx => ctx.shots.some(s => { const n = s.nativeId ?? s.id; return n >= 100 && isPalindrome(n); }) },
    { id: 'secret_golden_shot', card: 'secret', secret: true,
        check: ctx => ctx.shots.some(s => { const r = shotRatio(s); return r != null && Math.abs(r - 1.618) < 0.005; }) },
];

// ── small memoized-per-call aggregates shared by a few checks above ──────
function _countryCodes(beans) {
    const set = new Set();
    for (const b of beans) {
        const list = (b.origins && b.origins.length) ? b.origins : (b.origin ? [{ code: b.origin }] : []);
        for (const o of list) if (o.code) set.add(o.code);
    }
    return set;
}
function _roasterNames(beans) {
    const set = new Set();
    for (const b of beans) if (b.roaster && b.roaster.trim()) set.add(b.roaster.trim().toLowerCase());
    return set;
}
function _processesCovered(beans) {
    const set = new Set();
    for (const b of beans) {
        for (const [key, keywords] of Object.entries(PROCESS_KEYWORDS)) {
            if (textMatchesAny(b.process, keywords)) set.add(key);
        }
    }
    return set;
}
function _maintenanceTasksDone(ctx) {
    const set = new Set();
    for (const l of ctx.maintenanceLogs) if (ctx.staticMaintenanceTasks.has(l.task)) set.add(l.task);
    return set;
}
function _shotDaySet(shots) {
    return new Set(shots.map(s => new Date(s.timestamp * 1000).toISOString().slice(0, 10)));
}
function _flavoredShots(ctx) {
    return ctx.shots.filter(s => {
        const bean = resolveBeanForShot(s, ctx.beans);
        return !!bean && Array.isArray(bean.flavors) && bean.flavors.length > 0;
    });
}

// Non-secret badges reachable from BADGES + the 6 secret ids' public shape
// (id/card/stamp/secret only -- name/description come from secrets.js only
// once unlocked, see routes/achievements.js).
function allBadges() {
    return BADGES;
}

function isRetired(badge) {
    return !!badge.retired;
}

module.exports = {
    CARD_KEYS, BADGES, SECRET_IDS, allBadges, isRetired, getSecretCopy,
};
