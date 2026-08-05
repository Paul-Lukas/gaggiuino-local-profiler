const express       = require('express');
const router         = express.Router();
const fs             = require('fs');
const path           = require('path');
const shotService    = require('../lib/services/ShotService');
const shotRepo       = require('../lib/repositories/ShotRepository');
const libService     = require('../lib/services/LibraryService');
const libraryRepo    = require('../lib/repositories/LibraryRepository');
const orderRepo      = require('../lib/repositories/OrderRepository');
const registry       = require('../lib/machines/registry');
const mqttSettingsRepo   = require('../lib/repositories/MqttSettingsRepository');
const importSettingsRepo = require('../lib/repositories/ImportSettingsRepository');
const { loadMenu, saveMenu, loadOrdersSettings, saveOrdersSettings,
        loadNotifyMapping, saveNotifyMapping } = require('../lib/data');
const { getDb }                    = require('../lib/db');
const { GLP_VERSION, MAX_SHOT_ID, BEAN_IMAGE_DIR, BEAN_IMAGE_MAX_BYTES, TOKEN_FILE } = require('../lib/constants');
const { log, rateLimit, writeFileSafe } = require('../lib/helpers');
const { annotationSchema, maintenanceLogSchema } = require('../lib/validation/schemas');
const { sanitizeBeanFields, sanitizeGrinderFields, sanitizeRecipeFields,
        sanitizeMilkFields, sanitizeBasketFields, sanitizePuckScreenFields } = require('../lib/sanitize-bean');
const { imagePath, imageFilename, matchesImageMagicBytes, CONTENT_TYPE_EXT } = require('../lib/services/ImageService');
const { encryptSecrets, decryptSecrets } = require('../lib/backup-crypto');
const state = require('../lib/state');

// The library entity types that can carry an uploaded image, and the
// filename prefix each uses under BEAN_IMAGE_DIR (see routes/library/*.js).
// Export no longer depends on this list (see buildBackupBundle()'s directory
// scan) -- restore still does, deliberately: writing a restored image file
// is only ever allowed for a filename an actually-restored entity claims via
// its own id + prefix, which is the path-traversal/integrity guard below.
// Shots aren't in this list -- their images validate separately in the
// restore transaction (prefix 'shot-', id = shot.id), since shots live at
// the backup's top level, not nested under coffee_library like these do.
const IMAGE_ENTITY_TYPES = [
    ['beans', ''],
    ['grinders', 'grinder-'],
    ['baskets', 'basket-'],
    ['puckScreens', 'puckscreen-'],
];

// A restored coffee_library bypasses the regular POST/PUT bean/grinder/recipe
// routes entirely (it's written straight to the DB), so it never went through
// their field sanitizers — a crafted backup could otherwise inject
// unsanitized strings (e.g. into bean.notes/flavors) that later render
// unescaped in the frontend. Re-run the same per-field sanitizers here.
//
// #635: milks used to be missing from this list entirely (bug/inconsistency
// — every other library entity was already covered); fixed alongside adding
// baskets/puckScreens rather than leaving it for a separate round.
function sanitizeRestoredLibrary(lib) {
    if (!lib || typeof lib !== 'object') return lib;
    return {
        ...lib,
        beans:       Array.isArray(lib.beans)       ? lib.beans.map(sanitizeBeanFields)             : lib.beans,
        grinders:    Array.isArray(lib.grinders)    ? lib.grinders.map(sanitizeGrinderFields)        : lib.grinders,
        recipes:     Array.isArray(lib.recipes)     ? lib.recipes.map(sanitizeRecipeFields)          : lib.recipes,
        milks:       Array.isArray(lib.milks)       ? lib.milks.map(sanitizeMilkFields)               : lib.milks,
        baskets:     Array.isArray(lib.baskets)     ? lib.baskets.map(sanitizeBasketFields)           : lib.baskets,
        puckScreens: Array.isArray(lib.puckScreens) ? lib.puckScreens.map(sanitizePuckScreenFields)   : lib.puckScreens,
    };
}

