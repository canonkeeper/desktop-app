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

func (s *Store) FenceChapterDeletion(ctx context.Context, input library.FenceChapterDeletionInput) (deletion.SourceFenceResult, error) {
	request := governedChapterDeleteRequest(input.TombstoneID, input.ActorUserID, input.ProjectID, input.ChapterID, input.DeletedAt)
	return s.fenceChapterDeletion(ctx, request, input.BookID)
}

// governedChapterDeleteRequest is the deletion of a chapter the author can restore for 7 days.
func governedChapterDeleteRequest(tombstoneID, actorUserID, projectID, chapterID string, deletedAt time.Time) deletion.SourceDeleteRequest {
	request := deletion.SourceDeleteRequest{
		TombstoneID: tombstoneID,
		ActorUserID: actorUserID,
		ProjectID:   projectID,
		EntityKind:  deletion.EntityChapter,
		EntityID:    chapterID,
		FencedAt:    deletedAt.UTC().Truncate(time.Microsecond),
	}
	request.GovernedWindow = &deletion.GovernedSourceDeleteWindow{
		PurgeAfter:           request.FencedAt.Add(deletion.SourceRestoreWindow),
		DerivedPurgeDeadline: request.FencedAt.Add(deletion.DerivedPurgeDeadline),
		SourcePurgeDeadline:  request.FencedAt.Add(deletion.SourceRestoreWindow + deletion.SourceHardPurgeGrace),
	}
	return request
}

