package library

import "net/http"

// This file ports routes/library/recipes.js.

var validBrewMethods = map[string]bool{
	"espresso": true, "aeropress": true, "v60": true, "french_press": true,
	"moka": true, "cold_brew": true, "other": true,
}

func findRecipeIndex(lib Library, id int64) int {
	for i, rc := range lib.Recipes {
		if rid, ok := idOf(rc, "id"); ok && rid == id {
			return i
		}
	}
	return -1
}

// parseSteps ports routes/library/recipes.js's local _parseSteps: up to 30
// {text, duration_s} steps, entries with a blank text dropped entirely.
func parseSteps(raw any) []any {
	arr, ok := raw.([]any)
	if !ok {
		return []any{}
	}
	out := []any{}
	for i, item := range arr {
		if i >= 30 {
			break
		}
		step, _ := item.(Entity)
		text := trimMax(step["text"], 500)
		if text == "" {
			continue
		}
		out = append(out, Entity{"text": text, "duration_s": floatOrNilFalsy(step["duration_s"])})
	}
	return out
}

func brewMethodOrOther(v any) string {
	s, ok := v.(string)
	if ok && validBrewMethods[s] {
		return s
	}
	return "other"
}

// createRecipe ports POST /api/library/recipe.
func (h *Handlers) createRecipe(w http.ResponseWriter, r *http.Request) {
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
	recipe := Entity{
		"id": newID(), "name": trimMax(body["name"], 200),
		"brewMethod": brewMethodOrOther(body["brewMethod"]), "drinkType": trimMax(body["drinkType"], 50),
		"targetDose_g": floatOrNilFalsy(body["targetDose_g"]), "targetYield_g": floatOrNilFalsy(body["targetYield_g"]),
		"targetTime_s": floatOrNilFalsy(body["targetTime_s"]),
		"waterTemp_c":  floatOrNilFalsy(body["waterTemp_c"]), "water_g": floatOrNilFalsy(body["water_g"]),
		"ice_g":     floatOrNilFalsy(body["ice_g"]),
		"grindSize": trimMax(body["grindSize"], 200),
		"sourceUrl": safeURL(body["sourceUrl"]),
		"steps":     parseSteps(body["steps"]),
		"notes":     trimMax(body["notes"], 1000), "profileName": trimMax(body["profileName"], 200),
		"beanName": trimMax(body["beanName"], 200),
	}
	lib.Recipes = append(lib.Recipes, recipe)
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

// updateRecipe ports PUT /api/library/recipe/:id.
func (h *Handlers) updateRecipe(w http.ResponseWriter, r *http.Request) {
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
		idx = findRecipeIndex(lib, id)
	}
	if idx == -1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	recipe := lib.Recipes[idx]
	if v, present := trimMaxOrUndefined(body, "name", 200); present {
		if v != "" {
			recipe["name"] = v
		}
	}
	if _, present := body["brewMethod"]; present {
		recipe["brewMethod"] = brewMethodOrOther(body["brewMethod"])
	}
	if v, present := trimMaxOrUndefined(body, "drinkType", 50); present {
		recipe["drinkType"] = v
	}
	if _, present := body["targetDose_g"]; present {
		recipe["targetDose_g"] = floatOrNilFalsy(body["targetDose_g"])
	}
	if _, present := body["targetYield_g"]; present {
		recipe["targetYield_g"] = floatOrNilFalsy(body["targetYield_g"])
	}
	if _, present := body["targetTime_s"]; present {
		recipe["targetTime_s"] = floatOrNilFalsy(body["targetTime_s"])
	}
	if _, present := body["waterTemp_c"]; present {
		recipe["waterTemp_c"] = floatOrNilFalsy(body["waterTemp_c"])
	}
	if _, present := body["water_g"]; present {
		recipe["water_g"] = floatOrNilFalsy(body["water_g"])
	}
	if _, present := body["ice_g"]; present {
		recipe["ice_g"] = floatOrNilFalsy(body["ice_g"])
	}
	if v, present := trimMaxOrUndefined(body, "grindSize", 200); present {
		recipe["grindSize"] = v
	}
	if _, present := body["sourceUrl"]; present {
		recipe["sourceUrl"] = safeURL(body["sourceUrl"])
	}
	if _, present := body["steps"]; present {
		recipe["steps"] = parseSteps(body["steps"])
	}
	if v, present := trimMaxOrUndefined(body, "notes", 1000); present {
		recipe["notes"] = v
	}
	if v, present := trimMaxOrUndefined(body, "profileName", 200); present {
		recipe["profileName"] = v
	}
	if v, present := trimMaxOrUndefined(body, "beanName", 200); present {
		recipe["beanName"] = v
	}
	lib.Recipes[idx] = recipe
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

// deleteRecipe ports POST /api/library/recipe/:id/delete.
func (h *Handlers) deleteRecipe(w http.ResponseWriter, r *http.Request) {
	id, noMatch := parseIDParam(r.PathValue("id"))
	lib, err := h.repo.GetLibrary()
	if err != nil {
		internalError(w, err)
		return
	}
	filtered := make([]Entity, 0, len(lib.Recipes))
	for _, rc := range lib.Recipes {
		rid, ok := idOf(rc, "id")
		if !noMatch && ok && rid == id {
			continue
		}
		filtered = append(filtered, rc)
	}
	lib.Recipes = filtered
	if err := h.repo.SaveLibrary(lib); err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
