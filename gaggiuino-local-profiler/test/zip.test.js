import { describe, it, expect } from 'vitest';
import { createZip, readZip, crc32 } from '../lib/zip.js';

describe('lib/zip', () => {
    it('round-trips an empty archive', () => {
        const zip = createZip([]);
        expect(readZip(zip)).toEqual({});
    });

    it('round-trips a single text entry', () => {
        const zip = createZip([{ name: 'backup.json', data: Buffer.from('{"glp_backup":true}') }]);
        const out = readZip(zip);
        expect(Object.keys(out)).toEqual(['backup.json']);
        expect(out['backup.json'].toString('utf8')).toBe('{"glp_backup":true}');
    });

    it('round-trips multiple entries including nested paths and binary content', () => {
        const pngBytes = Buffer.concat([
            Buffer.from([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]),
            Buffer.from(Array.from({ length: 500 }, (_, i) => i % 256)),
        ]);
        const zip = createZip([
            { name: 'backup.json', data: Buffer.from(JSON.stringify({ a: 1, b: 'x'.repeat(2000) })) },
            { name: 'images/shot-173.jpg', data: pngBytes },
            { name: 'images/shot-1.jpg', data: Buffer.from([1, 2, 3, 4]) },
        ]);
        const out = readZip(zip);
        expect(Object.keys(out).sort()).toEqual(['backup.json', 'images/shot-1.jpg', 'images/shot-173.jpg']);
        expect(JSON.parse(out['backup.json'].toString('utf8')).a).toBe(1);
        expect(out['images/shot-173.jpg'].equals(pngBytes)).toBe(true);
        expect(out['images/shot-1.jpg'].equals(Buffer.from([1, 2, 3, 4]))).toBe(true);
    });

    it('round-trips an entry with no data', () => {
        const zip = createZip([{ name: 'empty.txt', data: Buffer.alloc(0) }]);
        const out = readZip(zip);
        expect(out['empty.txt'].length).toBe(0);
    });

    it('rejects a truncated/corrupt buffer', () => {
        expect(() => readZip(Buffer.from('not a zip file'))).toThrow(/valid zip/);
    });

    it('rejects a buffer with a valid EOCD signature but a corrupted central directory', () => {
        const zip = createZip([{ name: 'a.txt', data: Buffer.from('hello') }]);
        // Flip a byte inside the central directory file header's signature area.
        const tampered = Buffer.from(zip);
        const centralDirOffset = tampered.readUInt32LE(tampered.length - 22 + 16);
        tampered[centralDirOffset] = 0x00;
        expect(() => readZip(tampered)).toThrow(/Corrupt zip/);
    });

    it('crc32 matches known test vectors', () => {
        expect(crc32(Buffer.from(''))).toBe(0);
        expect(crc32(Buffer.from('123456789'))).toBe(0xCBF43926);
    });
});
