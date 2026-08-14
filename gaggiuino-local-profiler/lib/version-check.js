// GitHub-release version check, extracted from routes/system.js's GET
// /api/version handler (#812) so lib/achievements/context.js can read the
// last-known result without triggering a network call of its own --
// achievements only ever reads getCached() (sync, no I/O), never
// checkForUpdate() itself. The actual fetch still only ever happens because
// a browser tab hit /api/version (public-src/components/update-check.js on
// app load), never because of anything achievements does.
const { GLP_VERSION } = require('./constants');

let _cache = null;
let _cacheAt = 0;
const CACHE_MS = 60 * 60 * 1000;

async function checkForUpdate() {
    const now = Date.now();
    if (!_cache || now - _cacheAt > CACHE_MS) {
        try {
            const r = await fetch(
                'https://api.github.com/repos/mxkissnr/gaggiuino-local-profiler/releases/latest',
                { headers: { 'User-Agent': 'GLP-Server' }, signal: AbortSignal.timeout(8000) }
            );
            if (r.ok) {
                const data = await r.json();
                // eslint-disable-next-line require-atomic-updates -- benign cache-fill race: concurrent requests before this resolves would all compute the same value from the same GitHub release
                _cache = data.tag_name?.replace(/^v/, '') || null;
                // eslint-disable-next-line require-atomic-updates -- see above
                _cacheAt = now;
            }
        } catch { /* ignore */ }
    }
    return _result();
}

// #704: GLP_VERSION only moves at an actual release, so a dev build is
// permanently "behind" the last stable tag by design -- comparing against it
// would tell dev-channel users to update via the stable Add-on Store, which
// is wrong (there's no store listing for GLP DEV). Same dev-build-aware
// guard as the "UNSTABLE DEV BUILD" banner (#683) and /api/status's devBuild
// field.
function _result() {
    const latest = _cache;
    const updateAvailable = !process.env.GLP_DEV_BUILD && !!(latest && latest !== GLP_VERSION);
    return {
        current:         GLP_VERSION,
        latest:          latest || null,
        updateAvailable,
        releaseUrl:      'https://github.com/mxkissnr/gaggiuino-local-profiler/releases/latest',
    };
}

// Synchronous, no fetch -- returns whatever the last checkForUpdate() call
// (triggered by a real GET /api/version request) already cached. null cache
// still yields a well-shaped result with latest: null / updateAvailable:
// false, which lib/achievements' up_to_date check treats as "nothing known
// yet", never as "you're up to date".
function getCached() {
    return _result();
}

module.exports = { checkForUpdate, getCached };
