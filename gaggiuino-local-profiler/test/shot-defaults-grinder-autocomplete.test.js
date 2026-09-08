// #322: Settings -> "Shot logging defaults" grinder field (#sdGrinder) uses
// the same library select + free-text fallback as shot annotation and the
// dial-in wizard.
import { describe, it, expect, beforeEach, vi } from 'vitest';

globalThis.localStorage ??= { getItem: () => null, setItem: () => {} };
globalThis.navigator ??= { language: 'en-US' };

const renderGrinderFieldMock = vi.fn();
vi.mock('../public-src/views/shots/annotation.js', () => ({
  loadShotDefaults: vi.fn(),
  loadDrinkMenu: vi.fn(),
  renderGrinderField: renderGrinderFieldMock,
  getGrinderFieldValue: vi.fn(() => ''),
}));

const { S } = await import('../public-src/state.js');
const { renderShotDefaultsSettingsCard } = await import('../public-src/components/shot-defaults-settings.js');

function makeFakeDocument(fields) {
  const registry = new Map(Object.entries(fields));
  return { getElementById: id => registry.get(id) };
}

describe('shot defaults grinder select (#322)', () => {
  beforeEach(() => {
    renderGrinderFieldMock.mockClear();
    globalThis.document = makeFakeDocument({});
    S.shotDefaults = {};
  });

  it('renders #sdGrinder through the shared grinder select helper', () => {
    renderShotDefaultsSettingsCard();
    expect(renderGrinderFieldMock).toHaveBeenCalledTimes(1);
    expect(renderGrinderFieldMock.mock.calls[0].slice(0, 3)).toEqual(['sdGrinder', 'sdGrinderOther', '']);
  });

  it('passes an existing defaults grinder value through for preselection', () => {
    S.shotDefaults = { grinder: 'Niche Zero' };
    renderShotDefaultsSettingsCard();
    expect(renderGrinderFieldMock.mock.calls[0].slice(0, 3)).toEqual(['sdGrinder', 'sdGrinderOther', 'Niche Zero']);
  });

  it('does not require the coffee library to be loaded yet', () => {
    S.coffeeLibrary = null;
    renderShotDefaultsSettingsCard();
    expect(renderGrinderFieldMock).toHaveBeenCalledTimes(1);
  });
});
