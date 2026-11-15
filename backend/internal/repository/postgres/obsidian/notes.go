// SPDX-License-Identifier: AGPL-3.0-only

package obsidian

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	domainmaterials "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/obsidian"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/projects"
	materials "canonkeeper/backend/internal/repository/postgres/authormaterials"
	service "canonkeeper/backend/internal/services/obsidian"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// materialsOperation names a load of the records of notes in idempotency_records (specs/2026-11-15-desk-obsidian-notes.md
// ON1.8).
const materialsOperation = "obsidian.materials.v1"

var _ service.Notes = (*Notes)(nil)

// Notes keep the records of the notes of a saga (ON1.5, ON1.9): the links of the records with their notes
// (obsidian_notes), the one transaction of a load and the answers of the loads by their keys.
type Notes struct {
	db *storedb.DB
	// Failpoint stops a load for a test of what a failure or a race leaves — "facts" once the records it depends on are
	// read and locked, "links" once the links of the notes are written, "answer" before the answer of the key is kept, the
	// last write —; nil in a build.
	Failpoint func(point string) error
}

// fail is the failure of a test at the point of a load; nil in a build.
func (store *Notes) fail(point string) error {
	if store.Failpoint == nil {
		return nil
	}
	return store.Failpoint(point)
}

func NewNotes(db *storedb.DB) *Notes {
	if db == nil {
		panic("obsidian notes: the database is required")
	}
	return &Notes{db: db}
}

// Notes are what the states of the records of the notes of a batch depend on in the saga (ON1.2): the records whose names
// the keys are or that the notes are linked with, with their links, and how many records the saga keeps — the same reads a
// load makes again in its transaction with the rows locked.
func (books *Books) Notes(ctx context.Context, projectID string, keys, notes []string) (obsidian.NoteFacts, error) {
	var facts obsidian.NoteFacts
	err := books.db.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		facts, err = noteFactsInTx(ctx, tx, projectID, keys, notes, false)
		return err
	})
	return facts, err
}

// noteFactsInTx reads the facts of the notes on a transaction: the links of the notes, the records of the keys and of those
// links by the owner of the records — lock locks them —, and the links of those records.
func noteFactsInTx(ctx context.Context, tx pgx.Tx, projectID string, keys, notes []string, lock bool) (obsidian.NoteFacts, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return obsidian.NoteFacts{}, service.ErrNotFound
	}
	linked, err := scanLinks(tx.Query(ctx, `
		SELECT note_path, record_id::text, revision, fingerprint FROM obsidian_notes WHERE project_id = $1 AND note_path = ANY($2::text[])
	`, project, nonNil(notes)))
	if err != nil {
		return obsidian.NoteFacts{}, err
	}
	ids := make([]string, 0, len(linked))
	for _, link := range linked {
		ids = append(ids, link.ID)
	}
	records, total, err := materials.RecordsOfKeysInTx(ctx, tx, projectID, keys, ids, lock)
	if err != nil {
		return obsidian.NoteFacts{}, err
	}
	ids = ids[:0]
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	links, err := scanLinks(tx.Query(ctx, `
		SELECT note_path, record_id::text, revision, fingerprint FROM obsidian_notes WHERE project_id = $1 AND record_id::text = ANY($2::text[])
	`, project, ids))
	if err != nil {
		return obsidian.NoteFacts{}, err
	}
	byRecord := map[string]obsidian.HeldRecord{}
	for _, link := range links {
		byRecord[link.ID] = link
	}
	facts := obsidian.NoteFacts{Total: total}
	for _, record := range records {
		link := byRecord[record.ID]
		facts.Records = append(facts.Records, obsidian.HeldRecord{
			ID: record.ID, Key: record.Key, Revision: record.Revision, Note: link.Note, LinkRevision: link.LinkRevision,
			Fingerprint: link.Fingerprint,
		})
	}
	return facts, nil
}

// scanLinks reads rows of links as records named by their ids, with their notes, revisions and fingerprints.
func scanLinks(rows pgx.Rows, err error) ([]obsidian.HeldRecord, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (obsidian.HeldRecord, error) {
		var link obsidian.HeldRecord
		err := row.Scan(&link.Note, &link.ID, &link.LinkRevision, &link.Fingerprint)
		return link, err
	})
}

