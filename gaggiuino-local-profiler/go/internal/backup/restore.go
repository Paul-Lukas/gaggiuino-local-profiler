package backup

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/auth"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/library"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/machines"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/maintenance"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/orders"
	"github.com/mxkissnr/gaggiuino-local-profiler/go/internal/shots"
)

// This file ports routes/backup.js's POST /api/restore: the single
// largest handler in the Node app, so this port is split across a few
// helper functions the handler (postRestore, in handlers.go's sibling —
// see below) composes, roughly mirroring the Node function's own
// top-to-bottom structure (parse request -> validate -> sanitize/preview
// -> [dry-run: return] -> apply -> side effects -> respond).
//
// # Atomicity: a real, documented gap from Node
//
// routes/backup.js wraps every DB write below in one
// getDb().transaction(() => {...}) — shotRepo.wipeAll() through the kv
// writes all roll back together on any failure. This Go port does NOT
// reproduce that: internal/shots/library/maintenance/orders/machines'
// Repository types each own *sql.DB directly and manage their own
// transactions internally (see e.g. shots.Repository.WipeAll), with no
// shared external *sql.Tx parameter threaded through every write method
// across five packages — building that is a real architecture change
// (every Repository constructor and write method across five packages
// would need a Querier/Tx-accepting variant), out of scope for a single
// Phase 1f task. applyRestore below instead writes each section
// sequentially, each internally atomic on its own, but a failure partway
// through (e.g. shots restore succeeds, then a maintenance-table write
// fails) leaves earlier sections applied and later ones not — unlike
// Node, where that same failure would roll back the shots write too. This
// is flagged here, in the doc.go summary, and in the go/README.md status
// update as the one known, deliberate behavior gap in this domain.
const maxShotID = shots.MaxShotID

// restoreRequest mirrors normaliseRestoreRequest's `{ b, imagesMap }`
// return shape: b is the bundle (as a generic decoded map, so every loose/
// presence check below reads it exactly like Node's plain-object `req.body`
// does), imagesMap is always filename -> raw bytes (no base64 anywhere past
// this point).
type restoreRequest struct {
	b         map[string]any
	imagesMap map[string][]byte
}

