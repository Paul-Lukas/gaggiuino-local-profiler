// A minimal, dependency-free ZIP reader/writer -- built for routes/backup.js's
// export/restore of `backup.json` + real image files sitting alongside it in
// one downloadable archive. Deliberately not a general-purpose ZIP library
// (no multi-disk archives, no ZIP64, no encryption, no directory entries):
// just enough of APPNOTE.TXT's format to write and read back what this app's
// own backups need, using only Node's built-in `zlib` for DEFLATE -- the same
// "no new dependency" choice `lib/backup-crypto.js` already made for AES-256-GCM.
'use strict';
const zlib = require('zlib');

const LOCAL_FILE_HEADER_SIG    = 0x04034b50;
const CENTRAL_DIR_HEADER_SIG   = 0x02014b50;
const END_OF_CENTRAL_DIR_SIG   = 0x06054b50;
const VERSION_NEEDED           = 20; // DEFLATE, no ZIP64 -- 2.0 covers both
const METHOD_DEFLATE           = 8;

// CRC-32 (IEEE 802.3 / zlib polynomial 0xEDB88320), the exact table-driven
// algorithm ZIP's own spec requires per-entry -- Node's `zlib` doesn't expose
// crc32 directly, so this is the one piece of the format not already covered
// by a built-in.
const CRC_TABLE = (() => {
    const table = new Uint32Array(256);
    for (let n = 0; n < 256; n++) {
        let c = n;
        for (let k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320 ^ (c >>> 1)) : (c >>> 1);
        table[n] = c >>> 0;
    }
    return table;
})();

function crc32(buf) {
    let c = 0xFFFFFFFF;
    for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xFF] ^ (c >>> 8);
    return (c ^ 0xFFFFFFFF) >>> 0;
}

// ZIP stores timestamps in DOS date/time format (2-second resolution, no
// timezone) -- the backup's own `created` ISO timestamp already carries the
// real time, so entry timestamps here are purely cosmetic (what a file
// browser shows) and just use "now".
function dosDateTime(date = new Date()) {
    const dosTime = (date.getHours() << 11) | (date.getMinutes() << 5) | (date.getSeconds() >> 1);
    const dosDate = (((date.getFullYear() - 1980) & 0x7F) << 9) | ((date.getMonth() + 1) << 5) | date.getDate();
    return { dosTime, dosDate };
}

// entries: [{ name: string (forward-slash separated, no leading '/'), data: Buffer }]
// Every entry is DEFLATEd unconditionally -- simplest correct choice; the
// CPU cost of deflating already-compressed JPEGs is negligible at the sizes
// a home coffee setup's backups actually reach, and readZip() below still
// has to handle both STORE and DEFLATE regardless (future-proofing, and
// robustness against a `backup.json` produced by a different writer).
function createZip(entries) {
    const { dosTime, dosDate } = dosDateTime();
    const localParts = [];
    const centralParts = [];
    let offset = 0;

    for (const { name, data } of entries) {
        const nameBuf   = Buffer.from(name, 'utf8');
        const crc       = crc32(data);
        const compressed = zlib.deflateRawSync(data);

        const localHeader = Buffer.alloc(30);
        localHeader.writeUInt32LE(LOCAL_FILE_HEADER_SIG, 0);
        localHeader.writeUInt16LE(VERSION_NEEDED, 4);
        localHeader.writeUInt16LE(0, 6); // flags
        localHeader.writeUInt16LE(METHOD_DEFLATE, 8);
        localHeader.writeUInt16LE(dosTime, 10);
        localHeader.writeUInt16LE(dosDate, 12);
        localHeader.writeUInt32LE(crc, 14);
        localHeader.writeUInt32LE(compressed.length, 18);
        localHeader.writeUInt32LE(data.length, 22);
        localHeader.writeUInt16LE(nameBuf.length, 26);
        localHeader.writeUInt16LE(0, 28); // extra length

        localParts.push(localHeader, nameBuf, compressed);

        const centralHeader = Buffer.alloc(46);
        centralHeader.writeUInt32LE(CENTRAL_DIR_HEADER_SIG, 0);
        centralHeader.writeUInt16LE(VERSION_NEEDED, 4);  // version made by
        centralHeader.writeUInt16LE(VERSION_NEEDED, 6);  // version needed
        centralHeader.writeUInt16LE(0, 8);  // flags
        centralHeader.writeUInt16LE(METHOD_DEFLATE, 10);
        centralHeader.writeUInt16LE(dosTime, 12);
        centralHeader.writeUInt16LE(dosDate, 14);
        centralHeader.writeUInt32LE(crc, 16);
        centralHeader.writeUInt32LE(compressed.length, 20);
        centralHeader.writeUInt32LE(data.length, 24);
        centralHeader.writeUInt16LE(nameBuf.length, 28);
        centralHeader.writeUInt16LE(0, 30); // extra length
        centralHeader.writeUInt16LE(0, 32); // comment length
        centralHeader.writeUInt16LE(0, 34); // disk number start
        centralHeader.writeUInt16LE(0, 36); // internal attrs
        centralHeader.writeUInt32LE(0, 38); // external attrs
        centralHeader.writeUInt32LE(offset, 42); // local header offset

        centralParts.push(centralHeader, nameBuf);

        offset += localHeader.length + nameBuf.length + compressed.length;
    }

    const localSection   = Buffer.concat(localParts);
    const centralSection = Buffer.concat(centralParts);

    const eocd = Buffer.alloc(22);
    eocd.writeUInt32LE(END_OF_CENTRAL_DIR_SIG, 0);
    eocd.writeUInt16LE(0, 4); // disk number
    eocd.writeUInt16LE(0, 6); // disk with central dir
    eocd.writeUInt16LE(entries.length, 8);
    eocd.writeUInt16LE(entries.length, 10);
    eocd.writeUInt32LE(centralSection.length, 12);
    eocd.writeUInt32LE(localSection.length, 16); // central dir offset = right after local section
    eocd.writeUInt16LE(0, 20); // comment length

    return Buffer.concat([localSection, centralSection, eocd]);
}

