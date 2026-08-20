package machines

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines/proto"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/sse"
)

// This file ports lib/gaggiuino-live-client.js: a persistent, auto-
// reconnecting WebSocket session per machine baseURL that caches the
// continuously-pushed d_sensor_snap/d_sys_state frames (#597), read by
// GET /api/machine/live (via gaggiuinoAdapter.GetLiveSensorSnapshot/
// GetLiveSystemState) without opening a fresh connection per poll — the
// same rationale the Node original's header comment gives (staying within
// the firmware's WS_MAX_CONNECTIONS=3 budget regardless of poll frequency).
//
// New in this port (closing the loop the task brief asked for): every
// cache update also Publish()es onto the shared internal/sse.Hub as an
// EventLiveSnapshot event, the producer internal/sse's Phase 1b Prime/Hub
// scaffolding was left expecting (see internal/sse/doc.go). The payload
// shape here — {machineHost, sensorSnap} or {machineHost, sysState} — is
// this package's own machine-adapter-level snapshot, NOT necessarily the
// same shape openapi.yaml's LiveData schema documents for a *future*
// system-domain port of lib/poll.js's own LIVE_SNAPSHOT emission (which
// blends in shot-in-progress datapoints from a different source entirely —
// see routes/system.js's buildLiveDataResponse). Reconciling the two into
// one payload shape is system-domain work, out of this package's scope —
// see doc.go.
//
// A persistent GaggiMate equivalent (ws-client.js's GaggiMateLiveClient
// class) is deliberately NOT ported: every REST endpoint in this phase's
// scope that would read cached live GaggiMate data
// (GET /api/machine/live) is gated by requireSettingsProxySupport, and the
// GaggiMate adapter reports capabilities().settingsProxy == false — so
// that endpoint 501s for GaggiMate before ever reaching a live-cache read.
// The persistent class exists in Node only to feed lib/poll.js's
// multi-machine live-poll loop (system domain, not yet ported) — see
// gaggimate_ws.go's header comment for the short-lived request/
// waitForStatus functions this package does port (used by
// GaggiMateAdapter.GetStatus, reachable via POST /api/machines/{id}/test).

const (
	liveReconnectDelay = 3 * time.Second
	liveStaleAfter     = 15 * time.Second
)

type gaggiuinoLiveSession struct {
	mu           sync.Mutex
	sensorSnap   *proto.SensorStateSnapshotDto
	sensorSnapAt time.Time
	sysState     *proto.SystemStateDto
	sysStateAt   time.Time

	cancel context.CancelFunc
}

// gaggiuinoLiveClient ports gaggiuino-live-client.js's module-level
// `sessions` Map + connect()/disconnect() functions as a struct so
// cmd/server can own one instance instead of relying on Node's
// module-singleton pattern.
type gaggiuinoLiveClient struct {
	hub *sse.Hub

	mu       sync.Mutex
	sessions map[string]*gaggiuinoLiveSession
}

func newGaggiuinoLiveClient(hub *sse.Hub) *gaggiuinoLiveClient {
	return &gaggiuinoLiveClient{hub: hub, sessions: make(map[string]*gaggiuinoLiveSession)}
}

// session ports connect(baseUrl)'s lazy-open-or-reuse behavior.
func (c *gaggiuinoLiveClient) session(baseURL string) *gaggiuinoLiveSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sessions[baseURL]; ok {
		return s
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &gaggiuinoLiveSession{cancel: cancel}
	c.sessions[baseURL] = s
	go c.run(ctx, baseURL, s)
	return s
}

// run ports connect()'s ws.on('close'/'error', scheduleReconnect) loop:
// keep dialing baseURL, with a fixed RECONNECT_DELAY_MS pause between
// attempts, until ctx is cancelled (by Disconnect).
func (c *gaggiuinoLiveClient) run(ctx context.Context, baseURL string, s *gaggiuinoLiveSession) {
	for {
		if ctx.Err() != nil {
			return
		}
		c.connectOnce(ctx, baseURL, s)
		select {
		case <-ctx.Done():
			return
		case <-time.After(liveReconnectDelay):
		}
	}
}