// parseRestoreRequest ports normaliseRestoreRequest + legacyImagesMap: zip
// body (backup.json + images/*, sections/dryRun/passphrase via headers) or
// legacy self-contained JSON body (images embedded as base64 under `images`).
// postRestore ports POST /api/restore end to end.
func (h *Handlers) postRestore(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	isZip := strings.HasPrefix(contentType, "application/zip")

	req, err := parseRestoreRequest(r, isZip)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b := req.b

	isDryRun, _ := b["dryRun"].(bool)
	ip := auth.RemoteIP(r)
	var limitOK bool
	if isDryRun {
		limitOK = h.rl.Allow("restore-preview:"+ip, 30)
	} else {
		limitOK = h.rl.Allow("restore:"+ip, 3)
	}
	if !limitOK {
		writeError(w, http.StatusTooManyRequests, "Rate limit exceeded")
		return
	}

	glpBackup, _ := b["glp_backup"].(bool)
	shotsArr, shotsIsArray := b["shots"].([]any)
	if !glpBackup || !shotsIsArray {
		writeError(w, http.StatusBadRequest, "Invalid backup file")
		return
	}
	if len(shotsArr) > maxShotID {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Backup contains too many shots (max %d)", maxShotID))
		return
	}

	sec := normaliseSections(b["sections"])
	wantsShots := sec.has("shots")
	if wantsShots {
		for i, v := range shotsArr {
			s, ok := v.(map[string]any)
			if !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Backup shot #%d is not a valid object", i))
				return
			}
			id, idOK := jsIntStrict(s["id"])
			if !idOK || id <= 0 {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Backup shot #%d has an invalid id (%v)", i, s["id"]))
				return
			}
			if _, ok := s["timestamp"].(float64); !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("Backup shot #%d (id=%d) has an invalid or missing timestamp", i, id))
				return
			}
		}
	}

	plan := buildRestorePlan(b, req.imagesMap)

	if isDryRun {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dryRun": true, "preview": plan.preview()})
		return
	}

	if err := h.applyRestore(plan); err != nil {
		internalError(w, err)
		return
	}

	h.applyRestoredToken(plan.restoredToken)
	h.writePendingImages(plan.pending)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "shots": len(shotsArr) * boolToInt(wantsShots),
		"secretsPresent": plan.secretsPresent, "secretsRestored": plan.secretsRestored,
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func parseRestoreRequest(r *http.Request, isZip bool) (restoreRequest, error) {
	if isZip {
		data, err := io.ReadAll(io.LimitReader(r.Body, restoreZipBodyLimit+1))
		if err != nil {
			return restoreRequest{}, err
		}
		if len(data) > restoreZipBodyLimit {
			return restoreRequest{}, errBodyTooLarge
		}
		backupJSON, images, err := readZip(data)
		if err != nil {
			return restoreRequest{}, err
		}
		var parsed map[string]any
		if err := json.Unmarshal(backupJSON, &parsed); err != nil {
			return restoreRequest{}, fmt.Errorf("invalid backup file (backup.json is not valid JSON)")
		}
		b := map[string]any{}
		for k, v := range parsed {
			b[k] = v
		}
		b["dryRun"] = r.Header.Get("X-GLP-Dry-Run") == "true"
		if sectionsHeader := r.Header.Get("X-GLP-Sections"); sectionsHeader != "" {
			var sec any
			if err := json.Unmarshal([]byte(sectionsHeader), &sec); err != nil {
				return restoreRequest{}, fmt.Errorf("Invalid X-GLP-Sections header")
			}
			b["sections"] = sec
		}
		if pass := r.Header.Get("X-GLP-Passphrase"); pass != "" {
			b["passphrase"] = pass
		}
		return restoreRequest{b: b, imagesMap: images}, nil
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, restoreJSONBodyLimit+1))
	if err != nil {
		return restoreRequest{}, err
	}
	if len(data) > restoreJSONBodyLimit {
		return restoreRequest{}, errBodyTooLarge
	}
	var b map[string]any
	if err := json.Unmarshal(data, &b); err != nil {
		return restoreRequest{}, fmt.Errorf("Invalid JSON body")
	}
	images := map[string][]byte{}
	if raw, ok := b["images"].(map[string]any); ok {
		for name, v := range raw {
			s, _ := v.(string)
			if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
				images[name] = decoded
			}
		}
	}
	return restoreRequest{b: b, imagesMap: images}, nil
}

var errBodyTooLarge = fmt.Errorf("request entity too large")

// restorePlan is every "what would actually be written" computation —
// identical whether this is a dry run or a real restore, so preview counts
// and applied counts can never drift apart.
type restorePlan struct {
	b          map[string]any
	sec        sections
	wantsShots bool

	sanitizedLib map[string]any // nil if not restoring a library
	pending      []pendingImageWrite

	validMaintenance    []maintenance.RawRow
	maintenanceTotal    int
	validMaintenanceLog []maintenance.RawLogRow
	maintenanceLogTotal int

	validOrders []orders.Order
	ordersTotal int

	wantsMachines    bool
	restoredMachines []machines.Machine

	wantsSettings bool

	secretsPresent   bool
	secretsRestored  bool
	decryptedSecrets map[string]any
	restoredToken    string
}

