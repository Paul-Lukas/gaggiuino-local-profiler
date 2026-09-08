package system

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// These tests exercise gaggiMatePhaseResolver directly rather than the full
// syncGaggiMateShots orchestration: that function's first real step is
// machines.BaseURLFor, which runs the actual SSRF/DNS guard (see
// helpers_test.go's fakeAdapter doc comment) — a hostname like
// "fake-machine.invalid" (RFC 2606, guaranteed never to resolve) always
// fails there by design, and the guard's internals are unexported and not
// reachable from this package (confirmed: internal/machines/ssrf_test.go's
// lookupIPAddr override and internal/machines/helpers_test.go's
// machineHostGuard seam are both package-private). syncGaggiMateShots
// already degrades correctly when BaseURLFor fails (returns nil, logs, and
// retries next tick — see TestSyncDefaultMachineShots_GaggiMateProbesInsteadOfHammering
// in sync_triggers_test.go, which relies on exactly that). The phase-
// resolution and persistence logic these tests actually care about lives
// entirely in gaggiMatePhaseResolver and the shot["gmPhases"]=...+Upsert
// wiring, neither of which touches the network layer, so testing at that
// level gives full coverage without fighting the SSRF guard.
func TestGaggiMatePhaseResolver_ResolvesPhasesByProfileName(t *testing.T) {
	fake := &fakeAdapter{
		profilesOK: true,
		profiles: []machines.ProfileSummary{
			{ID: "morning-id", Name: "Morning"},
		},
		profileBodies: map[string]json.RawMessage{
			"morning-id": json.RawMessage(`{"id":"morning-id","label":"Morning","phases":[{"name":"Preinfusion","phase":"preinfusion","duration":8},{"name":"Brew","phase":"brew","duration":24}]}`),
		},
	}
	machine := &machines.Machine{ID: 1, Type: "gaggimate"}
	resolver := newGaggiMatePhaseResolver(context.Background(), machine, fake)

	shot := map[string]any{"profileName": "Morning"}
	phases, ok := resolver.phasesForShot(context.Background(), shot)
	if !ok {
		t.Fatal("phasesForShot: ok = false, want true")
	}
	if len(phases) != 2 {
		t.Fatalf("phasesForShot: len = %d, want 2; phases=%+v", len(phases), phases)
	}
	first, _ := phases[0].(map[string]any)
	if first["name"] != "Preinfusion" || first["phase"] != "preinfusion" || first["duration"] != float64(8) {
		t.Fatalf("first phase = %+v", first)
	}

	// Second call for the same profile must hit the resolver's cache, not
	// GetProfile again — GetProfile isn't call-counted here, so this just
	// asserts the cached result stays identical.
	again, ok := resolver.phasesForShot(context.Background(), shot)
	if !ok || len(again) != 2 {
		t.Fatalf("second phasesForShot call: ok=%v len=%d, want ok=true len=2", ok, len(again))
	}
}

func TestGaggiMatePhaseResolver_DegradesGracefullyWhenProfileListUnavailable(t *testing.T) {
	fake := &fakeAdapter{profilesErr: errors.New("profile service unavailable")}
	machine := &machines.Machine{ID: 1, Type: "gaggimate"}
	resolver := newGaggiMatePhaseResolver(context.Background(), machine, fake)

	shot := map[string]any{"profileName": "Missing"}
	phases, ok := resolver.phasesForShot(context.Background(), shot)
	if ok || phases != nil {
		t.Fatalf("phasesForShot with unavailable profile list: ok=%v phases=%+v, want ok=false phases=nil", ok, phases)
	}
}

func TestGaggiMatePhaseResolver_DegradesGracefullyWhenProfileNameUnmatched(t *testing.T) {
	fake := &fakeAdapter{
		profilesOK: true,
		profiles: []machines.ProfileSummary{
			{ID: "morning-id", Name: "Morning"},
		},
	}
	machine := &machines.Machine{ID: 1, Type: "gaggimate"}
	resolver := newGaggiMatePhaseResolver(context.Background(), machine, fake)

	shot := map[string]any{"profileName": "Some Other Profile"}
	phases, ok := resolver.phasesForShot(context.Background(), shot)
	if ok || phases != nil {
		t.Fatalf("phasesForShot with unmatched profile name: ok=%v phases=%+v, want ok=false phases=nil", ok, phases)
	}
}

