package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/db"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines/proto"
)

// fakeSettingsAdapter is a hand-written stand-in for machines.Adapter — the
// same pattern internal/system/helpers_test.go's own fakeAdapter
// establishes, and for the same reason: it sidesteps internal/machines'
// real SSRF guard entirely, which a loopback httptest.Server (the obvious
// alternative) is exactly what that guard exists to reject for a real
// machine host, and internal/machines/helpers_test.go's own
// allowLoopbackMachineHost seam is unexported and not reachable from this
// package. Only GetSettings/UpdateSettings/Capabilities are ever called by
// handlers_settings.go; every other method panics if this package's code
// ever changed to call it.
type fakeSettingsAdapter struct {
	caps      machines.Capabilities
	settings  map[string]json.RawMessage
	getErr    map[string]error
	updateErr error
}

var _ machines.Adapter = (*fakeSettingsAdapter)(nil)

func (f *fakeSettingsAdapter) Capabilities() machines.Capabilities { return f.caps }

func (f *fakeSettingsAdapter) GetSettings(ctx context.Context, m *machines.Machine, category string) (json.RawMessage, error) {
	if err, ok := f.getErr[category]; ok {
		return nil, err
	}
	return f.settings[category], nil
}

func (f *fakeSettingsAdapter) UpdateSettings(ctx context.Context, m *machines.Machine, category string, payload json.RawMessage) (json.RawMessage, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	f.settings[category] = payload
	return payload, nil
}

func (f *fakeSettingsAdapter) notImplemented(name string) error {
	panic("fakeSettingsAdapter: unexpected call to " + name)
}

func (f *fakeSettingsAdapter) GetStatus(context.Context, *machines.Machine) (machines.Status, error) {
	return machines.Status{}, f.notImplemented("GetStatus")
}
func (f *fakeSettingsAdapter) ListProfiles(context.Context, *machines.Machine) ([]machines.ProfileSummary, error) {
	return nil, f.notImplemented("ListProfiles")
}
func (f *fakeSettingsAdapter) GetProfile(context.Context, *machines.Machine, int) (json.RawMessage, error) {
	return nil, f.notImplemented("GetProfile")
}
func (f *fakeSettingsAdapter) CreateProfile(context.Context, *machines.Machine, machines.ProfileInput) (machines.ProfileSummary, error) {
	return machines.ProfileSummary{}, f.notImplemented("CreateProfile")
}
func (f *fakeSettingsAdapter) UpdateProfile(context.Context, *machines.Machine, machines.ProfileInput) (machines.ProfileSummary, error) {
	return machines.ProfileSummary{}, f.notImplemented("UpdateProfile")
}
func (f *fakeSettingsAdapter) DeleteProfile(context.Context, *machines.Machine, int) ([]machines.ProfileSummary, error) {
	return nil, f.notImplemented("DeleteProfile")
}
func (f *fakeSettingsAdapter) SelectProfile(context.Context, *machines.Machine, int) error {
	return f.notImplemented("SelectProfile")
}
func (f *fakeSettingsAdapter) SaveSettings(context.Context, *machines.Machine) error {
	return f.notImplemented("SaveSettings")
}
func (f *fakeSettingsAdapter) SetOperationMode(context.Context, *machines.Machine, proto.OperationMode) error {
	return f.notImplemented("SetOperationMode")
}
func (f *fakeSettingsAdapter) Tare(context.Context, *machines.Machine) error {
	return f.notImplemented("Tare")
}
func (f *fakeSettingsAdapter) ServiceTest(context.Context, *machines.Machine, proto.ServiceTestPeripheral) error {
	return f.notImplemented("ServiceTest")
}
func (f *fakeSettingsAdapter) SaveActiveProfile(context.Context, *machines.Machine) error {
	return f.notImplemented("SaveActiveProfile")
}
func (f *fakeSettingsAdapter) GetFirmwareProgress(context.Context, *machines.Machine) (json.RawMessage, error) {
	return nil, f.notImplemented("GetFirmwareProgress")
}
func (f *fakeSettingsAdapter) TriggerFirmwareUpdate(context.Context, *machines.Machine) (json.RawMessage, error) {
	return nil, f.notImplemented("TriggerFirmwareUpdate")
}
func (f *fakeSettingsAdapter) GetLiveSensorSnapshot(context.Context, *machines.Machine) (*proto.SensorStateSnapshotDto, error) {
	return nil, f.notImplemented("GetLiveSensorSnapshot")
}
func (f *fakeSettingsAdapter) GetLiveSystemState(context.Context, *machines.Machine) (*proto.SystemStateDto, error) {
	return nil, f.notImplemented("GetLiveSystemState")
}

