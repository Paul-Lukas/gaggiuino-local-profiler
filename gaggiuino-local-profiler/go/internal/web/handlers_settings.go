package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/httputil"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/web/templates"
)

// This file is Phase 2e's (#901) Settings-domain page: GET /settings, the
// default machine's Gaggiuino settings categories, plus one htmx write
// action per category — POST /settings/{category} — built on
// machines.Adapter's GetSettings/UpdateSettings, the exact same #597
// settings/control proxy GET/POST /api/machine/settings(/{category}) uses
// (see internal/machines/handlers_control.go's getSettings/updateSettings).
// Not internal/machines' own JSON handlers — this page calls
// machines.Adapter directly via AdapterProvider below, mirroring every
// earlier Phase-2 page's "call the service/adapter layer, not the REST
// handler" convention (internal/web/doc.go).
//
// # Why every category round-trips as opaque JSON text
//
// internal/machines/doc.go's "bool-as-string quirk" section documents
// several Gaggiuino settings fields that are the JSON *strings*
// "true"/"false" instead of real booleans (boiler.brewDeltaState,
// display.lcdDarkMode, scales.forcePredictive, led.state, …). The adapter
// layer already preserves that quirk correctly end to end by never
// decoding a settings payload into a typed struct (json.RawMessage in,
// json.RawMessage out — gaggiuino_adapter.go). This page keeps that same
// discipline instead of building typed per-field form widgets: every
// category is fetched as raw bytes, pretty-printed into a <textarea>, and
// posted back as raw bytes (machines.ValidateSettingsPayload only checks
// "is this a JSON object", the same opaque check updateSettings itself
// applies — see internal/machines/validation.go). A typed form would have
// to explicitly re-derive which fields are quirky strings vs. real
// booleans just to not corrupt them on save; a raw-text round trip needs
// no such logic at all, per this phase's "use the existing service/adapter
// layer unchanged, no new parsing logic" instruction.
//
// # display/led/scales are editable — boiler/system stay read-only
//
// An earlier pass of this page only made "display" editable (boiler/led/
// scales/system stayed read-only <pre> blocks) — a dispatch-brief-allowed
// reduced scope for "a full five-way form is too much for this phase". A
// later pass (#901, the Go web-UI Create/Edit follow-up prompted by "ich
// kann garnix anlegen") made all five editable — but a subsequent code
// review (finding #1) flagged that as a real safety regression:
// machines.ValidateSettingsPayload only checks "is this valid JSON",
// never per-field ranges or types, before forwarding the payload straight
// to adapter.UpdateSettings — and that's not a gap this page introduced.
// The REST proxy (routes/machine-control.js's POST /api/machine/settings/
// :category) has always had the exact same opaque check
// (lib/validation/schemas.js's settingsPayloadSchema = z.record(z.string(),
// z.any())) for every one of Node's GAGGIUINO_SETTINGS_CATEGORIES
// (lib/constants.js), boiler included — so closing this for the REST API
// too is out of scope for a web-UI forms package and belongs to a
// dedicated validation-hardening pass across both.
//
// What this page controls is only whether *its own* write path exposes
// that gap. boiler holds real hardware setpoints (targetBrewTemp,
// brewDeltaState — see the fixture data in handlers_settings_test.go) that
// a bad value can genuinely damage or endanger the machine; system holds
// releaseChannel, which selects which firmware channel the machine's own
// OTA flow tracks (stable/test/debug — see
// internal/machines/firmware_check.go) — a wrong value there risks landing
// unstable firmware on real hardware, not just a cosmetic glitch. Neither
// is something this page can safely validate itself (the payload is
// deliberately never decoded into typed fields — see this file's own
// "opaque JSON" section above), so saveAction and settingsCategoryBlock
// (settings.templ) go back to treating both as read-only, restoring their
// pre-#901 behavior, while display/led/scales — cosmetic (display/led) or
// calibration-only (scales), where a bad value is at worst a wrong number
// on screen or a mis-scaled reading — stay editable. See
// settingsReadOnlyCategories below.
//
// Every category still gets fetched and shown (read-only categories render
// as a <pre> block instead of a form), sharing saveAction/
// renderCategoryFragment below instead of duplicating per-category handler
// code. settingsCategoryNames is the fixed fetch/render order;
// isEditableSettingsCategory is the allow-list saveAction checks an
// incoming {category} path value's write access against.
//
// # No per-machine switcher
//
// Unlike handlers_maintenance.go's GET /maintenance, this page always shows
// the registry's *default* machine only — the dispatch brief's own wording
// ("für die aktuelle Maschine", singular) — since a settings round trip is
// a live network call to that one machine's REST API, and
// machines.Adapter's settings-proxy methods are gated by
// Capabilities().SettingsProxy per machine anyway (GaggiMate doesn't
// implement it at all — see this file's supported-flag handling below).