// TestGaggiMateShotUpsert_PersistsGmPhases confirms the other half of the
// feature — once syncGaggiMateShots has resolved phases and set
// shot["gmPhases"], that value survives the ordinary Upsert/FindByID round
// trip via shots.Repository's existing "any unrecognized key goes into the
// data JSON blob" behavior (repository.go's shotInsertArgs). No network or
// machines-package involvement needed for this half either.
func TestGaggiMateShotUpsert_PersistsGmPhases(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := shots.NewRepository(sqlDB)

	shot := shots.Shot{
		"id":          int64(1),
		"timestamp":   int64(1788825600),
		"duration":    int64(310),
		"machineId":   int64(1),
		"profileName": "Morning",
		"gmPhases": []any{
			map[string]any{"name": "Preinfusion", "phase": "preinfusion", "duration": float64(8)},
			map[string]any{"name": "Brew", "phase": "brew", "duration": float64(24)},
		},
	}
	if err := repo.Upsert(shot); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	stored, err := repo.FindByID(1)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	phases, _ := stored["gmPhases"].([]any)
	if len(phases) != 2 {
		t.Fatalf("gmPhases len = %d, want 2; shot=%+v", len(phases), stored)
	}
	first, _ := phases[0].(map[string]any)
	if first["name"] != "Preinfusion" {
		t.Fatalf("first gmPhase = %+v", first)
	}
}

// TestGaggiMateShotUpsert_NoGmPhasesWhenNotSet confirms the "profile lookup
// failed, degrade gracefully" half: a shot Upserted WITHOUT gmPhases set
// (the syncGaggiMateShots behavior when phasesForShot returns ok=false)
// stores and reads back fine, with no gmPhases key at all — no broken/empty
// overlay on the frontend, no failed sync.
func TestGaggiMateShotUpsert_NoGmPhasesWhenNotSet(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := shots.NewRepository(sqlDB)

	shot := shots.Shot{
		"id":          int64(1),
		"timestamp":   int64(1788825600),
		"duration":    int64(310),
		"machineId":   int64(1),
		"profileName": "Missing",
	}
	if err := repo.Upsert(shot); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	stored, err := repo.FindByID(1)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored == nil {
		t.Fatal("shot was not stored")
	}
	if _, ok := stored["gmPhases"]; ok {
		t.Fatalf("gmPhases present despite never being set: %+v", stored["gmPhases"])
	}
}

// TestBackfillGaggiMatePhases_ResolvesOnlyShotsMissingIt is the regression
// test for the 2026-09-08 request: shots synced before the
// phase-persistence feature landed have no gmPhases and, unlike new shots,
// the regular sync loop never revisits them (it only walks shots newer
// than what's already stored) — this is the one-time manual catch-up path.
func TestBackfillGaggiMatePhases_ResolvesOnlyShotsMissingIt(t *testing.T) {
	fake := &fakeAdapter{
		profilesOK: true,
		profiles: []machines.ProfileSummary{
			{ID: "morning-id", Name: "Morning"},
		},
		profileBodies: map[string]json.RawMessage{
			"morning-id": json.RawMessage(`{"id":"morning-id","label":"Morning","phases":[{"name":"Preinfusion","phase":"preinfusion","duration":8},{"name":"Brew","phase":"brew","duration":24}]}`),
		},
	}
	p, sqlDB := newTestPoller(t, fake)
	gm := "gaggimate"
	if _, err := machines.NewRegistry(sqlDB).UpdateMachine(1, machines.MachineInput{Type: &gm}, nil); err != nil {
		t.Fatalf("set machine type: %v", err)
	}
	repo := shots.NewRepository(sqlDB)
	p.SetShotsRepo(repo)

	// Shot 1: no gmPhases yet (the pre-feature backlog this exists for).
	if err := repo.Upsert(shots.Shot{
		"id": int64(1), "timestamp": int64(1788825600), "duration": int64(300),
		"machineId": int64(1), "profileName": "Morning",
	}); err != nil {
		t.Fatalf("seed shot 1: %v", err)
	}
	// Shot 2: already has gmPhases — must be left untouched (not
	// re-resolved, not double-counted in the updated total).
	if err := repo.Upsert(shots.Shot{
		"id": int64(2), "timestamp": int64(1788825700), "duration": int64(300),
		"machineId": int64(1), "profileName": "Morning",
		"gmPhases": []any{map[string]any{"name": "Already Set", "phase": "brew", "duration": float64(30)}},
	}); err != nil {
		t.Fatalf("seed shot 2: %v", err)
	}

	updated, err := p.BackfillGaggiMatePhases(context.Background())
	if err != nil {
		t.Fatalf("BackfillGaggiMatePhases: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	shot1, err := repo.FindByID(1)
	if err != nil {
		t.Fatalf("FindByID(1): %v", err)
	}
	phases, _ := shot1["gmPhases"].([]any)
	if len(phases) != 2 {
		t.Fatalf("shot 1 gmPhases len = %d, want 2; shot=%+v", len(phases), shot1)
	}

	shot2, err := repo.FindByID(2)
	if err != nil {
		t.Fatalf("FindByID(2): %v", err)
	}
	phases2, _ := shot2["gmPhases"].([]any)
	if len(phases2) != 1 {
		t.Fatalf("shot 2 gmPhases should stay untouched, len = %d, want 1", len(phases2))
	}
	first, _ := phases2[0].(map[string]any)
	if first["name"] != "Already Set" {
		t.Fatalf("shot 2 gmPhases was overwritten: %+v", first)
	}
}
