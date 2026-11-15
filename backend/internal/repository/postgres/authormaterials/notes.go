package authormaterials

import (
	"context"
	"errors"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// The records of the notes of a vault of Obsidian (specs/2026-11-15-desk-obsidian-notes.md ON1.5): the bridge reads and
// writes the records of a saga on its own transaction through these variants of the owner — one name is one record, as
// everywhere; none of them checks who writes, the transaction of the bridge does.

// KeyedRecord is a record of a project as the states of the notes read it: its id, the key of its name and its revision.
type KeyedRecord struct {
	ID       string
	Key      string
	Revision int64
}

// RecordsOfKeysInTx reads the records of the project whose names have the keys or that the ids name, and how many records
// the project keeps, on the transaction of the caller; lock locks the rows it reads until the transaction ends, in the order
// of their ids.
func RecordsOfKeysInTx(ctx context.Context, tx pgx.Tx, projectID string, keys, ids []string, lock bool) ([]KeyedRecord, int, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, 0, domain.ErrNotFound
	}
	query := `
		SELECT id::text, name_key, revision FROM author_materials
		WHERE project_id = $1 AND (name_key = ANY($2::text[]) OR id::text = ANY($3::text[]))
		ORDER BY id`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := tx.Query(ctx, query, project, nonNil(keys), nonNil(ids))
	if err != nil {
		return nil, 0, err
	}
	records, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (KeyedRecord, error) {
		var record KeyedRecord
		err := row.Scan(&record.ID, &record.Key, &record.Revision)
		return record, err
	})
	if err != nil {
		return nil, 0, err
	}
	var total int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM author_materials WHERE project_id = $1`, project).Scan(&total)
	return records, total, err
}

// NoteDraft is a record of a note as the bridge writes it: what it says by the rules of a record, the key of its name and
// the name of its source — the path of the note by the rule of the bridge.
type NoteDraft struct {
	Draft      domain.Draft
	Key        string
	SourceName string
}

// InsertNoteRecordInTx keeps a new record of the source obsidian written by the actor and gives its id and revision; a
// name another record has writes nothing — ok is false —, as a record by hand.
func InsertNoteRecordInTx(ctx context.Context, tx pgx.Tx, projectID, actorUserID string, record NoteDraft) (string, int64, bool, error) {
	project, actor, err := projectAndActor(projectID, actorUserID)
	if err != nil {
		return "", 0, false, err
	}
	properties, err := propertiesJSON(record.Draft.Properties)
	if err != nil {
		return "", 0, false, domain.ErrInvalid
	}
	var id string
	var revision int64
	err = tx.QueryRow(ctx, `
		INSERT INTO author_materials (
		  project_id, name, name_key, kind, description, properties, section, source_name, source_kind, created_by, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $10)
		ON CONFLICT (project_id, name_key) DO NOTHING
		RETURNING id::text, revision
	`, project, record.Draft.Name, record.Key, string(record.Draft.Kind), record.Draft.Description, properties, record.Draft.Section,
		record.SourceName, string(domain.SourceObsidian), actor).Scan(&id, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	return id, revision, err == nil, err
}

// parkedKey is the key a record keeps while the records of a batch change their names: no name has it — a key of a name
// holds no control sign.
func parkedKey(id string) string { return "\x01obsidian:" + id }

// ParkKeyInTx gives a record a key no name has until the end of the transaction, so the records of a batch that exchange
// their names do not meet on one key; the write that follows gives it its own.
func ParkKeyInTx(ctx context.Context, tx pgx.Tx, projectID, id string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return domain.ErrNotFound
	}
	record, err := storedb.ParseUUID(id)
	if err != nil {
		return domain.ErrNotFound
	}
	tag, err := tx.Exec(ctx, `UPDATE author_materials SET name_key = $3 WHERE project_id = $1 AND id = $2`, project, record, parkedKey(id))
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return err
}

// UpdateNoteRecordInTx writes what a note says into a record at the expected revision, with the source obsidian and the
// path of the note, written by the actor, and gives its new revision; another revision is domain.ErrConflict.
func UpdateNoteRecordInTx(ctx context.Context, tx pgx.Tx, projectID, actorUserID, id string, expected int64, record NoteDraft) (int64, error) {
	project, actor, err := projectAndActor(projectID, actorUserID)
	if err != nil {
		return 0, err
	}
	target, err := storedb.ParseUUID(id)
	if err != nil {
		return 0, domain.ErrNotFound
	}
	properties, err := propertiesJSON(record.Draft.Properties)
	if err != nil {
		return 0, domain.ErrInvalid
	}
	var revision int64
	err = tx.QueryRow(ctx, `
		UPDATE author_materials
		SET name = $4, name_key = $5, kind = $6, description = $7, properties = $8::jsonb, section = $9, source_name = $10,
		    source_kind = $11, revision = revision + 1, updated_by = $12, updated_at = clock_timestamp()
		WHERE project_id = $1 AND id = $2 AND revision = $3
		RETURNING revision
	`, project, target, expected, record.Draft.Name, record.Key, string(record.Draft.Kind), record.Draft.Description, properties,
		record.Draft.Section, record.SourceName, string(domain.SourceObsidian), actor).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrConflict
	}
	return revision, err
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
