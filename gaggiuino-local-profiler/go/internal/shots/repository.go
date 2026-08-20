package shots

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Repository ports lib/repositories/ShotRepository.js's DB access, scoped
// to the operations routes/shots.js's endpoints actually need in this
// phase (machineId-scoped variants every pre-existing call site left
// unused there — findAll(machineId), findAllExcludingTrash(machineId),
// upsertMany, getMaxId, count, getAnnotatedDoses, getAllAnnotations,
// getMachineId, getTrashEntry, setTrashEntry — are import/sync/backup-path
// only, so they're deliberately not ported here; add them alongside
// whichever later domain (machines/backup) actually calls them).
type Repository struct {
	db *sql.DB
}

// NewRepository wraps an already-open *sql.DB (see internal/db.Open).
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// FindByID ports ShotRepository.js's findById. Returns (nil, nil) — not an
// error — when no such shot exists, matching _hydrate(undefined) => null.
func (r *Repository) FindByID(id int64) (Shot, error) {
	row := r.db.QueryRow(selectBase+` WHERE s.id = ?`, id)
	shot, err := hydrateRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("shots: finding shot %d: %w", id, err)
	}
	return shot, nil
}

// FindAllExcludingTrash ports ShotRepository.js's findAllExcludingTrash()
// (no machineId — see the type doc comment), ordered by timestamp ASC.
func (r *Repository) FindAllExcludingTrash() ([]Shot, error) {
	rows, err := r.db.Query(selectBase + ` WHERE s.id NOT IN (SELECT shot_id FROM trash) ORDER BY s.timestamp ASC`)
	if err != nil {
		return nil, fmt.Errorf("shots: listing shots: %w", err)
	}
	defer rows.Close()

	var out []Shot
	for rows.Next() {
		shot, err := hydrateRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, shot)
	}
	return out, rows.Err()
}

