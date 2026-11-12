package librarystore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/library"
	"canonkeeper/backend/internal/projects"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	"canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) FenceBookDeletion(ctx context.Context, input library.FenceBookDeletionInput) (deletion.SourceFenceResult, error) {
	request := deletion.SourceDeleteRequest{
		TombstoneID: input.TombstoneID,
		ActorUserID: input.ActorUserID,
		ProjectID:   input.ProjectID,
		EntityKind:  deletion.EntityBook,
		EntityID:    input.BookID,
		FencedAt:    input.DeletedAt.UTC().Truncate(time.Microsecond),
	}
	request.GovernedWindow = &deletion.GovernedSourceDeleteWindow{
		PurgeAfter:           request.FencedAt.Add(deletion.SourceRestoreWindow),
		DerivedPurgeDeadline: request.FencedAt.Add(deletion.DerivedPurgeDeadline),
		SourcePurgeDeadline:  request.FencedAt.Add(deletion.SourceRestoreWindow + deletion.SourceHardPurgeGrace),
	}
	return s.fenceBookDeletion(ctx, request)
}

func (s *Store) fenceBookDeletion(ctx context.Context, request deletion.SourceDeleteRequest) (deletion.SourceFenceResult, error) {
	if err := request.Validate(); err != nil || request.EntityKind != deletion.EntityBook {
		return deletion.SourceFenceResult{}, library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(request.ProjectID)
	if err != nil {
		return deletion.SourceFenceResult{}, library.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(request.EntityID)
	if err != nil {
		return deletion.SourceFenceResult{}, library.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(request.ActorUserID)
	if err != nil {
		return deletion.SourceFenceResult{}, library.ErrNotFound
	}
	tombstoneID, err := storedb.ParseUUID(request.TombstoneID)
	if err != nil {
		return deletion.SourceFenceResult{}, library.ErrValidation
	}

	var result deletion.SourceFenceResult
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var projectGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role
			FROM project_memberships
			WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
			FOR UPDATE
		`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if role != string(projects.RoleEditor) && role != string(projects.RoleAdmin) && role != string(projects.RoleOwner) {
			return library.ErrForbidden
		}
		if err := ensureNoActiveImportQueueReservation(ctx, tx, projectID); err != nil {
			return err
		}

		var oldBookGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM books
			WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, bookID, projectID).Scan(&oldBookGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("book_after_row_lock"); err != nil {
			return err
		}
		revisions, err := bumpSourceViewsForBook(ctx, tx, projectID, projectGeneration, bookID, oldBookGeneration)
		if err != nil {
			return err
		}
		for _, revision := range revisions {
			if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, refreshTextIndexIntent); err != nil {
				return err
			}
		}

		newBookGeneration := oldBookGeneration + 1
		window := request.GovernedWindow
		if err := tx.QueryRow(ctx, `
			UPDATE books
			SET deleted_at = $3,
			    governance_retention_started_at = $3,
			    purge_after = $4,
			    purge_deadline = $5,
			    lifecycle_generation = $6,
			    revision = revision + 1,
			    updated_at = $3
			WHERE id = $1 AND project_id = $2
			  AND deleted_at IS NULL AND lifecycle_generation = $7
			RETURNING lifecycle_generation
		`, bookID, projectID, request.FencedAt, window.PurgeAfter, window.SourcePurgeDeadline, newBookGeneration, oldBookGeneration).Scan(&newBookGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("book_after_source_update"); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, book_id, book_generation,
				status, fenced_at, purge_deadline, created_at, updated_at
			) VALUES (
				$1, $2, $3, 'book', 'derived_generation', $4,
				$5, $4, $6, 'pending', $7, $8, $7, $7
			)
		`, tombstoneID, projectID, "governed_online", bookID, projectGeneration, oldBookGeneration, request.FencedAt, request.GovernedWindow.DerivedPurgeDeadline); err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("book_after_tombstone_insert"); err != nil {
			return err
		}

		for _, target := range deletion.SourceDeletionTargets() {
			if _, err := tx.Exec(ctx, `
				INSERT INTO deletion_target_tasks (
					tombstone_id, project_id, target, state, attempts,
					claim_incarnation, next_attempt_at
				) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
			`, tombstoneID, projectID, string(target), request.FencedAt); err != nil {
				return err
			}
			if err := s.runDeletionFailpoint("book_after_target_" + string(target)); err != nil {
				return err
			}
		}
		if err := deletionstore.InsertPostgresStepRegistry(
			ctx,
			tx,
			tombstoneID.String(),
			projectID.String(),
			governance.DeletionSelectionBook,
			governance.PurgeModeDerivedGeneration,
		); err != nil {
			return err
		}

		// The work on the book's text stops with it, the import of the book too (deletion-stops-processing UC1).
		if err := s.jobs.StopDeletedSourceWorkInTx(ctx, tx, jobstore.DeletedSourceWork{
			ProjectID: request.ProjectID, ProjectGeneration: projectGeneration,
			Views: revisionViews(revisions), BookID: request.EntityID, At: request.FencedAt,
		}); err != nil {
			return err
		}
		deletedVector := governance.SourceGenerationVector{
			ProjectID:         request.ProjectID,
			ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			BookID:            request.EntityID,
			BookGeneration:    governance.LifecycleGeneration(oldBookGeneration),
		}
		if err := s.canon.RevokeExactSourceArtifactOwnersAtFence(
			ctx, tx, governance.DeletionSelectionBook, deletedVector, request.FencedAt,
		); err != nil {
			return err
		}
		if err := s.canon.ScrubCanonicalDerivedSourceInTx(
			ctx, tx, governance.DeletionSelectionBook, deletedVector, request.FencedAt,
		); err != nil {
			return err
		}
		result = deletion.SourceFenceResult{
			TombstoneID: request.TombstoneID,
			Deleted:     deletedVector,
			Current: governance.SourceGenerationVector{
				ProjectID:         request.ProjectID,
				ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
				BookID:            request.EntityID,
				BookGeneration:    governance.LifecycleGeneration(newBookGeneration),
			},
		}
		return result.Validate()
	})
	if err != nil {
		return deletion.SourceFenceResult{}, err
	}
	return result, nil
}

