import { describe, it, expect } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);
const { formatLogTimestamp, log } = require('../lib/helpers');

// Regression: log() used to hardcode 'de-DE'/'Europe/Berlin' regardless of
// the container's own TZ, making the add-on log (the primary support
// channel) unreadable for anyone outside Germany. formatLogTimestamp now
// reads local Date fields directly, which resolve against whatever `TZ`
// the process actually has, and produces a fixed-width sortable format
// instead of a locale-dependent one.
describe('formatLogTimestamp', () => {
    it('formats as sortable "YYYY-MM-DD HH:mm:ss", not a de-DE locale string', () => {
        const d = new Date(2026, 0, 5, 9, 3, 7); // local time, deliberately single-digit month/day/hour/min/sec
        expect(formatLogTimestamp(d)).toBe('2026-01-05 09:03:07');
    });

    it('never contains a comma or a dot-separated date (de-DE locale artifacts)', () => {
        const out = formatLogTimestamp(new Date());
        expect(out).not.toContain(',');
        expect(out).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    });

    it('log() wraps the message in brackets with that timestamp format', () => {
        const logs = [];
        const orig = console.log;
        console.log = (...args) => logs.push(args.join(' '));
        try { log('hello world'); } finally { console.log = orig; }
        expect(logs[0]).toMatch(/^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] hello world$/);
    });
});