// MaterialsReplay is the answer kept for the key of a load: nil for none, service.ErrReplayConflict for the key of another
// request.
func (store *Notes) MaterialsReplay(ctx context.Context, key service.ReplayKey) (*service.MaterialsLoaded, error) {
	record, err := sqlc.New(store.db.Pool).GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		ScopeHash: scopeOf(key), OperationID: materialsOperation, IdempotencyKeyHash: hashOf(key.Key),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return keptMaterials(record.RequestHash, record.ResponseJson, key)
}

func keptMaterials(requestHash string, response []byte, key service.ReplayKey) (*service.MaterialsLoaded, error) {
	var loaded service.MaterialsLoaded
	if requestHash != key.Request || json.Unmarshal(response, &loaded) != nil {
		return nil, service.ErrReplayConflict
	}
	return &loaded, nil
}

// WriteNotes is the one transaction of a load of the records of notes (ON1.3–ON1.5, ON1.8): the saga alive and held from
// its deletion, the proof of the session and the role of an editor, the link of the vault held from its removal, the
// answer of the key; then the states of the records again by their exact keys with the rows locked, the plan by what the
// author saw and decided, the records by the owner — the records that change their names out of the way first —, the links
// of the notes with the revisions after the writes, and the answer for the key. A failure of any part leaves nothing.
func (store *Notes) WriteNotes(ctx context.Context, write service.NotesWrite) (service.MaterialsLoaded, error) {
	project, err := storedb.ParseUUID(write.ProjectID)
	if err != nil {
		return service.MaterialsLoaded{}, service.ErrNotFound
	}
	actor, err := storedb.ParseUUID(write.ActorUserID)
	if err != nil {
		return service.MaterialsLoaded{}, service.ErrNotFound
	}
	var loaded service.MaterialsLoaded
	err = store.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := fenceInTx(ctx, tx, project, actor, write.Proof); err != nil {
			return err
		}
		queries := sqlc.New(tx)
		replay, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: scopeOf(write.Key), OperationID: materialsOperation, IdempotencyKeyHash: hashOf(write.Key.Key),
			RequestHash: write.Key.Request, StatusCode: 200, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(time.Now()),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: scopeOf(write.Key), OperationID: materialsOperation, IdempotencyKeyHash: hashOf(write.Key.Key),
			})
			if err != nil {
				return err
			}
			answer, err := keptMaterials(kept.RequestHash, kept.ResponseJson, write.Key)
			if err == nil {
				loaded = *answer
			}
			return err
		}
		if err != nil {
			return err
		}
		if loaded, err = store.writeInTx(ctx, tx, project, write); err != nil {
			return err
		}
		response, err := json.Marshal(loaded)
		if err != nil {
			return err
		}
		if err := store.fail("answer"); err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: replay.ID, StatusCode: 200, ResponseJson: response})
		return err
	})
	return loaded, writeError(err)
}

// fenceInTx holds the saga from its deletion and its link with the vault from its removal for the transaction, and checks
// the proof of the session and the role of an editor or above.
func fenceInTx(ctx context.Context, tx pgx.Tx, project, actor pgtype.UUID, proof auth.SessionWriteProof) error {
	if err := tx.QueryRow(ctx, `SELECT id FROM projects WHERE id = $1 AND deleted_at IS NULL FOR SHARE`, project).Scan(new(any)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.ErrNotFound
		}
		return err
	}
	if _, err := authstore.LockSessionWriteProof(ctx, tx, actor, proof); err != nil {
		return err
	}
	role, err := sqlc.New(tx).GetActiveProjectRoleForActor(ctx, sqlc.GetActiveProjectRoleForActorParams{ActorUserID: actor, ProjectID: project})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return service.ErrNotFound
	case err != nil:
		return err
	case !projects.CanMutateProject(projects.Role(role)):
		return service.ErrForbidden
	}
	if err := tx.QueryRow(ctx, `SELECT project_id FROM obsidian_vaults WHERE project_id = $1 FOR SHARE`, project).Scan(new(any)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.ErrNotLinked
		}
		return err
	}
	return nil
}

