package canonstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
)

// openCurrentStoreTestDB поднимает изолированную схему из текущего baseline.
func openCurrentStoreTestDB(t *testing.T) *Store {
	return New(testutil.OpenIsolatedPostgresTestDB(t, "ck_store"))
}

// insertSourceDeletionTombstoneForTest создаёт обязательный tombstone и полный набор целей.
// Canon-тестам этого достаточно: выполнение самих задач удаления проверяют owner-пакеты deletion и retention.
func insertSourceDeletionTombstoneForTest(
	ctx context.Context,
	tx pgx.Tx,
	kind governance.DeletionSelectionKind,
	vector governance.SourceGenerationVector,
	fencedAt time.Time,
) (string, error) {
	tombstoneID := projects.NewSecureIDGenerator().NewDeletionTombstoneID()
	var err error
	switch kind {
	case governance.DeletionSelectionProject:
		_, err = tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, status, fenced_at, purge_deadline, created_at, updated_at
			) VALUES (
				$1, $2, 'governed_online', 'project', 'derived_generation', $2,
				$3, 'pending', $4, $4::timestamptz + interval '24 hours', $4, $4
			)
		`, tombstoneID, vector.ProjectID, vector.ProjectGeneration, fencedAt)
	case governance.DeletionSelectionBook:
		_, err = tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, book_id, book_generation,
				status, fenced_at, purge_deadline, created_at, updated_at
			) VALUES (
				$1, $2, 'governed_online', 'book', 'derived_generation', $3,
				$4, $3, $5, 'pending', $6, $6::timestamptz + interval '24 hours', $6, $6
			)
		`, tombstoneID, vector.ProjectID, vector.BookID,
			vector.ProjectGeneration, vector.BookGeneration, fencedAt)
	case governance.DeletionSelectionChapter:
		_, err = tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, book_id, book_generation, chapter_id, chapter_generation,
				status, fenced_at, purge_deadline, created_at, updated_at
			) VALUES (
				$1, $2, 'governed_online', 'chapter', 'derived_generation', $3,
				$4, $5, $6, $3, $7, 'pending', $8, $8::timestamptz + interval '24 hours', $8, $8
			)
		`, tombstoneID, vector.ProjectID, vector.ChapterID,
			vector.ProjectGeneration, vector.BookID, vector.BookGeneration, vector.ChapterGeneration, fencedAt)
	default:
		return "", fmt.Errorf("unexpected deletion selection %q", kind)
	}
	if err != nil {
		return "", err
	}
	for _, target := range deletion.SourceDeletionTargets() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_target_tasks (
				tombstone_id, project_id, target, state, attempts, claim_incarnation, next_attempt_at
			) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
		`, tombstoneID, vector.ProjectID, string(target), fencedAt); err != nil {
			return "", err
		}
	}
	steps, err := deletion.PostgresStepRegistry(kind, governance.PurgeModeDerivedGeneration)
	if err != nil {
		return "", err
	}
	for _, step := range steps {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_target_step_progress (
				tombstone_id, project_id, target, step_name, step_order,
				cursor_kind, state, processed_count
			) VALUES ($1, $2, 'postgresql', $3, $4, $5, 'pending', 0)
		`, tombstoneID, vector.ProjectID, step.Name, step.Order, string(step.CursorKind)); err != nil {
			return "", err
		}
	}
	return tombstoneID, nil
}
