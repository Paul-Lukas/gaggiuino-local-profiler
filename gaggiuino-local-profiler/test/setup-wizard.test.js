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

const { S, setState } = await import('../public-src/state.js');
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

// #748: the connect->done auto-advance used to subscribe to the generic
// 'machines' state, which testMachineForm()'s implicit save-before-test also
// triggers (via loadMachines()) — that closed/advanced the wizard the moment
// the user clicked "Test connection", before they ever saw the result. It
// now subscribes to 'machineExplicitSave', a signal only saveMachineForm()
// sets. document.getElementById('swBody') returning nothing here makes
// renderSetupWizard() a no-op past the state change, so these tests can
// drive the subscription without a full DOM.
describe('setup wizard connect->done auto-advance (#748)', () => {
  globalThis.document ??= { getElementById: () => undefined };

  beforeEach(() => {
    S.setupWizardOpen = true;
    S.setupWizardStep = 'connect';
  });

  it('advances to done on an explicit save (machineExplicitSave signal)', () => {
    setState('machineExplicitSave', 42);
    expect(S.setupWizardStep).toBe('done');
  });

  it('does NOT advance on a plain "machines" state update (e.g. Test connection\'s implicit save)', () => {
    setState('machines', [{ id: 1, isDefault: true, host: '192.168.1.50' }]);
    expect(S.setupWizardStep).toBe('connect');
  });

  it('ignores machineExplicitSave when the wizard is not open or not on the connect step', () => {
    S.setupWizardOpen = false;
    setState('machineExplicitSave', 42);
    expect(S.setupWizardStep).toBe('connect');

    S.setupWizardOpen = true;
    S.setupWizardStep = 'welcome';
    setState('machineExplicitSave', 42);
    expect(S.setupWizardStep).toBe('welcome');
  });
});