// writeInTx writes the records of the load and the links of their notes and gives its answer.
func (store *Notes) writeInTx(ctx context.Context, tx pgx.Tx, project pgtype.UUID, write service.NotesWrite) (service.MaterialsLoaded, error) {
	keys := make([]string, len(write.Records))
	paths := make([]string, len(write.Records))
	for index, record := range write.Records {
		keys[index], paths[index] = sagamemory.GlossaryKey(record.Draft.Name), record.Path
	}
	facts, err := noteFactsInTx(ctx, tx, write.ProjectID, keys, paths, true)
	if err != nil {
		return service.MaterialsLoaded{}, err
	}
	if err := store.fail("facts"); err != nil {
		return service.MaterialsLoaded{}, err
	}
	states := obsidian.NoteStates(write.Records, facts, write.Book, write.Seen)
	outcomes := obsidian.PlanNotes(write.Records, states, write.Seen, write.Decisions)
	creates := 0
	for _, outcome := range outcomes {
		if outcome == obsidian.OutcomeCreated {
			creates++
		}
	}
	if err := obsidian.CheckRecords(len(write.Records), facts.Total, creates); err != nil {
		return service.MaterialsLoaded{}, err
	}
	current := map[string]string{}
	for _, held := range facts.Records {
		current[held.ID] = held.Key
	}
	// The records that change their names leave them first: two records that exchange names never meet on one key.
	for index, outcome := range outcomes {
		if writesOver(outcome) && current[states[index].Target] != keys[index] {
			if err := materials.ParkKeyInTx(ctx, tx, write.ProjectID, states[index].Target); err != nil {
				return service.MaterialsLoaded{}, err
			}
		}
	}
	targets := make([]string, len(outcomes))
	revisions := make([]int64, len(outcomes))
	for index, outcome := range outcomes {
		record := materials.NoteDraft{Draft: write.Records[index].Draft, Key: keys[index], SourceName: obsidian.NoteSource(write.Records[index].Path)}
		switch {
		case writesOver(outcome):
			targets[index] = states[index].Target
			if revisions[index], err = materials.UpdateNoteRecordInTx(ctx, tx, write.ProjectID, write.ActorUserID, targets[index], states[index].Revision, record); err != nil {
				return service.MaterialsLoaded{}, err
			}
		case outcome == obsidian.OutcomeCreated:
			id, revision, ok, err := materials.InsertNoteRecordInTx(ctx, tx, write.ProjectID, write.ActorUserID, record)
			if err != nil {
				return service.MaterialsLoaded{}, err
			}
			if !ok {
				// The author gave a record the name since the facts were read: it is his, as a change since the preview.
				outcomes[index] = obsidian.OutcomeKeptChanged
				continue
			}
			targets[index], revisions[index] = id, revision
		}
	}
	if err := store.linkNotesInTx(ctx, tx, project, write.Records, targets, revisions); err != nil {
		return service.MaterialsLoaded{}, err
	}
	loaded := service.MaterialsLoaded{Skipped: write.Skipped}
	for index, outcome := range outcomes {
		loaded.Add(outcome, write.Records[index].Draft.Name)
	}
	return loaded, nil
}

// writesOver says an outcome writes over a record the saga keeps.
func writesOver(outcome obsidian.NoteOutcome) bool {
	return outcome == obsidian.OutcomeUpdated || outcome == obsidian.OutcomeReplaced
}

// linkNotesInTx links each written record with its note (ON1.9): the link the note had with another record and the link
// the record had with another note go — one record is one note —, and the new one keeps the revision after the write and
// the fingerprint of the record of the note.
func (store *Notes) linkNotesInTx(ctx context.Context, tx pgx.Tx, project pgtype.UUID, notes []obsidian.Record, targets []string, revisions []int64) error {
	var paths, records []string
	for index, target := range targets {
		if target != "" {
			paths, records = append(paths, notes[index].Path), append(records, target)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM obsidian_notes WHERE project_id = $1 AND (note_path = ANY($2::text[]) OR record_id::text = ANY($3::text[]))
	`, project, paths, records); err != nil {
		return err
	}
	for index, target := range targets {
		if target == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO obsidian_notes (project_id, note_path, record_id, revision, fingerprint) VALUES ($1, $2, $3::uuid, $4, $5)
		`, project, notes[index].Path, target, revisions[index], obsidian.RecordsFingerprint([]obsidian.Record{notes[index]})); err != nil {
			return err
		}
	}
	return store.fail("links")
}

// writeError is a failure of the transaction as the bridge says it: a name another record took meanwhile, a record changed
// under a write, a deadlock or a failure of serialization the database ended it with is a conflict — the same key repeats
// it —, the other refusals of the owner its own.
func writeError(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "40P01" || pgErr.Code == "40001"),
		errors.Is(err, domainmaterials.ErrConflict):
		return service.ErrConflict
	case errors.Is(err, domainmaterials.ErrNotFound):
		return service.ErrNotFound
	}
	return err
}
