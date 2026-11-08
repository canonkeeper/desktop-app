package indexingstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

func softDeleteIndexingStoreBookForTest(t *testing.T, db *storedb.DB, bookID string) {
	t.Helper()
	ctx := context.Background()
	deletedAt := time.Now().UTC().Truncate(time.Microsecond)
	tombstoneID := projects.NewSecureIDGenerator().NewDeletionTombstoneID()
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		var projectID string
		var projectGeneration, bookGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT book.project_id::text, project.lifecycle_generation, book.lifecycle_generation
			FROM books AS book
			JOIN projects AS project ON project.id = book.project_id
			WHERE book.id = $1 AND book.deleted_at IS NULL AND project.deleted_at IS NULL
			FOR UPDATE OF project, book
		`, bookID).Scan(&projectID, &projectGeneration, &bookGeneration); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, book_id, book_generation,
				status, fenced_at, purge_deadline
			) VALUES (
				$1, $2, 'governed_online', 'book', 'derived_generation', $3,
				$4, $3, $5, 'pending', $6, $7
			)
		`, tombstoneID, projectID, bookID, projectGeneration, bookGeneration,
			deletedAt, deletedAt.Add(deletion.DerivedPurgeDeadline)); err != nil {
			return err
		}
		for _, target := range deletion.SourceDeletionTargets() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_target_tasks (
					tombstone_id, project_id, target, state, attempts,
					claim_incarnation, next_attempt_at
				) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
			`, tombstoneID, projectID, string(target), deletedAt); err != nil {
				return err
			}
		}
		if err := deletionstore.InsertPostgresStepRegistry(
			ctx, tx, tombstoneID, projectID,
			governance.DeletionSelectionBook, governance.PurgeModeDerivedGeneration,
		); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE books
			SET deleted_at = $1,
			    governance_retention_started_at = $1,
			    purge_after = $1::timestamptz + interval '7 days',
			    purge_deadline = $1::timestamptz + interval '8 days',
			    lifecycle_generation = $2 + 1
			WHERE id = $3 AND lifecycle_generation = $2
		`, deletedAt, bookGeneration, bookID)
		return err
	})
	if err != nil {
		t.Fatalf("soft delete book: %v", err)
	}
}

func TestBookDeletionOwnsChapterSearchRows(t *testing.T) {
	fixture := newStoreFixture(t, 7310, governance.SourceViewDraft)
	indexCurrentCanonTextChaptersForTest(t, context.Background(), fixture)

	var deleteAction string
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT confdeltype
		FROM pg_catalog.pg_constraint AS foreign_key
		WHERE foreign_key.conname = 'chapter_text_search_rows_chapter_id_project_id_fkey'
	`).Scan(&deleteAction); err != nil {
		t.Fatal(err)
	}
	if deleteAction != "c" {
		t.Fatalf("chapter search row delete action = %q, want c (CASCADE)", deleteAction)
	}

	var rows int
	if err := fixture.db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM chapter_text_search_rows WHERE project_id=$1
	`, fixture.projectID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("chapter search rows = %d, want 2", rows)
	}
}
