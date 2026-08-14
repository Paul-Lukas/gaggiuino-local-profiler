// #812: the 6 secret badges' name/description text, deliberately kept out of
// registry.js and out of public-src/i18n/*.js.
//
// What base64 here does and does NOT do: GLP is open source, so nothing
// server-side is secret from anyone willing to run the code and call
// decode() themselves -- this is obfuscation, not encryption, and it isn't
// meant to be. What it DOES stop is the much more common way a stamp card
// spoils itself: the name/description sitting in plaintext in the public
// i18n bundle (public-src/i18n/*.js) that ships to every browser on every
// page load, where a plain `grep -r "Schaltjahreskind"` (or just opening dev
// tools) reveals it before anyone has unlocked it. Keeping the encoded copy
// server-side and only decoding it into an API response after
// routes/achievements.js confirms the badge is unlocked (see
// lib/achievements/service.js's getState()) means the browser never
// receives the bytes at all until that's true.
const SECRETS = {
    secret_leap_day: {
        stamp: 'leaf',
        de: { name: 'U2NoYWx0amFocmVza2luZA==', desc: 'RWluIEJlenVnIGFuIGVpbmVtIDI5LiBGZWJydWFyLg==' },
        en: { name: 'TGVhcCBDaGlsZA==', desc: 'QSBzaG90IHB1bGxlZCBvbiBGZWJydWFyeSAyOXRoLg==' },
        it: { name: 'QmFtYmlubyBiaXNlc3RpbGU=', desc: 'VW4nZXN0cmF6aW9uZSBpbCAyOSBmZWJicmFpby4=' },
        fr: { name: 'RW5mYW50IGJpc3NleHRpbGU=', desc: 'VW5lIGV4dHJhY3Rpb24gbGUgMjkgZsOpdnJpZXIu' },
        es: { name: 'TmnDsW8gYmlzaWVzdG8=', desc: 'VW5hIGV4dHJhY2Npw7NuIGVsIDI5IGRlIGZlYnJlcm8u' },
        nl: { name: 'U2Nocmlra2Vsa2luZA==', desc: 'RWVuIHNob3QgZ2V0cm9ra2VuIG9wIDI5IGZlYnJ1YXJpLg==' },
    },
    secret_friday_13: {
        stamp: 'moon',
        de: { name: 'VW5nbMO8Y2tzYnJpbmdlcg==', desc: 'RWluIEJlenVnIGFuIGVpbmVtIEZyZWl0YWcsIGRlbSAxMy4=' },
        en: { name: 'QmFkIEx1Y2sgQ2hhcm0=', desc: 'QSBzaG90IHB1bGxlZCBvbiBGcmlkYXkgdGhlIDEzdGgu' },
        it: { name: 'UG9ydGFzZm9ydHVuYQ==', desc: 'VW4nZXN0cmF6aW9uZSBkaSB2ZW5lcmTDrCAxMy4=' },
        fr: { name: 'UG9ydGUtbWFsaGV1cg==', desc: 'VW5lIGV4dHJhY3Rpb24gdW4gdmVuZHJlZGkgMTMu' },
        es: { name: 'QW11bGV0byBkZSBtYWxhIHN1ZXJ0ZQ==', desc: 'VW5hIGV4dHJhY2Npw7NuIGVuIHZpZXJuZXMgMTMu' },
        nl: { name: 'T25nZWx1a3NicmVuZ2Vy', desc: 'RWVuIHNob3QgZ2V0cm9ra2VuIG9wIHZyaWpkYWcgZGUgMTNlLg==' },
    },
    secret_witching_hour: {
        stamp: 'clock',
        de: { name: 'R2Vpc3RlcnN0dW5kZQ==', desc: 'RWluIEJlenVnIGdlbmF1IHVtIDM6MzMgVWhyLg==' },
        en: { name: 'V2l0Y2hpbmcgSG91cg==', desc: 'QSBzaG90IHB1bGxlZCBhdCBleGFjdGx5IDM6MzMgYW0u' },
        it: { name: 'T3JhIGRlbGxlIHN0cmVnaGU=', desc: 'VW4nZXN0cmF6aW9uZSBlc2F0dGFtZW50ZSBhbGxlIDM6MzMu' },
        fr: { name: 'SGV1cmUgZGVzIHNvcmNpw6hyZXM=', desc: 'VW5lIGV4dHJhY3Rpb24gw6AgM2gzMyBwcsOpY2lzZXMu' },
        es: { name: 'SG9yYSBicnVqYQ==', desc: 'VW5hIGV4dHJhY2Npw7NuIGV4YWN0YW1lbnRlIGEgbGFzIDM6MzMu' },
        nl: { name: 'U3Bvb2t1dXI=', desc: 'RWVuIHNob3QgZ2V0cm9ra2VuIG9tIHByZWNpZXMgMzozMyB1dXIu' },
    },
    secret_new_year: {
        stamp: 'star',
        de: { name: 'TmV1amFocnNzY2hsdWNr', desc: 'RGVyIGVyc3RlIEJlenVnIGRlcyBKYWhyZXMsIGluIGRlciBhbGxlcmVyc3RlbiBNaW51dGUu' },
        en: { name: 'TmV3IFllYXIncyBTaXA=', desc: 'VGhlIHllYXIncyBmaXJzdCBzaG90LCBwdWxsZWQgd2l0aGluIGl0cyB2ZXJ5IGZpcnN0IG1pbnV0ZS4=' },
        it: { name: 'U29yc28gZGkgQ2Fwb2Rhbm5v', desc: 'SWwgcHJpbW8gY2FmZsOoIGRlbGwnYW5ubywgbmVsIHByaW1pc3NpbW8gbWludXRvLg==' },
        fr: { name: 'R29yZ8OpZSBkdSBOb3V2ZWwgQW4=', desc: 'TGUgcHJlbWllciBjYWbDqSBkZSBsJ2FubsOpZSwgZGFucyBzYSB0b3V0ZSBwcmVtacOocmUgbWludXRlLg==' },
        es: { name: 'U29yYm8gZGUgQcOxbyBOdWV2bw==', desc: 'RWwgcHJpbWVyIGNhZsOpIGRlbCBhw7FvLCBlbiBzdSBwcmltZXIgbWludXRvLg==' },
        nl: { name: 'TmlldXdqYWFyc3Nsb2s=', desc: 'RGUgZWVyc3RlIHNob3QgdmFuIGhldCBqYWFyLCBiaW5uZW4gZGUgYWxsZXJlZXJzdGUgbWludXV0Lg==' },
    },
    secret_palindrome_id: {
        stamp: 'target',
        de: { name: 'U3BpZWdlbGJpbGQ=', desc: 'RWluZSBCZXp1Z3NudW1tZXIsIGRpZSB2b3J3w6RydHMgd2llIHLDvGNrd8OkcnRzIGdlbGVzZW4gZ2xlaWNoIGJsZWlidC4=' },
        en: { name: 'TWlycm9yIEltYWdl', desc: 'QSBzaG90IG51bWJlciB0aGF0IHJlYWRzIHRoZSBzYW1lIGZvcndhcmRzIGFuZCBiYWNrd2FyZHMu' },
        it: { name: 'SW1tYWdpbmUgc3BlY3VsYXJl', desc: 'VW4gbnVtZXJvIGRpIGVzdHJhemlvbmUgY2hlIHNpIGxlZ2dlIHVndWFsZSBpbiBlbnRyYW1iaSBpIHNlbnNpLg==' },
        fr: { name: 'SW1hZ2UgbWlyb2ly', desc: 'VW4gbnVtw6lybyBkJ2V4dHJhY3Rpb24gcXVpIHNlIGxpdCBwYXJlaWwgZGFucyBsZXMgZGV1eCBzZW5zLg==' },
        es: { name: 'SW1hZ2VuIGVzcGVjdWxhcg==', desc: 'VW4gbsO6bWVybyBkZSBleHRyYWNjacOzbiBxdWUgc2UgbGVlIGlndWFsIGVuIGFtYm9zIHNlbnRpZG9zLg==' },
        nl: { name: 'U3BpZWdlbGJlZWxk', desc: 'RWVuIHNob3RudW1tZXIgZGF0IHZvb3ItIGVuIGFjaHRlcnN0ZXZvcmVuIGhldHplbGZkZSBpcy4=' },
    },
    secret_golden_shot: {
        stamp: 'drop',
        de: { name: 'R29sZGVuZXIgU2Nobml0dA==', desc: 'RWluIFZlcmjDpGx0bmlzIHZvbiBFaW53YWFnZSB6dSBBdXN3YWFnZSBnZW5hdSBhbSBHb2xkZW5lbiBTY2huaXR0IOKAlCAxOjEsNjE4Lg==' },
        en: { name: 'R29sZGVuIFJhdGlv', desc: 'QSBkb3NlLXRvLXlpZWxkIHJhdGlvIGxhbmRpbmcgZXhhY3RseSBvbiB0aGUgR29sZGVuIFJhdGlvIOKAlCAxOjEuNjE4Lg==' },
        it: { name: 'U2V6aW9uZSBhdXJlYQ==', desc: 'VW4gcmFwcG9ydG8gZG9zZS9yZXNhIGVzYXR0YW1lbnRlIHN1bGxhIHNlemlvbmUgYXVyZWEg4oCUIDE6MSw2MTgu' },
        fr: { name: 'Tm9tYnJlIGQnb3I=', desc: 'VW4gcmF0aW8gZG9zZS9yZW5kZW1lbnQgdG9tYmFudCBleGFjdGVtZW50IHN1ciBsZSBub21icmUgZCdvciDigJQgMToxLDYxOC4=' },
        es: { name: 'UHJvcG9yY2nDs24gw6F1cmVh', desc: 'VW5hIHByb3BvcmNpw7NuIGRlIGRvc2lzIGEgcmVuZGltaWVudG8gZXhhY3RhbWVudGUgZW4gbGEgcHJvcG9yY2nDs24gw6F1cmVhIOKAlCAxOjEsNjE4Lg==' },
        nl: { name: 'R3VsZGVuIHNuZWRl', desc: 'RWVuIGRvc2lzLXRvdC15aWVsZC12ZXJob3VkaW5nIGRpZSBwcmVjaWVzIG9wIGRlIGd1bGRlbiBzbmVkZSB1aXRrb210IOKAlCAxOjEsNjE4Lg==' },
    },
};

const FALLBACK_LANG = 'en';

function decode(b64) {
    return Buffer.from(b64, 'base64').toString('utf8');
}

// Returns { stamp, name, description } for a secret badge id in the given
// language, or null when the id isn't a known secret. Falls back to English
// for an unrecognised/missing lang, same convention as lib/notify-i18n.js.
function getSecretCopy(id, lang) {
    const entry = SECRETS[id];
    if (!entry) return null;
    const table = entry[lang] || entry[FALLBACK_LANG];
    return { stamp: entry.stamp, name: decode(table.name), description: decode(table.desc) };
}

const SECRET_IDS = Object.keys(SECRETS);

module.exports = { getSecretCopy, SECRET_IDS };
