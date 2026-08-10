import { describe, it, expect, beforeEach } from 'vitest';

// setup-wizard.js imports state.js/i18n.js, which read localStorage/navigator
// at module load time — stub the minimum browser globals needed so the
// module graph can be imported under vitest's node environment (same pattern
// as test/profile-dialin-wizard.test.js).
const store = {};
globalThis.localStorage ??= {
  getItem: k => (k in store ? store[k] : null),
  setItem: (k, v) => { store[k] = v; },
  removeItem: k => { delete store[k]; },
};
globalThis.navigator ??= { language: 'en-US' };

const { shouldOpenSetupWizard } = await import('../public-src/views/setup-wizard.js');

const COMPLETED_KEY = 'glp_setup_wizard_completed';

describe('shouldOpenSetupWizard (#744, #746)', () => {
  beforeEach(() => {
    delete store[COMPLETED_KEY];
  });

  it('opens when there are zero machines and the wizard was never completed', () => {
    expect(shouldOpenSetupWizard([])).toBe(true);
  });

  // #746: registry.ensureDefaultMachine() always seeds an empty-host default
  // machine #1 on a fresh DB, so a real fresh install's machines array is
  // [{ host: '' }], never []. The wizard must still trigger on that shape —
  // this is the exact case that shipped broken (machineCount === 0 was never
  // true in the real world).
  it('opens when the only machine is the auto-seeded default with no host set', () => {
    expect(shouldOpenSetupWizard([{ id: 1, host: '' }])).toBe(true);
    expect(shouldOpenSetupWizard([{ id: 1, host: null }])).toBe(true);
  });

  it('does not open once at least one machine has a configured host', () => {
    expect(shouldOpenSetupWizard([{ id: 1, host: '192.168.1.50' }])).toBe(false);
    expect(shouldOpenSetupWizard([{ id: 1, host: '' }, { id: 2, host: '192.168.1.51' }])).toBe(false);
  });

  it('does not open once the wizard was completed, even with zero machines', () => {
    localStorage.setItem(COMPLETED_KEY, '1');
    expect(shouldOpenSetupWizard([])).toBe(false);
  });
});