// Returns { [entryName]: Buffer } with every entry already inflated. Reads
// via the central directory (not by scanning local headers) -- the correct,
// spec-mandated way to enumerate a ZIP's contents, and the only one that
// works if a future writer ever adds a data descriptor or extra fields this
// module doesn't otherwise parse.
function readZip(buffer) {
    // #665: the only call site (routes/backup.js) already checks
    // Buffer.isBuffer(req.body) before calling in, but that guard lives in a
    // different function -- assert it again here too, as this function's own
    // contract, so a future call site can't skip it and so static analysis
    // tools that don't track guards across function boundaries (CodeQL
    // flagged buffer.length/readUInt32LE below as a potential type-confusion
    // sink without this) have a barrier in the same scope as the sink.
    if (!Buffer.isBuffer(buffer)) throw new Error('readZip() expects a Buffer');
    // The EOCD record is fixed-size (22 bytes) plus an optional trailing
    // comment of up to 65535 bytes, so its signature isn't at a fixed offset
    // from the end -- scan backwards for it, same approach every ZIP reader
    // uses. Archives this app produces never have a comment, but a
    // defensively-sized search window costs nothing.
    const searchStart = Math.max(0, buffer.length - 22 - 65535);
    let eocdOffset = -1;
    for (let i = buffer.length - 22; i >= searchStart; i--) {
        if (buffer.readUInt32LE(i) === END_OF_CENTRAL_DIR_SIG) { eocdOffset = i; break; }
    }
    if (eocdOffset === -1) throw new Error('Not a valid zip file (no end-of-central-directory record found)');

    const totalEntries  = buffer.readUInt16LE(eocdOffset + 10);
    const centralDirSize   = buffer.readUInt32LE(eocdOffset + 12);
    const centralDirOffset = buffer.readUInt32LE(eocdOffset + 16);
    if (centralDirOffset + centralDirSize > eocdOffset) throw new Error('Corrupt zip file (central directory out of bounds)');

    const result = {};
    let pos = centralDirOffset;
    for (let i = 0; i < totalEntries; i++) {
        if (buffer.readUInt32LE(pos) !== CENTRAL_DIR_HEADER_SIG) throw new Error('Corrupt zip file (bad central directory entry)');
        const method           = buffer.readUInt16LE(pos + 10);
        const crc               = buffer.readUInt32LE(pos + 16);
        const compressedSize    = buffer.readUInt32LE(pos + 20);
        const uncompressedSize  = buffer.readUInt32LE(pos + 24);
        const nameLen            = buffer.readUInt16LE(pos + 28);
        const extraLen           = buffer.readUInt16LE(pos + 30);
        const commentLen         = buffer.readUInt16LE(pos + 32);
        const localHeaderOffset  = buffer.readUInt32LE(pos + 42);
        const name = buffer.toString('utf8', pos + 46, pos + 46 + nameLen);
        pos += 46 + nameLen + extraLen + commentLen;

        if (buffer.readUInt32LE(localHeaderOffset) !== LOCAL_FILE_HEADER_SIG) throw new Error(`Corrupt zip file (bad local header for ${name})`);
        const localNameLen  = buffer.readUInt16LE(localHeaderOffset + 26);
        const localExtraLen = buffer.readUInt16LE(localHeaderOffset + 28);
        const dataStart = localHeaderOffset + 30 + localNameLen + localExtraLen;
        const raw = buffer.subarray(dataStart, dataStart + compressedSize);

        let data;
        if (method === METHOD_DEFLATE) data = zlib.inflateRawSync(raw);
        else if (method === 0) data = Buffer.from(raw); // STORE
        else throw new Error(`Unsupported zip compression method (${method}) for ${name}`);

        if (data.length !== uncompressedSize) throw new Error(`Corrupt zip entry ${name} (size mismatch)`);
        if (crc32(data) !== crc) throw new Error(`Corrupt zip entry ${name} (checksum mismatch)`);

        result[name] = data;
    }
    return result;
}

module.exports = { createZip, readZip, crc32 };
