package projects

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxProjectTitleLength       = 160
	maxProjectDescriptionLength = 1000
	defaultPageLimit            = 50
	maxPageLimit                = 100
)

type IDGenerator interface {
	NewProjectID() string
	NewProjectMembershipID() string
	NewProjectInvitationID() string
	NewDeletionTombstoneID() string
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	store Store
	ids   IDGenerator
	clock Clock
}

func NewService(store Store, ids IDGenerator, clock Clock) *Service {
	return &Service{
		store: store,
		ids:   ids,
		clock: clock,
	}
}

type CreateProjectInput struct {
	Title       string
	Description string
}

type ListProjectsInput struct {
	Limit  int
	Cursor string
	State  ProjectListState
}

func (s *Service) RestoreProject(ctx context.Context, actorUserID, projectID string) (ProjectRecord, error) {
	if actorUserID == "" {
		return ProjectRecord{}, ErrForbidden
	}
	if projectID == "" {
		return ProjectRecord{}, ErrNotFound
	}
	return s.store.RestoreProject(ctx, RestoreProjectStoreInput{
		ActorUserID: actorUserID,
		ProjectID:   projectID,
	})
}

type UpdateProjectInput struct {
	Title          *string
	Description    *string
	DescriptionSet bool
}

type ListProjectMembersInput struct {
	Limit  int
	Cursor string
}

type UpdateProjectMemberRoleInput struct {
	Role Role
}

func (s *Service) CreateProject(ctx context.Context, actorUserID string, input CreateProjectInput) (ProjectRecord, error) {
	if actorUserID == "" {
		return ProjectRecord{}, ErrForbidden
	}

	title := strings.TrimSpace(input.Title)
	if title == "" ||
		utf8.RuneCountInString(title) > maxProjectTitleLength ||
		utf8.RuneCountInString(input.Description) > maxProjectDescriptionLength {
		return ProjectRecord{}, ErrValidation
	}

	now := s.clock.Now()
	return s.store.CreateProjectWithOwnerMembership(ctx, CreateProjectStoreInput{
		ProjectID:         s.ids.NewProjectID(),
		OwnerMembershipID: s.ids.NewProjectMembershipID(),
		OwnerUserID:       actorUserID,
		Title:             title,
		Description:       input.Description,
		CreatedAt:         now,
	})
}

func (s *Service) ListProjects(ctx context.Context, actorUserID string, input ListProjectsInput) (ProjectPage, error) {
	if actorUserID == "" {
		return ProjectPage{}, ErrForbidden
	}
	limit, err := collectionLimit(input.Limit)
	if err != nil {
		return ProjectPage{}, err
	}
	state := input.State
	if state == "" {
		state = ProjectListActive
	}
	cursorKind := projectCollectionCursorKind
	switch state {
	case ProjectListActive:
	case ProjectListDeleted:
		cursorKind = deletedProjectCursorKind
	default:
		return ProjectPage{}, ErrValidation
	}
	cursor, err := decodeCollectionCursor(input.Cursor, cursorKind, actorUserID, "")
	if err != nil {
		return ProjectPage{}, err
	}
	records, err := s.store.ListProjectsForActor(ctx, ListProjectsStoreInput{
		ActorUserID:     actorUserID,
		State:           state,
		Limit:           limit + 1,
		HasCursor:       input.Cursor != "",
		CursorCreatedAt: cursor.CreatedAt,
		CursorID:        cursor.ID,
	})
	if err != nil {
		return ProjectPage{}, err
	}
	return projectPageForState(records, limit, actorUserID, state)
}

func (s *Service) GetProject(ctx context.Context, actorUserID, projectID string) (ProjectRecord, error) {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return ProjectRecord{}, err
	}
	if !CanReadProject(project.CurrentMembership.Role) {
		return ProjectRecord{}, ErrForbidden
	}
	return project, nil
}