// buildRestorePlan ports the "Every 'what would actually be written'
// computation" block of routes/backup.js's POST /api/restore, from
// `sections := normaliseSections(...)` through `restoredToken`.
func buildRestorePlan(b map[string]any, imagesMap map[string][]byte) restorePlan {
	sec := normaliseSections(b["sections"])
	wantsShots := sec.has("shots")

	plan := restorePlan{b: b, sec: sec, wantsShots: wantsShots}

	wantsSecrets := sec.has("secrets")
	passphrase, _ := b["passphrase"].(string)
	secretsBlob, hasSecretsField := b["secrets"].(map[string]any)
	plan.secretsPresent = wantsSecrets && hasSecretsField
	if plan.secretsPresent && passphrase != "" {
		if enc := decodeEncryptedSecrets(secretsBlob); enc != nil {
			plan.decryptedSecrets = DecryptSecrets(enc, passphrase)
		}
	}
	plan.secretsRestored = plan.decryptedSecrets != nil

	if wantsShots {
		if rawLib, ok := b["coffee_library"].(map[string]any); ok {
			sanitized := map[string]any{}
			for k, v := range rawLib {
				sanitized[k] = v
			}
			validateRestoredLibraryImages(sanitized, imagesMap, &plan.pending)
			plan.sanitizedLib = sanitized
		}
		if shotsArr, ok := b["shots"].([]any); ok {
			list := make([]map[string]any, 0, len(shotsArr))
			for _, v := range shotsArr {
				if m, ok := v.(map[string]any); ok {
					list = append(list, m)
				}
			}
			validateEntityImages(list, "shot-", imagesMap, &plan.pending)
		}
	}

	if sec.has("maintenance") {
		if arr, ok := b["maintenance"].([]any); ok {
			plan.maintenanceTotal = len(arr)
			for _, v := range arr {
				m, _ := v.(map[string]any)
				if sanitized, ok := sanitizeMaintenanceRow(m); ok {
					plan.validMaintenance = append(plan.validMaintenance, toRawRow(sanitized))
				}
			}
		}
		if arr, ok := b["maintenance_log"].([]any); ok {
			plan.maintenanceLogTotal = len(arr)
			for _, v := range arr {
				m, _ := v.(map[string]any)
				if sanitized, ok := sanitizeMaintenanceLogRow(m); ok {
					plan.validMaintenanceLog = append(plan.validMaintenanceLog, toRawLogRow(sanitized))
				}
			}
		}
	}

	if sec.has("orders") {
		if arr, ok := b["orders"].([]any); ok {
			plan.ordersTotal = len(arr)
			for _, v := range arr {
				m, _ := v.(map[string]any)
				if sanitized, ok := sanitizeOrderRow(m); ok {
					plan.validOrders = append(plan.validOrders, orders.Order(sanitized))
				}
			}
		}
	}

	if arr, ok := b["machines"].([]any); ok && sec.has("machines") {
		plan.wantsMachines = true
		var list []machines.Machine
		if err := reDecode(arr, &list); err == nil {
			plan.restoredMachines = list
		}
	}

	plan.wantsSettings = sec.has("settings")

	if s, ok := plan.decryptedSecrets["apiToken"].(string); ok {
		plan.restoredToken = sanitizeToken(s)
	}

	return plan
}

func decodeEncryptedSecrets(m map[string]any) *EncryptedSecrets {
	var enc EncryptedSecrets
	if err := reDecode(m, &enc); err != nil {
		return nil
	}
	return &enc
}

// sanitizeToken ports `decryptedSecrets?.apiToken.replace(/[\r\n\0]/g,
// ”).trim().slice(0, 200)`.
func sanitizeToken(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\r' || r == '\n' || r == 0 {
			continue
		}
		out = append(out, r)
	}
	trimmed := trimString(string(out))
	return truncateRunes(trimmed, 200)
}

func toRawRow(m map[string]any) maintenance.RawRow {
	machineID, _ := m["machineId"].(int64)
	key, _ := m["key"].(string)
	data, _ := json.Marshal(m["data"])
	return maintenance.RawRow{MachineID: machineID, Key: key, Data: data}
}

func toRawLogRow(m map[string]any) maintenance.RawLogRow {
	get := func(k string) int64 { v, _ := m[k].(int64); return v }
	getS := func(k string) string { v, _ := m[k].(string); return v }
	return maintenance.RawLogRow{
		ID: get("id"), TS: get("ts"), Date: getS("date"), Task: getS("task"),
		Machine: getS("machine"), ShotCount: get("shotCount"), Notes: getS("notes"),
		MachineID: get("machineId"),
	}
}