// Validates one entity list's id/image fields against the actual restored
// `images` blob and pushes a {path, buffer} entry onto pendingImageWrites for
// each image that survives every check — this is the actual path-traversal
// guard. Callers' sanitizers (sanitizeBeanFields/sanitizeGrinderFields/etc.,
// and the raw shot objects on the restore path) deliberately never touch
// `.id`/`.image`, so both arrive here as fully attacker-controlled strings
// straight from the backup JSON; neither is ever used to build a filesystem
// path until both pass every check below. Any entity whose image fails
// validation for any reason (including simply having no matching key in
// `imagesMap`, i.e. every backup from before images were included at all)
// has its `.image` field cleared rather than left pointing at a file that
// will never exist.
function validateEntityImages(list, prefix, imagesMap, pendingImageWrites) {
    for (const entity of Array.isArray(list) ? list : []) {
        if (!entity || !entity.image) continue;
        const ext = entity.image;
        if (!Object.values(CONTENT_TYPE_EXT).includes(ext)) { entity.image = null; continue; }
        const id = entity.id;
        if (!Number.isInteger(id) || id <= 0) { entity.image = null; continue; }
        const filename = imageFilename(id, ext, prefix);
        const value = imagesMap && typeof imagesMap === 'object' ? imagesMap[filename] : undefined;
        if (typeof value !== 'string') { entity.image = null; continue; }
        let buffer;
        try { buffer = Buffer.from(value, 'base64'); } catch { entity.image = null; continue; }
        if (!buffer.length || buffer.length > BEAN_IMAGE_MAX_BYTES) { entity.image = null; continue; }
        if (!matchesImageMagicBytes(buffer, ext)) { entity.image = null; continue; }
        pendingImageWrites.push({ path: imagePath(id, ext, prefix), buffer });
    }
}

// One call per library entity type (beans/grinders/baskets/puckScreens) —
// shot images validate separately via validateEntityImages(b.shots, 'shot-',
// ...) directly in the restore transaction, since shots aren't nested under
// coffee_library.
function validateRestoredLibraryImages(lib, imagesMap, pendingImageWrites) {
    if (!lib || typeof lib !== 'object') return;
    for (const [key, prefix] of IMAGE_ENTITY_TYPES) {
        validateEntityImages(lib[key], prefix, imagesMap, pendingImageWrites);
    }
}

// Loosely validates one raw `maintenance` export row ({machineId, key, data})
// on restore. `data`'s shape follows MAINTENANCE_DEFAULTS as a guide (not a
// rigid schema, since per-grinder keys aren't in that constant) — nested
// string fields are length-capped defensively, everything else whitelisted
// to number/string/null.
function sanitizeMaintenanceRow(r) {
    if (!r || typeof r !== 'object') return null;
    if (!Number.isInteger(r.machineId) || r.machineId <= 0) return null;
    if (typeof r.key !== 'string' || !r.key.trim() || r.key.length > 100) return null;
    if (!r.data || typeof r.data !== 'object' || Array.isArray(r.data)) return null;
    const data = {};
    for (const [k, v] of Object.entries(r.data)) {
        if (typeof k !== 'string' || k.length > 50) continue;
        if (v === null || (typeof v === 'number' && Number.isFinite(v))) { data[k] = v; continue; }
        if (typeof v === 'string') { data[k] = v.slice(0, 500); continue; }
        // booleans/objects/arrays aren't part of any known maintenance task's shape — dropped.
    }
    return { machineId: r.machineId, key: r.key.trim().slice(0, 100), data };
}

function sanitizeMaintenanceLogRow(r) {
    if (!r || typeof r !== 'object') return null;
    if (!Number.isFinite(r.id) || !Number.isFinite(r.ts)) return null;
    if (typeof r.date !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(r.date)) return null;
    const parsed = maintenanceLogSchema.safeParse({ task: r.task, notes: r.notes, machine: r.machine });
    if (!parsed.success) return null;
    return {
        id: r.id, ts: r.ts, date: r.date,
        task: parsed.data.task, machine: parsed.data.machine, notes: parsed.data.notes,
        shotCount: Number.isFinite(r.shotCount) ? r.shotCount : 0,
        machineId: Number.isInteger(r.machineId) && r.machineId > 0 ? r.machineId : 1,
    };
}

const ORDER_STATUSES = ['pending', 'accepted', 'done', 'declined'];
function _str(v, max) { return typeof v === 'string' ? v.slice(0, max) : null; }
function _num(v)      { return typeof v === 'number' && Number.isFinite(v) ? v : null; }

