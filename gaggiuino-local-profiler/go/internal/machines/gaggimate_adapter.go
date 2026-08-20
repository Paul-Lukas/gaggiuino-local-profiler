package machines

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines/proto"
)

// GaggiMateAdapter ports lib/machines/gaggimate/adapter.js. Experimental,
// same as Node: no real device was available to verify against in this
// environment either (see doc.go) — built strictly from the protocol
// description ws-client.js/profiles.js document. Live status and profile
// read/select are supported; profile create/update/delete are exposed
// through the Adapter interface (gaggimate_profiles.go's pass-throughs
// exist) but are unreachable from any REST route in this phase's scope —
// Capabilities().ProfileEdit is false, and every route that writes a
// profile checks that first (requireProfileEditSupport's Go port, see
// handlers.go) — matching capabilities()'s comment in the Node original
// that this is a deliberate v1 UI-level gate, not a protocol limitation.
type GaggiMateAdapter struct{}

func NewGaggiMateAdapter() *GaggiMateAdapter { return &GaggiMateAdapter{} }

var _ Adapter = (*GaggiMateAdapter)(nil)

// gaggimateBrewingMode ports ws-client.js's BREWING_MODE — evt:status's
// `m` (mode) field's exact enum isn't documented anywhere public; mode 1
// is treated as "brewing", best-effort/advisory, same caveat as Node.
const gaggimateBrewingMode = 1

func (a *GaggiMateAdapter) GetStatus(ctx context.Context, m *Machine) (Status, error) {
	baseURL, err := BaseURLFor(ctx, m)
	if err != nil {
		return Status{}, err
	}
	evt, err := gaggimateWaitForStatus(ctx, baseURL, 5*time.Second)
	if err != nil {
		return Status{}, err
	}
	raw, _ := json.Marshal(evt)
	profileName := looseStringPtr(evt["p"])
	return Status{
		Reachable:         true,
		Temperature:       looseFloat(evt["ct"]),
		TargetTemperature: looseFloat(evt["tt"]),
		Pressure:          looseFloat(evt["pr"]),
		Weight:            nil, // evt:status carries no weight field
		Brewing:           looseFloat(evt["m"]) == gaggimateBrewingMode,
		SteamOn:           nil,
		ProfileID:         nil,
		ProfileName:       profileName,
		Raw:               raw,
	}, nil
}

// GetLatestShotId/GetShot (lib/machines/gaggimate/history.js's
// index.bin/.slog binary parsing) are deliberately NOT ported — see
// doc.go: they back lib/sync.js's shot-history sync, a background/cron
// concern outside every REST endpoint this phase's task brief lists.

func (a *GaggiMateAdapter) ListProfiles(ctx context.Context, m *Machine) ([]ProfileSummary, error) {
	baseURL, err := BaseURLFor(ctx, m)
	if err != nil {
		return nil, err
	}
	return gaggimateListProfiles(ctx, baseURL)
}

func (a *GaggiMateAdapter) GetProfile(ctx context.Context, m *Machine, id int) (json.RawMessage, error) {
	baseURL, err := BaseURLFor(ctx, m)
	if err != nil {
		return nil, err
	}
	return gaggimateLoadProfile(ctx, baseURL, id)
}

// CreateProfile/UpdateProfile take the same Adapter-interface ProfileInput
// shape the Gaggiuino adapter uses, which doesn't correspond to
// GaggiMate's own arbitrary profile JSON (profiles.js's saveProfile passes
// a profile straight through, untyped) — moot in practice since
// Capabilities().ProfileEdit gates both off before any route reaches here
// (see this file's header comment), so these simply report the same
// "not supported" condition the capability gate already communicates.
func (a *GaggiMateAdapter) CreateProfile(ctx context.Context, m *Machine, profile ProfileInput) (ProfileSummary, error) {
	return ProfileSummary{}, fmt.Errorf("gaggimate machines do not support remote profile editing yet")
}

func (a *GaggiMateAdapter) UpdateProfile(ctx context.Context, m *Machine, profile ProfileInput) (ProfileSummary, error) {
	return ProfileSummary{}, fmt.Errorf("gaggimate machines do not support remote profile editing yet")
}

func (a *GaggiMateAdapter) DeleteProfile(ctx context.Context, m *Machine, id int) ([]ProfileSummary, error) {
	return nil, fmt.Errorf("gaggimate machines do not support remote profile editing yet")
}

func (a *GaggiMateAdapter) SelectProfile(ctx context.Context, m *Machine, id int) error {
	baseURL, err := BaseURLFor(ctx, m)
	if err != nil {
		return err
	}
	return gaggimateSelectProfile(ctx, baseURL, id)
}

func (a *GaggiMateAdapter) Capabilities() Capabilities {
	return Capabilities{
		ProfileEdit:   false, // protocol supports it (req:profiles:save/delete); UI-gated off in v1
		BrewStart:     false, // GaggiMate has no start/stop API at all
		Preheat:       nil,   // not modeled yet — unknown until verified against hardware
		Volumetric:    nil,   // determined per-shot from slog systemInfo.volumetricCapable, not a static capability
		History:       true,
		SettingsProxy: false,
	}
}

// ── #597 settings/control proxy: unsupported for GaggiMate (no exports in
// lib/machines/gaggimate/adapter.js at all) — every method below only
// exists to satisfy the Adapter interface; Capabilities().SettingsProxy
// == false means handlers.go's requireSettingsProxySupport 501s every
// caller before any of these could ever run. ───────────────────────────

func (a *GaggiMateAdapter) GetSettings(ctx context.Context, m *Machine, category string) (json.RawMessage, error) {
	return nil, errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) UpdateSettings(ctx context.Context, m *Machine, category string, payload json.RawMessage) (json.RawMessage, error) {
	return nil, errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) SaveSettings(ctx context.Context, m *Machine) error {
	return errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) SetOperationMode(ctx context.Context, m *Machine, mode proto.OperationMode) error {
	return errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) Tare(ctx context.Context, m *Machine) error {
	return errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) ServiceTest(ctx context.Context, m *Machine, peripheral proto.ServiceTestPeripheral) error {
	return errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) SaveActiveProfile(ctx context.Context, m *Machine) error {
	return errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) GetFirmwareProgress(ctx context.Context, m *Machine) (json.RawMessage, error) {
	return nil, errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) TriggerFirmwareUpdate(ctx context.Context, m *Machine) (json.RawMessage, error) {
	return nil, errSettingsProxyUnsupported
}
func (a *GaggiMateAdapter) GetLiveSensorSnapshot(m *Machine) *proto.SensorStateSnapshotDto {
	return nil
}
func (a *GaggiMateAdapter) GetLiveSystemState(m *Machine) *proto.SystemStateDto { return nil }
