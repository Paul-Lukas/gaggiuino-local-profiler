package machines

import (
	"context"
	"testing"
)

func testGaggiMateMachine(host string) *Machine {
	return &Machine{ID: 2, Name: "GaggiMate", Type: "gaggimate", Host: host, Enabled: true}
}

func TestGaggiMateAdapter_GetStatus(t *testing.T) {
	allowLoopbackMachineHost(t)
	fake := newFakeGaggiMateMachine()
	defer fake.Close()
	a := NewGaggiMateAdapter()

	status, err := a.GetStatus(context.Background(), testGaggiMateMachine(fake.URL))
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Reachable || status.Temperature != 92.5 || !status.Brewing {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Weight != nil {
		t.Errorf("Weight = %v, want nil (evt:status carries no weight field)", status.Weight)
	}
	if status.ProfileName == nil || *status.ProfileName != "Espresso" {
		t.Errorf("ProfileName = %v, want \"Espresso\"", status.ProfileName)
	}
}

func TestGaggiMateAdapter_ProfileListLoadSelect(t *testing.T) {
	allowLoopbackMachineHost(t)
	fake := newFakeGaggiMateMachine()
	fake.profiles = []map[string]any{{"id": float64(1), "name": "Espresso"}}
	defer fake.Close()
	a := NewGaggiMateAdapter()
	m := testGaggiMateMachine(fake.URL)
	ctx := context.Background()

	list, err := a.ListProfiles(ctx, m)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Espresso" {
		t.Fatalf("unexpected profile list: %+v", list)
	}

	raw, err := a.GetProfile(ctx, m, 1)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if !jsonContains(string(raw), `"name":"Espresso"`) {
		t.Fatalf("unexpected profile detail: %s", raw)
	}

	if err := a.SelectProfile(ctx, m, 1); err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
}

// GaggiMate's capabilities gate profile writes and the settings/control
// proxy off entirely — verifies the capability flags handlers.go checks
// before ever calling the corresponding adapter methods.
func TestGaggiMateAdapter_Capabilities(t *testing.T) {
	caps := NewGaggiMateAdapter().Capabilities()
	if caps.ProfileEdit {
		t.Error("GaggiMate Capabilities().ProfileEdit = true, want false (v1 UI-level gate)")
	}
	if caps.SettingsProxy {
		t.Error("GaggiMate Capabilities().SettingsProxy = true, want false")
	}
	if caps.BrewStart {
		t.Error("GaggiMate Capabilities().BrewStart = true, want false")
	}
}

func TestGaggiMateAdapter_SettingsProxyUnsupported(t *testing.T) {
	a := NewGaggiMateAdapter()
	m := testGaggiMateMachine("gaggimate.local")
	ctx := context.Background()

	if _, err := a.GetSettings(ctx, m, "boiler"); err == nil {
		t.Error("expected GetSettings to error for GaggiMate")
	}
	if err := a.Tare(ctx, m); err == nil {
		t.Error("expected Tare to error for GaggiMate")
	}
	if a.GetLiveSensorSnapshot(m) != nil {
		t.Error("expected GetLiveSensorSnapshot to be nil for GaggiMate")
	}
}
