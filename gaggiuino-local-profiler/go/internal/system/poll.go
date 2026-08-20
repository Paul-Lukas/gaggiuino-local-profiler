package system

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/ha"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/sse"
)

// This file ports lib/poll.js: the 1s live-polling loop
// (startLivePolling/stopLivePolling/pollLive/pollViaGaggiuinoStatus) plus
// checkAndApplyMachinePower/backgroundHaCheck, the 30s HA-switch-state
// watcher that starts/stops it. See doc.go for what this phase
// deliberately does not port from lib/poll.js/lib/sync.js (the shot-sync
// triggers, connectivity-stats logging, MQTT transport).

// liveDatapoints mirrors the fixed set of per-tenth-second arrays
// state.liveAccum.datapoints accumulates during a brew — the exact shape
// GET /api/shots/:id already stores for a finished shot (lib/poll.js's
// liveAccum feeds ShotRepository on save), reused here unchanged for the
// in-progress GET /api/live/data / live-snapshot SSE payload.
type liveDatapoints struct {
	TimeInShot        []int `json:"timeInShot"`
	Pressure          []int `json:"pressure"`
	Temperature       []int `json:"temperature"`
	ShotWeight        []int `json:"shotWeight"`
	WeightFlow        []int `json:"weightFlow"`
	PumpFlow          []int `json:"pumpFlow"`
	TargetTemperature []int `json:"targetTemperature"`
}

type liveAccumState struct {
	startTime   int64
	profileName string
	prevWeight  float64
	datapoints  liveDatapoints
}

// LiveData mirrors openapi.yaml's LiveData schema exactly — GET
// /api/live/data's response and the live-snapshot SSE event's payload,
// both built by buildLiveDataResponse() (#736: single source of truth for
// both, matching routes/sse.js/routes/system.js sharing the same Node
// function).
type LiveData struct {
	IsLive           bool            `json:"isLive"`
	ProfileName      string          `json:"profileName"`
	Datapoints       *liveDatapoints `json:"datapoints"`
	Seq              int             `json:"seq"`
	MachineReachable *bool           `json:"machineReachable"`
}

// pollGlobalState ports the subset of lib/state.js's module-level fields
// this package needs (as opposed to lib/machine-runtime-state.js's
// per-machine RuntimeState) — mutex-guarded for the same reason
// RuntimeState is (see its own header comment). See RuntimeState's doc
// comment for this struct's mu's fixed lock ordering relative to
// RuntimeState.mu (RuntimeState.mu first, this one second) — a #901
// code-review minimal fix, not a full consolidation.
type pollGlobalState struct {
	mu sync.Mutex

	machineReachable *bool // nil = never checked (#274)
	// lastMachineError/lastMachineSuccess mirror lib/state.js's fields of
	// the same name — openapi.yaml documents them as GET /api/status's
	// `lastMachineError`/`lastMachineSuccess` fields, read via StatusInfo()
	// (Phase 3b, #901) since that endpoint's own Go port.
	lastMachineError     *string
	lastMachineSuccess   *int64
	cachedMachineVersion *string
	isPollRunning        bool
	liveAccum            *liveAccumState
	liveSeq              int
	// wasReachable is #725's tri-state: nil = never polled (the very first
	// successful poll after a host is configured is NOT a "recovery" —
	// that path belongs to routes/machines.js's own save-triggered sync,
	// not ported here either, see doc.go). Unread until the reachability-
	// recovery catch-up sync itself (lib/sync.js, doc.go's "Deliberately
	// not ported" — "the shot-history sync engine is its own future
	// phase") exists to consume the false->true transition this captures.
	wasReachable *bool

	readyByTargetAt   *int64
	plannedSwitchOnAt *int64
	// preheatNotifySent mirrors lib/state.js's field of the same name,
	// cleared here on machine-off exactly like Node's stopLivePolling does.
	// Unread until _checkPreheatNotify (doc.go's "Deliberately not
	// ported," tracked as a follow-up) is itself ported — nothing sets it
	// true yet, so this reset is currently a no-op every time.
	preheatNotifySent bool
}

