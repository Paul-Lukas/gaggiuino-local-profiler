// lib/machines/gaggiuino/adapter.js's getProfile() — REST-with-WS-fallback
// (#577). Newer firmware exposes GET /api/profile/:id as plain REST; older
// firmware still 404s there, so the adapter must fall back to the existing
// WebSocket path (lib/gaggiuino-ws-client.js's getProfileById) rather than
// failing outright. One combined HTTP+WS server (same host:port, matching
// how wsUrlFor() derives the ws:// URL from the REST base URL) simulates
// both firmware generations by toggling whether /api/profile/:id responds.
import { describe, it, expect, beforeAll, afterAll, vi } from 'vitest';
import { WebSocketServer } from 'ws';
import { createRequire } from 'module';
import http from 'http';

const req = createRequire(import.meta.url);

// Same SSRF-guard stub as test/gaggimate-ws-client.test.js — the mock
// device server lives on 127.0.0.1, which assertMachineHost() rejects by
// design for real machine hosts; stubbed here to isolate the REST/WS
// fallback behavior under test from that unrelated concern.
vi.spyOn(req('../lib/ssrf-guard'), 'assertMachineHost').mockResolvedValue();

describe('gaggiuino adapter getProfile (#577)', () => {
    let httpServer, port, adapter, proto, restEnabled;

    beforeAll(async () => {
        adapter = req('../lib/machines/gaggiuino/adapter');
        proto   = req('../lib/gaggiuino-proto');

        httpServer = http.createServer((request, response) => {
            const match = /^\/api\/profile\/(\d+)$/.exec(request.url);
            if (match && restEnabled) {
                response.writeHead(200, { 'Content-Type': 'application/json' });
                response.end(JSON.stringify({ name: 'REST Profile', phases: [], id: Number(match[1]) }));
                return;
            }
            response.writeHead(404, { 'Content-Type': 'text/plain' });
            response.end('Not Found');
        });

        const wss = new WebSocketServer({ server: httpServer, path: '/ws' });
        wss.on('connection', (ws) => {
            ws.on('message', (data) => {
                const envelope = proto.WebSocketMessageDto.fromBinary(data);
                if (envelope.action === proto.ND.GetProfileById) {
                    const { id } = proto.WebSocketProfileIdCommandDto.fromBinary(envelope.data);
                    const profile = proto.ProfileDto.create({ id, name: 'WS Fallback Profile', phases: [] });
                    const msg = proto.WebSocketMessageDto.create({
                        action: 'd_prof', data: proto.ProfileDto.toBinary(profile),
                    });
                    ws.send(proto.WebSocketMessageDto.toBinary(msg));
                }
            });
        });

        await new Promise(resolve => httpServer.listen(0, '127.0.0.1', resolve));
        port = httpServer.address().port;
    });

    afterAll(() => httpServer.close());

    it('uses the REST endpoint when the firmware supports it', async () => {
        restEnabled = true;
        const machine = { host: `127.0.0.1:${port}`, type: 'gaggiuino' };
        const profile = await adapter.getProfile(machine, 7);
        expect(profile).toEqual({ name: 'REST Profile', phases: [], id: 7 });
    });

    it('falls back to WebSocket when the REST endpoint 404s (older firmware)', async () => {
        restEnabled = false;
        const machine = { host: `127.0.0.1:${port}`, type: 'gaggiuino' };
        const profile = await adapter.getProfile(machine, 3);
        expect(profile.name).toBe('WS Fallback Profile');
        expect(profile.id).toBe(3);
    });
});
