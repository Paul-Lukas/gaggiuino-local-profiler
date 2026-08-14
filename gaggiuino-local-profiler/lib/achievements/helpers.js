// #812: small pure helpers shared by lib/achievements/registry.js's `check`
// functions. Kept separate from the registry itself so the registry stays
// readable as a data list (one entry per badge), not a wall of math.
const { MAX_SHOT_ID, isGlobalMaintenanceTask } = require('../constants');
const { DEMO_ID_BASE } = require('../demo-seed');

// Real shot ids never exceed MAX_SHOT_ID (see lib/constants.js's own
// comment on the multi-machine synthetic-id scheme); demo shots are
// namespaced far above it. Same idiom ShotService.importShots() already
// uses to keep bogus/oversized ids out.
function isDemoShot(shot) {
    return !shot || shot.id > MAX_SHOT_ID;
}

// Demo beans/recipes use DEMO_ID_BASE + small offsets (lib/demo-seed.js);
// real beans/recipes use Date.now()-based ids, which have been >
// DEMO_ID_BASE * 2 for every date since 2001. A generous range check keeps
// this correct without hardcoding the exact demo offsets here.
function isDemoLibraryId(id) {
    return typeof id === 'number' && id >= DEMO_ID_BASE && id < DEMO_ID_BASE * 2;
}

function stddev(vals) {
    if (!vals || vals.length < 2) return 0;
    const m = vals.reduce((a, b) => a + b, 0) / vals.length;
    return Math.sqrt(vals.reduce((a, b) => a + (b - m) ** 2, 0) / vals.length);
}

// Mirrors public-src/utils.js's detectPhases() -- reimplemented here rather
// than imported because lib/ is CommonJS and public-src/ is an ESM Vite
// source tree (same reasoning lib/score.js's own _detectChanneling()
// duplication comment gives for not sharing across that boundary).
// Returns preinfusion duration in seconds, or null when no clear
// preinfusion→extraction transition is detectable.
function detectPreinfusionSeconds(times, pressures) {
    if (!times?.length || !pressures || pressures.length < 5) return null;
    const THRESH = 3.5;
    let endIdx = -1;
    for (let i = 0; i < pressures.length; i++) {
        if (times[i] >= 1 && pressures[i] >= THRESH) { endIdx = i; break; }
    }
    if (endIdx <= 0) return null;
    const preinfusion = times[endIdx];
    return preinfusion >= 1.5 ? preinfusion : null;
}

// True when some >=windowSec-long stretch of the shot holds pressure within
// +/-tolerance bar. O(n^2) worst case over a single shot's datapoints
// (typically a few hundred samples) -- fine for a check that only runs
// while the badge is still locked.
function hasPressurePlateau(times, pressures, windowSec, tolerance) {
    if (!times?.length || times.length !== pressures.length) return false;
    let lo = 0;
    for (let hi = 0; hi < times.length; hi++) {
        while (times[hi] - times[lo] > windowSec) lo++;
        if (times[hi] - times[lo] >= windowSec) {
            const win = pressures.slice(lo, hi + 1);
            const mn = Math.min(...win), mx = Math.max(...win);
            if (mx - mn <= tolerance) return true;
        }
    }
    return false;
}

// Final brewed weight in grams, same convention as lib/score.js.
function finalWeightG(shot) {
    const d = shot.datapoints || {};
    const wArr = d.shotWeight || d.weight || [];
    return wArr.length ? Math.max(...wArr.map(v => v / 10)) : 0;
}

// Dose -> yield ratio (e.g. 2.0 for a 1:2), or null when dose/weight aren't
// both known.
function shotRatio(shot) {
    const dose = shot.annotation?.dose;
    const w = finalWeightG(shot);
    if (!dose || dose <= 0 || !w) return null;
    return w / dose;
}

// beanId-first, coffee-name fallback -- same resolution order as
// LibraryService/public-src's resolveBeanForAnnotation, applied here against
// the beans array already loaded into the achievements context.
function resolveBeanForShot(shot, beans) {
    const ann = shot.annotation || {};
    if (ann.beanId != null) {
        const byId = beans.find(b => b.id === ann.beanId);
        if (byId) return byId;
    }
    if (!ann.coffee) return null;
    const key = String(ann.coffee).toLowerCase();
    return beans.find(b => String(b.name || '').toLowerCase() === key) || null;
}

// Which bag of `bean` was open at `shotTimestampSec` -- mirrors
// public-src/views/shots/utils.js's _roastDateFromLibrary bag resolution.
function bagAtShotTime(bean, shotTimestampSec) {
    const bags = Array.isArray(bean.bags) ? bean.bags : [];
    if (!bags.length) return null;
    const shotMs = shotTimestampSec * 1000;
    const candidates = bags.filter(b => (b.openedAt || 0) <= shotMs);
    return (candidates.length ? candidates : bags).sort((a, b) => (b.openedAt || 0) - (a.openedAt || 0))[0];
}

// Roast date (parsed to a Date, or null) that applied to `bean` at the time
// `shot` was pulled -- bag-aware when bags exist, falling back to the bean's
// own top-level roastDate otherwise.
function roastDateAtShot(bean, shot) {
    const bag = bagAtShotTime(bean, shot.timestamp);
    const raw = bag?.roastDate || bean.roastDate;
    if (!raw) return null;
    const d = new Date(raw);
    return Number.isNaN(d.getTime()) ? null : d;
}

