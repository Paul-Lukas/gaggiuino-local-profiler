package library

import "net/http"

// This file ports routes/library/milks.js.

func findMilkIndex(lib Library, id int64) int {
	for i, m := range lib.Milks {
		if mid, ok := idOf(m, "id"); ok && mid == id {
			return i
		}
	}
	return -1
}

// listMilks ports GET /api/library/milks — a lightweight
// id/name/emoji/stockMl projection, defaulting emoji to the milk-carton
// emoji when unset.
func (h *Handlers) listMilks(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]Entity, 0, len(lib.Milks))
	for _, m := range lib.Milks {
		emoji, _ := m["emoji"].(string)
		if emoji == "" {
			emoji = "🥛"
		}
		out = append(out, Entity{"id": m["id"], "name": m["name"], "emoji": emoji, "stockMl": m["stockMl"]})
	}
	writeJSON(w, http.StatusOK, out)
}

// createMilk ports POST /api/library/milk.
func (h *Handlers) createMilk(w http.ResponseWriter, r *http.Request) {
	if !h.rateLimitCreate(w, r) {
		return
	}
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	if trimMax(body["name"], 100) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	// `emoji?.trim() || '🥛'` — no length cap on create (unlike the
	// restore-path sanitizeMilkFields, which isn't called here).
	emoji := trimMax(body["emoji"], 1<<30)
	if emoji == "" {
		emoji = "🥛"
	}
	milk := Entity{
		"id": newID(), "name": trimMax(body["name"], 100),
		"emoji": emoji, "stockMl": floatOrZero(body["stockMl"]), "updatedAt": newID(),
	}
	lib.Milks = append(lib.Milks, milk)
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, milk)
}

// updateMilk ports PUT /api/library/milk/:id.
func (h *Handlers) updateMilk(w http.ResponseWriter, r *http.Request) {
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
		idx = findMilkIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	milk := lib.Milks[idx]
	if v, present := body["name"]; present {
		s := trimMax(v, 100)
		if s != "" {
			milk["name"] = s
		}
	}
	if v, present := body["emoji"]; present {
		s := trimMax(v, 1<<30) // `String(emoji).trim() || <keep old>` — no length cap
		if s != "" {
			milk["emoji"] = s
		}
	}
	if _, present := body["stockMl"]; present {
		milk["stockMl"] = floatOrZero(body["stockMl"])
	}
	milk["updatedAt"] = newID()
	lib.Milks[idx] = milk
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, milk)
}

// deleteMilk ports DELETE /api/library/milk/:id.
func (h *Handlers) deleteMilk(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	filtered := make([]Entity, 0, len(lib.Milks))
	for _, m := range lib.Milks {
		mid, ok := idOf(m, "id")
		if !noMatch && ok && mid == id {
			continue
		}
		filtered = append(filtered, m)
	}
	lib.Milks = filtered
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deductMilk ports POST /api/library/milk/:id/deduct.
func (h *Handlers) deductMilk(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	ml := floatOrZero(body["ml"])
	if ml <= 0 {
		writeError(w, http.StatusBadRequest, "ml must be positive")
		return
	}
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	idx := -1
	if !noMatch {
		idx = findMilkIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	milk := lib.Milks[idx]
	current := floatOrZero(milk["stockMl"])
	remaining := current - ml
	if remaining < 0 {
		remaining = 0
	}
	milk["stockMl"] = remaining
	milk["updatedAt"] = newID()
	lib.Milks[idx] = milk
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, milk)
}
