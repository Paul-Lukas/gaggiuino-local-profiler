import { describe, it, expect, beforeEach, vi } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

// Node's global `navigator` is a getter-only accessor property — plain
// assignment throws ("Cannot set property navigator ... which has only a
// getter"), unlike globalThis.localStorage (a plain data property here).
function setNavigatorLanguage(language) {
    Object.defineProperty(globalThis, 'navigator', { value: { language }, configurable: true });
}

// Regression: S.currentLang used to be set from navigator.language with no
// validation against the languages GLP actually ships, and i18n.js's t()
// fell back to TRANSLATIONS.de for anything else — so an unsupported browser
// locale (pt, pl, sv, ...) showed the ENTIRE UI in German, not English, for
// a non-German user. state.js now validates against TRANSLATIONS and falls
// back to 'en'. vi.resetModules() + a fresh dynamic import is required for
// each case since S.currentLang is computed once at module-load time.
describe('S.currentLang browser-locale fallback', () => {
    beforeEach(() => {
        vi.resetModules();
    });

    it('falls back to en when navigator.language matches no shipped translation', async () => {
        globalThis.localStorage = { getItem: () => null, setItem: () => {} };
        setNavigatorLanguage('pt-BR');
        const { S } = await import('../public-src/state.js');
        expect(S.currentLang).toBe('en');
    });

    it('keeps a genuinely supported browser locale (nl)', async () => {
        globalThis.localStorage = { getItem: () => null, setItem: () => {} };
        setNavigatorLanguage('nl-NL');
        const { S } = await import('../public-src/state.js');
        expect(S.currentLang).toBe('nl');
    });

    it('falls back to en for a corrupted/unsupported stored glp_lang value too', async () => {
        globalThis.localStorage = { getItem: () => 'xx', setItem: () => {} };
        setNavigatorLanguage('de-DE');
        const { S } = await import('../public-src/state.js');
        expect(S.currentLang).toBe('en');
    });

    it('keeps a valid stored glp_lang over navigator.language', async () => {
        globalThis.localStorage = { getItem: () => 'fr', setItem: () => {} };
        setNavigatorLanguage('en-US');
        const { S } = await import('../public-src/state.js');
        expect(S.currentLang).toBe('fr');
    });
});

describe('localeFor() (public-src/constants.js)', () => {
    it('falls back to en-US for a language with no LOCALE_MAP entry', async () => {
        const { localeFor } = await import('../public-src/constants.js');
        expect(localeFor('pt')).toBe('en-US');
        expect(localeFor(undefined)).toBe('en-US');
    });

    it('returns the mapped locale for every supported language', async () => {
        const { localeFor, LOCALE_MAP } = await import('../public-src/constants.js');
        for (const lang of Object.keys(LOCALE_MAP)) {
            expect(localeFor(lang)).toBe(LOCALE_MAP[lang]);
        }
    });
});

describe('t() per-key fallback (public-src/i18n.js)', () => {
    beforeEach(() => {
        vi.resetModules();
    });

    it('falls back to English, not German, for a key missing in the active language file', async () => {
        globalThis.localStorage = { getItem: () => 'fr', setItem: () => {} };
        setNavigatorLanguage('fr-FR');
        const { S } = await import('../public-src/state.js');
        const { t } = await import('../public-src/i18n.js');
        const { TRANSLATIONS } = await import('../public-src/constants.js');
        expect(S.currentLang).toBe('fr');

        const probeKey = '__lang_fallback_probe__';
        TRANSLATIONS.en[probeKey] = 'english fallback text';
        try {
            // Neither fr nor de has this key — a de-fallback would return the
            // raw key itself (de also misses it), an en-fallback returns the text.
            expect(t(probeKey)).toBe('english fallback text');
        } finally {
            delete TRANSLATIONS.en[probeKey];
        }
    });
});

describe('backend language fallbacks default to en, not de', () => {
    it('lib/ha.js getHaLanguage() resolves to en when no Supervisor token is configured', async () => {
        const prevToken = process.env.SUPERVISOR_TOKEN;
        delete process.env.SUPERVISOR_TOKEN;
        delete require.cache[require.resolve('../lib/constants')];
        delete require.cache[require.resolve('../lib/ha')];
        try {
            const { getHaLanguage } = require('../lib/ha');
            await expect(getHaLanguage()).resolves.toBe('en');
        } finally {
            // eslint-disable-next-line require-atomic-updates -- single-threaded test cleanup restoring an env var this same test unset above, not shared/concurrent state
            if (prevToken !== undefined) process.env.SUPERVISOR_TOKEN = prevToken;
            delete require.cache[require.resolve('../lib/constants')];
            delete require.cache[require.resolve('../lib/ha')];
        }
    });

    it('lib/notify-i18n.js notifyT() falls back to the English table for an unrecognised language', () => {
        const { notifyT } = require('../lib/notify-i18n');
        expect(notifyT('xx', 'preheat_title')).toBe(notifyT('en', 'preheat_title'));
        expect(notifyT('xx', 'preheat_title')).not.toBe(notifyT('de', 'preheat_title'));
    });
});