// Loosely validates one raw order row on restore — mirrors the field set/
// length caps POST /api/orders and its lifecycle actions (accept/complete/
// decline, see lib/services/OrderService.js) already accept, since this is a
// round-trip of the same shape rather than a new order being placed.
function sanitizeOrderRow(o) {
    if (!o || typeof o !== 'object') return null;
    if (typeof o.id !== 'string' || !o.id.trim() || o.id.length > 100) return null;
    if (!ORDER_STATUSES.includes(o.status)) return null;
    return {
        id:            o.id.trim().slice(0, 100),
        status:        o.status,
        item:          _str(o.item, 100) ?? '',
        customer:      _str(o.customer, 50) ?? '',
        note:          _str(o.note, 200) ?? '',
        variant:       o.variant != null ? _str(o.variant, 50) : null,
        notifyService: o.notifyService != null ? _str(o.notifyService, 100) : null,
        declineReason: o.declineReason != null ? _str(o.declineReason, 200) : null,
        haUserId:      o.haUserId != null ? _str(o.haUserId, 100) : null,
        machine:       o.machine != null ? _str(o.machine, 100) : null,
        createdAt:     _num(o.createdAt) ?? Date.now(),
        completedAt:   _num(o.completedAt),
        acceptedAt:    _num(o.acceptedAt),
        eta:           _num(o.eta),
        machineId:     Number.isInteger(o.machineId) && o.machineId > 0 ? o.machineId : 1,
        beanId:        Number.isInteger(o.beanId) ? o.beanId : null,
        shotId:        Number.isInteger(o.shotId) ? o.shotId : null,
    };
}

// Six independently selectable backup domains -- the same set is used for
// export scope selection and restore scope selection, so a user picks
// between identical options on both ends (matches the "Restore Settings
// only" vs. "Restore Maintenance only" request the backup-completeness fix
// this file belongs to was filed alongside).
//
// 'shots' is deliberately one bucket covering shots/annotations/trash/
// blocklist/coffee_library/images rather than several finer-grained toggles:
// annotations and trash both key off shot ids, and shots reference library
// entities (beans/grinders/...) by id, so splitting library out from shots
// would let a restore recreate a shot whose bean was never restored.
const BACKUP_SECTIONS = ['shots', 'maintenance', 'orders', 'machines', 'settings', 'secrets'];

const SECTION_BUNDLE_KEYS = {
    shots:       ['shots', 'annotations', 'coffee_library', 'blocklist', 'trash', 'images'],
    maintenance: ['maintenance', 'maintenance_log'],
    orders:      ['orders'],
    machines:    ['machines'],
    settings:    ['kv'],
    secrets:     ['secrets'],
};

// `raw` is the caller-supplied `sections` field (export request body or a
// restore's own bundle). Three distinct outcomes:
//   undefined / not an array  -> null            ("all sections" -- the
//                                                  original, still-default
//                                                  behavior, and what keeps
//                                                  every pre-existing script/
//                                                  test/backup file working
//                                                  unchanged)
//   []                        -> empty Set        (caller explicitly chose
//                                                  nothing -- respected as-is,
//                                                  not silently upgraded to
//                                                  "all")
//   ['maintenance', 'orders'] -> Set{those two}   (unknown section names are
//                                                  dropped rather than
//                                                  rejected, so a future
//                                                  section name a newer
//                                                  export adds doesn't 400 an
//                                                  older client's request)
function normaliseSections(raw) {
    if (!Array.isArray(raw)) return null;
    return new Set(raw.filter(s => BACKUP_SECTIONS.includes(s)));
}

