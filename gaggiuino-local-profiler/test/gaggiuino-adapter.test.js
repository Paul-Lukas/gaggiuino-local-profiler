// lib/machines/gaggiuino/adapter.js's getProfile() and createProfile() —
// REST-with-WS-fallback (#577, #580). Newer firmware exposes GET
// /api/profile/:id and POST /api/profile as plain REST; older firmware
// still 404s there, so the adapter must fall back to the existing
// WebSocket path (lib/gaggiuino-ws-client.js's getProfileById/createProfile)
// rather than failing outright. One combined HTTP+WS server (same
// host:port, matching how wsUrlFor() derives the ws:// URL from the REST
// base URL) simulates both firmware generations by toggling whether
// /api/profile/:id and /api/profile respond.
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

describe('gaggiuino adapter getProfile (#577) / createProfile (#580)', () => {
    let httpServer, port, adapter, proto, restEnabled, restCreateEnabled;

    beforeAll(async () => {
        adapter = req('../lib/machines/gaggiuino/adapter');
        proto   = req('../lib/gaggiuino-proto');

        httpServer = http.createServer((request, response) => {
            const detailMatch = /^\/api\/profile\/(\d+)$/.exec(request.url);
            if (detailMatch && restEnabled) {
                response.writeHead(200, { 'Content-Type': 'application/json' });
                response.end(JSON.stringify({ name: 'REST Profile', phases: [], id: Number(detailMatch[1]) }));
                return;
            }
            if (request.method === 'POST' && request.url === '/api/profile') {
                if (!restCreateEnabled) {
                    response.writeHead(404, { 'Content-Type': 'text/plain' });
                    response.end('Not Found');
                    return;
                }
                let body = '';
                request.on('data', (chunk) => { body += chunk; });
                request.on('end', () => {
                    const { name } = JSON.parse(body);
                    response.writeHead(200, { 'Content-Type': 'application/json' });
                    response.end(JSON.stringify({ id: 99, name }));
                });
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
                } else if (envelope.action === proto.ND.CreateNewProfile) {
                    const p = proto.ProfileDto.fromBinary(envelope.data);
                    const dict = proto.SavedProfilesDto.create({ profiles: [{ id: 42, name: p.name }] });
                    const msg = proto.WebSocketMessageDto.create({
                        action: 'd_prof_dict', data: proto.SavedProfilesDto.toBinary(dict),
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

    it('createProfile uses the REST endpoint when the firmware supports it', async () => {
        restCreateEnabled = true;
        const machine = { host: `127.0.0.1:${port}`, type: 'gaggiuino' };
        const created = await adapter.createProfile(machine, { name: 'New Profile', phases: [] });
        expect(created).toEqual({ id: 99, name: 'New Profile' });
    });

    it('createProfile falls back to WebSocket when the REST endpoint 404s (older firmware)', async () => {
        restCreateEnabled = false;
        const machine = { host: `127.0.0.1:${port}`, type: 'gaggiuino' };
        const created = await adapter.createProfile(machine, { name: 'Older Firmware Profile', phases: [] });
        expect(created).toEqual({ id: 42, name: 'Older Firmware Profile' });
    });
});