// TrashIDs ports the id-extraction half of ShotRepository.js's getTrash()
// (`Object.keys(trash).map(Number)`) — ShotService.getTrash() is what pairs
// this with FindByID per id; the deleted_at timestamps getTrash()'s map
// also carries aren't read by that caller, so this returns bare ids.
func (r *Repository) TrashIDs() ([]int64, error) {
	rows, err := r.db.Query(`SELECT shot_id FROM trash`)
	if err != nil {
		return nil, fmt.Errorf("shots: listing trash: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("shots: scanning trash id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FindPreviousByProfile ports ShotRepository.js's findPreviousByProfile
// (#402): the most recent earlier shot before shotID with the same
// profileName on the same machine, excluding trashed shots.
func (r *Repository) FindPreviousByProfile(shotID int64, profileName string, machineID int64) (Shot, error) {
	row := r.db.QueryRow(selectBase+`
		WHERE s.machine_id = ?
		  AND s.profile_name = ?
		  AND s.timestamp < (SELECT timestamp FROM shots WHERE id = ?)
		  AND s.id NOT IN (SELECT shot_id FROM trash)
		ORDER BY s.timestamp DESC
		LIMIT 1
	`, machineID, profileName, shotID)
	shot, err := hydrateRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("shots: finding previous shot for profile %q: %w", profileName, err)
	}
	return shot, nil
}

// SetImage ports ShotRepository.js's setImage: merges the `image` key into
// the shot's JSON blob without disturbing the rest of the payload. Returns
// (nil, nil) if the shot doesn't exist.
func (r *Repository) SetImage(id int64, ext string) (Shot, error) {
	data, err := r.rawData(id)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	data["image"] = ext
	if err := r.writeData(id, data); err != nil {
		return nil, err
	}
	return r.FindByID(id)
}

// ClearImage ports ShotRepository.js's clearImage.
func (r *Repository) ClearImage(id int64) (Shot, error) {
	data, err := r.rawData(id)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	delete(data, "image")
	if err := r.writeData(id, data); err != nil {
		return nil, err
	}
	return r.FindByID(id)
}

func (r *Repository) rawData(id int64) (map[string]any, error) {
	var raw string
	err := r.db.QueryRow(`SELECT data FROM shots WHERE id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("shots: reading data for shot %d: %w", id, err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("shots: decoding data for shot %d: %w", id, err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return data, nil
}

func (r *Repository) writeData(id int64, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("shots: encoding data for shot %d: %w", id, err)
	}
	if _, err := r.db.Exec(`UPDATE shots SET data = ? WHERE id = ?`, string(b), id); err != nil {
		return fmt.Errorf("shots: writing data for shot %d: %w", id, err)
	}
	return nil
}

// SaveAnnotation ports ShotRepository.js's saveAnnotation — an upsert with
// no existence check against `shots` in the query itself, matching the
// Node original. In practice this still fails for a shot id that was never
// synced: annotations.shot_id REFERENCES shots(id) and foreign_keys=ON in
// both InitSchema and lib/db.js, so the INSERT hits a foreign-key
// constraint violation, surfaced as a generic error (500) by the caller —
// see handlers.go's annotate doc comment.
func (r *Repository) SaveAnnotation(shotID int64, annotation map[string]any) error {
	b, err := json.Marshal(annotation)
	if err != nil {
		return fmt.Errorf("shots: encoding annotation for shot %d: %w", shotID, err)
	}
	if _, err := r.db.Exec(`INSERT OR REPLACE INTO annotations (shot_id, data) VALUES (?, ?)`, shotID, string(b)); err != nil {
		return fmt.Errorf("shots: saving annotation for shot %d: %w", shotID, err)
	}
	return nil
}

// MoveToTrash ports ShotRepository.js's moveToTrash.
func (r *Repository) MoveToTrash(shotID int64) error {
	if _, err := r.db.Exec(`INSERT OR REPLACE INTO trash (shot_id, deleted_at) VALUES (?, ?)`, shotID, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("shots: trashing shot %d: %w", shotID, err)
	}
	return nil
}

// RestoreFromTrash ports ShotRepository.js's restoreFromTrash — no
// existence check, matching the Node original.
func (r *Repository) RestoreFromTrash(shotID int64) error {
	if _, err := r.db.Exec(`DELETE FROM trash WHERE shot_id = ?`, shotID); err != nil {
		return fmt.Errorf("shots: restoring shot %d: %w", shotID, err)
	}
	return nil
}

// DeleteByID ports ShotRepository.js's deleteById: annotations, then
// trash, then the shot row itself, inside one transaction.
func (r *Repository) DeleteByID(shotID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("shots: starting delete tx for shot %d: %w", shotID, err)
	}
	if _, err := tx.Exec(`DELETE FROM annotations WHERE shot_id = ?`, shotID); err != nil {
		tx.Rollback()
		return fmt.Errorf("shots: deleting annotation for shot %d: %w", shotID, err)
	}
	if _, err := tx.Exec(`DELETE FROM trash WHERE shot_id = ?`, shotID); err != nil {
		tx.Rollback()
		return fmt.Errorf("shots: deleting trash entry for shot %d: %w", shotID, err)
	}
	if _, err := tx.Exec(`DELETE FROM shots WHERE id = ?`, shotID); err != nil {
		tx.Rollback()
		return fmt.Errorf("shots: deleting shot %d: %w", shotID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("shots: committing delete of shot %d: %w", shotID, err)
	}
	return nil
}

// GetBlocklist ports ShotRepository.js's getBlocklist.
func (r *Repository) GetBlocklist() ([]string, error) {
	rows, err := r.db.Query(`SELECT value FROM blocklist`)
	if err != nil {
		return nil, fmt.Errorf("shots: listing blocklist: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("shots: scanning blocklist entry: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SaveBlocklist ports ShotRepository.js's saveBlocklist: replaces the
// entire table contents inside one transaction.
func (r *Repository) SaveBlocklist(list []string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("shots: starting blocklist save tx: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM blocklist`); err != nil {
		tx.Rollback()
		return fmt.Errorf("shots: clearing blocklist: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO blocklist (value) VALUES (?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("shots: preparing blocklist insert: %w", err)
	}
	defer stmt.Close()
	for _, v := range list {
		if _, err := stmt.Exec(v); err != nil {
			tx.Rollback()
			return fmt.Errorf("shots: inserting blocklist entry %q: %w", v, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("shots: committing blocklist save: %w", err)
	}
	return nil
}
