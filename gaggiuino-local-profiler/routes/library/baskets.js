const express = require('express');

const { loadLibrary, saveLibrary } = require('../../lib/data');
const { imagePath, CONTENT_TYPE_EXT, deleteImage, saveUploadedImage } = require('../../lib/services/ImageService');
const { BEAN_IMAGE_MAX_BYTES } = require('../../lib/constants');
const { rateLimit } = require('../../lib/helpers');

const VALID_IMAGE_EXTS = new Set(Object.values(CONTENT_TYPE_EXT));

// #635: portafilter baskets. wallType/shape are enums validated against an
// allowlist server-side (not just trimmed) — same treatment as bean's
// species/category fields.
const WALL_TYPES = new Set(['pressurized', 'single-wall', 'precision-machined', 'high-flow']);
const SHAPES     = new Set(['straight', 'tapered']);

const s = (v, max) => (typeof v === 'string' ? v.trim().slice(0, max) : '');

// Registers basket routes onto a shared router — see routes/library/beans.js
// for why this isn't its own express.Router() mounted as a sub-router.
module.exports = function registerBasketRoutes(router) {

router.get('/api/library/baskets', (req, res) => {
    const lib = loadLibrary();
    res.json(lib.baskets || []);
});

router.post('/api/library/basket', (req, res) => {
    if (!rateLimit(`lib:${req.ip}`, 30)) return res.status(429).json({ error: 'Rate limit exceeded' });
    const { name, doseCapacity, wallType, shape, holeCount, notes } = req.body || {};
    if (!name?.trim()) return res.status(400).json({ error: 'name required' });
    if (wallType && !WALL_TYPES.has(wallType)) return res.status(400).json({ error: 'invalid wallType' });
    if (shape && !SHAPES.has(shape)) return res.status(400).json({ error: 'invalid shape' });
    const lib = loadLibrary();
    const basket = {
        id: Date.now(), name: s(name, 200), doseCapacity: s(doseCapacity, 50),
        wallType: wallType || '', shape: shape || '',
        holeCount: s(holeCount, 50), notes: s(notes, 1000), updatedAt: Date.now(),
    };
    if (!Array.isArray(lib.baskets)) lib.baskets = [];
    lib.baskets.push(basket);
    saveLibrary(lib);
    res.json(basket);
});

router.put('/api/library/basket/:id', (req, res) => {
    const id  = parseInt(req.params.id, 10);
    const lib = loadLibrary();
    const idx = (lib.baskets || []).findIndex(b => b.id === id);
    if (idx === -1) return res.status(404).json({ error: 'not found' });
    const { name, doseCapacity, wallType, shape, holeCount, notes } = req.body || {};
    if (wallType !== undefined && wallType !== '' && !WALL_TYPES.has(wallType)) return res.status(400).json({ error: 'invalid wallType' });
    if (shape !== undefined && shape !== '' && !SHAPES.has(shape)) return res.status(400).json({ error: 'invalid shape' });
    const basket = lib.baskets[idx];
    if (name         !== undefined) basket.name         = s(name, 200) || basket.name;
    if (doseCapacity !== undefined) basket.doseCapacity  = s(doseCapacity, 50);
    if (wallType     !== undefined) basket.wallType      = wallType;
    if (shape        !== undefined) basket.shape         = shape;
    if (holeCount    !== undefined) basket.holeCount     = s(holeCount, 50);
    if (notes        !== undefined) basket.notes         = s(notes, 1000);
    basket.updatedAt = Date.now();
    saveLibrary(lib);
    res.json(basket);
});

router.delete('/api/library/basket/:id', (req, res) => {
    const id     = parseInt(req.params.id, 10);
    const lib    = loadLibrary();
    const basket = (lib.baskets || []).find(b => b.id === id);
    if (basket?.image) deleteImage(id, basket.image, 'basket-');
    lib.baskets = (lib.baskets || []).filter(b => b.id !== id);
    saveLibrary(lib);
    res.json({ ok: true });
});

// Basket photo — direct upload from the user's device, same pattern as
// grinder photos (routes/library/grinders.js) added for #635.
router.post('/api/library/basket/:id/image',
    express.raw({ type: Object.keys(CONTENT_TYPE_EXT), limit: BEAN_IMAGE_MAX_BYTES }),
    (req, res) => {
        const id     = parseInt(req.params.id, 10);
        const lib    = loadLibrary();
        const basket = (lib.baskets || []).find(b => b.id === id);
        if (!basket) return res.status(404).json({ error: 'not found' });
        if (!Buffer.isBuffer(req.body) || req.body.length === 0) return res.status(400).json({ error: 'no image data' });
        const ext = saveUploadedImage('basket-', id, req.body, req.get('Content-Type'));
        if (!ext) return res.status(400).json({ error: 'unsupported image' });
        if (basket.image && basket.image !== ext) deleteImage(id, basket.image, 'basket-');
        basket.image = ext;
        saveLibrary(lib);
        res.json(basket);
    });

router.get('/api/library/basket/:id/image', (req, res) => {
    const id     = parseInt(req.params.id, 10);
    const lib    = loadLibrary();
    const basket = (lib.baskets || []).find(b => b.id === id);
    const ext    = basket?.image;
    if (!ext || !VALID_IMAGE_EXTS.has(ext)) return res.status(404).json({ error: 'no image' });
    res.setHeader('Cache-Control', 'public, max-age=86400');
    res.type(ext);
    res.sendFile(imagePath(id, ext, 'basket-'), err => { if (err && !res.headersSent) res.status(404).json({ error: 'no image' }); });
});

};
