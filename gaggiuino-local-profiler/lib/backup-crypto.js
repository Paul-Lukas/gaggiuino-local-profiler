// Encrypts the two genuinely sensitive fields a full backup can optionally
// carry -- the API token and MQTT broker credentials -- with a passphrase the
// user types in at export time and again at restore time. Everything else in
// a backup (shots, library, maintenance, ...) stays plaintext JSON, same as
// before: this is deliberately narrow, not a whole-file-encryption feature.
//
// The passphrase is never derived from anything already inside the backup
// file or the app's own storage -- doing so would be security theater, since
// whoever has the file would then have everything needed to derive the key
// from it too. It exists only in the user's head and in the request body of
// the export/restore calls; it is never itself persisted anywhere.
'use strict';
const crypto = require('crypto');

const ALGORITHM = 'aes-256-gcm-scrypt-v1'; // versioned so a future KDF/cipher change can coexist with old blobs
const KEY_LEN   = 32; // AES-256
const IV_LEN    = 12; // GCM-recommended nonce size
const SALT_LEN  = 16;
// N=16384 (2^14) is scrypt's own recommended interactive-use minimum (RFC 7914)
// -- deliberately not higher, since this runs synchronously on the Node event
// loop with no worker offload, and a restore/export must stay responsive.
const SCRYPT_OPTS = { N: 16384, r: 8, p: 1 };

function deriveKey(passphrase, salt) {
    return crypto.scryptSync(passphrase, salt, KEY_LEN, SCRYPT_OPTS);
}

// payload: a plain JSON-serializable object (e.g. { apiToken, mqtt: {...} }).
// Returns a self-contained blob -- salt/iv/authTag/ciphertext, all base64 --
// safe to embed directly as a backup's top-level `secrets` field.
function encryptSecrets(payload, passphrase) {
    const salt = crypto.randomBytes(SALT_LEN);
    const iv   = crypto.randomBytes(IV_LEN);
    const key  = deriveKey(passphrase, salt);

    const cipher     = crypto.createCipheriv('aes-256-gcm', key, iv);
    const plaintext  = Buffer.from(JSON.stringify(payload), 'utf8');
    const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);

    return {
        alg:        ALGORITHM,
        salt:       salt.toString('base64'),
        iv:         iv.toString('base64'),
        authTag:    cipher.getAuthTag().toString('base64'),
        ciphertext: ciphertext.toString('base64'),
    };
}

// Returns the decrypted payload object, or null for anything that doesn't
// yield trustworthy plaintext: wrong passphrase, a corrupted/hand-edited
// blob, or a blob from a future/unknown algorithm version. GCM's auth tag
// check inside decipher.final() is what actually rejects a wrong passphrase
// -- it throws rather than silently producing garbage, so the try/catch
// below is load-bearing, not defensive boilerplate.
function decryptSecrets(blob, passphrase) {
    if (!blob || typeof blob !== 'object') return null;
    if (blob.alg !== ALGORITHM) return null;
    if (typeof passphrase !== 'string' || !passphrase) return null;
    try {
        const salt       = Buffer.from(blob.salt, 'base64');
        const iv         = Buffer.from(blob.iv, 'base64');
        const authTag    = Buffer.from(blob.authTag, 'base64');
        const ciphertext = Buffer.from(blob.ciphertext, 'base64');
        if (iv.length !== IV_LEN || authTag.length !== 16) return null;

        const key      = deriveKey(passphrase, salt);
        const decipher = crypto.createDecipheriv('aes-256-gcm', key, iv);
        decipher.setAuthTag(authTag);
        const plaintext = Buffer.concat([decipher.update(ciphertext), decipher.final()]);
        return JSON.parse(plaintext.toString('utf8'));
    } catch {
        return null;
    }
}

module.exports = { encryptSecrets, decryptSecrets };
