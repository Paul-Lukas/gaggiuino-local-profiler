package system

import (
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines/proto"
)

func ptrInt(v int) *int         { return &v }
func ptrStr(v string) *string   { return &v }
func ptrF64(v float64) *float64 { return &v }
func ptrI64(v int64) *int64     { return &v }
func ptrBool(v bool) *bool      { return &v }

func TestDeriveMachineState_RESTOnly_NoLiveSession(t *testing.T) {
	res := deriveMachineState(DeriveInput{
		Status: RawStatus{
			WaterLevel: 80, UpTime: 1234, Brewing: false,
			Temperature: 93.5, TargetTemperature: 94, Pressure: 9.0, Weight: 0,
			ProfileID: ptrInt(1), ProfileName: ptrStr("Espresso"), SteamSwitchState: false,
		},
		Now: 1000,
	})

	if res.IsBrewing {
		t.Fatal("expected IsBrewing=false")
	}
	if res.ProfileName != "Espresso" {
		t.Errorf("ProfileName = %q, want Espresso", res.ProfileName)
	}
	ms := res.MachineStatus
	if ms.Temperature != 93.5 || ms.Pressure != 9.0 || ms.WaterLevel != 80 || ms.UpTime != 1234 {
		t.Errorf("unexpected base fields: %+v", ms)
	}
	if ms.UpdatedAt != 1000 {
		t.Errorf("UpdatedAt = %d, want 1000", ms.UpdatedAt)
	}
	// No live session -> every sensorSnap/sysState-sourced field must be
	// absent (nil), exactly like Node never assigning them outside the
	// `if (sensorSnap)`/`if (sysState)` blocks.
	if ms.PumpFlow != nil || ms.BoilerState != nil || ms.ThermocoupleFaulted != nil {
		t.Errorf("expected sensorSnap/sysState fields to be nil, got %+v", ms)
	}
}

func TestDeriveMachineState_ProfileNameFallsBackToUnknown(t *testing.T) {
	res := deriveMachineState(DeriveInput{Status: RawStatus{}, Now: 1})
	if res.ProfileName != "Unknown" {
		t.Errorf("ProfileName = %q, want Unknown", res.ProfileName)
	}
	if res.MachineStatus.ProfileName != nil {
		t.Errorf("MachineStatus.ProfileName should stay nil (Node's `status.profileName || null`), got %v", *res.MachineStatus.ProfileName)
	}
}

func TestDeriveMachineState_SensorSnapPreferredOverREST(t *testing.T) {
	res := deriveMachineState(DeriveInput{
		Status: RawStatus{Temperature: 10, Pressure: 1, Weight: 1, Brewing: true},
		Now:    1,
		SensorSnap: &proto.SensorStateSnapshotDto{
			Temperature: 93.2, Pressure: 8.8, Weight: 18.4, PumpFlow: 2.5,
			WeightFlow: 1.1, WaterTemperature: 95, BoilerState: true, ValveState: true,
		},
	})
	// #615: brewing detection stays REST-sourced even with a live sample.
	if !res.IsBrewing {
		t.Fatal("expected IsBrewing=true (REST-sourced, unaffected by SensorSnap)")
	}
	ms := res.MachineStatus
	if ms.Temperature != 93.2 || ms.Pressure != 8.8 || ms.Weight != 18.4 {
		t.Errorf("expected SensorSnap values to win, got temp=%v pressure=%v weight=%v",
			ms.Temperature, ms.Pressure, ms.Weight)
	}
	if ms.PumpFlow == nil || *ms.PumpFlow != 2.5 {
		t.Errorf("MachineStatus.PumpFlow = %v, want 2.5", ms.PumpFlow)
	}
	if ms.BoilerState == nil || !*ms.BoilerState {
		t.Errorf("MachineStatus.BoilerState should be true")
	}
}

func TestDeriveMachineState_SysStateAddsFaultFields(t *testing.T) {
	res := deriveMachineState(DeriveInput{
		Status: RawStatus{},
		Now:    1,
		SysState: &proto.SystemStateDto{
			ThermocoupleFaulted: true, ThermocoupleFaultReason: "open circuit",
			PressureSensorFaulted: false, PressureSensorFaultReason: "",
		},
	})
	ms := res.MachineStatus
	if ms.ThermocoupleFaulted == nil || !*ms.ThermocoupleFaulted {
		t.Fatal("expected ThermocoupleFaulted=true")
	}
	if ms.ThermocoupleFaultReason == nil || *ms.ThermocoupleFaultReason != "open circuit" {
		t.Errorf("ThermocoupleFaultReason = %v, want 'open circuit'", ms.ThermocoupleFaultReason)
	}
	if ms.PressureSensorFaulted == nil || *ms.PressureSensorFaulted {
		t.Errorf("expected PressureSensorFaulted=false (present, not nil)")
	}
}

func TestIsStillWarm(t *testing.T) {
	now := int64(1_000_000)
	cases := []struct {
		name        string
		currentTemp *float64
		switchOnAt  *int64
		switchOffAt *int64
		want        bool
	}{
		{"hot and never switched off", ptrF64(85), nil, nil, true},
		{"cold temp, never switched off", ptrF64(70), nil, nil, false},
		{"hot but switched off long ago", ptrF64(85), nil, ptrI64(now - warmOffMaxDur.Milliseconds() - 1000), false},
		{"hot, switched off recently", ptrF64(85), nil, ptrI64(now - 1000), true},
		{"no temp reading, was switched on, not cold-off", nil, ptrI64(now - 1000), nil, true},
		{"no temp reading, never switched on", nil, nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isStillWarm(tc.currentTemp, tc.switchOnAt, tc.switchOffAt, now)
			if got != tc.want {
				t.Errorf("isStillWarm() = %v, want %v", got, tc.want)
			}
		})
	}
}
