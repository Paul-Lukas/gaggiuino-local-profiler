package machines

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"time"

	"nhooyr.io/websocket"
)

// This file ports lib/machines/gaggimate/ws-client.js's request()/
// waitForStatus() — the short-lived-connection-per-call JSON WebSocket
// client for GaggiMate machines. Protocol shape: one WebSocket at
// ws://<host>/ws, JSON frames with a `tp` (type) field; requests are
// `req:<name>` (optionally carrying an `rid` for correlation), answered by
// a `res:<name>` frame; the server also pushes unsolicited `evt:status`
// frames on its own cadence.
//
// ws-client.js's GaggiMateLiveClient (a third, persistent-connection
// pattern) IS ported now — gaggimate_live.go (#952): GaggiMateAdapter.GetStatus
// reads its cache and only falls back to gaggimateWaitForStatus below when
// the cache has no fresh frame yet.

const gaggimateWSTimeout = 8 * time.Second

func gaggimateWSURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/ws", scheme, u.Host), nil
}

// gaggimateRequest ports request(baseUrl, reqType, payload): sends one
// `req:<name>` frame with a request id for correlation, resolves with the
// payload of the first matching `res:<name>` frame that echoes the same
// rid. GaggiMate firmware echoes rid back as a string even though it's
// sent as a number (#342, live-verified) — the comparison below is
// type-tolerant (string(rid) either way), matching the Node original.
func gaggimateRequest(ctx context.Context, baseURL, reqType string, payload map[string]any) (map[string]any, error) {
	if len(reqType) < 4 || reqType[:4] != "req:" {
		return nil, fmt.Errorf("not a request type: %s", reqType)
	}
	resType := "res:" + reqType[4:]
	rid := rand.Intn(1_000_000_000)

	conn, ctx, cancel, err := wsConnect(ctx, baseURL, gaggimateWSURL, gaggimateWSTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer conn.CloseNow()

	frame := map[string]any{"tp": reqType, "rid": rid}
	for k, v := range payload {
		frame[k] = v
	}
	body, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("timed out waiting for %q from the machine", resType)
			}
			return nil, fmt.Errorf("waiting for %q from the machine: %w", resType, err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["tp"] != resType {
			continue
		}
		if msgRID, ok := msg["rid"]; ok && fmt.Sprint(msgRID) != fmt.Sprint(rid) {
			continue
		}
		conn.Close(websocket.StatusNormalClosure, "")
		return msg, nil
	}
}

func gaggimateSend(ctx context.Context, baseURL, reqType string, payload map[string]any) error {
	if len(reqType) < 4 || reqType[:4] != "req:" {
		return fmt.Errorf("not a request type: %s", reqType)
	}
	conn, ctx, cancel, err := wsConnect(ctx, baseURL, gaggimateWSURL, gaggimateWSTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	defer conn.CloseNow()

	frame := map[string]any{"tp": reqType}
	for k, v := range payload {
		frame[k] = v
	}
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	return nil
}

func gaggimateStartProcess(ctx context.Context, baseURL string, ignoreWarnings bool) error {
	return gaggimateSend(ctx, baseURL, "req:process:activate", map[string]any{"ignoreWarnings": ignoreWarnings})
}

func gaggimateCancelBrewConfirm(ctx context.Context, baseURL string) error {
	return gaggimateSend(ctx, baseURL, "req:brew:confirm:cancel", nil)
}

func gaggimateStartFlush(ctx context.Context, baseURL string) (map[string]any, error) {
	return gaggimateRequest(ctx, baseURL, "req:flush:start", nil)
}

func gaggimateStopFlush(ctx context.Context, baseURL string) (map[string]any, error) {
	return gaggimateRequest(ctx, baseURL, "req:flush:stop", nil)
}

func gaggimateOtaSettings(ctx context.Context, baseURL string, update bool, channel string) (map[string]any, error) {
	payload := map[string]any{"update": update}
	if channel != "" {
		payload["channel"] = channel
	}
	return gaggimateRequest(ctx, baseURL, "req:ota-settings", payload)
}

func gaggimateStartOta(ctx context.Context, baseURL, cp string) error {
	return gaggimateSend(ctx, baseURL, "req:ota-start", map[string]any{"cp": cp})
}

// gaggimateWaitForStatus ports waitForStatus(baseUrl): connects, waits for
// the first evt:status broadcast (unsolicited telemetry, not a
// request/response), resolves with its fields.
func gaggimateWaitForStatus(ctx context.Context, baseURL string, timeout time.Duration) (map[string]any, error) {
	conn, ctx, cancel, err := wsConnect(ctx, baseURL, gaggimateWSURL, timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer conn.CloseNow()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("timed out waiting for evt:status from the machine")
			}
			return nil, fmt.Errorf("waiting for evt:status from the machine: %w", err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["tp"] == "evt:status" {
			conn.Close(websocket.StatusNormalClosure, "")
			return msg, nil
		}
	}
}