// AdapterProvider is the subset of *machines.Handlers this package
// depends on — an interface (not *machines.Handlers directly) so tests can
// supply a fake Adapter without constructing the machines package's full
// HTTP surface (registry, both concrete adapters, firmware checker, ...).
type AdapterProvider interface {
	GetAdapter(m *machines.Machine) (machines.Adapter, error)
}

// Poller ports lib/poll.js's module-level polling loop as a struct so
// cmd/server can own one instance instead of relying on Node's
// module-singleton pattern (same rationale as machines.gaggiuinoLiveClient,
// Phase 1e).
type Poller struct {
	registry *machines.Registry
	adapters AdapterProvider
	hub      *sse.Hub
	ha       *ha.Client

	runtime *RuntimeState
	state   pollGlobalState

	liveMu     sync.Mutex
	liveTicker *time.Ticker
	liveStop   chan struct{}
}

// NewPoller wires registry (the default machine's host/switch-entity
// source of truth) + adapters (machines.Handlers.GetAdapter) + hub
// (live-snapshot/preheat-update SSE producer) + haClient (switch-state
// reads, the ready-by auto turn-on call) into one Poller, matching
// lib/poll.js's own module-level dependencies (lib/machines/registry.js,
// lib/ha.js, lib/events.js's bus).
func NewPoller(registry *machines.Registry, adapters AdapterProvider, hub *sse.Hub, haClient *ha.Client) *Poller {
	return &Poller{registry: registry, adapters: adapters, hub: hub, ha: haClient, runtime: NewRuntimeState()}
}

// Runtime exposes the default machine's RuntimeState to handlers.go
// (GET /api/machine/status) and preheat.go.
func (p *Poller) Runtime() *RuntimeState { return p.runtime }

// StatusInfo is the subset of pollGlobalState GET /api/status reports —
// see that struct's own field comments (lastMachineError/lastMachineSuccess
// were kept, unread, specifically for this endpoint back in Phase 1g).
type StatusInfo struct {
	MachineReachable     *bool
	LastMachineError     *string
	LastMachineSuccess   *int64
	CachedMachineVersion *string
}

// StatusInfo snapshots pollGlobalState's fields GET /api/status needs.
func (p *Poller) StatusInfo() StatusInfo {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	return StatusInfo{
		MachineReachable:     p.state.machineReachable,
		LastMachineError:     p.state.lastMachineError,
		LastMachineSuccess:   p.state.lastMachineSuccess,
		CachedMachineVersion: p.state.cachedMachineVersion,
	}
}

// Start ports server.js's startup sequence for this domain: load any
// persisted preheat session, run one unconditional checkAndApplyMachinePower
// (the call that actually starts live polling on a fresh boot for the
// common no-HA-switch-control install, see checkAndApplyMachinePower's own
// comment), then launch the 30s HA-check and 30s preheat-watch tickers.
// ctx bounds both tickers' lifetime — cancelling it stops this Poller,
// though it does NOT stop an already-running live-poll ticker (that one's
// own lifecycle is startLivePolling/stopLivePolling-driven, exactly like
// Node's livePollTimer).
func (p *Poller) Start(ctx context.Context) {
	p.loadPreheatState()
	if err := p.checkAndApplyMachinePower(ctx); err != nil {
		log.Printf("system: machine power check failed on startup: %v", err)
	}
	go p.runTicker(ctx, backgroundHaCheckInterval, func() {
		if err := p.checkAndApplyMachinePower(ctx); err != nil {
			log.Printf("system: background HA check failed: %v", err)
		}
	})
	go p.runTicker(ctx, preheatWatchInterval, func() { p.preheatWatchTick(ctx) })
}

