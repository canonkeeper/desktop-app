package projectstore

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const listProjectInvitationsForActorCursorSQL = `
SELECT
  i.id,
  i.project_id,
  p.name,
  inviter.display_name,
  inviter.email,
  i.email,
  i.role,
  i.status,
  i.expires_at,
  i.accepted_at,
  i.created_at,
  i.updated_at
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN users inviter ON inviter.id = i.inviter_user_id
JOIN users invitee ON invitee.id = $1
WHERE i.email_hash = $2
  AND i.email = lower(btrim(invitee.email))
  AND invitee.email_verified_at IS NOT NULL
  AND invitee.disabled_at IS NULL
  AND p.deleted_at IS NULL
  AND (NOT $3::boolean OR (i.created_at, i.id) < ($4::timestamptz, $5::uuid))
ORDER BY i.created_at DESC, i.id DESC
LIMIT $6`

const resolveProjectInvitationMemberAccessForActorSQL = `
SELECT i.project_id, i.id, m.role
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN project_memberships m ON m.project_id = p.id
WHERE i.id = $1
  AND p.deleted_at IS NULL
  AND m.user_id = $2
  AND m.removed_at IS NULL`

const resolveProjectInvitationInviteeAccessForActorSQL = `
SELECT i.project_id, i.id
FROM project_invitations i
JOIN projects p ON p.id = i.project_id
JOIN users invitee ON invitee.id = $1
WHERE i.id = $2
  AND i.email_hash = $3
  AND i.email = lower(btrim(invitee.email))
  AND invitee.email_verified_at IS NOT NULL
  AND invitee.disabled_at IS NULL
  AND p.deleted_at IS NULL`

type projectInvitationActorCollectionRow struct {
	ID                 pgtype.UUID
	ProjectID          pgtype.UUID
	ProjectTitle       string
	InviterDisplayName string
	InviterEmail       string
	Email              string
	Role               string
	Status             string
	ExpiresAt          pgtype.Timestamptz
	AcceptedAt         pgtype.Timestamptz
	CreatedAt          pgtype.Timestamptz
	UpdatedAt          pgtype.Timestamptz
}

func (s *Store) CreateProjectInvitation(ctx context.Context, input projects.CreateProjectInvitationStoreInput) (projects.ProjectInvitationRecord, error) {
	invitationID, err := storedb.ParseUUID(input.ID)
	if err != nil {
		return projects.ProjectInvitationRecord{}, err
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectInvitationRecord{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectInvitationRecord{}, err
	}

	var row sqlc.CreateProjectInvitationRow
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		expireParams := sqlc.ExpireProjectInvitationCreateConflictsParams{
			CreatedAt:   storedb.Timestamptz(input.CreatedAt),
			EmailHash:   input.EmailHash,
			ProjectID:   projectID,
			ActorUserID: actorUserID,
		}
		if err := queries.ExpireProjectInvitationCreateConflicts(ctx, expireParams); err != nil {
			return err
		}
		row, err = queries.CreateProjectInvitation(ctx, sqlc.CreateProjectInvitationParams{
			ID:          invitationID,
			ActorUserID: actorUserID,
			Email:       input.Email,
			EmailHash:   input.EmailHash,
			Role:        string(input.Role),
			ExpiresAt:   storedb.Timestamptz(input.ExpiresAt),
			CreatedAt:   storedb.Timestamptz(input.CreatedAt),
			ProjectID:   projectID,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ProjectInvitationRecord{}, s.createProjectInvitationDeniedError(ctx, input)
		}
		if isUniqueViolation(err) {
			return projects.ProjectInvitationRecord{}, projects.ErrConflict
		}
		return projects.ProjectInvitationRecord{}, err
	}
	return projectInvitationRecordFromCreate(row), nil
}

func (s *Store) ListProjectInvitations(ctx context.Context, input projects.ListProjectInvitationsStoreInput) ([]projects.ProjectInvitationRecord, error) {
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return nil, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return nil, err
	}
	project, err := s.GetProject(ctx, input.ActorUserID, input.ProjectID)
	if err != nil {
		return nil, err
	}
	if !projects.CanManageMembers(project.CurrentMembership.Role) {
		return nil, projects.ErrForbidden
	}

	rows, err := sqlc.New(s.Pool).ListProjectInvitations(ctx, sqlc.ListProjectInvitationsParams{
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		LimitCount:  invitationLimit(input.Limit),
	})
	if err != nil {
		return nil, err
	}
	invitations := make([]projects.ProjectInvitationRecord, len(rows))
	for i, row := range rows {
		invitations[i] = projectInvitationRecordFromList(row)
	}
	return invitations, nil
}

