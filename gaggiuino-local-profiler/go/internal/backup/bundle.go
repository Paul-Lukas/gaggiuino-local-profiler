package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// This file ports routes/backup.js's gatherBackupData/buildBackupBundleJson/
// buildBackupZip: the export side of the backup domain.

// glpVersion mirrors lib/constants.js's GLP_VERSION. Not read from the
// same source Node's package.json-derived constant is (no Go equivalent
// exists in this rewrite) — hardcoded to the version this Go port targets
// parity with. Update alongside lib/constants.js's own GLP_VERSION bumps.
const glpVersion = "2.35.0"

// imageFile is one file read from BEAN_IMAGE_DIR — filename plus raw bytes.
type imageFile struct {
	filename string
	data     []byte
}

// gatheredBundle is gatherBackupData's return shape: bundle (the JSON
// object, minus embedded image bytes), imageFiles, and whether images were
// actually requested by the caller's section scope.
type gatheredBundle struct {
	bundle          map[string]any
	imageFiles      []imageFile
	imagesRequested bool
}

func (d Dependencies) gatherBackupData(passphrase string, sec sections) (gatheredBundle, error) {
	allShots, err := d.ShotsRepo.FindAll()
	if err != nil {
		return gatheredBundle{}, err
	}
	trashedShots, err := d.ShotsRepo.FindTrashed()
	if err != nil {
		return gatheredBundle{}, err
	}

	strippedShots := make([]map[string]any, len(allShots))
	annotations := map[string]any{}
	for i, s := range allShots {
		rest := map[string]any{}
		for k, v := range s {
			if k == "annotation" || k == "score" {
				continue
			}
			rest[k] = v
		}
		strippedShots[i] = rest
		if ann, ok := s["annotation"].(map[string]any); ok && len(ann) > 0 {
			id := fmt.Sprintf("%v", s["id"])
			annotations[id] = ann
		}
	}

	trashObj := map[string]any{}
	for _, s := range trashedShots {
		id, _ := s["id"].(int64)
		deletedAt, ok, err := d.ShotsRepo.GetTrashEntry(id)
		if err != nil {
			return gatheredBundle{}, err
		}
		if !ok {
			deletedAt = time.Now().UnixMilli()
		}
		trashObj[fmt.Sprintf("%d", id)] = deletedAt
	}

	lib, err := d.LibRepo.GetLibrary()
	if err != nil {
		return gatheredBundle{}, err
	}

	var imageFiles []imageFile
	if entries, err := os.ReadDir(imageDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(imageDir, entry.Name()))
			if err != nil {
				continue // best-effort — one unreadable file must not fail the whole export
			}
			imageFiles = append(imageFiles, imageFile{filename: entry.Name(), data: data})
		}
	}

	safeMqtt, err := getMqttSettings(d.DB)
	if err != nil {
		return gatheredBundle{}, err
	}
	delete(safeMqtt, "username")
	delete(safeMqtt, "password")

	blocklist, err := d.ShotsRepo.GetBlocklist()
	if err != nil {
		return gatheredBundle{}, err
	}
	maintRaw, err := d.MaintenanceRepo.GetAllMaintenanceRaw()
	if err != nil {
		return gatheredBundle{}, err
	}
	maintLogRaw, err := d.MaintenanceRepo.GetAllMaintenanceLogRaw()
	if err != nil {
		return gatheredBundle{}, err
	}
	allOrders, err := d.OrdersRepo.FindAll()
	if err != nil {
		return gatheredBundle{}, err
	}
	allMachines, err := d.Registry.ListMachines()
	if err != nil {
		return gatheredBundle{}, err
	}
	menu, err := d.OrdersRepo.GetMenu()
	if err != nil {
		return gatheredBundle{}, err
	}
	ordersSettings, err := d.OrdersRepo.GetSettings()
	if err != nil {
		return gatheredBundle{}, err
	}
	notifyMapping, err := d.OrdersRepo.GetNotifyMapping()
	if err != nil {
		return gatheredBundle{}, err
	}
	importSettings, err := getImportSettings(d.DB)
	if err != nil {
		return gatheredBundle{}, err
	}

	fullBundle := map[string]any{
		"glp_backup":      true,
		"version":         glpVersion,
		"created":         time.Now().UTC().Format(time.RFC3339Nano),
		"shots":           strippedShots,
		"annotations":     annotations,
		"coffee_library":  lib,
		"blocklist":       blocklist,
		"trash":           trashObj,
		"maintenance":     maintRaw,
		"maintenance_log": maintLogRaw,
		"orders":          allOrders,
		"machines":        allMachines,
		"kv": map[string]any{
			"menu": menu, "orders_settings": ordersSettings, "notify_mapping": notifyMapping,
			"import_settings": importSettings, "mqtt_settings": safeMqtt,
		},
	}

	if passphrase != "" {
		rawMqtt, err := getMqttSettings(d.DB)
		if err != nil {
			return gatheredBundle{}, err
		}
		secretPayload := map[string]any{}
		if d.Token != "" {
			secretPayload["apiToken"] = d.Token
		}
		username, _ := rawMqtt["username"].(string)
		password, _ := rawMqtt["password"].(string)
		if username != "" || password != "" {
			secretPayload["mqtt"] = map[string]any{"username": username, "password": password}
		}
		if len(secretPayload) > 0 {
			enc, err := EncryptSecrets(secretPayload, passphrase)
			if err != nil {
				return gatheredBundle{}, err
			}
			fullBundle["secrets"] = enc
		}
	}

	imagesRequested := sec == nil || sec.has("shots")

	if sec == nil {
		return gatheredBundle{bundle: fullBundle, imageFiles: imageFiles, imagesRequested: imagesRequested}, nil
	}

	scoped := map[string]any{
		"glp_backup": true, "version": fullBundle["version"], "created": fullBundle["created"],
		"sections": sec.orderedNames(),
	}
	for section := range sec {
		for _, key := range sectionBundleKeys[section] {
			if v, ok := fullBundle[key]; ok {
				scoped[key] = v
			}
		}
	}
	files := imageFiles
	if !imagesRequested {
		files = nil
	}
	return gatheredBundle{bundle: scoped, imageFiles: files, imagesRequested: imagesRequested}, nil
}
