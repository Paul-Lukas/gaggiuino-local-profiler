package system

import (
	"net/http"
	"testing"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

func newFullTestHandlers(t *testing.T) (*Handlers, *http.ServeMux, *Poller) {
	t.Helper()
	fake := &fakeAdapter{}
	p, sqlDB := newTestPoller(t, fake)
	demo := NewDemoService(sqlDB, shots.NewRepository(sqlDB), library.NewRepository(sqlDB))
	h := NewHandlers(p, demo)
	return h, newSystemMux(h), p
}

// TestGetPreheat_NeverSwitchedOn ports buildPreheatResponse's "machine off
// / never switched on" branch — ready:false, and stabilityReady must be
// entirely absent from the JSON (Node's object literal omits the key on
// this branch, see preheat.go's PreheatStatus doc comment).
func TestGetPreheat_NeverSwitchedOn(t *testing.T) {
	_, mux, _ := newFullTestHandlers(t)
	rec := doGet(mux, "/api/preheat")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeMap(t, rec.Body.Bytes())
	if body["ready"] != false {
		t.Errorf("ready = %v, want false", body["ready"])
	}
	if _, present := body["stabilityReady"]; present {
		t.Errorf("stabilityReady should be entirely absent, got %v", body["stabilityReady"])
	}
	if body["preheatTime"] != float64(20) {
		t.Errorf("preheatTime = %v, want 20 (default)", body["preheatTime"])
	}
}

// TestPostPreheatReadyBy_InvalidTargetAt ports the 400 "targetAt must be
// an epoch-ms number or null" contract.
func TestPostPreheatReadyBy_InvalidTargetAt(t *testing.T) {
	_, mux, _ := newFullTestHandlers(t)
	rec := doPost(mux, "/api/preheat/ready-by", `{"targetAt":"not-a-number"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeMap(t, rec.Body.Bytes())
	if body["error"] == nil {
		t.Error("expected an error field")
	}
}

// TestPostPreheatReadyBy_NoSwitchEntity_Rejects ports the 400
// "switch_entity nicht konfiguriert" contract: setting (not clearing) a
// target without a usable HA switch is rejected up front.
func TestPostPreheatReadyBy_NoSwitchEntity_Rejects(t *testing.T) {
	_, mux, _ := newFullTestHandlers(t)
	rec := doPost(mux, "/api/preheat/ready-by", `{"targetAt":9999999999999}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (no switch_entity/HA token configured in this test)", rec.Code)
	}
	body := decodeMap(t, rec.Body.Bytes())
	if body["error"] != "switch_entity nicht konfiguriert" {
		t.Errorf("error = %v, want 'switch_entity nicht konfiguriert'", body["error"])
	}
}

// TestPostPreheatReadyBy_NullClears verifies targetAt: null is always
// accepted (cancelling a target never needs a switch entity).
func TestPostPreheatReadyBy_NullClears(t *testing.T) {
	_, mux, p := newFullTestHandlers(t)
	rec := doPost(mux, "/api/preheat/ready-by", `{"targetAt":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	p.state.mu.Lock()
	readyBy := p.state.readyByTargetAt
	p.state.mu.Unlock()
	if readyBy != nil {
		t.Errorf("readyByTargetAt = %v, want nil after clearing", *readyBy)
	}
}

// TestGetVersion_ReturnsCurrent exercises GET /api/version against no
// network access (the GitHub fetch will fail in this sandboxed test — the
// contract must still degrade to {current, latest: null, update_available:
// false, release_url}, never an error response).
func TestGetVersion_ReturnsCurrent(t *testing.T) {
	_, mux, _ := newFullTestHandlers(t)
	rec := doGet(mux, "/api/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeMap(t, rec.Body.Bytes())
	if body["current"] != glpVersion {
		t.Errorf("current = %v, want %v", body["current"], glpVersion)
	}
	if _, present := body["release_url"]; !present {
		t.Error("expected a release_url field")
	}
}

// TestDemoSeedAndEnd exercises the full POST /api/demo/seed -> POST
// /api/demo/end round trip against a real temp SQLite DB.
func TestDemoSeedAndEnd(t *testing.T) {
	_, mux, _ := newFullTestHandlers(t)

	rec := doPost(mux, "/api/demo/seed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("seed status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeMap(t, rec.Body.Bytes())
	if body["isDemo"] != true {
		t.Errorf("isDemo = %v, want true", body["isDemo"])
	}

	// Seeding again on top of demo data must 409 (not empty anymore).
	rec = doPost(mux, "/api/demo/seed", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second seed status = %d, want 409", rec.Code)
	}

	rec = doPost(mux, "/api/demo/end", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("end status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body = decodeMap(t, rec.Body.Bytes())
	if body["isDemo"] != false {
		t.Errorf("isDemo = %v, want false", body["isDemo"])
	}

	// Seeding must succeed again now that the demo data is gone.
	rec = doPost(mux, "/api/demo/seed", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("re-seed status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

// TestLiveData_DefaultShape exercises GET /api/live/data before any poll
// has happened — isLive:false, seq:0, machineReachable: null (never
// checked, distinct from false).
func TestLiveData_DefaultShape(t *testing.T) {
	_, mux, _ := newFullTestHandlers(t)
	rec := doGet(mux, "/api/live/data")
	body := decodeMap(t, rec.Body.Bytes())
	if body["isLive"] != false {
		t.Errorf("isLive = %v, want false", body["isLive"])
	}
	if body["machineReachable"] != nil {
		t.Errorf("machineReachable = %v, want null (never polled)", body["machineReachable"])
	}
	if body["datapoints"] != nil {
		t.Errorf("datapoints = %v, want null", body["datapoints"])
	}
}
