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

describe('shouldOpenSetupWizard (#744)', () => {
  beforeEach(() => {
    delete store[COMPLETED_KEY];
  });

  it('opens when there are zero machines and the wizard was never completed', () => {
    expect(shouldOpenSetupWizard(0)).toBe(true);
  });

  it('does not open once at least one machine is configured', () => {
    expect(shouldOpenSetupWizard(1)).toBe(false);
    expect(shouldOpenSetupWizard(3)).toBe(false);
  });

  it('does not open once the wizard was completed, even with zero machines', () => {
    localStorage.setItem(COMPLETED_KEY, '1');
    expect(shouldOpenSetupWizard(0)).toBe(false);
  });
});
