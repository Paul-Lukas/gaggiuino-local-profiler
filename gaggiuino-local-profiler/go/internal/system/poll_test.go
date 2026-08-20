package system

import (
	"context"
	"testing"
	"time"
)

// TestPollViaGaggiuinoStatus_MachineReachable is the #655 regression test:
// a powered-off/unreachable machine must be distinguishable from an
// idle-but-reachable one via machineReachable (false vs. true), not both
// collapsing to the same "isLive: false" shape.
func TestPollViaGaggiuinoStatus_MachineReachable(t *testing.T) {
	fake := &fakeAdapter{}
	fake.setStatus(okStatus(t, `{"waterLevel":80,"upTime":1234}`, 93.5, 94, 9, 18.2, false, "Espresso", 1), nil)
	p, _ := newTestPoller(t, fake)

	p.pollViaGaggiuinoStatus(context.Background())
	ld := p.LiveData()
	if ld.MachineReachable == nil || !*ld.MachineReachable {
		t.Fatalf("MachineReachable = %v, want true after a successful poll", ld.MachineReachable)
	}
	if ld.IsLive {
		t.Errorf("IsLive = true, want false (machine not brewing)")
	}

	fake.setStatus(machinesStatusZero(), errBoom)
	p.pollViaGaggiuinoStatus(context.Background())
	ld = p.LiveData()
	if ld.MachineReachable == nil || *ld.MachineReachable {
		t.Fatalf("MachineReachable = %v, want false after a failed poll", ld.MachineReachable)
	}
	// #655: still must NOT look identical to "isLive: false, reachable" —
	// the whole point of this field.
	if ld.IsLive {
		t.Errorf("IsLive = true, want false while unreachable")
	}
}

// TestPollViaGaggiuinoStatus_NoHostConfigured_SkipsCleanly ports #718: an
// unconfigured host must never flip machineReachable at all (stays nil,
// not false) — a false machineReachable specifically claims "this host was
// contacted and didn't answer," which isn't true when there's no host to
// contact.
func TestPollViaGaggiuinoStatus_NoHostConfigured_SkipsCleanly(t *testing.T) {
	sqlDB := newTestDB(t)
	registry := newRegistryForTest(t, sqlDB)
	hub := newHubForTest()
	haClient := newDisabledHAClient()
	poller := NewPoller(registry, fakeAdapterProvider{adapter: &fakeAdapter{}}, hub, haClient)

	poller.pollViaGaggiuinoStatus(context.Background())
	ld := poller.LiveData()
	if ld.MachineReachable != nil {
		t.Fatalf("MachineReachable = %v, want nil (never checked) when no host is configured", *ld.MachineReachable)
	}
}

// TestMachineStatus_AvailableAndStale exercises GET /api/machine/status'
// two booleans across a poll cycle.
func TestMachineStatus_AvailableAndStale(t *testing.T) {
	fake := &fakeAdapter{}
	fake.setStatus(okStatus(t, `{}`, 93.5, 94, 9, 18.2, false, "Espresso", 1), nil)
	p, sqlDB := newTestPoller(t, fake)
	demo := NewDemoService(sqlDB, nil, nil)
	h := NewHandlers(p, demo)
	mux := newSystemMux(h)

	// Before any poll: available:false.
	rec := doGet(mux, "/api/machine/status")
	body := decodeMap(t, rec.Body.Bytes())
	if body["available"] != false {
		t.Fatalf("available = %v, want false before any poll", body["available"])
	}

	p.pollViaGaggiuinoStatus(context.Background())
	rec = doGet(mux, "/api/machine/status")
	body = decodeMap(t, rec.Body.Bytes())
	if body["available"] != true {
		t.Fatalf("available = %v, want true after a poll", body["available"])
	}
	if body["stale"] != false {
		t.Fatalf("stale = %v, want false right after a fresh poll", body["stale"])
	}
	if body["temperature"] != 93.5 {
		t.Errorf("temperature = %v, want 93.5", body["temperature"])
	}

	// Force staleness by backdating updatedAt directly on the runtime.
	snap := p.Runtime().Get()
	backdated := *snap.MachineStatus
	backdated.UpdatedAt = time.Now().UnixMilli() - 11_000
	p.Runtime().SetMachineStatus(&backdated)
	rec = doGet(mux, "/api/machine/status")
	body = decodeMap(t, rec.Body.Bytes())
	if body["stale"] != true {
		t.Fatalf("stale = %v, want true once updatedAt is >10s old", body["stale"])
	}
}

// TestBrewAccumulation_LiveDataDatapoints exercises the isBrewing
// start/accumulate/stop cycle that feeds GET /api/live/data's datapoints.
func TestBrewAccumulation_LiveDataDatapoints(t *testing.T) {
	fake := &fakeAdapter{}
	p, _ := newTestPoller(t, fake)

	fake.setStatus(okStatus(t, `{}`, 93, 94, 9, 5, true, "Test Profile", 1), nil)
	p.pollViaGaggiuinoStatus(context.Background())
	ld := p.LiveData()
	if !ld.IsLive {
		t.Fatal("expected IsLive=true once brewSwitchState flips true")
	}
	if ld.ProfileName != "Test Profile" {
		t.Errorf("ProfileName = %q, want Test Profile", ld.ProfileName)
	}
	if ld.Datapoints == nil || len(ld.Datapoints.TimeInShot) != 1 {
		t.Fatalf("expected exactly one datapoint after the first brewing poll, got %+v", ld.Datapoints)
	}
	seqBeforeStop := ld.Seq

	fake.setStatus(okStatus(t, `{}`, 93, 94, 9, 9, true, "Test Profile", 1), nil)
	p.pollViaGaggiuinoStatus(context.Background())
	ld = p.LiveData()
	if len(ld.Datapoints.TimeInShot) != 2 {
		t.Fatalf("expected two datapoints after the second brewing poll, got %d", len(ld.Datapoints.TimeInShot))
	}

	fake.setStatus(okStatus(t, `{}`, 93, 94, 0, 9, false, "Test Profile", 1), nil)
	p.pollViaGaggiuinoStatus(context.Background())
	ld = p.LiveData()
	if ld.IsLive {
		t.Fatal("expected IsLive=false once brewSwitchState flips false")
	}
	if ld.Seq != seqBeforeStop+1 {
		t.Errorf("Seq = %d, want %d (incremented on brew finish)", ld.Seq, seqBeforeStop+1)
	}
}

// TestStopLivePolling_ForcesUnreachableFalse ports stopLivePolling's #655
// unconditional machineReachable=false flip.
func TestStopLivePolling_ForcesUnreachableFalse(t *testing.T) {
	fake := &fakeAdapter{}
	fake.setStatus(okStatus(t, `{}`, 93, 94, 9, 5, false, "Espresso", 1), nil)
	p, _ := newTestPoller(t, fake)

	p.pollViaGaggiuinoStatus(context.Background())
	if ld := p.LiveData(); ld.MachineReachable == nil || !*ld.MachineReachable {
		t.Fatalf("precondition failed: expected reachable=true, got %v", ld.MachineReachable)
	}

	p.stopLivePolling()
	ld := p.LiveData()
	if ld.MachineReachable == nil || *ld.MachineReachable {
		t.Fatalf("MachineReachable = %v, want false after stopLivePolling", ld.MachineReachable)
	}
}