// connectOnce dials once and reads frames until the connection closes or
// errors, updating s's cache (and publishing to the SSE hub) for every
// d_sensor_snap/d_sys_state push — ports connect()'s ws.on('message', ...).
func (c *gaggiuinoLiveClient) connectOnce(ctx context.Context, baseURL string, s *gaggiuinoLiveSession) {
	wsURL, err := gaggiuinoWSURL(baseURL)
	if err != nil {
		return
	}
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return // closed/errored/ctx cancelled — run() decides whether to reconnect
		}
		var envelope proto.WebSocketMessageDto
		if err := envelope.Unmarshal(data); err != nil {
			continue // not a valid envelope frame, ignore
		}
		// See ws.go's wsSendAndWait doc comment on the identical
		// `!envelope.data` check in gaggiuino-live-client.js's Node
		// original — always false in JS (an empty bytes field decodes to
		// a truthy empty Uint8Array, never null/undefined), so it never
		// actually filters there either; only the action switch below does.

		switch envelope.Action {
		case pushSensor:
			var snap proto.SensorStateSnapshotDto
			if err := snap.Unmarshal(envelope.Data); err != nil {
				continue
			}
			s.mu.Lock()
			s.sensorSnap = &snap
			s.sensorSnapAt = time.Now()
			s.mu.Unlock()
			c.publish(baseURL, map[string]any{"machineHost": baseURL, "sensorSnap": &snap})
		case pushSysState:
			var state proto.SystemStateDto
			if err := state.Unmarshal(envelope.Data); err != nil {
				continue
			}
			s.mu.Lock()
			s.sysState = &state
			s.sysStateAt = time.Now()
			s.mu.Unlock()
			c.publish(baseURL, map[string]any{"machineHost": baseURL, "sysState": &state})
		}
	}
}

func (c *gaggiuinoLiveClient) publish(baseURL string, data any) {
	if c.hub == nil {
		return
	}
	c.hub.Publish(sse.Event{Type: sse.EventLiveSnapshot, Data: data})
}

// freshOrNil ports gaggiuino-live-client.js's freshOrNull(): a cached value
// older than STALE_MS is reported as unavailable rather than served stale.
func freshOrNilAt(at time.Time) bool { return time.Since(at) > liveStaleAfter }

// GetLiveSensorSnapshot ports getLiveSensorSnapshot(baseUrl): lazily
// (re)opens the session as a side effect, same as the Node original.
func (c *gaggiuinoLiveClient) GetLiveSensorSnapshot(baseURL string) *proto.SensorStateSnapshotDto {
	s := c.session(baseURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sensorSnap == nil || freshOrNilAt(s.sensorSnapAt) {
		return nil
	}
	return s.sensorSnap
}

// GetLiveSystemState ports getLiveSystemState(baseUrl).
func (c *gaggiuinoLiveClient) GetLiveSystemState(baseURL string) *proto.SystemStateDto {
	s := c.session(baseURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sysState == nil || freshOrNilAt(s.sysStateAt) {
		return nil
	}
	return s.sysState
}

// Disconnect ports disconnect(baseUrl): closes and forgets exactly one
// machine's session.
func (c *gaggiuinoLiveClient) Disconnect(baseURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sessions[baseURL]
	if !ok {
		return
	}
	delete(c.sessions, baseURL)
	s.cancel()
}

// normalizeBaseURL ports gaggiuino-live-client.js's normalizeBaseUrl(host):
// the same session-key normalization connect() applies (scheme defaulted
// to http://, then re-serialized), minus the async SSRF check — eviction
// of a now-unreachable machine's stale session must not depend on that
// host still resolving. Returns ("", false) for an empty/unparseable host.
func normalizeBaseURL(host string) (string, bool) {
	raw := strings.TrimSpace(host)
	if raw == "" {
		return "", false
	}
	withScheme := raw
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		withScheme = "http://" + raw
	}
	u, err := url.Parse(withScheme)
	if err != nil {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// DisconnectForHost ports gaggiuino-live-client.js's disconnectForHost(host)
// — registry.go's UpdateMachine/DeleteMachine onHostChanged/onHostEvicted
// callbacks wire straight to this.
func (c *gaggiuinoLiveClient) DisconnectForHost(host string) {
	baseURL, ok := normalizeBaseURL(host)
	if ok {
		c.Disconnect(baseURL)
	}
}
