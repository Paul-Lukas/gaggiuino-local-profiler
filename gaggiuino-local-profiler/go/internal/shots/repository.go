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

// FindTrashed ports ShotRepository.js's getTrash() paired with
// ShotService.getTrash()'s per-id findById hydration — but as one joined
// query instead of a TrashIDs()-then-FindByID(id)-per-id round trip: the
// naive port issued 1+N queries (one to list trash ids, one more per id,
// each re-running selectBase's shots<->annotations join), which scales
// linearly with trash size. Driving the join FROM trash instead of shots
// keeps the same "only rows with a live shots record" semantics
// ShotService.getTrash()'s `.filter(Boolean)` had (an INNER JOIN silently
// drops a trash entry whose shot row is somehow already gone, same as a nil
// FindByID result did), and ordering by t.shot_id makes the result
// deterministic (trash's shot_id is its INTEGER PRIMARY KEY, so this matches
// the rowid-order SQLite returned for the old unordered `SELECT shot_id FROM
// trash` in practice).
func (r *Repository) FindTrashed() ([]Shot, error) {
	rows, err := r.db.Query(`
		SELECT s.id, s.timestamp, s.duration, s.profile_name, s.data, s.machine_id, a.data AS ann_data
		FROM trash t
		JOIN shots s ON s.id = t.shot_id
		LEFT JOIN annotations a ON a.shot_id = s.id
		ORDER BY t.shot_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("shots: listing trash: %w", err)
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

// AppendToBlocklist atomically adds a single value to the blocklist without
// the read-then-replace round trip SaveBlocklist requires for a
// single-id add. Node's saveBlocklist(list) has no concurrency issue
// (single-threaded event loop, so a route handler's read-modify-write
// always runs to completion before the next request starts), but Go's
// handlers run concurrently: two overlapping DELETE /api/shots/{id}/delete
// requests can each read the same blocklist snapshot via GetBlocklist,
// append their own id, and then SaveBlocklist — whose DELETE+re-INSERT
// replaces the whole table — so the second write silently drops the first
// request's id (#901). blocklist.value has a UNIQUE constraint (see
// internal/db/db.go), so INSERT OR IGNORE is a single atomic statement with
// no read step and therefore no lost-update window.
func (r *Repository) AppendToBlocklist(value string) error {
	if _, err := r.db.Exec(`INSERT OR IGNORE INTO blocklist (value) VALUES (?)`, value); err != nil {
		return fmt.Errorf("shots: appending blocklist entry %q: %w", value, err)
	}
	return nil
}