// Shared by GET and POST /api/backup below. `passphrase` is only ever
// non-null on the POST path (see there for why GET can never carry one) and
// gates whether an encrypted `secrets` block (API token + MQTT credentials)
// is appended to the bundle -- omitting it entirely reproduces the exact
// output the plain GET route has always returned. `sections`: null for a
// full export (legacy/default), or a Set from normaliseSections() to
// restrict the bundle to only those domains.
function buildBackupBundle(passphrase, sections) {
    // findAll() (not the trash-excluding getAll()) — a trashed shot's full
    // payload must be part of the export, or the recycle bin is
    // unrecoverable after a restore (the bug this fixes).
    const shots = shotRepo.findAll();
    const trash = shotService.getTrash();
    const annotationsObj = Object.fromEntries(
        shots.map(s => [String(s.id), s.annotation]).filter(([, a]) => a && Object.keys(a).length)
    );
    const trashObj = Object.fromEntries(
        trash.map(s => [String(s.id), shotRepo.getTrashEntry(s.id) ?? Date.now()])
    );

    const lib = libService.getLibrary();
    // Reads BEAN_IMAGE_DIR directly rather than deriving the file list from
    // "each library entity type that can carry a photo" (the previous
    // approach): that list had to be updated by hand every time a new
    // photo-bearing entity type was added, and silently missed shot photos
    // entirely -- reported after they showed up fine in the Library/shot
    // view but never appeared in an export. BEAN_IMAGE_DIR is a single flat
    // directory every entity type's photo upload writes into (see
    // lib/services/ImageService.js), so scanning it can't miss a category
    // again, current or future, without needing to know what a "shot" or a
    // "bean" even is.
    const images = {};
    if (fs.existsSync(BEAN_IMAGE_DIR)) {
        for (const filename of fs.readdirSync(BEAN_IMAGE_DIR)) {
            const filePath = path.join(BEAN_IMAGE_DIR, filename);
            try {
                if (!fs.statSync(filePath).isFile()) continue;
                images[filename] = fs.readFileSync(filePath).toString('base64');
            } catch { /* best-effort, matches this codebase's existing image-handling style */ }
        }
    }

    // MQTT broker credentials are deliberately excluded from the plaintext
    // export: this JSON file routinely ends up in Downloads/cloud backups,
    // and a plaintext broker password sitting there is not an acceptable
    // trade-off for restore convenience. Do not "fix" this back to a raw kv
    // dump — MqttSettingsRepository.saveSettings() merges into the
    // *currently stored* settings rather than overwriting wholesale, which
    // is what lets a locally configured password survive a restore from a
    // backup that never had one. A passphrase-encrypted copy travels
    // separately, in `secrets` below.
    const { username: _mqttUser, password: _mqttPass, ...safeMqttSettings } = mqttSettingsRepo.getSettings();

    // Every domain is gathered unconditionally above and filtered down to the
    // requested sections at the very end (rather than skipping the DB reads
    // for deselected sections) — trivial cost for a Settings-page action on a
    // home-sized dataset, and it keeps this function's data-gathering half
    // simple and single-purpose instead of threading `sections` through every
    // branch of it.
    const fullBundle = {
        glp_backup:      true,
        version:         GLP_VERSION,
        created:         new Date().toISOString(),
        shots:           shots.map(({ annotation: _, score: __, ...rest }) => rest),
        annotations:     annotationsObj,
        coffee_library:  lib,
        blocklist:       shotService.getBlocklist(),
        trash:           trashObj,
        maintenance:     libraryRepo.getAllMaintenanceRaw(),
        maintenance_log: libraryRepo.getAllMaintenanceLogRaw(),
        orders:          orderRepo.findAll(),
        machines:        registry.listMachines(),
        kv: {
            menu:            loadMenu(),
            orders_settings: loadOrdersSettings(),
            notify_mapping:  loadNotifyMapping(),
            import_settings: importSettingsRepo.getSettings(),
            mqtt_settings:   safeMqttSettings,
        },
        images,
    };

    // The API token grants full API access (including this very restore
    // endpoint) and the MQTT username/password are real infrastructure
    // credentials -- both are withheld from every plaintext field above and
    // only ever included, encrypted, when the caller opted in with a
    // passphrase. `secrets` is entirely absent (not an empty object) when
    // there is nothing worth encrypting, so an old-format-compatible reader
    // sees no difference from a backup with no secrets at all. Computed even
    // when 'secrets' isn't a requested section, since the section filter
    // below is what actually decides whether it ends up in the response --
    // one code path, not two.
    if (passphrase) {
        const rawMqtt = mqttSettingsRepo.getSettings();
        const secretPayload = {};
        if (state.apiToken) secretPayload.apiToken = state.apiToken;
        if (rawMqtt.username || rawMqtt.password) {
            secretPayload.mqtt = { username: rawMqtt.username, password: rawMqtt.password };
        }
        if (Object.keys(secretPayload).length) {
            fullBundle.secrets = encryptSecrets(secretPayload, passphrase);
        }
    }

    if (sections === null) return fullBundle;

    const bundle = { glp_backup: true, version: fullBundle.version, created: fullBundle.created, sections: [...sections] };
    for (const section of sections) {
        for (const key of SECTION_BUNDLE_KEYS[section] || []) {
            if (key in fullBundle) bundle[key] = fullBundle[key];
        }
    }
    return bundle;
}

