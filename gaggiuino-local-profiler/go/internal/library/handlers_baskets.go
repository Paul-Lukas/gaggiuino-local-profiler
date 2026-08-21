package library

import (
	"errors"
	"net/http"
)

// This file ports routes/library/baskets.js (#635).

var basketWallTypes = map[string]bool{"pressurized": true, "single-wall": true, "precision-machined": true, "high-flow": true}
var basketShapes = map[string]bool{"straight": true, "tapered": true}

func findBasketIndex(lib Library, id int64) int {
	for i, b := range lib.Baskets {
		if bid, ok := idOf(b, "id"); ok && bid == id {
			return i
		}
	}
	return -1
}

// listBaskets ports GET /api/library/baskets.
func (h *Handlers) listBaskets(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lib.Baskets)
}

// createBasket ports POST /api/library/basket — a thin wrapper around
// CreateBasket (create.go), the same logic internal/web's "New basket" form
// also calls.
func (h *Handlers) createBasket(w http.ResponseWriter, r *http.Request) {
	if !h.rateLimitCreate(w, r) {
		return
	}
	body, ok := decodeJSONBody(w, r)
	if !ok {
		return
	}
	basket, err := CreateBasket(h.repo, body)
	if err != nil {
		var verr *ValidationError
		if errors.As(err, &verr) {
			writeError(w, http.StatusBadRequest, verr.Message)
			return
		}
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, basket)
}

// updateBasket ports PUT /api/library/basket/:id.
func (h *Handlers) updateBasket(w http.ResponseWriter, r *http.Request) {
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
		idx = findBasketIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	wallType, wallTypePresent, wallTypeOK := enumStringField(body, "wallType", basketWallTypes)
	if !wallTypeOK {
		writeError(w, http.StatusBadRequest, "invalid wallType")
		return
	}
	shape, shapePresent, shapeOK := enumStringField(body, "shape", basketShapes)
	if !shapeOK {
		writeError(w, http.StatusBadRequest, "invalid shape")
		return
	}
	basket := lib.Baskets[idx]
	if v, present := trimMaxOrUndefined(body, "name", 200); present {
		if v != "" {
			basket["name"] = v
		}
	}
	if v, present := trimMaxOrUndefined(body, "doseCapacity", 50); present {
		basket["doseCapacity"] = v
	}
	if wallTypePresent {
		basket["wallType"] = wallType
	}
	if shapePresent {
		basket["shape"] = shape
	}
	if v, present := trimMaxOrUndefined(body, "holeCount", 50); present {
		basket["holeCount"] = v
	}
	if v, present := trimMaxOrUndefined(body, "notes", 1000); present {
		basket["notes"] = v
	}
	basket["updatedAt"] = newID()
	lib.Baskets[idx] = basket
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, basket)
}

// deleteBasket ports DELETE /api/library/basket/:id.
func (h *Handlers) deleteBasket(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	if !noMatch {
		if idx := findBasketIndex(lib, id); idx != -1 {
			if ext, _ := lib.Baskets[idx]["image"].(string); ext != "" {
				deleteImage(h.imageDir, id, ext, "basket-")
			}
		}
	}
	filtered := make([]Entity, 0, len(lib.Baskets))
	for _, b := range lib.Baskets {
		bid, ok := idOf(b, "id")
		if !noMatch && ok && bid == id {
			continue
		}
		filtered = append(filtered, b)
	}
	lib.Baskets = filtered
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// getBasketImage ports GET /api/library/basket/:id/image.
func (h *Handlers) getBasketImage(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	ext := ""
	if !noMatch {
		if idx := findBasketIndex(lib, id); idx != -1 {
			ext, _ = lib.Baskets[idx]["image"].(string)
		}
	}
	h.serveImage(w, r, ext, "basket-", id)
}

// postBasketImage ports POST /api/library/basket/:id/image.
func (h *Handlers) postBasketImage(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	idx := -1
	if !noMatch {
		idx = findBasketIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	data, contentType, ok := readUploadedImage(w, r)
	if !ok {
		return
	}
	ext, ok := saveUploadedImage(h.imageDir, "basket-", id, data, contentType)
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported image")
		return
	}
	basket := lib.Baskets[idx]
	if oldExt, _ := basket["image"].(string); oldExt != "" && oldExt != ext {
		deleteImage(h.imageDir, id, oldExt, "basket-")
	}
	basket["image"] = ext
	lib.Baskets[idx] = basket
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, basket)
}
