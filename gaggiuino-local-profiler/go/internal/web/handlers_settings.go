package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/httputil"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/web/templates"
)

// This file is Phase 2e's (#901) Settings-domain page: GET /settings, the
// default machine's Gaggiuino settings categories, plus one htmx write
// action — POST /settings/display — built on machines.Adapter's
// GetSettings/UpdateSettings, the exact same #597 settings/control proxy
// GET/POST /api/machine/settings(/{category}) uses (see
// internal/machines/handlers_control.go's getSettings/updateSettings). Not
// internal/machines' own JSON handlers — this page calls machines.Adapter
// directly via AdapterProvider below, mirroring every earlier Phase-2
// page's "call the service/adapter layer, not the REST handler" convention
// (internal/web/doc.go).
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
// category is fetched as raw bytes, pretty-printed into a <textarea> for
// the one editable category ("display"), and posted back as raw bytes
// (machines.ValidateSettingsPayload only checks "is this a JSON object",
// the same opaque check updateSettings itself applies — see
// internal/machines/validation.go). A typed form would have to explicitly
// re-derive which fields are quirky strings vs. real booleans just to not
// corrupt them on save; a raw-text round trip needs no such logic at all,
// per this phase's "use the existing service/adapter layer unchanged, no
// new parsing logic" instruction.
//
// # Scope: one editable category, four read-only
//
// The dispatch brief allows a single editable category if a full five-way
// form is too much for this phase; "display" was picked as that one
// category (boiler/led/scales/system stay read-only <pre> blocks) — small,
// human-editable, and not safety-critical the way e.g. boiler PID/PWM
// tuning would be. A typed, friendlier form (or making every category
// editable) is a reasonable follow-up, not part of this package.
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

// settingsReadOnlyCategories/settingsEditableCategory are this page's fixed
// category list — see this file's own doc comment for why "display" is the
// one editable category.
var settingsReadOnlyCategories = []string{"boiler", "led", "scales", "system"}

const settingsEditableCategory = "display"

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
	mux.HandleFunc("POST /settings/"+settingsEditableCategory, h.saveEditableAction)
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
	return templates.SettingsCategory{Name: category, JSON: prettyJSON(raw)}
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
		if err := templates.SettingsPage(false, "No machine configured", nil, templates.SettingsCategory{}, "").Render(r.Context(), w); err != nil {
			log.Printf("web: rendering /settings (no machine): %v", err)
		}
		return
	}
	if !adapter.Capabilities().SettingsProxy {
		if err := templates.SettingsPage(false, machine.Name, nil, templates.SettingsCategory{}, "").Render(r.Context(), w); err != nil {
			log.Printf("web: rendering /settings (unsupported): %v", err)
		}
		return
	}
	readOnly := make([]templates.SettingsCategory, 0, len(settingsReadOnlyCategories))
	for _, cat := range settingsReadOnlyCategories {
		readOnly = append(readOnly, h.fetchCategory(r.Context(), adapter, machine, cat))
	}
	editable := h.fetchCategory(r.Context(), adapter, machine, settingsEditableCategory)
	if err := templates.SettingsPage(true, machine.Name, readOnly, editable, "").Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /settings: %v", err)
	}
}

// saveEditableAction ports the htmx `hx-post="/settings/display"`
// interaction: forward the submitted textarea's exact bytes to
// adapter.UpdateSettings, unmodified — see this file's own doc comment on
// why this stays a raw-bytes round trip. Re-fetches the category from the
// machine after a successful save (rather than trusting the submitted
// text) so the re-rendered textarea reflects whatever the machine actually
// persisted.
func (h *SettingsHandlers) saveEditableAction(w http.ResponseWriter, r *http.Request) {
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
		h.renderEditableFragment(w, r, templates.SettingsCategory{Name: settingsEditableCategory, JSON: submitted}, err.Error())
		return
	}
	if _, err := adapter.UpdateSettings(r.Context(), machine, settingsEditableCategory, raw); err != nil {
		h.renderEditableFragment(w, r, templates.SettingsCategory{Name: settingsEditableCategory, JSON: submitted}, "Save failed: "+err.Error())
		return
	}
	h.renderEditableFragment(w, r, h.fetchCategory(r.Context(), adapter, machine, settingsEditableCategory), "")
}

func (h *SettingsHandlers) renderEditableFragment(w http.ResponseWriter, r *http.Request, editable templates.SettingsCategory, saveError string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.SettingsEditableFragment(editable, saveError).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /settings/%s fragment: %v", settingsEditableCategory, err)
	}
}
