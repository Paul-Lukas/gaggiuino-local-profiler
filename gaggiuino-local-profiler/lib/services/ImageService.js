const fs    = require('fs');
const path  = require('path');
const axios = require('axios');
const { BEAN_IMAGE_DIR, ALLOWED_IMAGE_HOSTS, BEAN_IMAGE_MAX_BYTES } = require('../constants');
const { log } = require('../helpers');

const CONTENT_TYPE_EXT = {
    'image/jpeg': 'jpg',
    'image/png':  'png',
    'image/webp': 'webp',
    'image/gif':  'gif',
};

// Protocol-relative shop CDN URLs ("//cdn.shopify.com/...") → https.
function normalizeImageUrl(url) {
    if (typeof url !== 'string') return null;
    const trimmed = url.trim();
    if (trimmed.startsWith('//')) return 'https:' + trimmed;
    return trimmed;
}

function isAllowedImageUrl(url) {
    try {
        const u = new URL(url);
        return ['http:', 'https:'].includes(u.protocol) && ALLOWED_IMAGE_HOSTS.includes(u.hostname);
    } catch { return false; }
}

// prefix distinguishes entity types sharing BEAN_IMAGE_DIR (e.g. 'grinder-')
// so a grinder id can never collide with a bean id's filename. Shared by
// imagePath() below and the backup export/restore path (routes/backup.js) so
// both derive the same key from (id, ext, prefix) instead of two copies
// drifting apart.
function imageFilename(id, ext, prefix = '') {
    return `${prefix}${id}.${ext}`;
}

function imagePath(id, ext, prefix = '') {
    return path.join(BEAN_IMAGE_DIR, imageFilename(id, ext, prefix));
}

// Magic-byte sniff for the four whitelisted image types (backup
// restore path) — Content-Type headers/extensions are caller-supplied and
// trivially spoofable, so a base64 blob claiming to be `png` must actually
// start with a PNG signature before it's ever written to disk. Not a full
// image-format validator (doesn't decode pixel data), just the same
// first-bytes sniff every content-sniffing library uses to tell formats apart.
function matchesImageMagicBytes(buffer, ext) {
    if (!Buffer.isBuffer(buffer) || buffer.length < 12) return false;
    switch (ext) {
        case 'jpg':  return buffer[0] === 0xFF && buffer[1] === 0xD8 && buffer[2] === 0xFF;
        case 'png':  return buffer.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]));
        case 'gif':  return buffer.subarray(0, 4).equals(Buffer.from('GIF8'));
        case 'webp': return buffer.subarray(0, 4).toString('ascii') === 'RIFF' && buffer.subarray(8, 12).toString('ascii') === 'WEBP';
        default:     return false;
    }
}

// Downloads a bean image once, validating hard against SSRF: exact host
// allowlist, no redirect following (Shopify/kaffeebraun CDN URLs are direct),
// a size cap, and an image content-type whitelist. The filename is derived
// from the (already-numeric) bean id, never from the URL. Returns the file
// extension on success, or null (never throws — caller treats this as
// best-effort).
async function fetchBeanImage(beanId, imageUrl) {
    const url = normalizeImageUrl(imageUrl);
    if (!url || !isAllowedImageUrl(url)) return null;
    try {
        const r = await axios.get(url, {
            responseType: 'arraybuffer',
            maxRedirects: 0,
            maxContentLength: BEAN_IMAGE_MAX_BYTES,
            timeout: 8000,
            headers: { 'User-Agent': 'GLP/1.0 (Gaggiuino Local Profiler; private use)' },
            validateStatus: s => s === 200,
        });
        const contentType = String(r.headers['content-type'] || '').split(';')[0].trim();
        const ext = CONTENT_TYPE_EXT[contentType];
        if (!ext || !Buffer.isBuffer(r.data) || r.data.length === 0 || r.data.length > BEAN_IMAGE_MAX_BYTES) return null;
        fs.mkdirSync(BEAN_IMAGE_DIR, { recursive: true });
        fs.writeFileSync(imagePath(beanId, ext), r.data);
        return ext;
    } catch (e) {
        log(`Bean image download failed for bean ${beanId}: ${e.message}`, true);
        return null;
    }
}

function deleteImage(id, ext, prefix = '') {
    if (!ext) return;
    try { fs.unlinkSync(imagePath(id, ext, prefix)); } catch { /* already gone */ }
}

function deleteBeanImage(beanId, ext) { deleteImage(beanId, ext); }

// Saves a directly-uploaded image buffer (e.g. a grinder photo picked from
// the user's device) — no URL fetch, so no SSRF surface, but the same
// content-type whitelist and size cap apply. Returns the extension on
// success, or null when the content-type isn't an accepted image type or the
// buffer exceeds the size cap.
function saveUploadedImage(prefix, id, buffer, contentType) {
    // Type-check the body first: express.raw() only guarantees a Buffer when
    // the request's Content-Type matches its allowlist. Any other shape
    // (e.g. an array or plain object from an upstream JSON body parser, or a
    // repeated-header array standing in for contentType) must be rejected
    // before touching .length, so these stay standalone guards rather than
    // folded into the OR-chain below. Array.isArray is checked explicitly
    // (not just Buffer.isBuffer) since that's the specific string/array
    // type-confusion shape parameter tampering exploits.
    if (Array.isArray(buffer) || Array.isArray(contentType)) return null;
    if (!Buffer.isBuffer(buffer)) return null;
    if (typeof contentType !== 'string') return null;
    const ext = CONTENT_TYPE_EXT[contentType.split(';')[0].trim()];
    if (!ext || buffer.length === 0 || buffer.length > BEAN_IMAGE_MAX_BYTES) return null;
    fs.mkdirSync(BEAN_IMAGE_DIR, { recursive: true });
    fs.writeFileSync(imagePath(id, ext, prefix), buffer);
    return ext;
}

module.exports = {
    fetchBeanImage, deleteBeanImage, deleteImage, saveUploadedImage,
    imagePath, imageFilename, matchesImageMagicBytes, isAllowedImageUrl, normalizeImageUrl, CONTENT_TYPE_EXT,
};
