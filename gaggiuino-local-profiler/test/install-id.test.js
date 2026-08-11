import { describe, it, expect } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const Database = require('better-sqlite3');
const { initSchema, ensureInstallId } = require('../lib/db');

// #750: ensureInstallId() is the backend half of the fix for the setup
// wizard staying suppressed after a Supervisor-level "delete add-on data"
// wipe -- see test/setup-wizard.test.js's syncInstallId() tests for the
// frontend half.
describe('ensureInstallId (#750)', () => {
  it('generates and persists an id on a fresh DB', () => {
    const db = new Database(':memory:');
    initSchema(db);
    const id = ensureInstallId(db);
    expect(typeof id).toBe('string');
    expect(id.length).toBeGreaterThan(0);
  });

  it('returns the same id on repeated calls against the same DB', () => {
    const db = new Database(':memory:');
    initSchema(db);
    const first = ensureInstallId(db);
    const second = ensureInstallId(db);
    expect(second).toBe(first);
  });

  it('returns a different id for a separate (freshly created) DB', () => {
    const dbA = new Database(':memory:');
    initSchema(dbA);
    const dbB = new Database(':memory:');
    initSchema(dbB);
    expect(ensureInstallId(dbA)).not.toBe(ensureInstallId(dbB));
  });
});