func (s *Store) ResolveProjectInvitationMemberAccessForActor(ctx context.Context, input projects.ResolveProjectInvitationMemberAccessStoreInput) (projects.ProjectInvitationMemberAccess, error) {
	invitationID, err := storedb.ParseUUID(input.InvitationID)
	if err != nil {
		return projects.ProjectInvitationMemberAccess{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectInvitationMemberAccess{}, projects.ErrNotFound
	}

	var projectID pgtype.UUID
	var resolvedInvitationID pgtype.UUID
	var roleValue string
	err = s.Pool.QueryRow(ctx, resolveProjectInvitationMemberAccessForActorSQL, invitationID, actorUserID).Scan(
		&projectID,
		&resolvedInvitationID,
		&roleValue,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ProjectInvitationMemberAccess{}, projects.ErrNotFound
	}
	if err != nil {
		return projects.ProjectInvitationMemberAccess{}, err
	}
	role, ok := projects.FromGovernanceProjectRole(governance.ProjectRole(roleValue))
	if !ok {
		return projects.ProjectInvitationMemberAccess{}, projects.ErrNotFound
	}
	return projects.ProjectInvitationMemberAccess{
		ProjectID:    projectID.String(),
		InvitationID: resolvedInvitationID.String(),
		ProjectRole:  role,
	}, nil
}

func (s *Store) ResolveProjectInvitationInviteeAccessForActor(ctx context.Context, input projects.ResolveProjectInvitationInviteeAccessStoreInput) (projects.ProjectInvitationInviteeAccess, error) {
	invitationID, err := storedb.ParseUUID(input.InvitationID)
	if err != nil {
		return projects.ProjectInvitationInviteeAccess{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectInvitationInviteeAccess{}, projects.ErrNotFound
	}

	var projectID pgtype.UUID
	var resolvedInvitationID pgtype.UUID
	err = s.Pool.QueryRow(ctx, resolveProjectInvitationInviteeAccessForActorSQL, actorUserID, invitationID, input.EmailHash).Scan(
		&projectID,
		&resolvedInvitationID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ProjectInvitationInviteeAccess{}, projects.ErrNotFound
	}
	if err != nil {
		return projects.ProjectInvitationInviteeAccess{}, err
	}
	return projects.ProjectInvitationInviteeAccess{
		ProjectID:    projectID.String(),
		InvitationID: resolvedInvitationID.String(),
	}, nil
}

func (s *Store) CancelProjectInvitation(ctx context.Context, input projects.CancelProjectInvitationStoreInput) (projects.ProjectInvitationRecord, error) {
	invitationID, err := storedb.ParseUUID(input.InvitationID)
	if err != nil {
		return projects.ProjectInvitationRecord{}, projects.ErrNotFound
	}
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return projects.ProjectInvitationRecord{}, err
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectInvitationRecord{}, projects.ErrNotFound
	}

	var row sqlc.CancelProjectInvitationRow
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		row, err = sqlc.New(tx).CancelProjectInvitation(ctx, sqlc.CancelProjectInvitationParams{
			ActorUserID:  actorUserID,
			ProjectID:    projectID,
			InvitationID: invitationID,
			CanceledAt:   storedb.Timestamptz(input.CanceledAt),
		})
		if err != nil {
			return err
		}
		if row.ProjectID != projectID {
			return projects.ErrNotFound
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return projects.ProjectInvitationRecord{}, s.cancelProjectInvitationDeniedError(ctx, input)
	}
	if err != nil {
		return projects.ProjectInvitationRecord{}, err
	}
	return projectInvitationRecordFromCancel(row), nil
}

func (s *Store) AcceptProjectInvitation(ctx context.Context, input projects.AcceptProjectInvitationStoreInput) (projects.ProjectMembershipRecord, error) {
	invitationID, err := storedb.ParseUUID(input.InvitationID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, projects.ErrNotFound
	}
	membershipID, err := storedb.ParseUUID(input.MembershipID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, err
	}
	userID, err := storedb.ParseUUID(input.UserID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, err
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return projects.ProjectMembershipRecord{}, projects.ErrNotFound
	}
	acceptedAt := storedb.Timestamptz(input.AcceptedAt)

	var createdMembership sqlc.ProjectMembership
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		invitation, err := queries.GetProjectInvitationForInviteeForUpdate(ctx, sqlc.GetProjectInvitationForInviteeForUpdateParams{
			InvitationID: invitationID,
			ProjectID:    projectID,
			UserID:       userID,
			EmailHash:    input.EmailHash,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrNotFound
		}
		if err != nil {
			return err
		}
		if invitation.ProjectID != projectID {
			return projects.ErrNotFound
		}
		if invitation.Status != string(projects.ProjectInvitationStatusPending) ||
			invitation.AcceptedAt.Valid ||
			invitation.CanceledAt.Valid ||
			!input.AcceptedAt.Before(invitation.ExpiresAt.Time) {
			return projects.ErrConflict
		}

		createdMembership, err = queries.UpsertProjectInvitationMembership(ctx, sqlc.UpsertProjectInvitationMembershipParams{
			ID:        membershipID,
			ProjectID: invitation.ProjectID,
			UserID:    userID,
			Role:      invitation.Role,
			CreatedAt: acceptedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrConflict
		}
		if isUniqueViolation(err) {
			return projects.ErrConflict
		}
		if err != nil {
			return err
		}

		_, err = queries.MarkProjectInvitationAccepted(ctx, sqlc.MarkProjectInvitationAcceptedParams{
			InvitationID: invitationID,
			ProjectID:    projectID,
			UserID:       userID,
			EmailHash:    input.EmailHash,
			AcceptedAt:   acceptedAt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return projects.ErrConflict
		}
		return err
	})
	if err != nil {
		return projects.ProjectMembershipRecord{}, err
	}
	return projectMembershipRecord(createdMembership), nil
}

func (s *Store) ListProjectInvitationsForActor(ctx context.Context, input projects.ListProjectInvitationsForActorStoreInput) ([]projects.ProjectInvitationRecord, error) {
	actorUserID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return nil, projects.ErrNotFound
	}
	cursorCreatedAt, cursorID, err := actorCollectionCursorValues(input.HasCursor, input.CursorCreatedAt, input.CursorID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, listProjectInvitationsForActorCursorSQL,
		actorUserID,
		input.EmailHash,
		input.HasCursor,
		cursorCreatedAt,
		cursorID,
		actorCollectionLimit(input.Limit, 100),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	invitations := make([]projects.ProjectInvitationRecord, 0)
	for rows.Next() {
		var row projectInvitationActorCollectionRow
		if err := rows.Scan(
			&row.ID,
			&row.ProjectID,
			&row.ProjectTitle,
			&row.InviterDisplayName,
			&row.InviterEmail,
			&row.Email,
			&row.Role,
			&row.Status,
			&row.ExpiresAt,
			&row.AcceptedAt,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		invitations = append(invitations, projectInvitationRecordFromActorCollection(row))
	}
	return invitations, rows.Err()
}

func (s *Store) createProjectInvitationDeniedError(ctx context.Context, input projects.CreateProjectInvitationStoreInput) error {
	project, err := s.GetProject(ctx, input.ActorUserID, input.ProjectID)
	if err != nil {
		return err
	}
	if !projects.CanManageMembers(project.CurrentMembership.Role) {
		return projects.ErrForbidden
	}
	return projects.ErrConflict
}

func (s *Store) cancelProjectInvitationDeniedError(ctx context.Context, input projects.CancelProjectInvitationStoreInput) error {
	access, err := s.ResolveProjectInvitationMemberAccessForActor(ctx, projects.ResolveProjectInvitationMemberAccessStoreInput{
		ActorUserID:  input.ActorUserID,
		InvitationID: input.InvitationID,
	})
	if err != nil {
		return err
	}
	if access.ProjectID != input.ProjectID {
		return projects.ErrNotFound
	}
	if !projects.CanManageMembers(access.ProjectRole) {
		return projects.ErrForbidden
	}
	return projects.ErrConflict
}

func projectInvitationRecordFromActorCollection(row projectInvitationActorCollectionRow) projects.ProjectInvitationRecord {
	return projects.ProjectInvitationRecord{
		ID:                 row.ID.String(),
		ProjectID:          row.ProjectID.String(),
		ProjectTitle:       row.ProjectTitle,
		InviterDisplayName: row.InviterDisplayName,
		InviterEmail:       row.InviterEmail,
		Email:              row.Email,
		Role:               projects.Role(row.Role),
		Status:             projects.ProjectInvitationStatus(row.Status),
		ExpiresAt:          row.ExpiresAt.Time,
		AcceptedAt:         optionalTime(row.AcceptedAt),
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
}

func projectInvitationRecordFromCreate(row sqlc.CreateProjectInvitationRow) projects.ProjectInvitationRecord {
	return projectInvitationRecordFromColumns(
		row.ID,
		row.ProjectID,
		row.ProjectTitle,
		row.InviterUserID,
		row.InviterDisplayName,
		row.InviterEmail,
		row.Email,
		row.EmailHash,
		row.Role,
		row.Status,
		row.ExpiresAt,
		row.AcceptedAt,
		row.CanceledAt,
		row.CreatedAt,
		row.UpdatedAt,
	)
}

func projectInvitationRecordFromList(row sqlc.ListProjectInvitationsRow) projects.ProjectInvitationRecord {
	return projectInvitationRecordFromColumns(
		row.ID,
		row.ProjectID,
		row.ProjectTitle,
		row.InviterUserID,
		row.InviterDisplayName,
		row.InviterEmail,
		row.Email,
		row.EmailHash,
		row.Role,
		row.Status,
		row.ExpiresAt,
		row.AcceptedAt,
		row.CanceledAt,
		row.CreatedAt,
		row.UpdatedAt,
	)
}

func projectInvitationRecordFromCancel(row sqlc.CancelProjectInvitationRow) projects.ProjectInvitationRecord {
	return projectInvitationRecordFromColumns(
		row.ID,
		row.ProjectID,
		row.ProjectTitle,
		row.InviterUserID,
		row.InviterDisplayName,
		row.InviterEmail,
		row.Email,
		row.EmailHash,
		row.Role,
		row.Status,
		row.ExpiresAt,
		row.AcceptedAt,
		row.CanceledAt,
		row.CreatedAt,
		row.UpdatedAt,
	)
}

func projectInvitationRecordFromColumns(
	id pgtype.UUID,
	projectID pgtype.UUID,
	projectTitle string,
	inviterUserID pgtype.UUID,
	inviterDisplayName string,
	inviterEmail string,
	email string,
	emailHash string,
	role string,
	status string,
	expiresAt pgtype.Timestamptz,
	acceptedAt pgtype.Timestamptz,
	canceledAt pgtype.Timestamptz,
	createdAt pgtype.Timestamptz,
	updatedAt pgtype.Timestamptz,
) projects.ProjectInvitationRecord {
	return projects.ProjectInvitationRecord{
		ID:                 id.String(),
		ProjectID:          projectID.String(),
		ProjectTitle:       projectTitle,
		InviterUserID:      inviterUserID.String(),
		InviterDisplayName: inviterDisplayName,
		InviterEmail:       inviterEmail,
		Email:              email,
		EmailHash:          emailHash,
		Role:               projects.Role(role),
		Status:             projects.ProjectInvitationStatus(status),
		ExpiresAt:          expiresAt.Time,
		AcceptedAt:         optionalTime(acceptedAt),
		CanceledAt:         optionalTime(canceledAt),
		CreatedAt:          createdAt.Time,
		UpdatedAt:          updatedAt.Time,
	}
}

func invitationLimit(limit int) int32 {
	if limit <= 0 || limit > 100 {
		return 100
	}
	return int32(limit)
}
