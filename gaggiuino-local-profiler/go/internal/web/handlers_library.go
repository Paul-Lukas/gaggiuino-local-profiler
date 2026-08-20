package web

import (
	"log"
	"net/http"
	"strconv"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/httputil"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/web/templates"
)

// This file is Phase 2b's (#901) Library-domain counterpart to handlers.go:
// GET /beans (+ its one htmx write action, toggle-active) and five read-only
// list pages (Grinders, Baskets, Puck Screens, Milks, Recipes) — see
// go/README.md's Status section and this package's own doc.go for the auth
// model every route below relies on unchanged (GET pages public per the
// Ingress-trust model, the one write action behind auth.RequireToken's
// GET/HEAD-scoped bypass, X-GLP-Token wired in globally via
// templates/layout.templ's glp-token.js — nothing new to wire per page).
//
// Every handler here calls internal/library's existing Repository/exported
// service functions (library.ComputeGrinderWearStats, library.ComputeBeanRemaining,
// library.ToggleBeanActive) directly — the same dependencies
// internal/library's own REST handlers.go call — never internal/library's
// http.Handler-returning REST endpoints themselves, mirroring how
// handlers.go's shots page calls shots.Service instead of internal/shots'
// JSON handlers.

// LibraryHandlers wires library.Repository (+ a shots.Repository for the
// two cross-domain reads Beans/Grinders need — bean consumption, grinder
// wear) into the HTML handlers below.
type LibraryHandlers struct {
	repo      *library.Repository
	shotsRepo *shots.Repository
}

// NewLibraryHandlers builds LibraryHandlers around repo and shotsRepo — the
// same *library.Repository/*shots.Repository cmd/server already constructs
// once and shares with internal/library's and internal/shots' own REST
// handlers.
func NewLibraryHandlers(repo *library.Repository, shotsRepo *shots.Repository) *LibraryHandlers {
	return &LibraryHandlers{repo: repo, shotsRepo: shotsRepo}
}

// RegisterRoutes registers this file's page and htmx-action routes onto
// mux — not prefixed with /api/, for the same GET/HEAD-auth-bypass reason
// handlers.go's RegisterRoutes documents.
func (h *LibraryHandlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /beans", h.beansPage)
	mux.HandleFunc("POST /beans/{id}/toggle-active", h.toggleBeanActiveAction)

	mux.HandleFunc("GET /grinders", h.grindersPage)
	mux.HandleFunc("GET /baskets", h.basketsPage)
	mux.HandleFunc("GET /puckscreens", h.puckScreensPage)
	mux.HandleFunc("GET /milks", h.milksPage)
	mux.HandleFunc("GET /recipes", h.recipesPage)
}

// ── Beans ──────────────────────────────────────────────────────────────

// beansPage ports GET /beans: every bean in the library, projected the same
// way public-src/views/library.js's renderBeanList reads S.coffeeLibrary —
// see view_library.go's toBeanRow.
func (h *LibraryHandlers) beansPage(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	doseRows, err := h.shotsRepo.GetAnnotatedDoses()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	rows := make([]templates.BeanRow, len(lib.Beans))
	for i, bean := range lib.Beans {
		rows[i] = toBeanRow(bean, doseRows, lib.Beans)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.BeansPage(rows).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /beans: %v", err)
	}
}

// toggleBeanActiveAction ports the htmx `hx-post="/beans/{id}/toggle-active"`
// interaction: flips the bean's enabled flag via the same
// library.ToggleBeanActive the REST API's POST /api/library/bean/:id/toggle-active
// now also calls (internal/library/handlers_beans.go), then answers with the
// re-rendered row so htmx's `hx-swap="outerHTML"` reflects the new
// enabled/disabled state in place — unlike shots' trash/restore actions,
// this one doesn't remove the row from the page.
func (h *LibraryHandlers) toggleBeanActiveAction(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLibraryID(r.PathValue("id"))
	if !ok {
		writeFragmentError(w, http.StatusBadRequest, "Invalid bean ID")
		return
	}
	bean, found, err := library.ToggleBeanActive(h.repo, id)
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	if !found {
		writeFragmentError(w, http.StatusNotFound, "Bean not found")
		return
	}
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	doseRows, err := h.shotsRepo.GetAnnotatedDoses()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	row := toBeanRow(bean, doseRows, lib.Beans)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.BeanFragment(row).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /beans/%d/toggle-active fragment: %v", id, err)
	}
}

// ── Grinders ───────────────────────────────────────────────────────────

// grindersPage ports GET /grinders: a read-only list — see this package's
// dispatch brief's scope call that only Beans gets a write action in this
// phase, every other entity a plain list (a full CRUD UI is later-phase
// work).
func (h *LibraryHandlers) grindersPage(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	rows := make([]templates.GrinderRow, len(lib.Grinders))
	for i, grinder := range lib.Grinders {
		shotsSince, gramsSince, err := library.ComputeGrinderWearStats(h.shotsRepo, grinder)
		if err != nil {
			httputil.InternalError(w, "web", err)
			return
		}
		rows[i] = toGrinderRow(grinder, shotsSince, gramsSince)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.GrindersPage(rows).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /grinders: %v", err)
	}
}

// ── Baskets, Puck Screens, Milks, Recipes ─────────────────────────────
//
// All four are plain read-only list pages over their own library.Library
// collection — no cross-domain computation (unlike Beans' consumption math
// or Grinders' wear stats), so each handler is a straight GetLibrary +
// per-entity projection + Render.

func (h *LibraryHandlers) basketsPage(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	rows := make([]templates.BasketRow, len(lib.Baskets))
	for i, basket := range lib.Baskets {
		rows[i] = toBasketRow(basket)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.BasketsPage(rows).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /baskets: %v", err)
	}
}

func (h *LibraryHandlers) puckScreensPage(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	rows := make([]templates.PuckScreenRow, len(lib.PuckScreens))
	for i, puckScreen := range lib.PuckScreens {
		rows[i] = toPuckScreenRow(puckScreen)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.PuckScreensPage(rows).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /puckscreens: %v", err)
	}
}

func (h *LibraryHandlers) milksPage(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	rows := make([]templates.MilkRow, len(lib.Milks))
	for i, milk := range lib.Milks {
		rows[i] = toMilkRow(milk)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.MilksPage(rows).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /milks: %v", err)
	}
}

func (h *LibraryHandlers) recipesPage(w http.ResponseWriter, r *http.Request) {
	lib, err := h.repo.GetLibrary()
	if err != nil {
		httputil.InternalError(w, "web", err)
		return
	}
	rows := make([]templates.RecipeRow, len(lib.Recipes))
	for i, recipe := range lib.Recipes {
		rows[i] = toRecipeRow(recipe)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.RecipesPage(rows).Render(r.Context(), w); err != nil {
		log.Printf("web: rendering /recipes: %v", err)
	}
}

// parseLibraryID parses a path {id} segment the same way handlers.go's
// parseShotID does for the Shots page: plain strconv (this path segment is
// always htmx's own hx-post URL built from a Row's ID, never user free
// text), with no upper bound — library entity ids are millisecond-epoch
// timestamps (see internal/library/model.go's newID), not the small
// sequential range internal/shots.MaxShotID caps.
func parseLibraryID(param string) (int64, bool) {
	id, err := strconv.ParseInt(param, 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}
