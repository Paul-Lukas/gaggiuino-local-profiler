const express = require('express');
const router  = express.Router();

const achievementService = require('../lib/services/AchievementService');
const { CARD_KEYS } = require('../lib/achievements/registry');

const SUPPORTED_LANGS = new Set(['de', 'en', 'it', 'fr', 'es', 'nl']);

// #812: badge state + progress for the stamp card view. Open badges always
// carry their real `stamp` (frontend owns name/description via its own
// public-src/i18n/*.js, keyed by id); secret badges carry neither `stamp`
// nor `name`/`description` until `unlocked` is true -- see
// lib/achievements/secrets.js's header comment for exactly what that does
// and doesn't protect against.
router.get('/api/achievements', (req, res, next) => {
    try {
        const lang = SUPPORTED_LANGS.has(req.query.lang) ? req.query.lang : 'en';
        res.json({ cards: CARD_KEYS, badges: achievementService.getState(lang) });
    } catch (err) { next(err); }
});

module.exports = router;
