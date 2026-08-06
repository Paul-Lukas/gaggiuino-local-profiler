import { describe, it, expect } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);
const { encryptSecrets, decryptSecrets } = require('../lib/backup-crypto');

describe('backup-crypto', () => {
    it('round-trips a payload with the correct passphrase', () => {
        const payload = { apiToken: 'abc123', mqtt: { username: 'glp', password: 'super-secret' } };
        const blob = encryptSecrets(payload, 'correct horse battery staple');
        expect(decryptSecrets(blob, 'correct horse battery staple')).toEqual(payload);
    });

    it('returns null for the wrong passphrase instead of garbage', () => {
        const blob = encryptSecrets({ apiToken: 'abc123' }, 'right-passphrase');
        expect(decryptSecrets(blob, 'wrong-passphrase')).toBe(null);
    });

    it('returns null when the ciphertext has been tampered with', () => {
        const blob = encryptSecrets({ apiToken: 'abc123' }, 'a-passphrase');
        const tampered = { ...blob, ciphertext: Buffer.from('tampered-bytes-here!').toString('base64') };
        expect(decryptSecrets(tampered, 'a-passphrase')).toBe(null);
    });

    it('returns null when the auth tag has been tampered with', () => {
        const blob = encryptSecrets({ apiToken: 'abc123' }, 'a-passphrase');
        const flipped = Buffer.from(blob.authTag, 'base64');
        flipped[0] ^= 0xFF;
        expect(decryptSecrets({ ...blob, authTag: flipped.toString('base64') }, 'a-passphrase')).toBe(null);
    });

    it('produces a different salt and iv on every call, even for the same payload and passphrase', () => {
        const a = encryptSecrets({ apiToken: 'same' }, 'same-passphrase');
        const b = encryptSecrets({ apiToken: 'same' }, 'same-passphrase');
        expect(a.salt).not.toBe(b.salt);
        expect(a.iv).not.toBe(b.iv);
        expect(a.ciphertext).not.toBe(b.ciphertext);
    });

    it('rejects malformed or missing blobs without throwing', () => {
        expect(decryptSecrets(null, 'x')).toBe(null);
        expect(decryptSecrets({}, 'x')).toBe(null);
        expect(decryptSecrets({ alg: 'some-future-algorithm' }, 'x')).toBe(null);
        expect(decryptSecrets({ alg: 'aes-256-gcm-scrypt-v1', salt: 'not-base64!!', iv: '', authTag: '', ciphertext: '' }, 'x')).toBe(null);
    });

    it('rejects an empty or non-string passphrase up front', () => {
        const blob = encryptSecrets({ apiToken: 'abc123' }, 'a-passphrase');
        expect(decryptSecrets(blob, '')).toBe(null);
        expect(decryptSecrets(blob, undefined)).toBe(null);
        expect(decryptSecrets(blob, null)).toBe(null);
    });
});
