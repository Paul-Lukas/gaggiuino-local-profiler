package maintenance

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// This file ports routes/maintenance.js's Express router onto Go 1.22+'s
// method-and-wildcard http.ServeMux, the same pattern established in
// shots/handlers.go, library/handlers.go, and internal/orders/handlers.go.
const jsonBodyLimit = 16 * 1024 // express.json({ limit: '16kb' }) — server.js's global default.

// Handlers wires Repository (+ the shots/library/machines cross-domain
// dependencies computeMaintenanceStats/canonicalTask/machineHostname need)
// into net/http handlers.
type Handlers struct {
	repo      *Repository
	shotsRepo *shots.Repository
	libRepo   *library.Repository
	registry  *machines.Registry
}

// NewHandlers builds Handlers around the same *sql.DB-backed dependencies
// cmd/server already opens once and shares across every domain package.
func NewHandlers(repo *Repository, shotsRepo *shots.Repository, libRepo *library.Repository, registry *machines.Registry) *Handlers {
	return &Handlers{repo: repo, shotsRepo: shotsRepo, libRepo: libRepo, registry: registry}
}

// RegisterRoutes registers every /api/maintenance* route onto mux.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/maintenance", h.getMaintenance)
	mux.HandleFunc("POST /api/maintenance/{task}/done", h.taskDone)
	mux.HandleFunc("POST /api/maintenance/{task}/threshold", h.taskThreshold)
	mux.HandleFunc("GET /api/maintenance/log", h.getLog)
	mux.HandleFunc("POST /api/maintenance/log", h.postLog)
	mux.HandleFunc("DELETE /api/maintenance/log/{id}", h.deleteLog)
}

// ── response/body helpers ───────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"Internal server error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func internalError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "Internal server error")
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

// machineHostname ports routes/maintenance.js's machineHostname()
// (registry.hostFor()): the default machine's host, as a bare hostname,
// falling back to "gaggiuino" on any error — cosmetic display/log text
// stored on newly-written maintenance_log rows, no behavior depends on it.
func (h *Handlers) machineHostname() string {
	m, err := h.registry.GetDefaultMachine()
	if err != nil || m == nil || m.Host == "" {
		return "gaggiuino"
	}
	raw := m.Host
	if !strings.HasPrefix(strings.ToLower(raw), "http://") && !strings.HasPrefix(strings.ToLower(raw), "https://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "gaggiuino"
	}
	return u.Hostname()
}

// parseMachineIDParam ports parseMachineIdParam(req): 'all' passes
// through literally, otherwise a finite integer, defaulting to 1.
func parseMachineIDParam(r *http.Request) (id int64, isAll bool) {
	raw := r.URL.Query().Get("machineId")
	if raw == "all" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 1, false
	}
	return v, false
}

// activeMachineID ports activeMachineId(req): 'all' resolves to 1 for
// write routes, which always need one concrete target machine.
func activeMachineID(r *http.Request) int64 {
	id, isAll := parseMachineIDParam(r)
	if isAll {
		return 1
	}
	return id
}