func (s *Store) RestoreBook(ctx context.Context, input library.RestoreBookStoreInput) (library.Book, error) {
	request := deletion.SourceRestoreRequest{
		ActorUserID: input.ActorUserID,
		ProjectID:   input.ProjectID,
		EntityKind:  deletion.EntityBook,
		EntityID:    input.BookID,
	}
	if err := request.Validate(); err != nil {
		return library.Book{}, library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return library.Book{}, library.ErrNotFound
	}
	var restored library.Book
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var projectGeneration int64
		var projectDeletedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, deleted_at
			FROM projects
			WHERE id = $1
			FOR UPDATE
		`, projectID).Scan(&projectGeneration, &projectDeletedAt); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}

		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role
			FROM project_memberships
			WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
			FOR UPDATE
		`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var bookGeneration int64
		var bookOrder int
		var purgeAfter, hardPurgeStartedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, "order", purge_after, hard_purge_started_at
			FROM books
			WHERE id = $1 AND project_id = $2 AND deleted_at IS NOT NULL
			FOR UPDATE
		`, bookID, projectID).Scan(&bookGeneration, &bookOrder, &purgeAfter, &hardPurgeStartedAt); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if role != string(projects.RoleEditor) && role != string(projects.RoleAdmin) && role != string(projects.RoleOwner) {
			return library.ErrForbidden
		}
		if err := ensureNoActiveImportQueueReservation(ctx, tx, projectID); err != nil {
			return err
		}
		if projectDeletedAt.Valid {
			return library.ErrConflict
		}
		if !purgeAfter.Valid || hardPurgeStartedAt.Valid {
			return library.ErrConflict
		}

		var beforePurge bool
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp() < $1`, purgeAfter).Scan(&beforePurge); err != nil {
			return err
		}
		if !beforePurge {
			return library.ErrConflict
		}
		// The book comes back to its place on the shelf; a book that took it meanwhile, and the books after it, step one
		// place on. The keys of the chapters do not depend on the places (specs/2026-11-11-ux7-reorder.md RK1.1).
		var orderInUse bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM books
				WHERE project_id = $1 AND "order" = $2 AND deleted_at IS NULL AND id <> $3
			)
		`, projectID, bookOrder, bookID).Scan(&orderInUse); err != nil {
			return err
		}
		if orderInUse {
			if err := shiftBooksForInsert(ctx, tx, projectID, bookOrder, time.Now()); err != nil {
				return err
			}
		}

		var restoredGeneration int64
		if err := tx.QueryRow(ctx, `
			UPDATE books
			SET deleted_at = NULL,
			    governance_retention_started_at = NULL,
			    purge_after = NULL,
			    purge_deadline = NULL,
			    revision = revision + 1,
			    updated_at = clock_timestamp()
			WHERE id = $1 AND project_id = $2
			  AND deleted_at IS NOT NULL
			  AND lifecycle_generation = $3
			  AND hard_purge_started_at IS NULL
			  AND clock_timestamp() < purge_after
			RETURNING lifecycle_generation
		`, bookID, projectID, bookGeneration).Scan(&restoredGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrConflict
		} else if err != nil {
			return err
		}
		if restoredGeneration != bookGeneration {
			return library.ErrConflict
		}
		if err := s.quota.CheckAdmittedSourcesInTx(ctx, tx, projectID.String(), quota.MeasureBooks|quota.MeasureChapters|quota.MeasureCharacters); err != nil {
			return err
		}
		if err := s.RestoreProjectSourceCatalog(ctx, tx, projectID, projectGeneration); err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("book_restore_after_source_update"); err != nil {
			return err
		}
		row, err := sqlc.New(tx).GetBookSummaryByID(ctx, sqlc.GetBookSummaryByIDParams{ProjectID: projectID, BookID: bookID})
		if err != nil {
			return err
		}
		restored = libraryBookFromGetSummarySQL(row)
		placed := []library.Book{restored}
		if err := placeBooks(ctx, tx, projectID, placed); err != nil {
			return err
		}
		restored = placed[0]
		return nil
	})
	if err != nil {
		return library.Book{}, err
	}
	return restored, nil
}

func (s *Store) runDeletionFailpoint(point string) error {
	if s.deletionFailpoint == nil {
		return nil
	}
	return s.deletionFailpoint(point)
}