func (s *Service) UpdateProject(ctx context.Context, actorUserID, projectID string, input UpdateProjectInput) (ProjectRecord, error) {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return ProjectRecord{}, err
	}
	if !CanMutateProject(project.CurrentMembership.Role) {
		return ProjectRecord{}, ErrForbidden
	}

	title := project.Title
	description := project.Description
	hasUpdate := false
	if input.Title != nil {
		title = strings.TrimSpace(*input.Title)
		if title == "" || utf8.RuneCountInString(title) > maxProjectTitleLength {
			return ProjectRecord{}, ErrValidation
		}
		hasUpdate = true
	}
	if input.DescriptionSet {
		description = ""
		if input.Description != nil {
			description = *input.Description
		}
		if utf8.RuneCountInString(description) > maxProjectDescriptionLength {
			return ProjectRecord{}, ErrValidation
		}
		hasUpdate = true
	}
	if !hasUpdate {
		return project, nil
	}

	return s.store.UpdateProject(ctx, UpdateProjectStoreInput{
		ActorUserID: actorUserID,
		ProjectID:   projectID,
		Title:       title,
		Description: description,
		UpdatedAt:   s.clock.Now(),
	})
}

func (s *Service) DeleteProject(ctx context.Context, actorUserID, projectID string) error {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return err
	}
	if !CanDeleteProject(project.CurrentMembership.Role) {
		return ErrForbidden
	}
	deletedAt := s.clock.Now().UTC().Truncate(time.Microsecond)
	return s.store.DeleteProject(ctx, DeleteProjectStoreInput{
		ActorUserID: actorUserID,
		ProjectID:   projectID,
		TombstoneID: s.ids.NewDeletionTombstoneID(),
		DeletedAt:   deletedAt,
	})
}

func (s *Service) ListProjectMembers(ctx context.Context, actorUserID, projectID string, input ListProjectMembersInput) ([]ProjectMembershipRecord, error) {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return nil, err
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return nil, ErrForbidden
	}
	limit, err := pageLimit(input.Limit, input.Cursor)
	if err != nil {
		return nil, err
	}
	return s.store.ListProjectMembers(ctx, ListProjectMembersStoreInput{
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		Limit:       limit,
	})
}

func (s *Service) UpdateProjectMemberRole(ctx context.Context, actorUserID, projectID, memberID string, input UpdateProjectMemberRoleInput) (ProjectMembershipRecord, error) {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return ProjectMembershipRecord{}, err
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return ProjectMembershipRecord{}, ErrForbidden
	}
	if input.Role == RoleOwner {
		return ProjectMembershipRecord{}, ErrConflict
	}
	if !CanAssignMemberRole(input.Role) {
		return ProjectMembershipRecord{}, ErrValidation
	}
	if memberID == "" {
		return ProjectMembershipRecord{}, ErrNotFound
	}
	return s.store.UpdateProjectMemberRole(ctx, UpdateProjectMemberRoleStoreInput{
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		MemberID:    memberID,
		Role:        input.Role,
		UpdatedAt:   s.clock.Now(),
	})
}

func (s *Service) RemoveProjectMember(ctx context.Context, actorUserID, projectID, memberID string) error {
	project, err := s.getActiveProjectForActor(ctx, actorUserID, projectID)
	if err != nil {
		return err
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return ErrForbidden
	}
	if memberID == "" {
		return ErrNotFound
	}
	return s.store.RemoveProjectMember(ctx, RemoveProjectMemberStoreInput{
		ProjectID:   projectID,
		ActorUserID: actorUserID,
		MemberID:    memberID,
		RemovedAt:   s.clock.Now(),
	})
}

func (s *Service) getActiveProjectForActor(ctx context.Context, actorUserID, projectID string) (ProjectRecord, error) {
	if actorUserID == "" || projectID == "" {
		return ProjectRecord{}, ErrNotFound
	}
	return s.store.GetProject(ctx, actorUserID, projectID)
}

func pageLimit(limit int, cursor string) (int, error) {
	if cursor != "" {
		return 0, ErrValidation
	}
	if limit == 0 {
		return defaultPageLimit, nil
	}
	if limit < 1 || limit > maxPageLimit {
		return 0, ErrValidation
	}
	return limit, nil
}
