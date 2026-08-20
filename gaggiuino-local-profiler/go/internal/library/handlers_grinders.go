package library

import (
	"net/http"
	"time"
)

// This file ports routes/library/grinders.js.

func findGrinderIndex(lib Library, id int64) int {
	for i, g := range lib.Grinders {
		if gid, ok := idOf(g, "id"); ok && gid == id {
			return i
		}
	}
	return -1
}

// createGrinder ports POST /api/library/grinder.
func (h *Handlers) createGrinder(w http.ResponseWriter, r *http.Request) {
	if !h.rateLimitCreate(w, r) {
		return
	}
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	if trimMax(body["name"], 200) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	purchaseDate := trimMax(body["purchaseDate"], 10)
	burrsResetAt := trimMax(body["burrsResetAt"], 10)
	if burrsResetAt == "" {
		burrsResetAt = purchaseDate
	}
	grinder := Entity{
		"id": newID(), "name": trimMax(body["name"], 200), "notes": trimMax(body["notes"], 1000),
		"burrType": trimMax(body["burrType"], 200), "purchaseDate": purchaseDate,
		"burrsResetAt": burrsResetAt,
	}
	lib.Grinders = append(lib.Grinders, grinder)
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, grinder)
}

// updateGrinder ports PUT /api/library/grinder/:id.
func (h *Handlers) updateGrinder(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	idx := -1
	if !noMatch {
		idx = findGrinderIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	grinder := lib.Grinders[idx]
	if v, present := trimMaxOrUndefined(body, "name", 200); present {
		if v != "" {
			grinder["name"] = v
		}
	}
	if v, present := trimMaxOrUndefined(body, "notes", 1000); present {
		grinder["notes"] = v
	}
	if v, present := trimMaxOrUndefined(body, "burrType", 200); present {
		grinder["burrType"] = v
	}
	if v, present := trimMaxOrUndefined(body, "purchaseDate", 10); present {
		grinder["purchaseDate"] = v
	}
	if v, present := trimMaxOrUndefined(body, "burrsResetAt", 10); present {
		grinder["burrsResetAt"] = v
	}
	lib.Grinders[idx] = grinder
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, grinder)
}

// resetBurrs ports POST /api/library/grinder/:id/reset-burrs.
func (h *Handlers) resetBurrs(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	idx := -1
	if !noMatch {
		idx = findGrinderIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	grinder := lib.Grinders[idx]
	grinder["burrsResetAt"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	lib.Grinders[idx] = grinder
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.withWear(grinder))
}

// deleteGrinder ports POST /api/library/grinder/:id/delete: also removes
// its photo. The maintenance_log/`maintenance` table cleanup Node performs
// here (LibraryService.js's getMaintenance()/saveMaintenance() round trip,
// dropping the `grinder_{id}` task) is deliberately NOT ported: it's a real
// piece of the maintenance domain's own logic (recomputing/rewriting every
// MAINTENANCE_DEFAULTS-derived row, not a one-time migration), which
// belongs to routes/maintenance.js's not-yet-ported Go port
// (internal/maintenance, still Phase 0) rather than being partially
// reimplemented here. Flagged in the Phase 1d report: until that domain
// lands, deleting a grinder through the Go server leaves a stale
// `grinder_{id}` row in the `maintenance` table (harmless — it just never
// surfaces again since getMaintenance() only iterates the library's actual
// grinders — but not byte-identical to Node's active cleanup).
func (h *Handlers) deleteGrinder(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	if !noMatch {
		if idx := findGrinderIndex(lib, id); idx != -1 {
			if ext, _ := lib.Grinders[idx]["image"].(string); ext != "" {
				deleteImage(h.imageDir, id, ext, "grinder-")
			}
		}
	}
	filtered := make([]Entity, 0, len(lib.Grinders))
	for _, g := range lib.Grinders {
		gid, ok := idOf(g, "id")
		if !noMatch && ok && gid == id {
			continue
		}
		filtered = append(filtered, g)
	}
	lib.Grinders = filtered
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// getGrinderImage ports GET /api/library/grinder/:id/image.
func (h *Handlers) getGrinderImage(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	ext := ""
	if !noMatch {
		if idx := findGrinderIndex(lib, id); idx != -1 {
			ext, _ = lib.Grinders[idx]["image"].(string)
		}
	}
	h.serveImage(w, r, ext, "grinder-", id)
}

// postGrinderImage ports POST /api/library/grinder/:id/image.
func (h *Handlers) postGrinderImage(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	idx := -1
	if !noMatch {
		idx = findGrinderIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	data, contentType, ok := readUploadedImage(w, r)
	if !ok {
		return
	}
	ext, ok := saveUploadedImage(h.imageDir, "grinder-", id, data, contentType)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported image")
		return
	}
	grinder := lib.Grinders[idx]
	if oldExt, _ := grinder["image"].(string); oldExt != "" && oldExt != ext {
		deleteImage(h.imageDir, id, oldExt, "grinder-")
	}
	grinder["image"] = ext
	lib.Grinders[idx] = grinder
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, grinder)
}
