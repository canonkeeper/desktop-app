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
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) ListProjectMembers(ctx context.Context, input projects.ListProjectMembersStoreInput) ([]projects.ProjectMembershipRecord, error) {
	project, err := s.GetProject(ctx, input.ActorUserID, input.ProjectID)
	if err != nil {
		return nil, err
	}
	if !projects.CanManageMembers(project.CurrentMembership.Role) {
		return nil, projects.ErrForbidden
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return nil, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return nil, err
	}

	rows, err := sqlc.New(s.Pool).ListProjectMembersForActor(ctx, sqlc.ListProjectMembersForActorParams{
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		LimitCount:  projectListLimit(input.Limit),
	})
	if err != nil {
		return nil, err
	}
	members := make([]projects.ProjectMembershipRecord, len(rows))
	for i, row := range rows {
		members[i] = projectMembershipRecordFromListProjectMembersForActor(row)
	}
	return members, nil
}

func (s *Store) UpdateProjectMemberRole(ctx context.Context, input projects.UpdateProjectMemberRoleStoreInput) (projects.ProjectMembershipRecord, error) {
	if input.Role == projects.RoleOwner {
		return projects.ProjectMembershipRecord{}, projects.ErrConflict
	}
	if !projects.CanAssignMemberRole(input.Role) {
		return projects.ProjectMembershipRecord{}, projects.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, err
	}
	memberID, err := storedb.ParseUUID(input.MemberID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, projects.ErrNotFound
	}

	var result projects.ProjectMembershipRecord
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		locked, err := lockProjectMemberMutation(ctx, tx, projectID, actorUserID, memberID, false)
		if err != nil {
			return err
		}
		updatedAt := input.UpdatedAt
		if input.Role == projects.RoleViewer && locked.targetRole == input.Role {
			updatedAt = locked.targetUpdatedAt.Time
		}
		row, err := sqlc.New(tx).UpdateProjectMemberRoleForActor(ctx, sqlc.UpdateProjectMemberRoleForActorParams{
			Role:        string(input.Role),
			UpdatedAt:   storedb.Timestamptz(updatedAt),
			MemberID:    memberID,
			ProjectID:   projectID,
			ActorUserID: actorUserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		}
		if err != nil {
			return err
		}
		result = projectMembershipRecordFromUpdateProjectMemberRoleForActor(row)
		if input.Role == projects.RoleViewer {
			return enqueuePermissionLossDeletion(
				ctx, tx, projectID, locked.targetUserID,
				locked.projectGeneration, result.UpdatedAt,
			)
		}
		return nil
	})
	if err != nil {
		return projects.ProjectMembershipRecord{}, err
	}
	return result, nil
}

func (s *Store) RemoveProjectMember(ctx context.Context, input projects.RemoveProjectMemberStoreInput) error {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return err
	}
	memberID, err := storedb.ParseUUID(input.MemberID)
	if err != nil {
		return projects.ErrNotFound
	}

	return s.WithTx(ctx, func(tx pgx.Tx) error {
		locked, err := lockProjectMemberMutation(ctx, tx, projectID, actorUserID, memberID, true)
		if err != nil {
			return err
		}
		if locked.removalRetry() {
			return enqueuePermissionLossDeletion(
				ctx, tx, projectID, locked.targetUserID,
				locked.projectGeneration, locked.targetUpdatedAt.Time,
			)
		}
		var updatedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `
			UPDATE project_memberships
			SET removed_at = $1, updated_at = $1
			WHERE id = $2 AND project_id = $3 AND removed_at IS NULL
			RETURNING updated_at
		`, storedb.Timestamptz(input.RemovedAt), memberID, projectID).Scan(&updatedAt); errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		} else if err != nil {
			return err
		}
		return enqueuePermissionLossDeletion(
			ctx, tx, projectID, locked.targetUserID,
			locked.projectGeneration, updatedAt.Time,
		)
	})
}

type lockedProjectMemberMutation struct {
	projectGeneration int64
	targetUserID      string
	targetRole        projects.Role
	targetRemovedAt   pgtype.Timestamptz
	targetUpdatedAt   pgtype.Timestamptz
}

func (locked lockedProjectMemberMutation) removalRetry() bool { return locked.targetRemovedAt.Valid }

