// options.json is a tracked input to the machine registry, not a live config
// source. These tests pin the two requirements that pull in opposite
// directions and that a plain read-time fallback cannot satisfy at once:
//
//   1. #643: clearing switch_entity in Settings must stick across restarts --
//      an unchanged add-on option must never resurrect the old value.
//   2. Reported on v2.29.0: setting switch_entity in the HA add-on config
//      *after* the initial ensureDefaultMachine() seed must still reach the
//      app -- previously the registry row stayed NULL forever and the power
//      button never appeared.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const fs   = require('fs');
const os   = require('os');
const path = require('path');
const Database = require('better-sqlite3');

const dbPath        = require.resolve('../lib/db');
const realDb        = require(dbPath);
const constantsPath = require.resolve('../lib/constants');
const realConstants = require(constantsPath);
const registryPath  = require.resolve('../lib/machines/registry');
const dataPath      = require.resolve('../lib/data');
const adoptionPath  = require.resolve('../lib/machines/options-adoption');

describe('options.json adoption into the machine registry', () => {
    let memDb, tmpFile;

    // Rewrites options.json and re-requires the modules that cache it, so a
    // call to boot() below models a fresh add-on start with those options.
    function writeOptions(opts) {
        fs.writeFileSync(tmpFile, JSON.stringify(opts));
        delete require.cache[registryPath];
        delete require.cache[dataPath];
        delete require.cache[adoptionPath];
    }

    // One add-on start: seed (no-op once machine #1 exists) then adopt.
    function boot() {
        require('../lib/machines/registry').ensureDefaultMachine();
        require('../lib/machines/options-adoption').adoptOptionChanges();
    }

    function machine() {
        return require('../lib/machines/registry').getDefaultMachine();
    }

    beforeEach(() => {
        memDb = new Database(':memory:');
        realDb.initSchema(memDb);
        require.cache[dbPath].exports = { getDb: () => memDb, initSchema: realDb.initSchema };

        tmpFile = path.join(os.tmpdir(), `glp-test-options-adopt-${Date.now()}-${Math.random().toString(36).slice(2)}.json`);
        require.cache[constantsPath].exports = { ...realConstants, OPTIONS_FILE: tmpFile };
    });

    afterEach(() => {
        memDb.close();
        require.cache[dbPath].exports = realDb;
        require.cache[constantsPath].exports = realConstants;
        try { fs.unlinkSync(tmpFile); } catch { /* already gone */ }
        delete require.cache[registryPath];
        delete require.cache[dataPath];
        delete require.cache[adoptionPath];
    });

    it('adopts a switch_entity set in the add-on options after the initial seed', () => {
        // Install predates the option: machine #1 is seeded with no switch.
        writeOptions({ machine_host: 'gaggiuino.local' });
        boot();
        expect(machine().switchEntity).toBe(null);

        // User now sets it in the HA add-on configuration.
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.sonoff_espresso' });
        boot();
        expect(machine().switchEntity).toBe('switch.sonoff_espresso');
    });

    it('adopts on the very first pass when the registry field is still empty', () => {
        // Upgrade case: the option was already set in options.json before this
        // module existed, so there is no kv baseline and the registry is NULL.
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.legacy' });
        memDb.prepare(
            `INSERT INTO machines (id, name, type, host, switch_entity, is_default, enabled, created_at)
             VALUES (1, 'Gaggiuino', 'gaggiuino', 'gaggiuino.local', NULL, 1, 1, ?)`
        ).run(Date.now());

        boot();
        expect(machine().switchEntity).toBe('switch.legacy');
    });

    it('does not resurrect a switch entity the user cleared in Settings (#643)', () => {
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.sonoff_espresso' });
        boot();
        expect(machine().switchEntity).toBe('switch.sonoff_espresso');

        // User clears the field in Settings -> Machines. The add-on option is
        // untouched, so the next restart must leave the clear alone.
        require('../lib/machines/registry').updateMachine(1, { switchEntity: null });
        boot();
        expect(machine().switchEntity).toBe(null);
    });

    it('lets the app-side value win over an unchanged add-on option', () => {
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.from_options' });
        boot();

        require('../lib/machines/registry').updateMachine(1, { switchEntity: 'switch.from_app' });
        boot();
        expect(machine().switchEntity).toBe('switch.from_app');
    });

    it('adopts a changed add-on option even when the app set its own value', () => {
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.from_options' });
        boot();
        require('../lib/machines/registry').updateMachine(1, { switchEntity: 'switch.from_app' });

        // Explicit edit in Home Assistant -- the most recent deliberate action
        // wins, same as any other config-reconciliation loop.
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.changed_in_ha' });
        boot();
        expect(machine().switchEntity).toBe('switch.changed_in_ha');
    });

    it('adopts a cleared add-on option as an intentional clear', () => {
        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: 'switch.sonoff_espresso' });
        boot();

        writeOptions({ machine_host: 'gaggiuino.local', switch_entity: '' });
        boot();
        expect(machine().switchEntity).toBe(null);
    });

    it('adopts a changed machine_host', () => {
        writeOptions({ machine_host: 'old-host.local' });
        boot();
        expect(machine().host).toBe('old-host.local');

        writeOptions({ machine_host: 'new-host.local' });
        boot();
        expect(machine().host).toBe('new-host.local');
    });

    it('never clears machine_host, even if the add-on option is emptied', () => {
        writeOptions({ machine_host: 'gaggiuino.local' });
        boot();

        // An empty host would leave the app with no way to reach the machine,
        // so an emptied option means "leave it alone", not "forget the host".
        writeOptions({ machine_host: '' });
        boot();
        expect(machine().host).toBe('gaggiuino.local');
    });

    it('leaves an app-side host edit alone when the add-on option is unchanged', () => {
        writeOptions({ machine_host: 'gaggiuino.local' });
        boot();

        require('../lib/machines/registry').updateMachine(1, { host: 'edited-in-app.local' });
        boot();
        expect(machine().host).toBe('edited-in-app.local');
    });
});
