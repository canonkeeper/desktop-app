// SPDX-License-Identifier: AGPL-3.0-only

// Package readingconsent keeps the reading of the books of a saga by the author's own AI in PostgreSQL
// (specs/2026-11-13-desk-consent.md): the consent of a saga with its count, the journal of the requests of the reading
// written with the count in one transaction, the sample Jobs, the chapters that did not read, and the reads the estimate
// and the status need.
package readingconsent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	layerdomain "canonkeeper/backend/internal/domain/sagalayer"
	service "canonkeeper/backend/internal/services/readingconsent"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Repository is the store of the reading of the books.
type Repository struct {
	db      *storedb.DB
	samples SampleQueue
}

func New(db *storedb.DB) *Repository {
	if db == nil {
		panic("reading consent: the database is required")
	}
	return &Repository{db: db}
}

// Owned says the author owns the live saga; any other is not found (CN1.3, CN3.2).
func (repository *Repository) Owned(ctx context.Context, actorID, projectID string) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	actor, err := storedb.ParseUUID(actorID)
	if err != nil {
		return service.ErrNotFound
	}
	var owned bool
	err = repository.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND owner_user_id = $2 AND deleted_at IS NULL)`,
		project, actor).Scan(&owned)
	if err != nil {
		return err
	}
	if !owned {
		return service.ErrNotFound
	}
	return nil
}

// ChapterRevision names a chapter by the revision of its text.
type ChapterRevision struct {
	ID       string
	Revision int64
}

// Characters are the characters of the published text of each chapter at its revision.
func (repository *Repository) Characters(ctx context.Context, projectID string, chapters []ChapterRevision) (map[string]int, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	ids := make([]pgtype.UUID, 0, len(chapters))
	revisions := make([]int64, 0, len(chapters))
	for _, chapter := range chapters {
		id, err := storedb.ParseUUID(chapter.ID)
		if err != nil {
			continue
		}
		ids, revisions = append(ids, id), append(revisions, chapter.Revision)
	}
	characters := make(map[string]int, len(chapters))
	if len(ids) == 0 {
		return characters, nil
	}
	// The paragraphs of each chapter by its revision go by the index (chapter_id, revision, variant, …).
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT wanted.chapter_id::text, coalesce(sum(char_length(paragraph.plain_text)), 0)::bigint
		FROM unnest($2::uuid[], $3::bigint[]) AS wanted(chapter_id, revision)
		JOIN chapters AS chapter ON chapter.id = wanted.chapter_id AND chapter.project_id = $1 AND chapter.deleted_at IS NULL
		JOIN chapter_paragraphs AS paragraph ON paragraph.chapter_id = wanted.chapter_id AND paragraph.revision = wanted.revision
		  AND paragraph.variant = 'published'
		GROUP BY wanted.chapter_id`, project, ids, revisions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count int64
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		characters[id] = int(count)
	}
	return characters, rows.Err()
}

var errNoRows = pgx.ErrNoRows

func noRows(err error) bool { return errors.Is(err, errNoRows) }

