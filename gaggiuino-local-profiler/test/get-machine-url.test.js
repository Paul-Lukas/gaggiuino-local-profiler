// #699: getMachineUrl() used to only append /api/shots when machine_host had
// no http(s):// scheme, so a host entered *with* a scheme (the format the
// Machines "Add machine" dialog and the legacy machine_host option both
// accept) silently dropped /api/shots and broke shot sync while
// status/live polling -- which only ever reduce the URL back to origin --
// kept looking completely normal.
import { describe, it, expect } from 'vitest';
import { getMachineUrl, getMachineBaseUrl } from '../lib/data.js';

describe('#699 getMachineUrl() always appends /api/shots regardless of input format', () => {
    it('appends /api/shots for a bare hostname (no scheme)', () => {
        expect(getMachineUrl({ machine_host: 'gaggia.intern' })).toBe('http://gaggia.intern/api/shots');
    });

    it('appends /api/shots for a host with an http:// scheme', () => {
        expect(getMachineUrl({ machine_host: 'http://192.168.1.50' })).toBe('http://192.168.1.50/api/shots');
    });

    it('appends /api/shots for a host with a scheme and trailing slash', () => {
        expect(getMachineUrl({ machine_host: 'http://192.168.1.50/' })).toBe('http://192.168.1.50/api/shots');
    });

    it('discards a path/query the user may have typed and rebuilds /api/shots', () => {
        expect(getMachineUrl({ machine_host: 'http://192.168.1.50/api/shots/latest?x=1' })).toBe('http://192.168.1.50/api/shots');
    });

    it('preserves a non-default port', () => {
        expect(getMachineUrl({ machine_host: 'http://192.168.1.50:8080' })).toBe('http://192.168.1.50:8080/api/shots');
    });

    it('preserves https scheme', () => {
        expect(getMachineUrl({ machine_host: 'https://gaggia.intern' })).toBe('https://gaggia.intern/api/shots');
    });

    it('falls back to the default host for a malformed machine_host', () => {
        expect(getMachineUrl({ machine_host: 'http://[invalid' })).toBe('http://gaggia.intern/api/shots');
    });
});

// #718: null (not a hardcoded placeholder hostname) when nothing is
// configured anywhere -- callers must treat that as "skip, don't request".
describe('#718 getMachineUrl()/getMachineBaseUrl() return null when no host is configured', () => {
    it('getMachineUrl returns null for empty opts', () => {
        expect(getMachineUrl({})).toBeNull();
    });

    it('getMachineUrl returns null when machine_host is an empty string', () => {
        expect(getMachineUrl({ machine_host: '' })).toBeNull();
    });

    it('getMachineBaseUrl returns null for empty opts', () => {
        expect(getMachineBaseUrl({})).toBeNull();
    });

    it('still resolves normally once a host is set', () => {
        expect(getMachineUrl({ machine_host: '192.168.1.50' })).toBe('http://192.168.1.50/api/shots');
        expect(getMachineBaseUrl({ machine_host: '192.168.1.50' })).toBe('http://192.168.1.50');
    });
});
