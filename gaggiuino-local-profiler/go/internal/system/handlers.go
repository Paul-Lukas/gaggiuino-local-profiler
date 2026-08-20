package system

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/httputil"
)

const jsonBodyLimit = 16 * 1024 // express.json({ limit: '16kb' }) — server.js's global default.

// Handlers wires Poller + DemoService into net/http handlers — the REST
// surface routes/system.js exposes for the endpoints this phase ports (see
// doc.go for the full list of what's in vs. out of scope).
type Handlers struct {
	poller *Poller
	demo   *DemoService
	vc     *versionChecker
}

func NewHandlers(poller *Poller, demo *DemoService) *Handlers {
	return &Handlers{poller: poller, demo: demo, vc: newVersionChecker()}
}

// RegisterRoutes registers every route this package owns onto mux.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/machine/status", h.machineStatus)
	mux.HandleFunc("GET /api/live/data", h.liveData)
	mux.HandleFunc("GET /api/preheat", h.getPreheat)
	mux.HandleFunc("POST /api/preheat/ready-by", h.postPreheatReadyBy)
	mux.HandleFunc("GET /api/version", h.getVersion)
	mux.HandleFunc("POST /api/demo/seed", h.postDemoSeed)
	mux.HandleFunc("POST /api/demo/end", h.postDemoEnd)
}

// machineStatus ports GET /api/machine/status: the default machine's
// latest cached live status, polled every 5s by glp-integration's
// machine_coordinator.py. Always 200 — `available: false` (no other
// fields) means no status has been received yet since startup, `stale:
// true` once the cached status is more than 10 seconds old.
// machineStatusResponse ports the `{ available: true, stale, ...machineStatus }`
// object literal: available/stale plus every MachineStatus field flattened
// into the same JSON level (Go's anonymous-embedding promotion mirrors
// JS's object spread here).
type machineStatusResponse struct {
	Available bool `json:"available"`
	Stale     bool `json:"stale"`
	MachineStatus
}

func (h *Handlers) machineStatus(w http.ResponseWriter, r *http.Request) {
	snap := h.poller.Runtime().Get()
	if snap.MachineStatus == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	staleSec := float64(time.Now().UnixMilli()-snap.MachineStatus.UpdatedAt) / 1000
	writeJSON(w, http.StatusOK, machineStatusResponse{
		Available:     true,
		Stale:         staleSec > 10,
		MachineStatus: *snap.MachineStatus,
	})
}

// liveData ports GET /api/live/data.
func (h *Handlers) liveData(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.poller.buildLiveDataResponse())
}

// getPreheat ports GET /api/preheat.
func (h *Handlers) getPreheat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.poller.buildPreheatResponse())
}

// postPreheatReadyBy ports POST /api/preheat/ready-by (#541).
func (h *Handlers) postPreheatReadyBy(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeOptionalJSONBody(w, r)
	if !ok {
		return
	}
	raw, present := body["targetAt"]
	if !present {
		raw = nil
	}
	var targetAt *int64
	if raw != nil {
		n, isNum := raw.(float64)
		if !isNum {
			writeError(w, http.StatusBadRequest, "targetAt must be an epoch-ms number or null")
			return
		}
		v := int64(n)
		targetAt = &v
	}
	if targetAt != nil {
		entity := h.poller.defaultSwitchEntity()
		if !h.poller.ha.Enabled() || entity == "" {
			writeError(w, http.StatusBadRequest, "switch_entity nicht konfiguriert")
			return
		}
	}
	h.poller.SetReadyByTarget(targetAt)
	writeJSON(w, http.StatusOK, h.poller.buildPreheatResponse())
}

// getVersion ports GET /api/version.
func (h *Handlers) getVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.vc.CheckForUpdate(r.Context()))
}

// postDemoSeed ports POST /api/demo/seed (#274).
func (h *Handlers) postDemoSeed(w http.ResponseWriter, r *http.Request) {
	empty, err := h.demo.IsEmpty()
	if err != nil {
		internalError(w, err)
		return
	}
	if !empty {
		writeError(w, http.StatusConflict, "Database is not empty")
		return
	}
	if err := h.demo.SeedDemoData(); err != nil {
		log.Printf("system: demo seed error: %v", err)
		internalError(w, err)
		return
	}
	log.Printf("system: demo data seeded")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "isDemo": true})
}

// postDemoEnd ports POST /api/demo/end (#274).
func (h *Handlers) postDemoEnd(w http.ResponseWriter, r *http.Request) {
	if err := h.demo.EndDemo(); err != nil {
		log.Printf("system: demo end error: %v", err)
		internalError(w, err)
		return
	}
	log.Printf("system: demo data removed")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "isDemo": false})
}

// ── response/body helpers (see internal/httputil: WriteJSON/WriteError were
// byte-identical copies across every domain package's handlers.go; this
// package's own internalError stays a one-line wrapper so every call site
// below keeps its existing internalError(w, err) shape) ──────────────────

var (
	writeJSON  = httputil.WriteJSON
	writeError = httputil.WriteError
)

func internalError(w http.ResponseWriter, err error) {
	httputil.InternalError(w, "system", err)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, jsonBodyLimit)
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "request entity too large")
		} else {
			writeError(w, http.StatusBadRequest, "Invalid JSON body")
		}
		return nil, false
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, true
}

// decodeOptionalJSONBody ports internal/orders/handlers.go's own helper of
// the same name: a request body that's allowed to be entirely absent
// (POST /api/preheat/ready-by's Node handler reads `req.body || {}`,
// tolerant of no body at all) decodes to {} instead of a 400.
func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	if r.ContentLength == 0 {
		return map[string]any{}, true
	}
	return decodeJSONBody(w, r)
}