func lockProjectMemberMutation(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	actorUserID pgtype.UUID,
	memberID pgtype.UUID,
	includeRemovedTarget bool,
) (lockedProjectMemberMutation, error) {
	var projectGeneration int64
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation
		FROM projects
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
		return lockedProjectMemberMutation{}, projects.ErrNotFound
	} else if err != nil {
		return lockedProjectMemberMutation{}, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, user_id, role, removed_at, updated_at
		FROM project_memberships
		WHERE project_id = $1
		  AND (
		    (user_id = $2 AND removed_at IS NULL)
		    OR (id = $3 AND ($4 OR removed_at IS NULL))
		  )
		ORDER BY id
		FOR UPDATE
	`, projectID, actorUserID, memberID, includeRemovedTarget)
	if err != nil {
		return lockedProjectMemberMutation{}, err
	}
	defer rows.Close()

	actorFound := false
	actorRole := projects.Role("")
	targetFound := false
	var targetUserID string
	targetRole := projects.Role("")
	var targetRemovedAt pgtype.Timestamptz
	var targetUpdatedAt pgtype.Timestamptz
	for rows.Next() {
		var rowID, rowUserID pgtype.UUID
		var role string
		var removedAt, updatedAt pgtype.Timestamptz
		if err := rows.Scan(&rowID, &rowUserID, &role, &removedAt, &updatedAt); err != nil {
			return lockedProjectMemberMutation{}, err
		}
		if rowUserID == actorUserID && !removedAt.Valid {
			actorFound = true
			actorRole = projects.Role(role)
		}
		if rowID == memberID {
			targetFound = true
			targetUserID = rowUserID.String()
			targetRole = projects.Role(role)
			targetRemovedAt = removedAt
			targetUpdatedAt = updatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return lockedProjectMemberMutation{}, err
	}
	if !actorFound {
		return lockedProjectMemberMutation{}, projects.ErrNotFound
	}
	if !projects.CanManageMembers(actorRole) {
		return lockedProjectMemberMutation{}, projects.ErrForbidden
	}
	if !targetFound {
		return lockedProjectMemberMutation{}, projects.ErrNotFound
	}
	if targetRole == projects.RoleOwner {
		return lockedProjectMemberMutation{}, projects.ErrConflict
	}
	return lockedProjectMemberMutation{
		projectGeneration: projectGeneration,
		targetUserID:      targetUserID, targetRole: targetRole,
		targetRemovedAt: targetRemovedAt, targetUpdatedAt: targetUpdatedAt,
	}, nil
}

func enqueuePermissionLossDeletion(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	actorUserID string,
	projectGeneration int64,
	fencedAt time.Time,
) error {
	actorID, err := storedb.ParseUUID(actorUserID)
	if err != nil || projectGeneration < 1 {
		return deletion.ErrInvalidTargetRequest
	}
	fencedAt = fencedAt.UTC().Truncate(time.Microsecond)
	var tombstoneID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO deletion_tombstones (
			id, project_id, origin, entity_kind, purge_mode, entity_id,
			permission_loss_actor_user_id, project_generation, status,
			fenced_at, purge_deadline, created_at, updated_at
		) VALUES (
			gen_random_uuid(), $1, 'governed_online', 'project', 'derived_generation', $1,
			$2, $3, 'pending', $4, $5, $4, $4
		)
		ON CONFLICT (project_id, permission_loss_actor_user_id, fenced_at)
			WHERE permission_loss_actor_user_id IS NOT NULL
		DO NOTHING
		RETURNING id
	`, projectID, actorID, projectGeneration, fencedAt, fencedAt.Add(deletion.DerivedPurgeDeadline)).Scan(&tombstoneID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, target := range deletion.SourceDeletionTargets() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO deletion_target_tasks (
				tombstone_id, project_id, target, state, attempts,
				claim_incarnation, next_attempt_at
			) VALUES ($1, $2, $3, 'pending', 0, 0, $4)
		`, tombstoneID, projectID, string(target), fencedAt); err != nil {
			return err
		}
	}
	return deletionstore.InsertPostgresStepRegistry(
		ctx, tx, tombstoneID.String(), projectID.String(),
		governance.DeletionSelectionProject, governance.PurgeModeDerivedGeneration,
	)
}

func projectMembershipRecord(row sqlc.ProjectMembership) projects.ProjectMembershipRecord {
	return projectMembershipRecordFromColumns(row.ID, row.ProjectID, row.UserID, row.Role, row.RemovedAt, row.CreatedAt, row.UpdatedAt)
}

func projectMembershipRecordFromListProjectMembersForActor(row sqlc.ListProjectMembersForActorRow) projects.ProjectMembershipRecord {
	return projectMembershipRecordWithPIIFromColumns(
		row.ID,
		row.ProjectID,
		row.UserID,
		row.Email,
		row.DisplayName,
		row.Role,
		row.RemovedAt,
		row.CreatedAt,
		row.UpdatedAt,
	)
}

func projectMembershipRecordFromUpdateProjectMemberRoleForActor(row sqlc.UpdateProjectMemberRoleForActorRow) projects.ProjectMembershipRecord {
	return projectMembershipRecordWithPIIFromColumns(
		row.ID,
		row.ProjectID,
		row.UserID,
		row.Email,
		row.DisplayName,
		row.Role,
		row.RemovedAt,
		row.CreatedAt,
		row.UpdatedAt,
	)
}

func projectMembershipRecordFromColumns(id, projectID, userID pgtype.UUID, role string, removedAt, createdAt, updatedAt pgtype.Timestamptz) projects.ProjectMembershipRecord {
	status := projects.MembershipStatusActive
	if removedAt.Valid {
		status = projects.MembershipStatusRemoved
	}
	return projects.ProjectMembershipRecord{
		ID:        id.String(),
		ProjectID: projectID.String(),
		UserID:    userID.String(),
		Role:      projects.Role(role),
		Status:    status,
		CreatedAt: createdAt.Time,
		UpdatedAt: updatedAt.Time,
	}
}

func projectMembershipRecordWithPIIFromColumns(id, projectID, userID pgtype.UUID, email, displayName, role string, removedAt, createdAt, updatedAt pgtype.Timestamptz) projects.ProjectMembershipRecord {
	record := projectMembershipRecordFromColumns(id, projectID, userID, role, removedAt, createdAt, updatedAt)
	record.Email = email
	record.DisplayName = &displayName
	return record
}
