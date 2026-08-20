package machines

import (
	"errors"
	"net/http"
)

// This file ports routes/machines.js: the machine-registry CRUD + probe
// endpoints (#317). NOT ported: the #729/#731 catch-up shot-history sync
// every save/update triggers (syncSoonAfterSave, lib/sync.js) — that's the
// shots-sync domain, which doesn't run as a background process in this Go
// binary yet (see go/README.md's "not wired into a running add-on"
// status). The `?sync=0` query param (#731) is still parsed and accepted
// for request-shape compatibility, but is currently a no-op either way
// since there's no sync to suppress.

func (h *Handlers) registerRegistryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/machines", h.listMachines)
	mux.HandleFunc("POST /api/machines", h.createMachine)
	mux.HandleFunc("PUT /api/machines/{id}", h.updateMachine)
	mux.HandleFunc("DELETE /api/machines/{id}", h.deleteMachine)
	mux.HandleFunc("POST /api/machines/{id}/default", h.setDefaultMachine)
	mux.HandleFunc("POST /api/machines/{id}/test", h.testMachine)
}

func (h *Handlers) listMachines(w http.ResponseWriter, r *http.Request) {
	if err := h.registry.EnsureDefaultMachine(); err != nil {
		internalError(w, err)
		return
	}
	list, err := h.registry.ListMachines()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handlers) createMachine(w http.ResponseWriter, r *http.Request) {
	var in MachineInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if err := in.validate(true); err != nil {
		writeError(w, http.StatusBadRequest, "invalid machine: "+err.Error())
		return
	}
	if in.Host != nil && *in.Host != "" {
		hostname, err := hostnameOf(*in.Host)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := assertMachineHost(r.Context(), hostname); err != nil {
			if isSSRFBlocked(err) {
				writeError(w, http.StatusBadRequest, "host not allowed")
			} else {
				writeError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
	}
	machine, err := h.registry.CreateMachine(in)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, machine)
}

func (h *Handlers) updateMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID64(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	existing, err := h.registry.GetMachine(id)
	if err != nil {
		internalError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	var in MachineInput
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if err := in.validate(false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid machine: "+err.Error())
		return
	}
	if in.Host != nil && *in.Host != "" {
		hostname, err := hostnameOf(*in.Host)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := assertMachineHost(r.Context(), hostname); err != nil {
			if isSSRFBlocked(err) {
				writeError(w, http.StatusBadRequest, "host not allowed")
			} else {
				writeError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
	}

	machine, err := h.registry.UpdateMachine(id, in, h.liveClient.DisconnectForHost)
	if err != nil {
		internalError(w, err)
		return
	}
	if machine == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, machine)
}

func (h *Handlers) deleteMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID64(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	deleted, err := h.registry.DeleteMachine(id, h.liveClient.DisconnectForHost)
	if err != nil {
		if errors.Is(err, ErrCannotDeleteDefault) || errors.Is(err, ErrCannotDeleteLastMachine) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		internalError(w, err)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) setDefaultMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID64(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	machine, err := h.registry.SetDefaultMachine(id)
	if err != nil {
		internalError(w, err)
		return
	}
	if machine == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, machine)
}

func (h *Handlers) testMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID64(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	machine, err := h.registry.GetMachine(id)
	if err != nil {
		internalError(w, err)
		return
	}
	if machine == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	adapter, err := h.GetAdapter(machine)
	if err != nil {
		internalError(w, err)
		return
	}
	status, err := adapter.GetStatus(r.Context(), machine)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reachable": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reachable": true, "status": status})
}
