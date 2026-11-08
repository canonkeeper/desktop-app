package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/mail"
	"strings"
	"time"
)

const (
	projectInvitationTTL   = 14 * 24 * time.Hour
	defaultInvitationLimit = 100
)

type AuthenticatedUser struct {
	ID            string
	Email         string
	EmailVerified bool
}

type CreateProjectInvitationInput struct {
	Email string
	Role  Role
}

func (s *Service) CreateProjectInvitation(ctx context.Context, actorUserID, projectID string, input CreateProjectInvitationInput) (ProjectInvitationRecord, error) {
	email := normalizeInvitationEmail(input.Email)
	if !isValidInvitationEmail(email) || !isInvitableRole(input.Role) {
		return ProjectInvitationRecord{}, ErrValidation
	}

	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return ProjectInvitationRecord{}, err
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return ProjectInvitationRecord{}, ErrForbidden
	}

	now := s.clock.Now()
	invitation, err := s.store.CreateProjectInvitation(ctx, CreateProjectInvitationStoreInput{
		ID:          s.ids.NewProjectInvitationID(),
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		Email:       email,
		EmailHash:   hashInvitationEmail(email),
		Role:        input.Role,
		ExpiresAt:   now.Add(projectInvitationTTL),
		CreatedAt:   now,
	})
	if err != nil {
		return ProjectInvitationRecord{}, err
	}
	return s.invitationWithReadStatus(invitation), nil
}

func (s *Service) ListProjectInvitations(ctx context.Context, actorUserID, projectID string) ([]ProjectInvitationRecord, error) {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return nil, err
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return nil, ErrForbidden
	}

	invitations, err := s.store.ListProjectInvitations(ctx, ListProjectInvitationsStoreInput{
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		Limit:       defaultInvitationLimit,
	})
	if err != nil {
		return nil, err
	}
	return s.invitationsWithReadStatus(invitations), nil
}

func (s *Service) CancelProjectInvitation(ctx context.Context, actorUserID, invitationID string) (ProjectInvitationRecord, error) {
	if actorUserID == "" || invitationID == "" {
		return ProjectInvitationRecord{}, ErrNotFound
	}

	access, err := s.store.ResolveProjectInvitationMemberAccessForActor(ctx, ResolveProjectInvitationMemberAccessStoreInput{
		ActorUserID:  actorUserID,
		InvitationID: invitationID,
	})
	if err != nil {
		return ProjectInvitationRecord{}, err
	}
	if !CanManageMembers(access.ProjectRole) {
		return ProjectInvitationRecord{}, ErrForbidden
	}

	canceled, err := s.store.CancelProjectInvitation(ctx, CancelProjectInvitationStoreInput{
		ActorUserID:  actorUserID,
		ProjectID:    access.ProjectID,
		InvitationID: invitationID,
		CanceledAt:   s.clock.Now(),
	})
	if err != nil {
		return ProjectInvitationRecord{}, err
	}
	return s.invitationWithReadStatus(canceled), nil
}

func (s *Service) AcceptProjectInvitation(ctx context.Context, actor AuthenticatedUser, invitationID string) (ProjectMembershipRecord, error) {
	email := normalizeInvitationEmail(actor.Email)
	if actor.ID == "" || invitationID == "" || !isValidInvitationEmail(email) {
		return ProjectMembershipRecord{}, ErrNotFound
	}
	if !actor.EmailVerified {
		return ProjectMembershipRecord{}, ErrForbidden
	}

	emailHash := hashInvitationEmail(email)
	access, err := s.store.ResolveProjectInvitationInviteeAccessForActor(ctx, ResolveProjectInvitationInviteeAccessStoreInput{
		ActorUserID:  actor.ID,
		InvitationID: invitationID,
		EmailHash:    emailHash,
	})
	if err != nil {
		return ProjectMembershipRecord{}, err
	}
	return s.store.AcceptProjectInvitation(ctx, AcceptProjectInvitationStoreInput{
		InvitationID: invitationID,
		ProjectID:    access.ProjectID,
		MembershipID: s.ids.NewProjectMembershipID(),
		UserID:       actor.ID,
		EmailHash:    emailHash,
		AcceptedAt:   s.clock.Now(),
	})
}

func (s *Service) ListMyProjectInvitations(ctx context.Context, actor AuthenticatedUser, input ListMyProjectInvitationsInput) (ProjectInvitationPage, error) {
	email := normalizeInvitationEmail(actor.Email)
	if actor.ID == "" || !isValidInvitationEmail(email) {
		return ProjectInvitationPage{}, ErrNotFound
	}
	if !actor.EmailVerified {
		return ProjectInvitationPage{}, ErrForbidden
	}
	limit, err := collectionLimit(input.Limit)
	if err != nil {
		return ProjectInvitationPage{}, err
	}
	emailHash := hashInvitationEmail(email)
	cursor, err := decodeCollectionCursor(input.Cursor, invitationCollectionCursorKind, actor.ID, emailHash)
	if err != nil {
		return ProjectInvitationPage{}, err
	}

	invitations, err := s.store.ListProjectInvitationsForActor(ctx, ListProjectInvitationsForActorStoreInput{
		ActorUserID:     actor.ID,
		EmailHash:       emailHash,
		Limit:           limit + 1,
		HasCursor:       input.Cursor != "",
		CursorCreatedAt: cursor.CreatedAt,
		CursorID:        cursor.ID,
	})
	if err != nil {
		return ProjectInvitationPage{}, err
	}
	return projectInvitationPage(s.invitationsWithReadStatus(invitations), limit, actor.ID, emailHash)
}

func (s *Service) invitationsWithReadStatus(invitations []ProjectInvitationRecord) []ProjectInvitationRecord {
	out := make([]ProjectInvitationRecord, len(invitations))
	for i, invitation := range invitations {
		out[i] = s.invitationWithReadStatus(invitation)
	}
	return out
}

func (s *Service) invitationWithReadStatus(invitation ProjectInvitationRecord) ProjectInvitationRecord {
	if invitation.Status == ProjectInvitationStatusPending && !s.clock.Now().Before(invitation.ExpiresAt) {
		invitation.Status = ProjectInvitationStatusExpired
	}
	return invitation
}

func isInvitableRole(role Role) bool {
	switch role {
	case RoleViewer, RoleEditor, RoleAdmin:
		return true
	default:
		return false
	}
}

func normalizeInvitationEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isValidInvitationEmail(email string) bool {
	if email == "" || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && strings.Contains(email, "@")
}

func hashInvitationEmail(email string) string {
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:])
}