// fakeSettingsAdapterProvider ports this package's AdapterProvider around a
// single fakeSettingsAdapter, regardless of which machine is asked for —
// every test here only ever has the one default machine.
type fakeSettingsAdapterProvider struct{ adapter *fakeSettingsAdapter }

func (p fakeSettingsAdapterProvider) GetAdapter(m *machines.Machine) (machines.Adapter, error) {
	return p.adapter, nil
}

func defaultTestSettings() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"boiler":  json.RawMessage(`{"brewDeltaState":"true","targetBrewTemp":93}`),
		"led":     json.RawMessage(`{"state":"false"}`),
		"scales":  json.RawMessage(`{"hwScalesEnabled":"true"}`),
		"system":  json.RawMessage(`{"releaseChannel":0}`),
		"display": json.RawMessage(`{"lcdDarkMode":"true","brightness":80}`),
	}
}

// newTestSettingsServer opens a throwaway on-disk SQLite DB (same pattern
// as newTestMachinesServer above), seeds the default machine, and wires a
// fresh web.SettingsHandlers around adapter routed through a real
// *http.ServeMux.
func newTestSettingsServer(t *testing.T, adapter *fakeSettingsAdapter) *http.ServeMux {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "glp.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	registry := machines.NewRegistry(sqlDB)
	if err := registry.EnsureDefaultMachine(); err != nil {
		t.Fatalf("EnsureDefaultMachine: %v", err)
	}

	h := NewSettingsHandlers(registry, fakeSettingsAdapterProvider{adapter: adapter})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

// ── Settings page ─────────────────────────────────────────────────────────

// TestSettingsPage_RendersCategoriesPreservingBoolAsStringQuirk verifies
// GET /settings renders every category's raw JSON verbatim — specifically
// that the bool-as-string quirk (led.state as the JSON *string* "false",
// not a real boolean — see internal/machines/doc.go) survives the
// pretty-print round trip unchanged, pinning that this page never decodes
// a category into a typed struct.
func TestSettingsPage_RendersCategoriesPreservingBoolAsStringQuirk(t *testing.T) {
	adapter := &fakeSettingsAdapter{
		caps:     machines.Capabilities{SettingsProxy: true},
		settings: defaultTestSettings(),
	}
	mux := newTestSettingsServer(t, adapter)

	rec := doRequest(t, mux, "GET", "/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`&#34;state&#34;: &#34;false&#34;`,         // led's bool-as-string quirk, unmodified (templ HTML-escapes the quotes)
		`&#34;lcdDarkMode&#34;: &#34;true&#34;`,    // display's, in the editable textarea
		`&#34;brewDeltaState&#34;: &#34;true&#34;`, // boiler's
		`hx-post="/settings/display"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /settings body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestSettingsPage_UnsupportedAdapter verifies the "this machine type
// doesn't support the settings proxy" branch (GaggiMate — see
// internal/machines.Capabilities.SettingsProxy) renders instead of trying
// to fetch any category.
func TestSettingsPage_UnsupportedAdapter(t *testing.T) {
	adapter := &fakeSettingsAdapter{caps: machines.Capabilities{SettingsProxy: false}}
	mux := newTestSettingsServer(t, adapter)

	rec := doRequest(t, mux, "GET", "/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not support the settings proxy") {
		t.Errorf("GET /settings body missing the unsupported-adapter message\nbody:\n%s", rec.Body.String())
	}
}

// TestSettingsPage_CategoryFetchErrorIsPerBlock verifies one category's
// fetch failure (e.g. the machine went unreachable mid-page) doesn't fail
// the whole page — every other category, and the editable form, must still
// render.
func TestSettingsPage_CategoryFetchErrorIsPerBlock(t *testing.T) {
	adapter := &fakeSettingsAdapter{
		caps:     machines.Capabilities{SettingsProxy: true},
		settings: defaultTestSettings(),
		getErr:   map[string]error{"boiler": errUnreachable},
	}
	mux := newTestSettingsServer(t, adapter)

	rec := doRequest(t, mux, "GET", "/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Could not reach machine") {
		t.Errorf("GET /settings body missing the boiler fetch-error message\nbody:\n%s", body)
	}
	if !strings.Contains(body, `hx-post="/settings/display"`) {
		t.Errorf("GET /settings: editable display form missing even though only boiler failed\nbody:\n%s", body)
	}
}

// ── Save (POST /settings/display) ────────────────────────────────────────

// TestSaveEditableAction_ForwardsRawBytesUnmodified pins the core contract
// this page's one write action must hold: the submitted textarea's bytes
// reach adapter.UpdateSettings byte-for-byte, including the bool-as-string
// quirk (a real bool true must NOT be re-quoted, and a quirky quoted
// "false" must NOT be unquoted) — see handlers_settings.go's doc comment.
func TestSaveEditableAction_ForwardsRawBytesUnmodified(t *testing.T) {
	adapter := &fakeSettingsAdapter{
		caps:     machines.Capabilities{SettingsProxy: true},
		settings: defaultTestSettings(),
	}
	mux := newTestSettingsServer(t, adapter)

	submitted := `{"lcdDarkMode":"false","brightness":true}`
	rec := doFormPost(t, mux, "/settings/display", url.Values{"raw": {submitted}})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /settings/display: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if string(adapter.settings["display"]) != submitted {
		t.Errorf("UpdateSettings received %q, want the exact submitted bytes %q", adapter.settings["display"], submitted)
	}
	if !strings.Contains(rec.Body.String(), `&#34;lcdDarkMode&#34;: &#34;false&#34;`) {
		t.Errorf("POST /settings/display response doesn't reflect the saved value\nbody:\n%s", rec.Body.String())
	}
}

// TestSaveEditableAction_InvalidJSONRejected verifies a non-object payload
// is rejected by machines.ValidateSettingsPayload before ever reaching
// UpdateSettings.
func TestSaveEditableAction_InvalidJSONRejected(t *testing.T) {
	adapter := &fakeSettingsAdapter{
		caps:     machines.Capabilities{SettingsProxy: true},
		settings: defaultTestSettings(),
	}
	mux := newTestSettingsServer(t, adapter)

	rec := doFormPost(t, mux, "/settings/display", url.Values{"raw": {"not json"}})
	if rec.Code != http.StatusOK { // htmx fragment error, not an HTTP-level failure — the form itself re-renders
		t.Fatalf("POST /settings/display with invalid JSON: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid settings payload") {
		t.Errorf("POST /settings/display body missing the validation error\nbody:\n%s", rec.Body.String())
	}
	if _, changed := adapter.settings["display"]; changed && string(adapter.settings["display"]) != string(defaultTestSettings()["display"]) {
		t.Errorf("UpdateSettings was called despite invalid JSON")
	}
}

// TestSettingsPagesRequireAuthBehindRequireToken verifies the one write
// action this page registers — POST /settings/display — requires either
// genuine HA Ingress or a valid X-GLP-Token, while GET /settings stays
// reachable without one, exactly like every earlier phase's write actions.
func TestSettingsPagesRequireAuthBehindRequireToken(t *testing.T) {
	const testToken = "test-fixture-token-not-a-real-secret"

	adapter := &fakeSettingsAdapter{
		caps:     machines.Capabilities{SettingsProxy: true},
		settings: defaultTestSettings(),
	}
	mux := newTestSettingsServer(t, adapter)
	handler := auth.RequireToken(testToken)(mux)

	doAuthedRequest := func(method, path, token string, body url.Values) *httptest.ResponseRecorder {
		var req *http.Request
		if body != nil {
			req = httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.RemoteAddr = "192.168.1.50:1234" // LAN, not Ingress/Supervisor
		if token != "" {
			req.Header.Set("X-GLP-Token", token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := doAuthedRequest("GET", "/settings", "", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /settings without a token: status = %d, want 200", rec.Code)
	}
	form := url.Values{"raw": {`{"lcdDarkMode":"true"}`}}
	if rec := doAuthedRequest("POST", "/settings/display", "", form); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /settings/display without a token: status = %d, want 401", rec.Code)
	}
	if rec := doAuthedRequest("POST", "/settings/display", testToken, form); rec.Code != http.StatusOK {
		t.Errorf("POST /settings/display with a valid token: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

var errUnreachable = &testError{"connection refused"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
