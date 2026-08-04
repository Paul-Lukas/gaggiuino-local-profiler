const express = require('express');

const { loadLibrary, saveLibrary } = require('../../lib/data');
const { imagePath, CONTENT_TYPE_EXT, deleteImage, saveUploadedImage } = require('../../lib/services/ImageService');
const { BEAN_IMAGE_MAX_BYTES } = require('../../lib/constants');
const { rateLimit } = require('../../lib/helpers');

const VALID_IMAGE_EXTS = new Set(Object.values(CONTENT_TYPE_EXT));

// #635: puck screens. thickness is an enum validated against an allowlist
// server-side (not just trimmed) — same treatment as bean's species/category.
const THICKNESSES = new Set(['very-thin', 'thin', 'medium', 'thick']);

const s = (v, max) => (typeof v === 'string' ? v.trim().slice(0, max) : '');

// Registers puck screen routes onto a shared router — see
// routes/library/beans.js for why this isn't its own express.Router()
// mounted as a sub-router.
module.exports = function registerPuckScreenRoutes(router) {

router.get('/api/library/puckscreens', (req, res) => {
    const lib = loadLibrary();
    res.json(lib.puckScreens || []);
});

router.post('/api/library/puckscreen', (req, res) => {
    if (!rateLimit(`lib:${req.ip}`, 30)) return res.status(429).json({ error: 'Rate limit exceeded' });
    const { name, thickness, material, notes } = req.body || {};
    if (!name?.trim()) return res.status(400).json({ error: 'name required' });
    if (thickness && !THICKNESSES.has(thickness)) return res.status(400).json({ error: 'invalid thickness' });
    const lib = loadLibrary();
    const puckScreen = {
        id: Date.now(), name: s(name, 200), thickness: thickness || '',
        material: s(material, 200), notes: s(notes, 1000), updatedAt: Date.now(),
    };
    if (!Array.isArray(lib.puckScreens)) lib.puckScreens = [];
    lib.puckScreens.push(puckScreen);
    saveLibrary(lib);
    res.json(puckScreen);
});

router.put('/api/library/puckscreen/:id', (req, res) => {
    const id  = parseInt(req.params.id, 10);
    const lib = loadLibrary();
    const idx = (lib.puckScreens || []).findIndex(p => p.id === id);
    if (idx === -1) return res.status(404).json({ error: 'not found' });
    const { name, thickness, material, notes } = req.body || {};
    if (thickness !== undefined && thickness !== '' && !THICKNESSES.has(thickness)) return res.status(400).json({ error: 'invalid thickness' });
    const puckScreen = lib.puckScreens[idx];
    if (name      !== undefined) puckScreen.name      = s(name, 200) || puckScreen.name;
    if (thickness !== undefined) puckScreen.thickness = thickness;
    if (material  !== undefined) puckScreen.material  = s(material, 200);
    if (notes     !== undefined) puckScreen.notes     = s(notes, 1000);
    puckScreen.updatedAt = Date.now();
    saveLibrary(lib);
    res.json(puckScreen);
});

router.delete('/api/library/puckscreen/:id', (req, res) => {
    const id         = parseInt(req.params.id, 10);
    const lib        = loadLibrary();
    const puckScreen = (lib.puckScreens || []).find(p => p.id === id);
    if (puckScreen?.image) deleteImage(id, puckScreen.image, 'puckscreen-');
    lib.puckScreens = (lib.puckScreens || []).filter(p => p.id !== id);
    saveLibrary(lib);
    res.json({ ok: true });
});

// Puck screen photo — direct upload from the user's device, same pattern as
// grinder photos (routes/library/grinders.js) added for #635.
router.post('/api/library/puckscreen/:id/image',
    express.raw({ type: Object.keys(CONTENT_TYPE_EXT), limit: BEAN_IMAGE_MAX_BYTES }),
    (req, res) => {
        const id         = parseInt(req.params.id, 10);
        const lib        = loadLibrary();
        const puckScreen = (lib.puckScreens || []).find(p => p.id === id);
        if (!puckScreen) return res.status(404).json({ error: 'not found' });
        if (!Buffer.isBuffer(req.body) || req.body.length === 0) return res.status(400).json({ error: 'no image data' });
        const ext = saveUploadedImage('puckscreen-', id, req.body, req.get('Content-Type'));
        if (!ext) return res.status(400).json({ error: 'unsupported image' });
        if (puckScreen.image && puckScreen.image !== ext) deleteImage(id, puckScreen.image, 'puckscreen-');
        puckScreen.image = ext;
        saveLibrary(lib);
        res.json(puckScreen);
    });

router.get('/api/library/puckscreen/:id/image', (req, res) => {
    const id         = parseInt(req.params.id, 10);
    const lib        = loadLibrary();
    const puckScreen = (lib.puckScreens || []).find(p => p.id === id);
    const ext        = puckScreen?.image;
    if (!ext || !VALID_IMAGE_EXTS.has(ext)) return res.status(404).json({ error: 'no image' });
    res.setHeader('Cache-Control', 'public, max-age=86400');
    res.type(ext);
    res.sendFile(imagePath(id, ext, 'puckscreen-'), err => { if (err && !res.headersSent) res.status(404).json({ error: 'no image' }); });
});

};
