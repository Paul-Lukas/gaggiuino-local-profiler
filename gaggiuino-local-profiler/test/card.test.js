import { describe, it, expect } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const {
  installCodeFor,
  INSTALL_CODE_ALPHABET,
  CARD_TOKENS,
  buildPalette,
  LINE_COLORS,
  contrastRatio,
  hexToRgbArr,
} = require('../lib/card');

// #811: install code always rendered on the share card, derived from the
// existing kv.install_id UUID (lib/db.js ensureInstallId(), #751) -- no new
// field, no new generation.
describe('installCodeFor (#811)', () => {
  it('is pinned against a fixed UUID -- must never silently change', () => {
    // If this ever goes red after an innocent-looking refactor, STOP: the
    // algorithm changing means every code already printed on a screenshot
    // out in the wild stops matching its owner's actual install.
    expect(installCodeFor('123e4567-e89b-12d3-a456-426614174000')).toBe('JAZE-NFFK');
  });

  it('is deterministic -- same UUID always yields the same code', () => {
    const uuid = 'a1b2c3d4-e5f6-7890-abcd-ef1234567890';
    expect(installCodeFor(uuid)).toBe(installCodeFor(uuid));
  });

  it('differs for a different UUID', () => {
    expect(installCodeFor('00000000-0000-0000-0000-000000000000'))
      .not.toBe(installCodeFor('ffffffff-ffff-ffff-ffff-ffffffffffff'));
  });

  it('renders as AAAA-AAAA using only the curated alphabet', () => {
    const code = installCodeFor('123e4567-e89b-12d3-a456-426614174000');
    const alphaClass = INSTALL_CODE_ALPHABET.split('').join('');
    expect(code).toMatch(new RegExp(`^[${alphaClass}]{4}-[${alphaClass}]{4}$`));
  });

  it('alphabet excludes every visually-ambiguous character (0/O, 1/I/l)', () => {
    for (const ch of ['0', 'O', '1', 'I', 'L']) {
      expect(INSTALL_CODE_ALPHABET).not.toContain(ch);
    }
    expect(new Set(INSTALL_CODE_ALPHABET.split('')).size).toBe(INSTALL_CODE_ALPHABET.length);
  });
});

// #811: buildPalette() no longer draws lines (border/borderDim) that fail
// WCAG 1.4.11 (>=3:1 for non-text lines) against the surfaces they're
// actually stroked on -- see LINE_COLORS in lib/card.js for how they're
// derived (lift toward text colour, same mechanic as glp-lovelace-card's
// _applyAccentLineContrast() for --glp-aline).
describe('buildPalette() line contrast (#811)', () => {
  const combos = [
    ['amber', 'dark'], ['amber', 'light'],
    ['crema', 'dark'], ['crema', 'light'],
    ['ocean', 'dark'], ['ocean', 'light'],
  ];

  it.each(combos)('border/borderDim clear 3:1 against bgChart and bgCard (%s/%s)', (accent, theme) => {
    const GLP = buildPalette(accent, theme);
    expect(contrastRatio(hexToRgbArr(GLP.border), hexToRgbArr(GLP.bgChart))).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(hexToRgbArr(GLP.border), hexToRgbArr(GLP.bgCard))).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(hexToRgbArr(GLP.borderDim), hexToRgbArr(GLP.bgChart))).toBeGreaterThanOrEqual(3);
  });

  it('borderDim is no longer identical to bgChart (previously an invisible stroke)', () => {
    for (const [accent, theme] of combos) {
      const GLP = buildPalette(accent, theme);
      expect(GLP.borderDim).not.toBe(GLP.bgChart);
    }
  });

  it('text roles clear 4.5:1 against every surface, for every mirrored theme/accent combo', () => {
    for (const gray of Object.values(CARD_TOKENS.gray)) {
      for (const bgKey of ['950', '900', '800']) {
        for (const textKey of ['200', '400', '500']) {
          const ratio = contrastRatio(hexToRgbArr(gray[textKey]), hexToRgbArr(gray[bgKey]));
          expect(ratio).toBeGreaterThanOrEqual(4.5);
        }
      }
    }
  });

  it('LINE_COLORS has an entry for every CARD_TOKENS.gray combo', () => {
    expect(Object.keys(LINE_COLORS).sort()).toEqual(Object.keys(CARD_TOKENS.gray).sort());
  });
});

// #462's legacy snapshot must stay byte-for-byte so old cached/bookmarked
// card links keep looking exactly the way they always did -- this includes
// the borderDim === bgChart quirk being fixed above for the live path.
describe('buildPalette() legacy snapshot (#462)', () => {
  it('is untouched by the #811 token/line-colour work', () => {
    const GLP = buildPalette();
    expect(GLP.bg).toBe('#09090b');
    expect(GLP.border).toBe('#3f3f46');
    expect(GLP.borderDim).toBe('#27272a');
    expect(GLP.bgChart).toBe('#27272a');
  });
});