func (h *Handlers) getMaintenance(w http.ResponseWriter, r *http.Request) {
	machineID, isAll := parseMachineIDParam(r)
	if isAll {
		result, err := ComputeAllMachinesMaintenance(h.repo, h.shotsRepo, h.registry)
		if err != nil {
			internalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	maint, err := h.repo.GetMaintenance(machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	stats, err := ComputeMaintenanceStats(h.shotsRepo, maint, machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handlers) taskDone(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	task, valid := canonicalTask(h.libRepo, r.PathValue("task"))
	if !valid {
		writeError(w, http.StatusNotFound, "Unknown task")
		return
	}
	machineID := activeMachineID(r)
	maint, err := h.repo.GetMaintenance(machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	t := maint[task]
	if t == nil {
		t = Task{}
	}
	// new Date().toISOString() — millisecond-precision UTC timestamp.
	t["lastDate"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	maint[task] = t
	if err := h.repo.SaveMaintenance(maint, machineID); err != nil {
		internalError(w, err)
		return
	}
	notes, _ := body["notes"].(string)
	if _, err := h.repo.AddMaintenanceLogEntry(task, notes, h.machineHostname(), shotCountFor(h, task, machineID), machineID); err != nil {
		internalError(w, err)
		return
	}
	stats, err := ComputeMaintenanceStats(h.shotsRepo, maint, machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// shotCountFor ports LibraryService.js's addMaintenanceLogEntry's shotCount
// computation: waterfilter/grinder_* (shared equipment) count shots across
// every machine, everything else scopes to the active machine.
func shotCountFor(h *Handlers, task string, machineID int64) int64 {
	var list []shots.Shot
	var err error
	if isGlobalMaintenanceTask(task) {
		list, err = h.shotsRepo.FindAll()
	} else {
		list, err = findAllByMachine(h.shotsRepo, machineID)
	}
	if err != nil {
		return 0
	}
	return int64(len(list))
}

// findAllByMachine ports ShotRepository.js's findAll(machineId) (with
// machineId supplied) — internal/shots.Repository.FindAll() has no
// machineId-scoped variant (only FindAllExcludingTrashByMachine, which
// this call needs to NOT apply, since shot_count on a maintenance log
// entry counts every synced shot, trashed or not — matching Node's
// findAll(machineId), not findAllExcludingTrash(machineId)). Filters
// client-side rather than adding a fifth query variant to
// internal/shots.Repository for this one cosmetic counter.
func findAllByMachine(shotsRepo *shots.Repository, machineID int64) ([]shots.Shot, error) {
	all, err := shotsRepo.FindAll()
	if err != nil {
		return nil, err
	}
	out := make([]shots.Shot, 0, len(all))
	for _, s := range all {
		mid, _ := s["machineId"].(int64)
		if mid == machineID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (h *Handlers) taskThreshold(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	task, valid := canonicalTask(h.libRepo, r.PathValue("task"))
	if !valid {
		writeError(w, http.StatusNotFound, "Unknown task")
		return
	}
	machineID := activeMachineID(r)
	maint, err := h.repo.GetMaintenance(machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	t := maint[task]
	if t == nil {
		t = Task{}
	}
	if v, present := body["threshold_shots"]; present {
		t["threshold_shots"] = clampThreshold(v, 1, 10000)
	}
	if v, present := body["threshold_days"]; present {
		t["threshold_days"] = clampThreshold(v, 1, 365)
	}
	maint[task] = t
	if err := h.repo.SaveMaintenance(maint, machineID); err != nil {
		internalError(w, err)
		return
	}
	stats, err := ComputeMaintenanceStats(h.shotsRepo, maint, machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// clampThreshold ports `const v = parseInt(x); (!isNaN(v) && v >= lo && v
// <= hi) ? v : null`.
func clampThreshold(v any, lo, hi int64) any {
	n, ok := jsParseIntAny(v)
	if !ok || n < lo || n > hi {
		return nil
	}
	return n
}

func jsParseIntAny(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func (h *Handlers) getLog(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("machineId")
	var machineID int64
	if raw != "" && raw != "all" {
		machineID, _ = strconv.ParseInt(raw, 10, 64)
	}
	log, err := h.repo.GetMaintenanceLog(machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, log)
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (h *Handlers) postLog(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	rawTask, _ := body["task"].(string)
	task, valid := canonicalTask(h.libRepo, rawTask)
	if !valid {
		writeError(w, http.StatusBadRequest, "Invalid task")
		return
	}
	date, _ := body["date"].(string)
	if date != "" && !dateRe.MatchString(date) {
		writeError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	notes, _ := body["notes"].(string)
	if len(notes) > 500 {
		notes = notes[:500]
	}
	machineID := activeMachineID(r)
	entry, err := h.repo.AddMaintenanceLogEntry(task, notes, h.machineHostname(), shotCountFor(h, task, machineID), machineID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (h *Handlers) deleteLog(w http.ResponseWriter, r *http.Request) {
	idParam := r.PathValue("id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return
	}
	log, err := h.repo.GetMaintenanceLog(0)
	if err != nil {
		internalError(w, err)
		return
	}
	found := false
	for _, e := range log {
		if e.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if err := h.repo.DeleteMaintenanceLog(id); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