function sendBackup(res, passphrase, sections) {
    const bundle   = buildBackupBundle(passphrase, sections);
    const filename = `glp-backup-${new Date().toISOString().slice(0, 10)}.json`;
    res.setHeader('Content-Disposition', `attachment; filename="${filename}"`);
    res.json(bundle);
}

router.get('/api/backup', (req, res, next) => {
    try {
        sendBackup(res, null, null);
    } catch (err) { next(err); }
});

// A passphrase must never travel in a URL (query strings end up in access
// logs, proxy logs and browser history), so including one is only possible
// via this POST variant's JSON body. GET above stays the plain, secrets-free,
// all-sections export for any existing bookmark/tooling and needs no request
// body at all.
router.post('/api/backup', (req, res, next) => {
    try {
        const passphrase = typeof req.body?.passphrase === 'string' && req.body.passphrase ? req.body.passphrase : null;
        sendBackup(res, passphrase, normaliseSections(req.body?.sections));
    } catch (err) { next(err); }
});

router.post('/api/restore', (req, res, next) => {
    // A dry run is read-only preview traffic the modal fires on every
    // section-checkbox toggle and passphrase keystroke (debounced, but still
    // several calls per interaction) — sharing the real restore's 3/min limit
    // meant just opening the modal and ticking a couple of boxes could 429
    // before the user ever clicked "Restore" (reported by Max). Real restores
    // stay tightly capped, since they wipe and replace live data; the dry-run
    // limit only needs to bound abuse, not user interaction speed.
    const isDryRun = req.body?.dryRun === true;
    const limitOk  = isDryRun
        ? rateLimit(`restore-preview:${req.ip}`, 30)
        : rateLimit(`restore:${req.ip}`, 3);
    if (!limitOk) return res.status(429).json({ error: 'Rate limit exceeded' });
    try {
        const b = req.body;
        if (!b || b.glp_backup !== true || !Array.isArray(b.shots))
            return res.status(400).json({ error: 'Invalid backup file' });
        if (b.shots.length > MAX_SHOT_ID)
            return res.status(400).json({ error: `Backup contains too many shots (max ${MAX_SHOT_ID})` });

        // sections: which of the six domains (see BACKUP_SECTIONS above) to
        // actually apply. null = every domain present in the file, the
        // original/default behavior. A file's own top-level `sections` field
        // (written by an export that itself used a subset) is the fallback
        // when the restore request doesn't specify one explicitly, so
        // re-uploading a scoped export without picking anything on the
        // restore side still only touches what it was scoped to on export.
        const sections = normaliseSections(req.body?.sections !== undefined ? req.body.sections : b.sections);
        const wantsShots = sections === null || sections.has('shots');
        const dryRun     = req.body?.dryRun === true;

        // Per-shot validation only matters for data that will actually be
        // applied — if 'shots' isn't a selected section, garbage in an
        // array that's about to be ignored must not block restoring
        // everything else the caller did ask for.
        if (wantsShots) {
            for (let i = 0; i < b.shots.length; i++) {
                const s = b.shots[i];
                if (s === null || typeof s !== 'object')
                    return res.status(400).json({ error: `Backup shot #${i} is not a valid object` });
                if (!Number.isInteger(s.id) || s.id <= 0)
                    return res.status(400).json({ error: `Backup shot #${i} has an invalid id (${s.id})` });
                if (typeof s.timestamp !== 'number')
                    return res.status(400).json({ error: `Backup shot #${i} (id=${s.id}) has an invalid or missing timestamp` });
            }
        }

        // Decryption is pure in-memory work (no DB/filesystem side effects),
        // so it happens up front rather than inside the transaction below.
        // A wrong or missing passphrase must never fail the whole restore --
        // everything else still applies -- so this only ever downgrades to
        // "secrets not restored", reported back in the response so the UI can
        // tell the user their token/MQTT login specifically didn't come back.
        // Successful decryption is not, on its own, proof the values are
        // safe to use as-is: whoever can call this authenticated endpoint at
        // all already holds a valid API token (see the trust model above
        // GET /api/token) and could pick their own passphrase for a crafted
        // blob, so the decrypted apiToken is still bounded/sanitised below
        // exactly like every other restored field in this file.
        const wantsSecrets = sections === null || sections.has('secrets');
        const passphrase   = typeof req.body?.passphrase === 'string' && req.body.passphrase ? req.body.passphrase : null;
        const secretsPresent   = wantsSecrets && !!(b.secrets && typeof b.secrets === 'object');
        const decryptedSecrets = secretsPresent && passphrase ? decryptSecrets(b.secrets, passphrase) : null;
        const secretsRestored  = decryptedSecrets !== null;

        // Every "what would actually be written" computation happens here,
        // before any DB/filesystem mutation and identically whether or not
        // this is a dry run -- a dry run's preview counts and the real
        // restore's applied counts can never drift apart, because they're
        // the same numbers.
        const pendingImageWrites = [];
        let sanitizedLib = null;
        if (wantsShots) {
            if (b.coffee_library) {
                sanitizedLib = sanitizeRestoredLibrary(b.coffee_library);
                validateRestoredLibraryImages(sanitizedLib, b.images, pendingImageWrites);
            }
            // Shot photos: same validation as library images, just not
            // nested under coffee_library -- each shot's own `.image`
            // field is mutated in place here, before shotService.upsertShot()
            // writes these same objects further down.
            validateEntityImages(b.shots, 'shot-', b.images, pendingImageWrites);
        }
        const validMaintenance = (sections === null || sections.has('maintenance')) && Array.isArray(b.maintenance)
            ? b.maintenance.map(sanitizeMaintenanceRow).filter(Boolean) : [];
        const validMaintenanceLog = (sections === null || sections.has('maintenance')) && Array.isArray(b.maintenance_log)
            ? b.maintenance_log.map(sanitizeMaintenanceLogRow).filter(Boolean) : [];
        const validOrders = (sections === null || sections.has('orders')) && Array.isArray(b.orders)
            ? b.orders.map(sanitizeOrderRow).filter(Boolean) : [];
        const wantsMachines = (sections === null || sections.has('machines')) && Array.isArray(b.machines);
        const wantsSettings = sections === null || sections.has('settings');
        const restoredToken = typeof decryptedSecrets?.apiToken === 'string'
            ? decryptedSecrets.apiToken.replace(/[\r\n\0]/g, '').trim().slice(0, 200) : '';

        if (dryRun) {
            return res.json({
                ok: true, dryRun: true,
                preview: {
                    shots:            wantsShots ? b.shots.length : 0,
                    library:          wantsShots && b.coffee_library ? true : false,
                    maintenance:      validMaintenance.length,
                    maintenanceTotal: Array.isArray(b.maintenance) ? b.maintenance.length : 0,
                    maintenanceLog:      validMaintenanceLog.length,
                    maintenanceLogTotal: Array.isArray(b.maintenance_log) ? b.maintenance_log.length : 0,
                    orders:      validOrders.length,
                    ordersTotal: Array.isArray(b.orders) ? b.orders.length : 0,
                    machines:    wantsMachines ? b.machines.length : 0,
                    settings:    wantsSettings && !!b.kv,
                    images:      pendingImageWrites.length,
                    secretsPresent, secretsRestored,
                },
            });
        }

        // Single atomic transaction over the whole restore (wipe + re-insert +
        // library/blocklist/maintenance/orders/machines/kv) — shotRepo.wipeAll()
        // and the other repos' write methods each run their own db.transaction()
        // internally, which better-sqlite3 nests as a SAVEPOINT when already
        // inside this outer one, so the guarantee is unchanged: a failure
        // anywhere below rolls back the whole restore, including the wipe.
        // Every write is gated by the same section checks the preview above
        // used, so a deselected domain is left completely untouched rather
        // than wiped-then-left-empty.
        getDb().transaction(() => {
            if (wantsShots) {
                shotRepo.wipeAll();

                for (const shot of b.shots) shotService.upsertShot(shot);
                if (b.annotations && typeof b.annotations === 'object') {
                    for (const [id, ann] of Object.entries(b.annotations)) {
                        const parsed = annotationSchema.safeParse(ann);
                        if (parsed.success) shotService.saveAnnotation(parseInt(id), parsed.data);
                    }
                }

                // Trash: skip any entry whose shot id isn't among the shots
                // that were just restored above — defensive, and also what
                // makes a backup whose own trash refers to shot ids absent
                // from its own `shots` array (a real bug in pre-fix exports)
                // restore cleanly instead of creating a dangling trash row.
                if (b.trash && typeof b.trash === 'object' && !Array.isArray(b.trash)) {
                    const restoredShotIds = new Set(b.shots.map(s => s.id));
                    for (const [idStr, deletedAtRaw] of Object.entries(b.trash)) {
                        const id = parseInt(idStr, 10);
                        if (!Number.isInteger(id) || !restoredShotIds.has(id)) continue;
                        const deletedAt = Number.isFinite(deletedAtRaw) ? deletedAtRaw : Date.now();
                        shotRepo.setTrashEntry(id, deletedAt);
                    }
                }

                if (sanitizedLib) libService.saveLibrary(sanitizedLib);
                if (Array.isArray(b.blocklist)) shotService.saveBlocklist(b.blocklist.map(Number));
            }

            if (sections === null || sections.has('maintenance')) {
                if (Array.isArray(b.maintenance))     libraryRepo.restoreMaintenanceRaw(validMaintenance);
                if (Array.isArray(b.maintenance_log)) libraryRepo.restoreMaintenanceLogRaw(validMaintenanceLog);
            }
            if ((sections === null || sections.has('orders')) && Array.isArray(b.orders)) {
                orderRepo.replaceAll(validOrders);
            }
            if (wantsMachines) registry.restoreMachines(b.machines);

            if (wantsSettings && b.kv && typeof b.kv === 'object' && !Array.isArray(b.kv)) {
                if (Array.isArray(b.kv.menu)) saveMenu(b.kv.menu);
                if (b.kv.orders_settings && typeof b.kv.orders_settings === 'object') saveOrdersSettings(b.kv.orders_settings);
                if (b.kv.notify_mapping && typeof b.kv.notify_mapping === 'object') saveNotifyMapping(b.kv.notify_mapping);
                if (b.kv.import_settings && typeof b.kv.import_settings === 'object') importSettingsRepo.saveSettings(b.kv.import_settings);
                if (b.kv.mqtt_settings && typeof b.kv.mqtt_settings === 'object') {
                    // Defense in depth: strip username/password even though our
                    // own export never includes them, in case a hand-edited
                    // backup file smuggles them back in. saveSettings() merges
                    // into the currently stored settings rather than
                    // overwriting, so an existing local password already
                    // survives this regardless.
                    const { username: _u, password: _p, ...rest } = b.kv.mqtt_settings;
                    mqttSettingsRepo.saveSettings(rest);
                }
            }

            // Decrypted MQTT credentials, independent of the b.kv block above
            // (a secrets-only restore is valid) — saveSettings() merges
            // rather than overwrites, matching the plaintext path.
            if (decryptedSecrets?.mqtt && typeof decryptedSecrets.mqtt === 'object') {
                const { username, password } = decryptedSecrets.mqtt;
                mqttSettingsRepo.saveSettings({
                    username: typeof username === 'string' ? username.slice(0, 200) : '',
                    password: typeof password === 'string' ? password.slice(0, 500) : '',
                });
            }
        })();

        // Deferred until after the DB transaction commits, same reasoning as
        // the image writes below: TOKEN_FILE is a filesystem write and can't
        // roll back with the SQLite transaction. Every character that isn't
        // safe in an HTTP header value (this token round-trips through
        // X-GLP-Token on every subsequent request) was already stripped
        // above rather than rejecting the whole restore over it.
        if (restoredToken) {
            try {
                state.apiToken = restoredToken;
                writeFileSafe(TOKEN_FILE, restoredToken);
            } catch (e) {
                log(`Restore: failed to write restored API token: ${e.message}`, true);
            }
        }

        for (const { path: filePath, buffer } of pendingImageWrites) {
            try {
                fs.mkdirSync(BEAN_IMAGE_DIR, { recursive: true });
                fs.writeFileSync(filePath, buffer);
            } catch (e) {
                log(`Restore: failed to write image ${filePath}: ${e.message}`, true);
            }
        }

        log(`Restore completed from backup v${b.version || '?'} (${wantsShots ? b.shots.length : 0} shots, `
            + `${validMaintenance.length} maintenance rows, ${validMaintenanceLog.length} log entries, `
            + `${validOrders.length} orders, ${wantsMachines ? b.machines.length : 0} machines, ${pendingImageWrites.length} images)`
            + (secretsPresent ? `, secrets ${secretsRestored ? 'restored' : 'NOT restored (wrong/missing passphrase?)'}` : ''));
        res.json({
            ok: true, shots: wantsShots ? b.shots.length : 0,
            // Only meaningful when the backup actually had a `secrets` block;
            // the frontend uses secretsPresent to decide whether to even
            // mention secrets in its result message at all.
            secretsPresent, secretsRestored,
        });
    } catch (err) { next(err); }
});

module.exports = router;
