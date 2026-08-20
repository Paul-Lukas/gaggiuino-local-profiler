// Package ha ports the subset of lib/ha.js the Phase 1f orders domain
// needs: SendNotify (mobile-app push via HA's notify.* services),
// GetNotifyServices (for the notify-service picker), and GetPersons (for
// the shop-open/closed broadcast's "only notify devices currently home"
// filter and the notify-mapping customer list). getSwitchState,
// getHaLanguage, callHaService, and getHaState are NOT ported here — no
// Phase 1f route reaches them (getHaLanguage backs lib/notify-i18n.js,
// which nothing in orders/maintenance/backup calls; getSwitchState backs
// lib/poll.js's preheat polling, part of the still-unported system domain;
// callHaService/getHaState back Settings-page MQTT discovery, not part of
// this phase's HTTP surface at all).
//
// This package did not exist before Phase 1f: earlier phases (shots,
// library, machines) never needed to call the Home Assistant REST API
// itself, only to be reached *through* HA Ingress (internal/auth). Every
// function here degrades to a no-op/empty-result when no token is
// configured, exactly like the Node original — never an error, since HA
// integration is optional (direct-port mode with no Supervisor and no
// GLP_HA_URL/GLP_HA_TOKEN is a fully supported configuration).
package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client ports lib/ha.js's module-level HA_API/HA_TOKEN constants as
// instance fields instead of process-wide globals, so tests can construct
// one pointed at an httptest.Server. NewClientFromEnv() below reproduces
// lib/constants.js's exact derivation for the values cmd/server actually
// wires in.
type Client struct {
	apiBase string // "" when no HA connection is configured (Ingress-only / no Supervisor, no GLP_HA_URL)
	token   string
	http    *http.Client
}

// NewClientFromEnv ports lib/constants.js's HA_API/HA_TOKEN derivation:
//   - SUPERVISOR_TOKEN set (running under the Supervisor) -> HA_API is the
//     internal supervisor proxy, HA_TOKEN is SUPERVISOR_TOKEN.
//   - otherwise, GLP_HA_URL set (standalone Docker, #764) -> HA_API is
//     GLP_HA_URL + "/api", HA_TOKEN is GLP_HA_TOKEN (only read in this
//     branch, matching the Node original's `GLP_HA_URL ? ... GLP_HA_TOKEN
//     : undefined`).
//   - neither set -> apiBase/token both empty; every method below becomes
//     a no-op, matching HA_TOKEN's falsy short-circuit in lib/ha.js.
func NewClientFromEnv() *Client {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")
	glpHAURL := strings.TrimSuffix(os.Getenv("GLP_HA_URL"), "/")

	var apiBase, token string
	switch {
	case supervisorToken != "":
		apiBase = "http://supervisor/core/api"
		token = supervisorToken
	case glpHAURL != "":
		apiBase = glpHAURL + "/api"
		token = os.Getenv("GLP_HA_TOKEN")
	}
	return &Client{apiBase: apiBase, token: token, http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) enabled() bool { return c.token != "" }

// SendNotify ports lib/ha.js's sendHaNotify(service, title, message, tag):
// posts to HA's notify.<service> service call. Best-effort — logs (via the
// returned error, which every call site in this phase treats as
// non-critical/fire-and-forget, matching Node's own `.catch` swallowing)
// rather than failing the caller's HTTP response.
func (c *Client) SendNotify(ctx context.Context, service, title, message, tag string) error {
	if !c.enabled() || service == "" {
		return nil
	}
	domain, svcName, ok := strings.Cut(service, ".")
	if !ok {
		return nil
	}
	body := map[string]any{
		"title":   title,
		"message": message,
		"data": map[string]any{
			"tag": nullableString(tag),
			"push": map[string]any{
				"sound": "default",
			},
		},
	}
	_, err := c.post(ctx, "/services/"+domain+"/"+svcName, body)
	return err
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NotifyService mirrors one entry of GetNotifyServices()'s result.
type NotifyService struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetNotifyServices ports lib/ha.js's getNotifyServices(): every
// notify.<x> service registered in HA, excluding the generic notify.notify/
// notify.send_message aliases. Returns an empty (non-nil) slice, never an
// error, on any failure — matching the Node original's `catch { return []
// }`.
func (c *Client) GetNotifyServices(ctx context.Context) []NotifyService {
	out := []NotifyService{}
	if !c.enabled() {
		return out
	}
	var services []struct {
		Domain   string                     `json:"domain"`
		Services map[string]json.RawMessage `json:"services"`
	}
	if err := c.get(ctx, "/services", &services); err != nil {
		return out
	}
	for _, d := range services {
		if d.Domain != "notify" {
			continue
		}
		for name := range d.Services {
			if name == "notify" || name == "send_message" {
				continue
			}
			out = append(out, NotifyService{ID: "notify." + name, Name: strings.ReplaceAll(name, "_", " ")})
		}
	}
	return out
}

// Person mirrors one entry of GetPersons()'s result.
type Person struct {
	HAUserID string
	Name     string
	State    string
}

// GetPersons ports lib/ha.js's getHaPersons(): every person.* entity that
// carries a user_id attribute. Returns an empty (non-nil) slice on any
// failure, matching the Node original.
func (c *Client) GetPersons(ctx context.Context) []Person {
	out := []Person{}
	if !c.enabled() {
		return out
	}
	var states []struct {
		EntityID   string `json:"entity_id"`
		State      string `json:"state"`
		Attributes struct {
			UserID       string `json:"user_id"`
			FriendlyName string `json:"friendly_name"`
		} `json:"attributes"`
	}
	if err := c.get(ctx, "/states", &states); err != nil {
		return out
	}
	for _, e := range states {
		if !strings.HasPrefix(e.EntityID, "person.") || e.Attributes.UserID == "" {
			continue
		}
		name := e.Attributes.FriendlyName
		if name == "" {
			name = strings.TrimPrefix(e.EntityID, "person.")
		}
		state := e.State
		if state == "" {
			state = "not_home"
		}
		out = append(out, Person{HAUserID: e.Attributes.UserID, Name: name, State: state})
	}
	return out
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errStatus(resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(ctx context.Context, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return resp, errStatus(resp.StatusCode)
	}
	return resp, nil
}

type errStatus int

func (e errStatus) Error() string { return http.StatusText(int(e)) }
