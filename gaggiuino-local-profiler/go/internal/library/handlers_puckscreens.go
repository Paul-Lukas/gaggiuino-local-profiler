package library

import "net/http"

// This file ports routes/library/puckscreens.js (#635).

var puckScreenThicknesses = map[string]bool{"very-thin": true, "thin": true, "medium": true, "thick": true}

func findPuckScreenIndex(lib Library, id int64) int {
	for i, p := range lib.PuckScreens {
		if pid, ok := idOf(p, "id"); ok && pid == id {
			return i
		}
	}
	return -1
}

// listPuckScreens ports GET /api/library/puckscreens.
func (h *Handlers) listPuckScreens(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lib.PuckScreens)
}

// createPuckScreen ports POST /api/library/puckscreen.
func (h *Handlers) createPuckScreen(w http.ResponseWriter, r *http.Request) {
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
	thickness, _ := body["thickness"].(string)
	if thickness != "" && !puckScreenThicknesses[thickness] {
		writeError(w, http.StatusBadRequest, "invalid thickness")
		return
	}
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	puckScreen := Entity{
		"id": newID(), "name": trimMax(body["name"], 200), "thickness": thickness,
		"material": trimMax(body["material"], 200), "notes": trimMax(body["notes"], 1000),
		"updatedAt": newID(),
	}
	lib.PuckScreens = append(lib.PuckScreens, puckScreen)
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, puckScreen)
}

// updatePuckScreen ports PUT /api/library/puckscreen/:id.
func (h *Handlers) updatePuckScreen(w http.ResponseWriter, r *http.Request) {
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
		idx = findPuckScreenIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if v, present := body["thickness"]; present {
		s, _ := v.(string)
		if s != "" && !puckScreenThicknesses[s] {
			writeError(w, http.StatusBadRequest, "invalid thickness")
			return
		}
	}
	puckScreen := lib.PuckScreens[idx]
	if v, present := trimMaxOrUndefined(body, "name", 200); present {
		if v != "" {
			puckScreen["name"] = v
		}
	}
	if v, present := body["thickness"]; present {
		s, _ := v.(string)
		puckScreen["thickness"] = s
	}
	if v, present := trimMaxOrUndefined(body, "material", 200); present {
		puckScreen["material"] = v
	}
	if v, present := trimMaxOrUndefined(body, "notes", 1000); present {
		puckScreen["notes"] = v
	}
	puckScreen["updatedAt"] = newID()
	lib.PuckScreens[idx] = puckScreen
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, puckScreen)
}

// deletePuckScreen ports DELETE /api/library/puckscreen/:id.
func (h *Handlers) deletePuckScreen(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	if !noMatch {
		if idx := findPuckScreenIndex(lib, id); idx != -1 {
			if ext, _ := lib.PuckScreens[idx]["image"].(string); ext != "" {
				deleteImage(h.imageDir, id, ext, "puckscreen-")
			}
		}
	}
	filtered := make([]Entity, 0, len(lib.PuckScreens))
	for _, p := range lib.PuckScreens {
		pid, ok := idOf(p, "id")
		if !noMatch && ok && pid == id {
			continue
		}
		filtered = append(filtered, p)
	}
	lib.PuckScreens = filtered
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// getPuckScreenImage ports GET /api/library/puckscreen/:id/image.
func (h *Handlers) getPuckScreenImage(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	ext := ""
	if !noMatch {
		if idx := findPuckScreenIndex(lib, id); idx != -1 {
			ext, _ = lib.PuckScreens[idx]["image"].(string)
		}
	}
	h.serveImage(w, r, ext, "puckscreen-", id)
}

// postPuckScreenImage ports POST /api/library/puckscreen/:id/image.
func (h *Handlers) postPuckScreenImage(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	idx := -1
	if !noMatch {
		idx = findPuckScreenIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	data, contentType, ok := readUploadedImage(w, r)
	if !ok {
		return
	}
	ext, ok := saveUploadedImage(h.imageDir, "puckscreen-", id, data, contentType)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported image")
		return
	}
	puckScreen := lib.PuckScreens[idx]
	if oldExt, _ := puckScreen["image"].(string); oldExt != "" && oldExt != ext {
		deleteImage(h.imageDir, id, oldExt, "puckscreen-")
	}
	puckScreen["image"] = ext
	lib.PuckScreens[idx] = puckScreen
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, puckScreen)
}
