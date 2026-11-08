package projectstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	deletionstore "canonkeeper/backend/internal/repository/postgres/deletion"
	"canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func withDeletionFailpoint(failpoint func(string) error) Option {
	return func(store *Store) {
		store.projectDeletionFailpoint = failpoint
	}
}

func (s *Store) listDeletedProjectsForActor(ctx context.Context, input projects.ListProjectsStoreInput, actorUserID pgtype.UUID) ([]projects.ProjectRecord, error) {
	cursorDeletedAt, cursorID, err := actorCollectionCursorValues(input.HasCursor, input.CursorCreatedAt, input.CursorID)
	if err != nil {
		return nil, err
	}
	var records []projects.ProjectRecord
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT
			  project.id, project.name, project.description,
			  project_counts.book_count, project_counts.chapter_count, project_counts.word_count,
			  project.created_at, project.updated_at,
			  membership.id, membership.project_id, membership.user_id,
			  membership.role, membership.removed_at,
			  membership.created_at, membership.updated_at,
			  project.deleted_at, project.purge_after
			FROM projects AS project
			JOIN project_memberships AS membership ON membership.project_id = project.id
			LEFT JOIN LATERAL (
			  SELECT
			    COUNT(DISTINCT book.id)::integer AS book_count,
			    COUNT(DISTINCT chapter.id)::integer AS chapter_count,
			    COALESCE(SUM(paragraph.word_count), 0)::integer AS word_count
			  FROM books AS book
			  LEFT JOIN chapters AS chapter
			    ON chapter.book_id = book.id AND chapter.deleted_at IS NULL
			  LEFT JOIN chapter_paragraphs AS paragraph
			    ON paragraph.chapter_id = chapter.id
			    AND paragraph.revision = chapter.revision
			    AND paragraph.variant = 'draft'
			  WHERE book.project_id = project.id
			    AND book.deleted_at IS NULL
			) AS project_counts ON true
			WHERE membership.user_id = $1
			  AND membership.removed_at IS NULL
			  AND membership.role IN ('admin', 'owner')
			  AND project.deleted_at IS NOT NULL
			  AND project.governance_retention_started_at IS NOT NULL
			  AND project.purge_after IS NOT NULL
			  AND project.hard_purge_started_at IS NULL
			  AND clock_timestamp() < project.purge_after
			  AND (
			    NOT $2::boolean
			    OR (project.deleted_at, project.id) < ($3::timestamptz, $4::uuid)
			  )
			ORDER BY project.deleted_at DESC, project.id DESC
			LIMIT $5
		`, actorUserID, input.HasCursor, cursorDeletedAt, cursorID, actorCollectionLimit(input.Limit, 50))
		if err != nil {
			return err
		}
		defer rows.Close()

		records = make([]projects.ProjectRecord, 0)
		for rows.Next() {
			var row projectActorCollectionRow
			var deletedAt, restoreUntil pgtype.Timestamptz
			if err := rows.Scan(
				&row.ID,
				&row.Name,
				&row.Description,
				&row.BookCount,
				&row.ChapterCount,
				&row.WordCount,
				&row.CreatedAt,
				&row.UpdatedAt,
				&row.MembershipID,
				&row.MembershipProjectID,
				&row.MembershipUserID,
				&row.MembershipRole,
				&row.MembershipRemovedAt,
				&row.MembershipCreatedAt,
				&row.MembershipUpdatedAt,
				&deletedAt,
				&restoreUntil,
			); err != nil {
				return err
			}
			record := projectRecordFromActorCollection(row)
			deleted := deletedAt.Time
			restore := restoreUntil.Time
			record.DeletedAt = &deleted
			record.RestoreUntil = &restore
			records = append(records, record)
		}
		return rows.Err()
	})
	return records, err
}

func (s *Store) RestoreProject(ctx context.Context, input projects.RestoreProjectStoreInput) (projects.ProjectRecord, error) {
	request := deletion.SourceRestoreRequest{
		ActorUserID: input.ActorUserID,
		ProjectID:   input.ProjectID,
		EntityKind:  deletion.EntityProject,
		EntityID:    input.ProjectID,
	}
	if err := request.Validate(); err != nil {
		return projects.ProjectRecord{}, projects.ErrValidation
	}

	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectRecord{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectRecord{}, projects.ErrForbidden
	}
	var restored projects.ProjectRecord
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var generation pgtype.Int8
		var purgeAfter, hardPurgeStartedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, purge_after, hard_purge_started_at
			FROM projects
			WHERE id = $1 AND deleted_at IS NOT NULL
			FOR UPDATE
		`, projectID).Scan(&generation, &purgeAfter, &hardPurgeStartedAt); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
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
			return projects.ErrNotFound
		} else if err != nil {
			return err
		}
		if role != string(projects.RoleAdmin) && role != string(projects.RoleOwner) {
			return projects.ErrForbidden
		}
		if !generation.Valid || generation.Int64 < 1 || !purgeAfter.Valid || hardPurgeStartedAt.Valid {
			return projects.ErrConflict
		}
		// A project of a deleted account stays deleted: the account's deletion serializes with
		// this lock (specs/2026-10-28-account-data-rights.md, R3.11).
		var ownerDeleted bool
		if err := tx.QueryRow(ctx, `
			SELECT owner.deleted_at IS NOT NULL
			FROM projects AS project
			JOIN users AS owner ON owner.id = project.owner_user_id
			WHERE project.id = $1
			FOR SHARE OF owner
		`, projectID).Scan(&ownerDeleted); err != nil {
			return err
		}
		if ownerDeleted {
			return projects.ErrConflict
		}

		var beforePurge bool
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp() < $1`, purgeAfter).Scan(&beforePurge); err != nil {
			return err
		}
		if !beforePurge {
			return projects.ErrConflict
		}

		var restoredGeneration int64
		if err := tx.QueryRow(ctx, `
			UPDATE projects
			SET deleted_at = NULL,
			    governance_retention_started_at = NULL,
			    purge_after = NULL,
			    purge_deadline = NULL,
			    revision = revision + 1,
			    updated_at = clock_timestamp()
			WHERE id = $1
			  AND deleted_at IS NOT NULL
			  AND lifecycle_generation = $2
			  AND hard_purge_started_at IS NULL
			  AND clock_timestamp() < purge_after
			RETURNING lifecycle_generation
		`, projectID, generation.Int64).Scan(&restoredGeneration); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrConflict
		} else if err != nil {
			return err
		}
		if restoredGeneration != generation.Int64 {
			return projects.ErrConflict
		}
		if err := quotastore.CheckRestoredProjectInTx(ctx, tx, projectID.String()); err != nil {
			return err
		}
		if err := s.library.RestoreProjectSourceCatalog(ctx, tx, projectID, restoredGeneration); err != nil {
			return err
		}
		if err := s.runProjectDeletionFailpoint("restore_after_source_update"); err != nil {
			return err
		}
		row, err := sqlc.New(tx).GetProjectForActor(ctx, sqlc.GetProjectForActorParams{
			ProjectID:   projectID,
			ActorUserID: actorUserID,
		})
		if err != nil {
			return err
		}
		restored = projectRecordFromGetProjectForActor(row)
		return nil
	})
	if err != nil {
		return projects.ProjectRecord{}, err
	}
	return restored, nil
}

func (s *Store) DeleteProject(ctx context.Context, input projects.DeleteProjectStoreInput) error {
	if _, err := storedb.ParseUUID(input.ProjectID); err != nil {
		return projects.ErrNotFound
	}
	if _, err := storedb.ParseUUID(input.ActorUserID); err != nil {
		return err
	}

	fencedAt := input.DeletedAt.UTC().Truncate(time.Microsecond)
	_, err := s.FenceProjectDeletion(ctx, deletion.SourceDeleteRequest{
		TombstoneID: input.TombstoneID,
		ActorUserID: input.ActorUserID,
		ProjectID:   input.ProjectID,
		EntityKind:  deletion.EntityProject,
		EntityID:    input.ProjectID,
		FencedAt:    fencedAt,
		GovernedWindow: &deletion.GovernedSourceDeleteWindow{
			PurgeAfter:           fencedAt.Add(deletion.SourceRestoreWindow),
			DerivedPurgeDeadline: fencedAt.Add(deletion.DerivedPurgeDeadline),
			SourcePurgeDeadline:  fencedAt.Add(deletion.SourceRestoreWindow + deletion.SourceHardPurgeGrace),
		},
	})
	return err
}

func (s *Store) FenceProjectDeletion(ctx context.Context, request deletion.SourceDeleteRequest) (deletion.SourceFenceResult, error) {
	if err := request.Validate(); err != nil || request.EntityKind != deletion.EntityProject || request.EntityID != request.ProjectID {
		return deletion.SourceFenceResult{}, projects.ErrValidation
	}
	projectID, err := storedb.ParseUUID(request.ProjectID)
	if err != nil {
		return deletion.SourceFenceResult{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(request.ActorUserID)
	if err != nil {
		return deletion.SourceFenceResult{}, projects.ErrForbidden
	}
	tombstoneID, err := storedb.ParseUUID(request.TombstoneID)
	if err != nil {
		return deletion.SourceFenceResult{}, projects.ErrValidation
	}

	var result deletion.SourceFenceResult
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var oldGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, projectID).Scan(&oldGeneration); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
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
			return projects.ErrNotFound
		} else if err != nil {
			return err
		}
		if role != string(projects.RoleAdmin) && role != string(projects.RoleOwner) {
			return projects.ErrForbidden
		}

		if err := s.runProjectDeletionFailpoint("after_row_lock"); err != nil {
			return err
		}

		newGeneration := oldGeneration + 1
		window := request.GovernedWindow
		if err := tx.QueryRow(ctx, `
			UPDATE projects
			SET deleted_at = $2,
			    governance_retention_started_at = $2,
			    purge_after = $3,
			    purge_deadline = $4,
			    lifecycle_generation = $5,
			    revision = revision + 1,
			    updated_at = $2
			WHERE id = $1 AND deleted_at IS NULL AND lifecycle_generation = $6
			RETURNING lifecycle_generation
		`, projectID, request.FencedAt, window.PurgeAfter, window.SourcePurgeDeadline, newGeneration, oldGeneration).Scan(&newGeneration); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrConflict
		} else if err != nil {
			return err
		}
		if err := s.runProjectDeletionFailpoint("after_source_update"); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_tombstones (
				id, project_id, origin, entity_kind, purge_mode, entity_id,
				project_generation, status, fenced_at, purge_deadline,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, 'project', 'derived_generation', $2,
				$4, 'pending', $5, $6, $5, $5
			)
		`, tombstoneID, projectID, "governed_online", oldGeneration, request.FencedAt, request.GovernedWindow.DerivedPurgeDeadline); err != nil {
			return err
		}
		if err := s.runProjectDeletionFailpoint("after_tombstone_insert"); err != nil {
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
			if err := s.runProjectDeletionFailpoint("after_target_" + string(target)); err != nil {
				return err
			}
		}
		if err := deletionstore.InsertPostgresStepRegistry(
			ctx,
			tx,
			tombstoneID.String(),
			projectID.String(),
			governance.DeletionSelectionProject,
			governance.PurgeModeDerivedGeneration,
		); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE project_invitations
			SET status = 'canceled', canceled_at = $2, updated_at = $2
			WHERE project_id = $1 AND status = 'pending'
			  AND accepted_at IS NULL AND canceled_at IS NULL
		`, projectID, request.FencedAt); err != nil {
			return err
		}
		deletedVector := governance.SourceGenerationVector{
			ProjectID:         request.ProjectID,
			ProjectGeneration: governance.LifecycleGeneration(oldGeneration),
		}
		if err := s.canon.RevokeExactSourceArtifactOwnersAtFence(
			ctx, tx, governance.DeletionSelectionProject, deletedVector, request.FencedAt,
		); err != nil {
			return err
		}
		if err := s.canon.ScrubCanonicalDerivedSourceInTx(
			ctx, tx, governance.DeletionSelectionProject, deletedVector, request.FencedAt,
		); err != nil {
			return err
		}
		result = deletion.SourceFenceResult{
			TombstoneID: request.TombstoneID,
			Deleted:     deletedVector,
			Current: governance.SourceGenerationVector{
				ProjectID:         request.ProjectID,
				ProjectGeneration: governance.LifecycleGeneration(newGeneration),
			},
		}
		return result.Validate()
	})
	if err != nil {
		return deletion.SourceFenceResult{}, err
	}
	return result, nil
}

func (s *Store) runProjectDeletionFailpoint(point string) error {
	if s.projectDeletionFailpoint == nil {
		return nil
	}
	return s.projectDeletionFailpoint(point)
}