func (s *Store) fenceChapterDeletion(ctx context.Context, request deletion.SourceDeleteRequest, requestedBookID string) (deletion.SourceFenceResult, error) {
	if err := request.Validate(); err != nil || request.EntityKind != deletion.EntityChapter {
		return deletion.SourceFenceResult{}, library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(request.ProjectID)
	if err != nil {
		return deletion.SourceFenceResult{}, library.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(requestedBookID)
	if err != nil {
		return deletion.SourceFenceResult{}, library.ErrNotFound
	}
	chapterID, err := storedb.ParseUUID(request.EntityID)
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
			SELECT lifecycle_generation FROM projects
			WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
		`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role FROM project_memberships
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
		var bookGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation FROM books
			WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
			FOR UPDATE
		`, bookID, projectID).Scan(&bookGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var oldChapterGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation FROM chapters
			WHERE id = $1 AND book_id = $2 AND project_id = $3 AND deleted_at IS NULL
			FOR UPDATE
		`, chapterID, bookID, projectID).Scan(&oldChapterGeneration); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		result, err = s.fenceLockedChapterInTx(
			ctx, tx, request, projectID, bookID, chapterID, tombstoneID,
			projectGeneration, bookGeneration, oldChapterGeneration,
		)
		return err
	})
	if err != nil {
		return deletion.SourceFenceResult{}, err
	}
	return result, nil
}

// fenceLockedChapterInTx deletes a live chapter whose project, book and chapter rows the caller's
// transaction already locked: the chapter, its tombstone, the purge tasks and the revoked owners.
func (s *Store) fenceLockedChapterInTx(
	ctx context.Context,
	tx pgx.Tx,
	request deletion.SourceDeleteRequest,
	projectID, bookID, chapterID, tombstoneID pgtype.UUID,
	projectGeneration, bookGeneration, oldChapterGeneration int64,
) (deletion.SourceFenceResult, error) {
	requestedBookID := bookID.String()
	if err := s.runDeletionFailpoint("chapter_after_row_lock"); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	revisions, err := bumpSourceViewsForChapter(ctx, tx, projectID, projectGeneration, bookID, bookGeneration, chapterID)
	if err != nil {
		return deletion.SourceFenceResult{}, err
	}
	for _, revision := range revisions {
		if err := s.upsertCanonTextIndexIntent(ctx, tx, revision, refreshTextIndexIntent); err != nil {
			return deletion.SourceFenceResult{}, err
		}
	}

	newChapterGeneration := oldChapterGeneration + 1
	window := request.GovernedWindow
	if err := tx.QueryRow(ctx, `
		UPDATE chapters
		SET deleted_at = $4,
		    governance_retention_started_at = $4,
		    purge_after = $5,
		    purge_deadline = $6,
		    lifecycle_generation = $7,
		    updated_at = $4
		WHERE id = $1 AND book_id = $2 AND project_id = $3
		  AND deleted_at IS NULL AND lifecycle_generation = $8
		RETURNING lifecycle_generation
	`, chapterID, bookID, projectID, request.FencedAt, window.PurgeAfter, window.SourcePurgeDeadline, newChapterGeneration, oldChapterGeneration).Scan(&newChapterGeneration); errors.Is(err, pgx.ErrNoRows) {
		return deletion.SourceFenceResult{}, library.ErrConflict
	} else if err != nil {
		return deletion.SourceFenceResult{}, err
	}
	if err := s.runDeletionFailpoint("chapter_after_source_update"); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO deletion_tombstones (
		id, project_id, origin, entity_kind, purge_mode, entity_id,
		project_generation, book_id, book_generation,
		chapter_id, chapter_generation,
		status, fenced_at, purge_deadline, created_at, updated_at
		) VALUES (
		$1, $2, $3, 'chapter', 'derived_generation', $4,
		$5, $6, $7, $4, $8,
		'pending', $9, $10, $9, $9
		)
	`, tombstoneID, projectID, "governed_online", chapterID,
		projectGeneration, bookID, bookGeneration,
		oldChapterGeneration, request.FencedAt, request.GovernedWindow.DerivedPurgeDeadline,
	); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	if err := s.runDeletionFailpoint("chapter_after_tombstone_insert"); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	for _, target := range deletion.SourceDeletionTargets() {
		if _, err := tx.Exec(ctx, `
		INSERT INTO deletion_target_tasks (
			tombstone_id, project_id, target, state, attempts,
			claim_incarnation, next_attempt_at
		) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
		`, tombstoneID, projectID, string(target), request.FencedAt); err != nil {
			return deletion.SourceFenceResult{}, err
		}
		if err := s.runDeletionFailpoint("chapter_after_target_" + string(target)); err != nil {
			return deletion.SourceFenceResult{}, err
		}
	}
	if err := deletionstore.InsertPostgresStepRegistry(
		ctx,
		tx,
		tombstoneID.String(),
		projectID.String(),
		governance.DeletionSelectionChapter,
		governance.PurgeModeDerivedGeneration,
	); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	// The work on the chapter's text versions stops with it (deletion-stops-processing UC1); an update of the book
	// from a file, which may be the caller, pairs the chapters when it ends and goes on.
	if err := s.jobs.StopDeletedSourceWorkInTx(ctx, tx, jobstore.DeletedSourceWork{
		ProjectID: request.ProjectID, ProjectGeneration: projectGeneration,
		Views: revisionViews(revisions), At: request.FencedAt,
	}); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	deletedVector := governance.SourceGenerationVector{
		ProjectID:         request.ProjectID,
		ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		BookID:            requestedBookID,
		BookGeneration:    governance.LifecycleGeneration(bookGeneration),
		ChapterID:         request.EntityID,
		ChapterGeneration: governance.LifecycleGeneration(oldChapterGeneration),
	}
	if err := s.canon.RevokeExactSourceArtifactOwnersAtFence(
		ctx, tx, governance.DeletionSelectionChapter, deletedVector, request.FencedAt,
	); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	if err := s.canon.ScrubCanonicalDerivedSourceInTx(
		ctx, tx, governance.DeletionSelectionChapter, deletedVector, request.FencedAt,
	); err != nil {
		return deletion.SourceFenceResult{}, err
	}
	result := deletion.SourceFenceResult{
		TombstoneID: request.TombstoneID,
		Deleted:     deletedVector,
		Current: governance.SourceGenerationVector{
			ProjectID:         request.ProjectID,
			ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			BookID:            requestedBookID,
			BookGeneration:    governance.LifecycleGeneration(bookGeneration),
			ChapterID:         request.EntityID,
			ChapterGeneration: governance.LifecycleGeneration(newChapterGeneration),
		},
	}
	return result, result.Validate()
}

func (s *Store) RestoreChapter(ctx context.Context, input library.RestoreChapterStoreInput) (library.Chapter, error) {
	request := deletion.SourceRestoreRequest{
		ActorUserID: input.ActorUserID,
		ProjectID:   input.ProjectID,
		EntityKind:  deletion.EntityChapter,
		EntityID:    input.ChapterID,
	}
	if err := request.Validate(); err != nil {
		return library.Chapter{}, library.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	bookID, err := storedb.ParseUUID(input.BookID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	chapterID, err := storedb.ParseUUID(input.ChapterID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return library.Chapter{}, library.ErrNotFound
	}
	var restored library.Chapter
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var projectGeneration int64
		var projectDeletedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, deleted_at FROM projects
			WHERE id = $1 FOR UPDATE
		`, projectID).Scan(&projectGeneration, &projectDeletedAt); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role FROM project_memberships
			WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
			FOR UPDATE
		`, projectID, actorUserID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var bookDeletedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			SELECT deleted_at FROM books
			WHERE id = $1 AND project_id = $2
			FOR UPDATE
		`, bookID, projectID).Scan(&bookDeletedAt); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		var chapterGeneration int64
		var purgeAfter, hardPurgeStartedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, purge_after, hard_purge_started_at
			FROM chapters
			WHERE id = $1 AND book_id = $2 AND project_id = $3 AND deleted_at IS NOT NULL
			FOR UPDATE
		`, chapterID, bookID, projectID).Scan(&chapterGeneration, &purgeAfter, &hardPurgeStartedAt); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		if role != string(projects.RoleEditor) && role != string(projects.RoleAdmin) && role != string(projects.RoleOwner) {
			return library.ErrForbidden
		}
		if projectDeletedAt.Valid || bookDeletedAt.Valid {
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
		if err := restoreLockedChapterInTx(ctx, tx, projectID, bookID, chapterID, chapterGeneration); err != nil {
			return err
		}
		if err := s.quota.CheckAdmittedSourcesInTx(ctx, tx, projectID.String(), quota.MeasureChapters|quota.MeasureCharacters); err != nil {
			return err
		}
		if err := s.RestoreProjectSourceCatalog(ctx, tx, projectID, projectGeneration); err != nil {
			return err
		}
		if err := s.runDeletionFailpoint("chapter_restore_after_source_update"); err != nil {
			return err
		}
		chapter, err := sqlc.New(tx).GetChapterByID(ctx, sqlc.GetChapterByIDParams{ProjectID: projectID, BookID: bookID, ChapterID: chapterID})
		if err != nil {
			return err
		}
		restored = libraryChapterFromSQL(chapter)
		return nil
	})
	if err != nil {
		return library.Chapter{}, err
	}
	return restored, nil
}

// restoreLockedChapterInTx takes a deleted chapter whose rows the caller's transaction locked out of «Недавно удалённые»
// while its 7 days go and its purge has not begun, at the generation it has: «Вернуть» of the trash and the return of a
// book to the version before its update (specs/2026-11-12-ux10-book-revert.md BR2.2). The caller restores the source
// catalog.
func restoreLockedChapterInTx(ctx context.Context, tx pgx.Tx, projectID, bookID, chapterID pgtype.UUID, generation int64) error {
	var restoredGeneration int64
	if err := tx.QueryRow(ctx, `
		UPDATE chapters
		SET deleted_at = NULL,
		    governance_retention_started_at = NULL,
		    purge_after = NULL,
		    purge_deadline = NULL,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND book_id = $2 AND project_id = $3
		  AND deleted_at IS NOT NULL
		  AND lifecycle_generation = $4
		  AND hard_purge_started_at IS NULL
		  AND clock_timestamp() < purge_after
		RETURNING lifecycle_generation
	`, chapterID, bookID, projectID, generation).Scan(&restoredGeneration); errors.Is(err, pgx.ErrNoRows) {
		return library.ErrConflict
	} else if err != nil {
		return err
	}
	if restoredGeneration != generation {
		return library.ErrConflict
	}
	return nil
}