// Unread are the chapters of a saga marked «не прочиталась» for the version of the instructions
// (specs/2026-11-13-desk-consent.md CN3.6), with the helper of each mark (specs/2026-11-15-desk-reading-rest.md RR1.5): a mark
// written before it names none.
func (repository *Repository) Unread(ctx context.Context, projectID string, version int) ([]service.Unread, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT chapter_id::text, content_hash, reason, coalesce(helper, '') FROM reading_unread_chapters
		WHERE project_id = $1 AND instructions_version = $2 ORDER BY marked_at, chapter_id`, project, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var unread []service.Unread
	for rows.Next() {
		var chapter service.Unread
		if err := rows.Scan(&chapter.ID, &chapter.Hash, &chapter.Reason, &chapter.Helper); err != nil {
			return nil, err
		}
		unread = append(unread, chapter)
	}
	return unread, rows.Err()
}

// MarkUnread marks a chapter of a live saga for its text and the version of the instructions with why and for which helper.
func (repository *Repository) MarkUnread(ctx context.Context, projectID, chapterID, hash string, version int, reason, helper string, at time.Time) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	chapter, err := storedb.ParseUUID(chapterID)
	if err != nil {
		return service.ErrNotFound
	}
	_, err = repository.db.Pool.Exec(ctx, `
		INSERT INTO reading_unread_chapters (project_id, chapter_id, content_hash, instructions_version, reason, helper, marked_at)
		SELECT project.id, $2, $3, $4, $5, nullif($6, ''), $7 FROM projects AS project WHERE project.id = $1 AND project.deleted_at IS NULL
		ON CONFLICT (project_id, chapter_id, content_hash, instructions_version)
		DO UPDATE SET reason = EXCLUDED.reason, helper = EXCLUDED.helper, marked_at = EXCLUDED.marked_at`,
		project, chapter, hash, version, reason, helper, at)
	return err
}

// RestMarks are the marks of the names and of the threads of a saga, of every text version (RR1.2, RR1.3).
func (repository *Repository) RestMarks(ctx context.Context, projectID string) ([]service.RestMark, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT kind, source_view, coalesce(names_digest, ''), layer_stamps, coalesce(layer_version, 0), reason, helper
		FROM reading_unread_rest WHERE project_id = $1 ORDER BY kind, source_view`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var marks []service.RestMark
	for rows.Next() {
		var mark service.RestMark
		var stamps []byte
		if err := rows.Scan(&mark.Kind, &mark.View, &mark.Names, &stamps, &mark.LayerVersion, &mark.Reason, &mark.Helper); err != nil {
			return nil, err
		}
		if stamps != nil {
			if err := json.Unmarshal(stamps, &mark.Stamps); err != nil {
				return nil, err
			}
		}
		marks = append(marks, mark)
	}
	return marks, rows.Err()
}

// MarkRest marks the names or the threads of a live saga by the text version of the Job (RR1.2, RR1.3, RR1.6): a new mark of
// the same kind and version replaces the old one; a saga that is gone is not marked.
func (repository *Repository) MarkRest(ctx context.Context, projectID string, mark service.RestMark, at time.Time) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	var names, stamps, version any
	switch mark.Kind {
	case service.RestNames:
		names = mark.Names
	case service.RestThreads:
		encoded, err := json.Marshal(append([]layerdomain.Stamp{}, mark.Stamps...))
		if err != nil {
			return err
		}
		stamps, version = encoded, mark.LayerVersion
	}
	_, err = repository.db.Pool.Exec(ctx, `
		INSERT INTO reading_unread_rest (project_id, kind, source_view, names_digest, layer_stamps, layer_version, reason, helper, marked_at)
		SELECT project.id, $2, $3, $4, $5::jsonb, $6, $7, $8, $9 FROM projects AS project WHERE project.id = $1 AND project.deleted_at IS NULL
		ON CONFLICT (project_id, kind, source_view) DO UPDATE SET
		  names_digest = EXCLUDED.names_digest, layer_stamps = EXCLUDED.layer_stamps, layer_version = EXCLUDED.layer_version,
		  reason = EXCLUDED.reason, helper = EXCLUDED.helper, marked_at = EXCLUDED.marked_at`,
		project, mark.Kind, mark.View, names, stamps, version, mark.Reason, mark.Helper, at)
	return err
}

// forgetUnread removes the marks of a saga in a transaction — the chapters, the names and the threads (RR1.7): the author asked
// to read them again, or a new count of a consent began.
func forgetUnread(ctx context.Context, tx pgx.Tx, project pgtype.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM reading_unread_chapters WHERE project_id = $1`, project); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM reading_unread_rest WHERE project_id = $1`, project)
	return err
}

// LiveReadingJobs are the Jobs of the reading of a saga that wait or run: its cards — not the sample — and its layer.
func (repository *Repository) LiveReadingJobs(ctx context.Context, projectID string) ([]string, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT id::text FROM jobs
		WHERE project_id = $1 AND kind IN ('chapter_cards', 'saga_layer') AND status IN ('queued', 'running')
		  AND NOT (kind = 'chapter_cards' AND subject_json ? 'sample')
		ORDER BY created_at`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CardsSince counts the cards of a saga written since a moment: the pace of its reading since the last continuation
// (specs/2026-11-13-desk-consent.md CN3.8).
func (repository *Repository) CardsSince(ctx context.Context, projectID string, since time.Time) (int, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return 0, service.ErrNotFound
	}
	var cards int
	err = repository.db.Pool.QueryRow(ctx, `SELECT count(*) FROM chapter_cards WHERE project_id = $1 AND created_at >= $2`, project, since).Scan(&cards)
	return cards, err
}
