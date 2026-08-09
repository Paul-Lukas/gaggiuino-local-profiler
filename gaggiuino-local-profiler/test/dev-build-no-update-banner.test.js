// #704: GLP_VERSION only moves at an actual release, so a dev build is
// permanently "behind" the last stable GitHub release tag by design.
// GET /api/version used to report update_available regardless, telling
// dev-channel users to update via the stable Home Assistant Add-on Store
// (wrong -- there's no store listing for GLP DEV, and it would just take
// them back to stable). Now guarded by process.env.GLP_DEV_BUILD, the same
// flag the "UNSTABLE DEV BUILD" banner (#683) and /api/status's devBuild
// field already use.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRequire } from 'module';
const require = createRequire(import.meta.url);

const systemPath = require.resolve('../routes/system');
const express = require('express');
const realFetch = globalThis.fetch;

// Routes the outbound GitHub releases lookup to a fake response while
// letting the test's own request to the local server through untouched --
// both go through the same global fetch since /api/version's handler uses
// it directly (no injectable HTTP client).
function stubGithubFetch(tag) {
    globalThis.fetch = vi.fn((url, ...rest) => {
        if (typeof url === 'string' && url.includes('api.github.com')) {
            return Promise.resolve({ ok: true, json: async () => ({ tag_name: tag }) });
        }
        return realFetch(url, ...rest);
    });
}

async function startServer() {
    delete require.cache[systemPath];
    const systemRouter = require('../routes/system');
    const app = express();
    app.use(systemRouter);
    const server = app.listen(0);
    await new Promise(resolve => server.once('listening', resolve));
    return { server, baseUrl: `http://127.0.0.1:${server.address().port}` };
}

describe('#704 GET /api/version suppresses update_available on a dev build', () => {
    let server;

    afterEach(async () => {
        if (server) await new Promise(resolve => server.close(resolve));
        delete require.cache[systemPath];
        delete process.env.GLP_DEV_BUILD;
        globalThis.fetch = realFetch;
    });

    it('reports update_available=true for a newer release when not a dev build', async () => {
        delete process.env.GLP_DEV_BUILD;
        stubGithubFetch('v9999.0.0');
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        const res = await realFetch(`${baseUrl}/api/version`);
        const data = await res.json();
        expect(data.update_available).toBe(true);
    });

    it('reports update_available=false for the same newer release when GLP_DEV_BUILD is set', async () => {
        process.env.GLP_DEV_BUILD = 'dev-20260809_0800';
        stubGithubFetch('v9999.0.0');
        let baseUrl;
        ({ server, baseUrl } = await startServer());
        const res = await realFetch(`${baseUrl}/api/version`);
        const data = await res.json();
        expect(data.update_available).toBe(false);
    });
});