// AdapterProvider is the subset of *machines.Handlers this file depends on:
// its GetAdapter(machine) dispatch (adapter.go). An interface, not
// *machines.Handlers directly, for the same reason
// internal/system/poll.go's own AdapterProvider seam exists — this package
// has no way to reconstruct machines.Handlers' private gaggiuino/gaggimate
// adapter fields itself, and importing internal/system just to reuse its
// interface type would be a needless cross-package dependency for a
// two-line contract. cmd/server passes its single already-constructed
// *machines.Handlers, the same instance internal/machines' and
// internal/system's own REST/poller code shares.
type AdapterProvider interface {
	GetAdapter(m *machines.Machine) (machines.Adapter, error)
}

// settingsCategoryNames is this page's fixed category list, fetch/render
// order, and (for saveAction) the allow-list of {category} path values a
// POST /settings/{category} request may name — see this file's own doc
// comment for why every one of these five is now editable. "display" stays
// first since it was this page's original (and most commonly touched)
// editable category before the other four joined it.
var settingsCategoryNames = []string{"display", "boiler", "led", "scales", "system"}

// settingsReadOnlyCategories are the settings categories this page never
// exposes a write path for — boiler (temperature/PID setpoints — see this
// file's own doc comment) and system (releaseChannel, the OTA firmware
// channel selector). Still fetched and rendered, just as a <pre> block
// instead of an editable form; see isEditableSettingsCategory.
var settingsReadOnlyCategories = map[string]bool{
	"boiler": true,
	"system": true,
}

func isKnownSettingsCategory(name string) bool {
	for _, c := range settingsCategoryNames {
		if c == name {
			return true
		}
	}
	return false
}

// isEditableSettingsCategory is isKnownSettingsCategory further narrowed to
// the categories this page allows a POST /settings/{category} write for —
// see settingsReadOnlyCategories.
func isEditableSettingsCategory(name string) bool {
	return isKnownSettingsCategory(name) && !settingsReadOnlyCategories[name]
}

// SettingsHandlers wires machines.Registry + AdapterProvider into the HTML
// handlers below.
type SettingsHandlers struct {
	registry *machines.Registry
	adapters AdapterProvider
}

// NewSettingsHandlers builds SettingsHandlers around registry and adapters
// — the same *machines.Registry/*machines.Handlers cmd/server already
// constructs once and shares with internal/machines' and internal/system's
// own handlers.
func NewSettingsHandlers(registry *machines.Registry, adapters AdapterProvider) *SettingsHandlers {
	return &SettingsHandlers{registry: registry, adapters: adapters}
}

// RegisterRoutes registers this file's page and htmx-action routes onto
// mux — not prefixed with /api/, for the same GET/HEAD-auth-bypass reason
// handlers.go's RegisterRoutes documents.
func (h *SettingsHandlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.settingsPage)
	mux.HandleFunc("POST /settings/{category}", h.saveAction)
}

// resolveDefaultAdapter looks up the registry's default machine and its
// adapter — nil machine (none configured yet) is a valid, non-error result,
// same as handlers_machines.go's livePage.
func (h *SettingsHandlers) resolveDefaultAdapter() (*machines.Machine, machines.Adapter, error) {
	if err := h.registry.EnsureDefaultMachine(); err != nil {
		return nil, nil, err
	}
	machine, err := h.registry.GetDefaultMachine()
	if err != nil {
		return nil, nil, err
	}
	if machine == nil {
		return nil, nil, nil
	}
	adapter, err := h.adapters.GetAdapter(machine)
	if err != nil {
		return nil, nil, err
	}
	return machine, adapter, nil
}

// fetchCategory reads one settings category, translating a live-machine
// fetch failure (unreachable host, non-2xx, …) into a per-block error
// rather than failing the whole page — every other category, and the rest
// of this page's chrome, should still render even if one category's fetch
// fails.
func (h *SettingsHandlers) fetchCategory(ctx context.Context, adapter machines.Adapter, machine *machines.Machine, category string) templates.SettingsCategory {
	raw, err := adapter.GetSettings(ctx, machine, category)
	if err != nil {
		return templates.SettingsCategory{Name: category, FetchError: "Could not reach machine: " + err.Error()}
	}
	return templates.SettingsCategory{Name: category, JSON: prettyJSON(raw), Editable: isEditableSettingsCategory(category)}
}