func (p *Poller) runTicker(ctx context.Context, interval time.Duration, fn func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// checkAndApplyMachinePower ports checkAndApplyMachinePower(runtime).
// Node's early-exit branch — `if (!entity || !HA_TOKEN)` (lib/poll.js) —
// fires on EITHER no switch entity configured OR no HA integration at all
// (a switch entity configured but no token to read it with is just as
// unable to tell GLP the machine's power state), and always just ensures
// live polling is running, treating the machine as permanently "on" since
// nothing in this install can tell GLP otherwise. That branch is also what
// Start() above relies on to begin polling on a fresh boot for the common
// case (no HA switch-control configured): calling this repeatedly on that
// path is a harmless no-op once live polling is already active, so
// backgroundHaCheck's own Node-side `if (!HA_TOKEN) return` gate has no Go
// equivalent here — this function is safe to call unconditionally on every
// 30s tick. #901 code review: this used to check only `entity == ""` and
// fall through to GetSwitchState otherwise, which always returns nil when
// no token is configured (ha/client.go's `!c.enabled()` guard) — live
// polling then never started for an entity-configured-but-tokenless
// install, for the entire process lifetime.
func (p *Poller) checkAndApplyMachinePower(ctx context.Context) error {
	machine, err := p.registry.GetDefaultMachine()
	if err != nil {
		return err
	}
	var entity string
	if machine != nil && machine.SwitchEntity != nil {
		entity = *machine.SwitchEntity
	}
	if entity == "" || !p.ha.Enabled() {
		if !p.livePollActive() {
			p.startLivePolling()
		}
		return nil
	}
	isOn := p.ha.GetSwitchState(ctx, entity)
	if isOn == nil {
		return nil
	}
	snap := p.runtime.Get()
	if *isOn == snap.MachineOn {
		return nil
	}
	p.runtime.SetMachineOn(*isOn)
	if *isOn {
		log.Printf("system: machine on -- live polling resumed")
		p.startLivePolling()
	} else {
		log.Printf("system: machine off -- live polling paused")
		p.stopLivePolling()
		p.state.mu.Lock()
		p.state.preheatNotifySent = false
		p.state.mu.Unlock()
	}
	return nil
}

func (p *Poller) livePollActive() bool {
	p.liveMu.Lock()
	defer p.liveMu.Unlock()
	return p.liveTicker != nil
}

// startLivePolling ports startLivePolling(runtime).
func (p *Poller) startLivePolling() {
	p.liveMu.Lock()
	if p.liveTicker != nil {
		p.liveMu.Unlock()
		return
	}
	now := time.Now().UnixMilli()
	snap := p.runtime.Get()
	if snap.SwitchOnAt == nil || !p.runtime.IsStillWarm(now) {
		p.runtime.SetSwitchOnAt(&now)
		p.savePreheatState()
	}
	p.runtime.ClearTempHistory()
	log.Printf("system: live polling started")
	ticker := time.NewTicker(pollInterval)
	stop := make(chan struct{})
	p.liveTicker = ticker
	p.liveStop = stop
	p.liveMu.Unlock()

	go func() {
		for {
			select {
			case <-ticker.C:
				p.pollTick()
			case <-stop:
				return
			}
		}
	}()

	p.hub.Publish(sse.Event{Type: sse.EventPreheatUpdate, Data: p.buildPreheatResponse()})
}

// stopLivePolling ports stopLivePolling(runtime): the #655 machineReachable
// flip is unconditional, applied even when there was no active live-poll
// ticker to stop, matching Node's own reasoning (see lib/poll.js's comment)
// — nothing else can ever flip this back to false on its own once a
// runtime never reaches startLivePolling.
func (p *Poller) stopLivePolling() {
	reachable := false
	p.state.mu.Lock()
	p.state.machineReachable = &reachable
	p.state.mu.Unlock()

	p.liveMu.Lock()
	if p.liveTicker != nil {
		p.liveTicker.Stop()
		close(p.liveStop)
		p.liveTicker = nil
		p.state.mu.Lock()
		p.state.liveAccum = nil
		p.state.mu.Unlock()
		now := time.Now().UnixMilli()
		p.runtime.SetSwitchOffAt(&now)
		p.runtime.SetStabilityReady(false)
		p.runtime.ClearTempHistory()
		p.savePreheatState()
		log.Printf("system: live polling stopped")
	}
	p.liveMu.Unlock()

	p.hub.Publish(sse.Event{Type: sse.EventPreheatUpdate, Data: p.buildPreheatResponse()})
	p.emitLiveSnapshot()
}

// pollTick ports pollLive(runtime): the isPollRunning mutex guard around
// one pollViaGaggiuinoStatus call, so a slow poll (e.g. a machine taking
// >1s to answer) can never overlap with the next tick.
func (p *Poller) pollTick() {
	p.state.mu.Lock()
	if p.state.isPollRunning {
		p.state.mu.Unlock()
		return
	}
	p.state.isPollRunning = true
	p.state.mu.Unlock()

	defer func() {
		p.state.mu.Lock()
		p.state.isPollRunning = false
		p.state.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p.pollViaGaggiuinoStatus(ctx)
}

// pollViaGaggiuinoStatus ports pollViaGaggiuinoStatus(runtime). Deliberately
// NOT ported (see doc.go): the #725 reachability-recovery catch-up sync
// (syncShots()), the brew-finished setTimeout(syncAfterBrew, 3000), and
// recordConnectivity()'s debug-log summary — all three depend on
// lib/sync.js, which this Go port doesn't have yet.
func (p *Poller) pollViaGaggiuinoStatus(ctx context.Context) {
	machine, err := p.registry.GetDefaultMachine()
	if err != nil || machine == nil {
		return
	}
	// #718: no host configured anywhere -- skip cleanly, don't request
	// against a placeholder/fallback hostname, and don't touch
	// machineReachable (nil stays nil, exactly like Node never assigning
	// state.machineReachable on this early-return path).
	if strings.TrimSpace(machine.Host) == "" {
		return
	}
	adapter, err := p.adapters.GetAdapter(machine)
	if err != nil {
		return
	}

	status, err := adapter.GetStatus(ctx, machine)
	if err != nil {
		p.state.mu.Lock()
		reachable := false
		p.state.machineReachable = &reachable
		p.state.wasReachable = &reachable
		msg := redactURLs(err.Error())
		p.state.lastMachineError = &msg
		p.state.mu.Unlock()
		log.Printf("system: live poll error: %v", err)
		p.emitLiveSnapshot()
		return
	}

	p.state.mu.Lock()
	reachable := true
	p.state.machineReachable = &reachable
	p.state.lastMachineError = nil
	now := time.Now().UnixMilli()
	p.state.lastMachineSuccess = &now
	p.state.wasReachable = &reachable
	if p.state.cachedMachineVersion == nil {
		if ver := extractVersion(status.Raw); ver != "" {
			p.state.cachedMachineVersion = &ver
			log.Printf("system: Gaggiuino firmware (from status): %s", ver)
		}
	}
	p.state.mu.Unlock()

	sensorSnap, _ := adapter.GetLiveSensorSnapshot(ctx, machine)
	sysState, _ := adapter.GetLiveSystemState(ctx, machine)

	result := deriveMachineState(DeriveInput{
		Status:     rawStatusFrom(status),
		Now:        now,
		SensorSnap: sensorSnap,
		SysState:   sysState,
	})
	ms := result.MachineStatus
	p.runtime.SetMachineStatus(&ms)
	p.runtime.SetCurrentTemps(zeroToNil(ms.Temperature), zeroToNil(ms.TargetTemperature))

	snap := p.runtime.Get()
	if ms.Temperature > 0 && !result.IsBrewing {
		p.runtime.PushTempHistory(ms.Temperature)
		if snap.SwitchOnAt != nil && ms.TargetTemperature > 0 &&
			ms.Temperature >= ms.TargetTemperature-2 && p.runtime.IsTempStable() {
			preheatMs := int64(loadPreheatMinutes()) * 60_000
			if now-*snap.SwitchOnAt < preheatMs {
				newOnAt := now - preheatMs
				p.runtime.SetSwitchOnAt(&newOnAt)
				p.runtime.SetStabilityReady(true)
				p.savePreheatState()
				log.Printf("system: temperature stable -- preheat marked complete")
				p.hub.Publish(sse.Event{Type: sse.EventPreheatUpdate, Data: p.buildPreheatResponse()})
			}
		}
	} else if result.IsBrewing {
		p.runtime.ClearTempHistory()
	}

	p.state.mu.Lock()
	if result.IsBrewing && p.state.liveAccum == nil {
		p.state.liveAccum = &liveAccumState{startTime: now, profileName: result.ProfileName, prevWeight: ms.Weight}
		log.Printf("system: brew started: profile %s", result.ProfileName)
	}
	if !result.IsBrewing && p.state.liveAccum != nil {
		log.Printf("system: brew finished")
		p.state.liveAccum = nil
		p.state.liveSeq++
	}
	if result.IsBrewing && p.state.liveAccum != nil {
		acc := p.state.liveAccum
		elapsed := elapsedTenths(now, acc.startTime)
		weightFlow := ms.Weight - acc.prevWeight
		if weightFlow < 0 {
			weightFlow = 0
		}
		acc.prevWeight = ms.Weight
		acc.datapoints.TimeInShot = append(acc.datapoints.TimeInShot, elapsed)
		acc.datapoints.Pressure = append(acc.datapoints.Pressure, round10(ms.Pressure))
		acc.datapoints.Temperature = append(acc.datapoints.Temperature, round10(ms.Temperature))
		acc.datapoints.ShotWeight = append(acc.datapoints.ShotWeight, round10(ms.Weight))
		acc.datapoints.WeightFlow = append(acc.datapoints.WeightFlow, round10(weightFlow))
		acc.datapoints.PumpFlow = append(acc.datapoints.PumpFlow, round10(derefFloat(ms.PumpFlow)))
		acc.datapoints.TargetTemperature = append(acc.datapoints.TargetTemperature, round10(ms.TargetTemperature))
	}
	p.state.mu.Unlock()

	p.emitLiveSnapshot()
}

func round10(v float64) int { return int(v*10 + 0.5) }

// elapsedTenths ports lib/poll.js:287's `Math.round((now - startTime) /
// 100)` (tenths-of-a-second precision timeInShot datapoints) — Node rounds,
// a bare Go `int(x/100)` truncates toward zero, which produces a
// systematic off-by-one offset against Node-recorded shots sharing the same
// DB (#901 code review: 950ms elapsed rounds to 10 in Node, truncated to 9).
func elapsedTenths(now, startTime int64) int {
	return int(math.Round(float64(now-startTime) / 100))
}

func zeroToNil(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

// rawStatusFrom decodes the two fields machines.Status doesn't already
// carry (waterLevel/upTime) straight off its Raw JSON — the rest come from
// Status's own already-parsed fields.
func rawStatusFrom(s machines.Status) RawStatus {
	var extra struct {
		WaterLevel json.Number `json:"waterLevel"`
		UpTime     json.Number `json:"upTime"`
	}
	_ = json.Unmarshal(s.Raw, &extra)
	waterLevel, _ := extra.WaterLevel.Int64()
	upTime, _ := extra.UpTime.Int64()

	var steamOn bool
	if s.SteamOn != nil {
		steamOn = *s.SteamOn
	}
	return RawStatus{
		WaterLevel:        int(waterLevel),
		UpTime:            int(upTime),
		Brewing:           s.Brewing,
		Temperature:       s.Temperature,
		TargetTemperature: s.TargetTemperature,
		Pressure:          s.Pressure,
		Weight:            derefFloat(s.Weight),
		ProfileID:         s.ProfileID,
		ProfileName:       s.ProfileName,
		SteamSwitchState:  steamOn,
	}
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// extractVersion ports pollViaGaggiuinoStatus's inline
// `status.softwareVersion || status.version || status.firmware ||
// status.buildNumber || status.fw_version || status.buildDate || null`.
func extractVersion(raw json.RawMessage) string {
	var obj struct {
		SoftwareVersion any `json:"softwareVersion"`
		Version         any `json:"version"`
		Firmware        any `json:"firmware"`
		BuildNumber     any `json:"buildNumber"`
		FwVersion       any `json:"fw_version"`
		BuildDate       any `json:"buildDate"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, v := range []any{obj.SoftwareVersion, obj.Version, obj.Firmware, obj.BuildNumber, obj.FwVersion, obj.BuildDate} {
		if s := anyToString(v); s != "" {
			return s
		}
	}
	return ""
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}

// redactURLs ports lib/poll.js's `err.message.replace(/https?:\/\/\S+/g,
// '[url]')` -- lastMachineError must never leak the configured machine
// host to a client.
func redactURLs(msg string) string {
	for {
		idx := strings.Index(msg, "http://")
		if idx == -1 {
			idx = strings.Index(msg, "https://")
		}
		if idx == -1 {
			return msg
		}
		end := idx
		for end < len(msg) && msg[end] != ' ' && msg[end] != '\t' && msg[end] != '\n' {
			end++
		}
		msg = msg[:idx] + "[url]" + msg[end:]
	}
}

// buildLiveDataResponse ports buildLiveDataResponse(): the single source
// of truth for GET /api/live/data and the live-snapshot SSE payload. Must
// return a value wholly independent of p.state.liveAccum once unlocked: a
// caller (emitLiveSnapshot -> Hub.Publish -> a per-subscriber buffered
// channel, see internal/sse) can hold onto this LiveData and json.Marshal
// it arbitrarily long after this call returns, concurrently with pollTick
// appending to the very same datapoints slices under its own lock (#901
// code review — a `go test -race` reproduction: returning a pointer into
// the locked struct here, as this used to, is a data race between that
// later Marshal and the next tick's writes). copyDatapoints below takes a
// deep copy of the slices while still holding the lock, exactly the
// "copy under lock, then hand out lock-free" pattern
// internal/machines/live.go's GetLiveSensorSnapshot/GetLiveSystemState
// follow for their own cached values (those are safe returning a bare
// pointer instead, since a fresh poll replaces sensorSnap/sysState
// wholesale rather than mutating the previous value in place — this
// package's own RuntimeState.SetMachineStatus relies on the same
// never-mutated-after-set invariant, see its doc comment).
func (p *Poller) buildLiveDataResponse() LiveData {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	var dp *liveDatapoints
	profileName := ""
	isLive := p.state.liveAccum != nil
	if p.state.liveAccum != nil {
		dp = copyDatapoints(&p.state.liveAccum.datapoints)
		profileName = p.state.liveAccum.profileName
	}
	return LiveData{
		IsLive:           isLive,
		ProfileName:      profileName,
		Datapoints:       dp,
		Seq:              p.state.liveSeq,
		MachineReachable: p.state.machineReachable,
	}
}

// copyDatapoints deep-copies src's slices — see buildLiveDataResponse's doc
// comment for why a shallow copy (or no copy at all) isn't safe here.
func copyDatapoints(src *liveDatapoints) *liveDatapoints {
	return &liveDatapoints{
		TimeInShot:        append([]int(nil), src.TimeInShot...),
		Pressure:          append([]int(nil), src.Pressure...),
		Temperature:       append([]int(nil), src.Temperature...),
		ShotWeight:        append([]int(nil), src.ShotWeight...),
		WeightFlow:        append([]int(nil), src.WeightFlow...),
		PumpFlow:          append([]int(nil), src.PumpFlow...),
		TargetTemperature: append([]int(nil), src.TargetTemperature...),
	}
}

// LiveData is the exported form of buildLiveDataResponse, for cmd/server's
// SSE-priming wiring.
func (p *Poller) LiveData() LiveData { return p.buildLiveDataResponse() }

// emitLiveSnapshot publishes the current buildLiveDataResponse() onto the
// SSE hub as EventLiveSnapshot. This is this package's sole producer of
// that event (see doc.go's "Reconciling with Phase 1e's live.go" section):
// machines/live.go's own WS session cache no longer publishes directly,
// since its raw {machineHost, sensorSnap}/{machineHost, sysState} shape
// doesn't match openapi.yaml's LiveData schema this endpoint/event are
// bound to. Deliberately simpler than Node's #708 optimization (an
// immediate push the instant a fresh WS/MQTT sample arrives, on top of the
// 1s tick) — every push here is tick-driven only; see doc.go.
func (p *Poller) emitLiveSnapshot() {
	p.hub.Publish(sse.Event{Type: sse.EventLiveSnapshot, Data: p.buildLiveDataResponse()})
}
