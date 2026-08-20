package system

import "sync"

// RuntimeState ports lib/machine-runtime-state.js's MachineRuntimeState:
// per-machine polling/preheat state. #549's hard-single-machine assumption
// holds throughout this package exactly as it does in every Node file this
// package replaces (lib/poll.js, lib/preheat.js, the live-status portion of
// routes/system.js) — Poller only ever drives one instance, obtained once
// at construction, same lifetime as Node's module-level defaultRuntime.
//
// Every field is unexported and reached only through the locked methods
// below: Node's single-threaded event loop needed no mutex, but Poller's
// 1s poll tick, the 30s HA-check tick, the 30s preheat tick, and concurrent
// HTTP handler reads (GET /api/machine/status, /api/preheat, /api/live/data)
// all touch this concurrently in Go.
type RuntimeState struct {
	mu sync.Mutex

	machineOn         bool
	currentTemp       *float64
	currentTargetTemp *float64
	tempHistory       []float64
	switchOnAt        *int64 // epoch ms, nil = never switched on this session
	switchOffAt       *int64 // epoch ms, nil = never switched off this session
	stabilityReady    bool
	machineStatus     *MachineStatus
}

// NewRuntimeState returns a zero-value RuntimeState, ready to use — ports
// MachineRuntimeState's constructor.
func NewRuntimeState() *RuntimeState {
	return &RuntimeState{}
}

// Snapshot is a point-in-time, lock-free copy of the fields
// buildPreheatResponse/buildLiveDataResponse/the machine/status handler
// need — computed once under the lock, then read freely.
type Snapshot struct {
	MachineOn         bool
	CurrentTemp       *float64
	CurrentTargetTemp *float64
	SwitchOnAt        *int64
	SwitchOffAt       *int64
	StabilityReady    bool
	MachineStatus     *MachineStatus
}

// Get returns a Snapshot of every field.
func (rs *RuntimeState) Get() Snapshot {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return Snapshot{
		MachineOn:         rs.machineOn,
		CurrentTemp:       rs.currentTemp,
		CurrentTargetTemp: rs.currentTargetTemp,
		SwitchOnAt:        rs.switchOnAt,
		SwitchOffAt:       rs.switchOffAt,
		StabilityReady:    rs.stabilityReady,
		MachineStatus:     rs.machineStatus,
	}
}

// SetMachineOn ports runtime.machineOn = isOn (checkAndApplyMachinePower).
func (rs *RuntimeState) SetMachineOn(on bool) {
	rs.mu.Lock()
	rs.machineOn = on
	rs.mu.Unlock()
}

// SetMachineStatus replaces the cached machine status wholesale (never
// mutated in place, so the returned pointer from a prior Get() stays
// valid/immutable for its caller even after a later SetMachineStatus).
func (rs *RuntimeState) SetMachineStatus(s *MachineStatus) {
	rs.mu.Lock()
	rs.machineStatus = s
	rs.mu.Unlock()
}

// SetCurrentTemps ports `runtime.currentTemp = tempVal || runtime.currentTemp`
// /`runtime.currentTargetTemp = tTempVal || runtime.currentTargetTemp` — a
// zero/absent reading leaves the previous value in place rather than
// clobbering it with 0, matching JS's `||` fallback exactly (nil, not the
// zero value, is this port's "absent").
func (rs *RuntimeState) SetCurrentTemps(temp, targetTemp *float64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if temp != nil {
		rs.currentTemp = temp
	}
	if targetTemp != nil {
		rs.currentTargetTemp = targetTemp
	}
}

// PushTempHistory ports `runtime.tempHistory.push(tempVal)` capped at
// tempHistoryMax, dropping the oldest entry once full (Array.shift()).
func (rs *RuntimeState) PushTempHistory(temp float64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.tempHistory = append(rs.tempHistory, temp)
	if len(rs.tempHistory) > tempHistoryMax {
		rs.tempHistory = rs.tempHistory[1:]
	}
}

// ClearTempHistory ports `runtime.tempHistory = []`.
func (rs *RuntimeState) ClearTempHistory() {
	rs.mu.Lock()
	rs.tempHistory = nil
	rs.mu.Unlock()
}

// isTempStable ports isTempStable(runtime): the last tempStableMin
// readings must all fall within tempStableVar °C of each other. Must be
// called with rs.mu already held (only preheat.go's buildPreheatResponse-
// adjacent callers use it, always via the locked helpers below).
func (rs *RuntimeState) isTempStableLocked() bool {
	if len(rs.tempHistory) < tempStableMin {
		return false
	}
	window := rs.tempHistory[len(rs.tempHistory)-tempStableMin:]
	min, max := window[0], window[0]
	for _, v := range window[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return max-min <= tempStableVar
}

// IsTempStable ports isTempStable(runtime) for external callers.
func (rs *RuntimeState) IsTempStable() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.isTempStableLocked()
}

// SetSwitchOnAt/SetSwitchOffAt/SetStabilityReady port the corresponding
// bare-field assignments scattered through lib/poll.js/lib/preheat.js.
func (rs *RuntimeState) SetSwitchOnAt(at *int64) {
	rs.mu.Lock()
	rs.switchOnAt = at
	rs.mu.Unlock()
}

func (rs *RuntimeState) SetSwitchOffAt(at *int64) {
	rs.mu.Lock()
	rs.switchOffAt = at
	rs.mu.Unlock()
}

func (rs *RuntimeState) SetStabilityReady(ready bool) {
	rs.mu.Lock()
	rs.stabilityReady = ready
	rs.mu.Unlock()
}

// IsStillWarm ports isStillWarm(runtime, now) — see derive.go for the pure
// implementation this locks around.
func (rs *RuntimeState) IsStillWarm(nowMs int64) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return isStillWarm(rs.currentTemp, rs.switchOnAt, rs.switchOffAt, nowMs)
}