// prettyJSON formats raw for the <textarea>/<pre> round trip — falls back
// to the raw bytes verbatim if they somehow aren't valid JSON (the machine
// itself is the source of that data, not this handler, so this is display
// robustness, not a validation step).
func prettyJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// settingsPage ports GET /settings.
func (h *SettingsHandlers) settingsPage(w http.ResponseWriter, r *http.Request) {
	machine, adapter, err := h.resolveDefaultAdapter()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if machine == nil {
		if err := templates.SettingsPage(false, "No machine configured", nil, nil).Render(r.Context(), w); err != nil {
			log.Printf("web: rendering /settings (no machine): %v", err)
		}
		return
	}
	if !adapter.Capabilities().SettingsProxy {
		if err := templates.SettingsPage(false, machine.Name, nil, nil).Render(r.Context(), w); err != nil {
			log.Printf("web: rendering /settings (unsupported): %v", err)
		}
		return
	}
	// The 5 category fetches are independent live-machine HTTP calls, same
	// as firmwareVersion's versions/system pair
	// (internal/machines/handlers_control.go, #901 code review) — fetch
	// them concurrently instead of paying 5 round-trips back to back. Each
	// goroutine writes only its own slice index, so no mutex is needed.
	categories := make([]templates.SettingsCategory, len(settingsCategoryNames))
	var wg sync.WaitGroup
	wg.Add(len(settingsCategoryNames))
	for i, cat := range settingsCategoryNames {
		go func(i int, cat string) {
			defer wg.Done()
			categories[i] = h.fetchCategory(r.Context(), adapter, machine, cat)
		}(i, cat)
	}
	wg.Wait()
	if err := templates.SettingsPage(true, machine.Name, categories, nil).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /settings: %v", err)
	}
}

// saveAction ports the htmx `hx-post="settings/{category}"` interaction,
// shared by every editable category: forward the submitted textarea's exact
// bytes to adapter.UpdateSettings, unmodified — see this file's own doc
// comment on why this stays a raw-bytes round trip. Re-fetches the category
// from the machine after a successful save (rather than trusting the
// submitted text) so the re-rendered textarea reflects whatever the
// machine actually persisted. {category} is checked against
// isKnownSettingsCategory before anything else — a request for a category
// this page doesn't know about (a stale link, a hand-crafted request) gets
// a plain 404 rather than reaching the adapter at all — and then against
// isEditableSettingsCategory: a request naming a real but read-only
// category (boiler, system — see this file's own doc comment) 403s instead
// of forwarding an unvalidated payload to the machine, even if a client
// crafts the POST directly rather than going through the (form-less) page.
func (h *SettingsHandlers) saveAction(w http.ResponseWriter, r *http.Request) {
	category := r.PathValue("category")
	if !isKnownSettingsCategory(category) {
		writeFragmentError(w, http.StatusNotFound, "Unknown settings category")
		return
	}
	if !isEditableSettingsCategory(category) {
		writeFragmentError(w, http.StatusForbidden, "This settings category is read-only")
		return
	}
	machine, adapter, err := h.resolveDefaultAdapter()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	if machine == nil {
		writeFragmentError(w, http.StatusNotFound, "No machine configured")
		return
	}
	if !adapter.Capabilities().SettingsProxy {
		writeFragmentError(w, http.StatusNotImplemented, "This machine type does not support the settings proxy")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeFragmentError(w, http.StatusBadRequest, "Invalid form submission")
		return
	}
	submitted := r.FormValue("raw")
	raw := json.RawMessage(submitted)
	if err := machines.ValidateSettingsPayload(raw); err != nil {
		h.renderCategoryFragment(w, r, templates.SettingsCategory{Name: category, JSON: submitted, Editable: true}, err.Error())
		return
	}
	if _, err := adapter.UpdateSettings(r.Context(), machine, category, raw); err != nil {
		h.renderCategoryFragment(w, r, templates.SettingsCategory{Name: category, JSON: submitted, Editable: true}, "Save failed: "+err.Error())
		return
	}
	h.renderCategoryFragment(w, r, h.fetchCategory(r.Context(), adapter, machine, category), "")
}

func (h *SettingsHandlers) renderCategoryFragment(w http.ResponseWriter, r *http.Request, category templates.SettingsCategory, saveError string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.SettingsEditableFragment(category, saveError).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /settings/%s fragment: %v", category.Name, err)
	}
}
