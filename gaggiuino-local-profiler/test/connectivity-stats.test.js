import { describe, it, expect } from 'vitest';
import { summarizeConnectivity } from '../lib/connectivity-stats.js';

describe('summarizeConnectivity (#710)', () => {
    it('returns null for an empty window', () => {
        expect(summarizeConnectivity([])).toBeNull();
    });

    it('summarizes an all-success window with average latency', () => {
        const records = [
            { ok: true, latencyMs: 80, err: null },
            { ok: true, latencyMs: 100, err: null },
            { ok: true, latencyMs: 60, err: null },
        ];
        expect(summarizeConnectivity(records)).toBe('3/3 ok, avg latency 80ms');
    });

    it('summarizes a mixed window with grouped error counts', () => {
        const records = [
            { ok: true, latencyMs: 50, err: null },
            { ok: false, latencyMs: null, err: 'EHOSTUNREACH' },
            { ok: false, latencyMs: null, err: 'EHOSTUNREACH' },
            { ok: false, latencyMs: null, err: 'ECONNABORTED' },
        ];
        expect(summarizeConnectivity(records)).toBe('1/4 ok, avg latency 50ms, errors: EHOSTUNREACH x2, ECONNABORTED x1');
    });

    it('summarizes an all-failure window with no latency figure', () => {
        const records = [
            { ok: false, latencyMs: null, err: 'EHOSTUNREACH' },
            { ok: false, latencyMs: null, err: null },
        ];
        expect(summarizeConnectivity(records)).toBe('0/2 ok, errors: EHOSTUNREACH x1, unknown x1');
    });
});
