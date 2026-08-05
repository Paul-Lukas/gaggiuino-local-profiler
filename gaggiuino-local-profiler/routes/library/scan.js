const axios = require('axios');

const { validate } = require('../../lib/middleware/validate');
const { scanBarcodeSchema } = require('../../lib/validation/schemas');
const { assertPublicHost, SsrfBlockedError } = require('../../lib/ssrf-guard');
const { SCAN_FETCH_MAX_BYTES } = require('../../lib/constants');
const { log, rateLimit } = require('../../lib/helpers');

const OFF_HOST = 'world.openfoodfacts.org';

// The browser can't call Open Food Facts directly: server.js's CSP pins
// connect-src to 'self' (a deliberate hardening measure — config.yaml
// advertises the app as "all local, no cloud") so every client-side fetch to
// world.openfoodfacts.org was silently blocked, and the surrounding
// try/catch in public-src/views/library.js showed the same generic
// scan_error for every failure mode with nothing logged. This proxies the
// lookup server-side and returns only the fields the bean-import form needs
// instead of the OFF API's full product payload.
//
// Registers onto a shared router — see routes/library/beans.js for why this
// isn't its own express.Router() mounted as a sub-router.
module.exports = function registerScanRoutes(router) {

router.get('/api/library/scan/:barcode', validate(scanBarcodeSchema, 'params'), async (req, res) => {
    res.set('Cache-Control', 'no-store');
    if (!rateLimit(`scan:${req.ip}`, 20)) return res.status(429).json({ error: 'rate_limited' });
    const { barcode } = req.params;
    try {
        // world.openfoodfacts.org is a fixed hostname, not user input, but
        // this still guards against DNS-rebinding-style redirection of that
        // hostname to an internal address — same defensive pattern as
        // routes/import.js's safeGet() uses for genuinely untrusted hosts.
        await assertPublicHost(OFF_HOST);
        const r = await axios.get(`https://${OFF_HOST}/api/v3/product/${barcode}.json`, {
            headers: { 'User-Agent': 'GLP/1.0 (Gaggiuino Local Profiler; private use)' },
            timeout: 8000,
            maxRedirects: 0,
            maxContentLength: SCAN_FETCH_MAX_BYTES,
            validateStatus: s => s < 500,
        });
        const p = r.data?.product;
        if (r.status === 404 || !p) return res.status(404).json({ error: 'not_found' });
        const name    = p.product_name || p.product_name_de || p.product_name_en || '';
        const roaster = p.brands || '';
        const notes   = [
            p.categories_tags?.find(c => typeof c === 'string' && c.startsWith('en:'))?.replace('en:', '') || '',
            p.labels || '',
        ].filter(Boolean).join(', ');
        // OFF can return status 200 with an empty/placeholder product for an
        // unrecognised barcode — treat "nothing usable came back" the same
        // as a real 404 so the frontend shows scan_not_found, not a bean
        // form silently prefilled with blanks.
        if (!name && !roaster) return res.status(404).json({ error: 'not_found' });
        res.json({ name, roaster, notes });
    } catch (e) {
        if (e instanceof SsrfBlockedError) {
            log(`Barcode scan lookup blocked for ${barcode}: ${e.message}`, true);
            return res.status(502).json({ error: 'lookup_failed' });
        }
        log(`Barcode scan lookup failed for ${barcode}: ${e.message}`, true);
        res.status(502).json({ error: 'lookup_failed' });
    }
});

};