// reDecode round-trips v (already-decoded generic JSON values) through
// encoding/json into a specifically-typed out — used where a typed
// destination (machines.Machine, EncryptedSecrets) is easier to populate
// via a second decode pass than by hand-picking fields out of a
// map[string]any, the same trade-off shots.Shot/library.Entity/
// orders.Order's own "just use the generic map" choice makes in the other
// direction for shapes with no fixed structure.
func reDecode(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// preview ports the dry-run response's `preview` object.
func (p restorePlan) preview() map[string]any {
	shotsCount := 0
	if p.wantsShots {
		if arr, ok := p.b["shots"].([]any); ok {
			shotsCount = len(arr)
		}
	}
	return map[string]any{
		"shots":               shotsCount,
		"library":             p.wantsShots && p.sanitizedLib != nil,
		"maintenance":         len(p.validMaintenance),
		"maintenanceTotal":    p.maintenanceTotal,
		"maintenanceLog":      len(p.validMaintenanceLog),
		"maintenanceLogTotal": p.maintenanceLogTotal,
		"orders":              len(p.validOrders),
		"ordersTotal":         p.ordersTotal,
		"machines":            machineCountForPreview(p),
		"settings":            p.wantsSettings && p.b["kv"] != nil,
		"images":              len(p.pending),
		"secretsPresent":      p.secretsPresent,
		"secretsRestored":     p.secretsRestored,
		"sectionsPresent":     sectionsPresent(p.b),
	}
}

func machineCountForPreview(p restorePlan) int {
	if !p.wantsMachines {
		return 0
	}
	if arr, ok := p.b["machines"].([]any); ok {
		return len(arr)
	}
	return 0
}

// sectionsPresent ports `Object.keys(SECTION_PRESENCE_BUNDLE_KEYS).filter(key
// => SECTION_PRESENCE_BUNDLE_KEYS[key].some(k => k in b))`.
func sectionsPresent(b map[string]any) []string {
	out := []string{}
	for _, name := range sectionOrder {
		for _, key := range sectionPresenceBundleKeys[name] {
			if _, ok := b[key]; ok {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// applyRestore ports the real (non-dry-run) restore transaction — see this
// file's header comment for the atomicity caveat.
func (h *Handlers) applyRestore(p restorePlan) error {
	d := h.deps
	if p.wantsShots {
		if err := d.ShotsRepo.WipeAll(); err != nil {
			return err
		}
		shotsArr, _ := p.b["shots"].([]any)
		var restoredIDs = map[int64]bool{}
		for _, v := range shotsArr {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if err := d.ShotsRepo.Upsert(shots.Shot(m)); err != nil {
				return err
			}
			if id, ok := jsIntStrict(m["id"]); ok {
				restoredIDs[id] = true
			}
		}
		if ann, ok := p.b["annotations"].(map[string]any); ok {
			for idStr, raw := range ann {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if issues := shots.ValidateAnnotation(m); len(issues) > 0 {
					continue
				}
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err != nil {
					continue
				}
				if err := d.ShotsRepo.SaveAnnotation(id, m); err != nil {
					return err
				}
			}
		}
		if trash, ok := p.b["trash"].(map[string]any); ok {
			for idStr, deletedAtRaw := range trash {
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err != nil || !restoredIDs[id] {
					continue
				}
				deletedAt, ok := jsFiniteNumber(deletedAtRaw)
				if !ok {
					deletedAt = float64(nowMillis())
				}
				if err := d.ShotsRepo.SetTrashEntry(id, int64(deletedAt)); err != nil {
					return err
				}
			}
		}
		if p.sanitizedLib != nil {
			lib := mapToLibrary(p.sanitizedLib)
			if err := d.LibRepo.SaveLibrary(lib); err != nil {
				return err
			}
		}
		if arr, ok := p.b["blocklist"].([]any); ok {
			list := make([]string, 0, len(arr))
			for _, v := range arr {
				if n, ok := jsFiniteNumber(v); ok {
					list = append(list, formatBlocklistValue(n))
				} else if s, ok := v.(string); ok {
					list = append(list, s)
				}
			}
			if err := d.ShotsRepo.SaveBlocklist(list); err != nil {
				return err
			}
		}
	}

	if p.sec.has("maintenance") {
		if _, ok := p.b["maintenance"].([]any); ok {
			if err := d.MaintenanceRepo.RestoreMaintenanceRaw(p.validMaintenance); err != nil {
				return err
			}
		}
		if _, ok := p.b["maintenance_log"].([]any); ok {
			if err := d.MaintenanceRepo.RestoreMaintenanceLogRaw(p.validMaintenanceLog); err != nil {
				return err
			}
		}
	}

	if p.sec.has("orders") {
		if _, ok := p.b["orders"].([]any); ok {
			if err := d.OrdersRepo.ReplaceAll(p.validOrders); err != nil {
				return err
			}
		}
	}

	if p.wantsMachines {
		if _, err := d.Registry.RestoreMachines(p.restoredMachines); err != nil {
			return err
		}
	}

	if p.wantsSettings {
		if kv, ok := p.b["kv"].(map[string]any); ok {
			if err := h.applyKVSettings(kv); err != nil {
				return err
			}
		}
	}

	if mqtt, ok := p.decryptedSecrets["mqtt"].(map[string]any); ok {
		username, _ := mqtt["username"].(string)
		password, _ := mqtt["password"].(string)
		if err := saveMqttSettings(d.DB, map[string]any{
			"username": truncateRunes(username, 200), "password": truncateRunes(password, 500),
		}); err != nil {
			return err
		}
	}

	return nil
}

func formatBlocklistValue(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

func (h *Handlers) applyKVSettings(kv map[string]any) error {
	d := h.deps
	if arr, ok := kv["menu"].([]any); ok {
		menu := make([]orders.MenuItem, 0, len(arr))
		for _, v := range arr {
			if m, ok := v.(map[string]any); ok {
				menu = append(menu, orders.MenuItem(m))
			}
		}
		if err := d.OrdersRepo.SaveMenu(menu); err != nil {
			return err
		}
	}
	if s, ok := kv["orders_settings"].(map[string]any); ok {
		if err := d.OrdersRepo.SaveSettings(orders.Settings(s)); err != nil {
			return err
		}
	}
	if m, ok := kv["notify_mapping"].(map[string]any); ok {
		mapping := orders.NotifyMapping{}
		for k, v := range m {
			if s, ok := v.(string); ok {
				mapping[k] = s
			}
		}
		if err := d.OrdersRepo.SaveNotifyMapping(mapping); err != nil {
			return err
		}
	}
	if s, ok := kv["import_settings"].(map[string]any); ok {
		if err := saveImportSettings(d.DB, s); err != nil {
			return err
		}
	}
	if s, ok := kv["mqtt_settings"].(map[string]any); ok {
		rest := map[string]any{}
		for k, v := range s {
			if k == "username" || k == "password" {
				continue
			}
			rest[k] = v
		}
		if err := saveMqttSettings(d.DB, rest); err != nil {
			return err
		}
	}
	return nil
}

// mapToLibrary converts a generic decoded coffee_library object into a
// typed library.Library — see restore.go's header comment on reDecode for
// why a typed destination is used here, and library.Entity's `= map[string]
// any` alias definition (library/model.go) for why the []any -> []Entity
// element conversion below needs no per-element type conversion syntax.
// mapToLibrary converts the generic decoded coffee_library object into a
// typed library.Library, THEN re-sanitizes every entity's fields via
// SanitizeLibraryForRestore — routes/backup.js's sanitizeRestoredLibrary()
// call, which must run on every restored library regardless of section
// scope, since a restored library bypasses the regular POST/PUT bean/
// grinder/recipe routes entirely (see restore_sanitize.go's doc comment).
func mapToLibrary(m map[string]any) library.Library {
	raw := library.Library{
		Beans: entityList(m["beans"]), Grinders: entityList(m["grinders"]),
		Recipes: entityList(m["recipes"]), Milks: entityList(m["milks"]),
		Baskets: entityList(m["baskets"]), PuckScreens: entityList(m["puckScreens"]),
	}
	return library.SanitizeLibraryForRestore(raw)
}

// entityList converts a generic decoded JSON array (`[]any` of
// `map[string]any` elements) into `[]library.Entity` — no per-element
// conversion needed since `library.Entity = map[string]any` is a type
// alias, not a distinct named type.
func entityList(v any) []library.Entity {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]library.Entity, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// applyRestoredToken persists a restored API token to disk. See
// Dependencies.Token's doc comment: this does NOT take effect in the
// already-running process (internal/auth.RequireToken closes over a fixed
// token string at startup) until the process restarts — a documented,
// deliberate gap from Node's live state.apiToken.
func (h *Handlers) applyRestoredToken(token string) {
	if token == "" {
		return
	}
	tmp := h.deps.TokenFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, h.deps.TokenFile)
}

func (h *Handlers) writePendingImages(pending []pendingImageWrite) {
	for _, w := range pending {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			continue
		}
		_ = os.WriteFile(w.path, w.data, 0o644)
	}
}
