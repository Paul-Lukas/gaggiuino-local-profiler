// In-app "What's New" changelog (#610). Hand-maintained, English-only
// subset of CHANGELOG.md's most recent releases — CHANGELOG.md stays the
// full/source-of-truth history; this is a curated highlight list meant to
// be readable inside the app itself, not generated from CHANGELOG.md's
// Markdown. Add a new entry here by hand whenever a release ships (see
// CLAUDE.md's Commits section). Same isomorphic sharing pattern as
// lib/machines/theme-presets.js (see vite.config.js's commonjsOptions):
// pure data, no Node/DOM deps, importable from both the backend and the
// ESM frontend build.
//
// Highlight text is deliberately English-only, not run through i18n like
// the rest of the UI — historical release notes aren't practical to
// machine-translate, same reasoning as shot annotations/tasting notes
// staying user-authored/English-source. Only the Settings card's own
// title/description are translated.
//
// Keep this list newest-first; getWhatsNewEntries() below re-sorts and
// caps it defensively so an out-of-order manual edit can't silently show
// entries in the wrong order or let the list grow unbounded.
const WHATS_NEW_ENTRIES = [
    { version: '2.32.0', date: '2026-08-11', highlights: [
        'Heads up if you manage the machine host/switch entity via the add-on\'s Configuration tab: those fields are removed there. Your existing value carries over automatically, but from now on the default machine is configured entirely under Settings → Machines instead.',
        'Added a guided first-run setup wizard: a fresh install with no machines configured now gets a welcome -> connect machine -> done walkthrough, with a one-click demo-data option and a "Restart setup tour" control in Settings → Machines.',
        'The Live tab and the sidebar\'s shot counter now update in real time instead of only polling every few seconds, falling back automatically if a live connection can\'t be established.',
        'Settings → Machines: "Test connection" now saves the machine automatically first, you can change the default machine or delete any machine, and the host field can be left empty to save a machine as "not configured yet".',
        'Fixed several shot-sync sticking points (a stuck backfill, an out-of-range shot id) and the Live tab\'s flow reading always showing 0.',
    ] },
];

const MAX_ENTRIES = 8;

function compareVersionsDesc(a, b) {
    const pa = a.version.split('.').map(Number);
    const pb = b.version.split('.').map(Number);
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
        const diff = (pb[i] || 0) - (pa[i] || 0);
        if (diff !== 0) return diff;
    }
    return 0;
}

// Newest-first, capped at MAX_ENTRIES — callers never need to sort/slice
// themselves.
function getWhatsNewEntries() {
    return [...WHATS_NEW_ENTRIES].sort(compareVersionsDesc).slice(0, MAX_ENTRIES);
}

module.exports = { WHATS_NEW_ENTRIES, MAX_ENTRIES, getWhatsNewEntries };