// Longest run of consecutive local calendar days (YYYY-MM-DD) in `dateSet`
// that ends on `now`'s date or the day before it -- an activity streak
// that's still "alive". Returns 0 when today/yesterday isn't in the set at
// all (streak broken).
function currentDayStreak(dateSet, now = Date.now()) {
    const dayMs = 86400000;
    const todayKey = new Date(now).toISOString().slice(0, 10);
    const yesterdayKey = new Date(now - dayMs).toISOString().slice(0, 10);
    let cursor;
    if (dateSet.has(todayKey)) cursor = new Date(now);
    else if (dateSet.has(yesterdayKey)) cursor = new Date(now - dayMs);
    else return 0;
    let streak = 0;
    for (;;) {
        const key = cursor.toISOString().slice(0, 10);
        if (!dateSet.has(key)) break;
        streak++;
        cursor = new Date(cursor.getTime() - dayMs);
    }
    return streak;
}

// Keyword match, case/diacritic-insensitive, against a free-text bean field
// (process/notes). Small curated lists, same "best-effort prose matching"
// spirit as lib/flavor-terms.js/lib/coffee-countries.js -- not exhaustive,
// just enough to catch how roasters actually word these terms.
function textMatchesAny(text, keywords) {
    if (!text) return false;
    const norm = String(text).toLowerCase();
    return keywords.some(k => norm.includes(k));
}

// "Days without an overdue maintenance task" (maint_30), computed purely
// from maintenance_log history + the CURRENT thresholds -- there's no daily
// snapshot stored anywhere (the achievements table only holds id/
// unlocked_at/progress, deliberately no room for a per-day ledger), so this
// re-derives the most recent moment any day-based task would have been
// overdue by applying today's threshold_days retroactively across the log's
// own gaps. A threshold changed after the fact is treated as if it always
// applied -- a documented approximation, not a stored history rewrite.
// Only tasks with threshold_days set are considered (backflush's default
// threshold is shot-count based and has no "days" framing to measure here).
// Returns 0 when at least one qualifying task is currently overdue, or when
// nothing qualifying has ever been logged.
function maintenanceCleanStreakDays(ctx) {
    const { maintenanceLogs, maintenanceConfigByMachine, machines, staticMaintenanceTasks, now } = ctx;
    let latestResetMs = null;

    for (const task of staticMaintenanceTasks) {
        const global = isGlobalMaintenanceTask(task);
        const scopeIds = global ? [machines[0]?.id ?? 1] : machines.map(m => m.id);
        for (const machineId of scopeIds) {
            const thresholdDays = maintenanceConfigByMachine[machineId]?.[task]?.threshold_days;
            if (!thresholdDays) continue;
            const stamps = maintenanceLogs
                .filter(l => l.task === task && (global || l.machineId === machineId))
                .map(l => l.ts * 1000)
                .sort((a, b) => a - b);
            if (!stamps.length) continue;

            let reset = stamps[0];
            for (let i = 1; i < stamps.length; i++) {
                if ((stamps[i] - stamps[i - 1]) / 86400000 > thresholdDays) reset = stamps[i];
            }
            const currentGapDays = (now - stamps[stamps.length - 1]) / 86400000;
            if (currentGapDays > thresholdDays) reset = now;

            if (latestResetMs === null || reset > latestResetMs) latestResetMs = reset;
        }
    }

    if (latestResetMs === null) return 0;
    return Math.floor((now - latestResetMs) / 86400000);
}

// For every (bean, bag) pair that's actually been brewed at least once,
// the age (in days) of the bag's roast date at its EARLIEST use -- i.e. how
// long the bean rested before the bag was opened, not how long it's rested
// by now. Powers "patient"/"rested": both are about not rushing a fresh
// bag, which only the *first* shot on it can tell you (a bag brewed on day
// 1 and again on day 20 must not count as "rested 20 days" -- it was opened
// on day 1). Beans without bags fall back to the bean's own top-level
// roastDate/shots as a single implicit "bag".
function bagFirstUseAgesDays(shots, beans) {
    const byBean = new Map();
    for (const shot of shots) {
        const bean = resolveBeanForShot(shot, beans);
        if (!bean) continue;
        if (!byBean.has(bean.id)) byBean.set(bean.id, []);
        byBean.get(bean.id).push(shot);
    }

    const ages = [];
    for (const [beanId, beanShots] of byBean) {
        const bean = beans.find(b => b.id === beanId);
        const bags = Array.isArray(bean.bags) && bean.bags.length ? bean.bags : [null];
        for (const bag of bags) {
            const shotsForBag = bag
                ? beanShots.filter(s => bagAtShotTime(bean, s.timestamp) === bag)
                : beanShots;
            if (!shotsForBag.length) continue;
            const earliest = shotsForBag.reduce((a, b) => (a.timestamp < b.timestamp ? a : b));
            const raw = bag?.roastDate || bean.roastDate;
            if (!raw) continue;
            const roast = new Date(raw);
            if (Number.isNaN(roast.getTime())) continue;
            ages.push((earliest.timestamp * 1000 - roast.getTime()) / 86400000);
        }
    }
    return ages;
}

module.exports = {
    isDemoShot, isDemoLibraryId,
    stddev, detectPreinfusionSeconds, hasPressurePlateau,
    finalWeightG, shotRatio, resolveBeanForShot, bagAtShotTime, roastDateAtShot,
    currentDayStreak, textMatchesAny, maintenanceCleanStreakDays, bagFirstUseAgesDays,
};
