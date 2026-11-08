package projects

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("projects: not found")
	ErrForbidden  = errors.New("projects: forbidden")
	ErrValidation = errors.New("projects: validation failed")
	ErrConflict   = errors.New("projects: conflict")
)

type Store interface {
	CreateProjectWithOwnerMembership(context.Context, CreateProjectStoreInput) (ProjectRecord, error)
	ListProjectsForActor(context.Context, ListProjectsStoreInput) ([]ProjectRecord, error)
	GetProject(context.Context, string, string) (ProjectRecord, error)
	UpdateProject(context.Context, UpdateProjectStoreInput) (ProjectRecord, error)
	DeleteProject(context.Context, DeleteProjectStoreInput) error
	RestoreProject(context.Context, RestoreProjectStoreInput) (ProjectRecord, error)
	ListProjectMembers(context.Context, ListProjectMembersStoreInput) ([]ProjectMembershipRecord, error)
	UpdateProjectMemberRole(context.Context, UpdateProjectMemberRoleStoreInput) (ProjectMembershipRecord, error)
	RemoveProjectMember(context.Context, RemoveProjectMemberStoreInput) error
	CreateProjectInvitation(context.Context, CreateProjectInvitationStoreInput) (ProjectInvitationRecord, error)
	ListProjectInvitations(context.Context, ListProjectInvitationsStoreInput) ([]ProjectInvitationRecord, error)
	ResolveProjectInvitationMemberAccessForActor(context.Context, ResolveProjectInvitationMemberAccessStoreInput) (ProjectInvitationMemberAccess, error)
	ResolveProjectInvitationInviteeAccessForActor(context.Context, ResolveProjectInvitationInviteeAccessStoreInput) (ProjectInvitationInviteeAccess, error)
	CancelProjectInvitation(context.Context, CancelProjectInvitationStoreInput) (ProjectInvitationRecord, error)
	AcceptProjectInvitation(context.Context, AcceptProjectInvitationStoreInput) (ProjectMembershipRecord, error)
	ListProjectInvitationsForActor(context.Context, ListProjectInvitationsForActorStoreInput) ([]ProjectInvitationRecord, error)
}

type CreateProjectStoreInput struct {
	ProjectID         string
	OwnerMembershipID string
	OwnerUserID       string
	Title             string
	Description       string
	CreatedAt         time.Time
}

type ListProjectsStoreInput struct {
	ActorUserID     string
	State           ProjectListState
	Limit           int
	HasCursor       bool
	CursorCreatedAt time.Time
	CursorID        string
}

type ProjectListState string

const (
	ProjectListActive  ProjectListState = "active"
	ProjectListDeleted ProjectListState = "deleted"
)

type UpdateProjectStoreInput struct {
	ActorUserID string
	ProjectID   string
	Title       string
	Description string
	UpdatedAt   time.Time
}

type DeleteProjectStoreInput struct {
	ActorUserID string
	ProjectID   string
	TombstoneID string
	DeletedAt   time.Time
}

type RestoreProjectStoreInput struct {
	ActorUserID string
	ProjectID   string
}

type ListProjectMembersStoreInput struct {
	ProjectID   string
	ActorUserID string
	Limit       int
}

type UpdateProjectMemberRoleStoreInput struct {
	ProjectID   string
	ActorUserID string
	MemberID    string
	Role        Role
	UpdatedAt   time.Time
}

type RemoveProjectMemberStoreInput struct {
	ProjectID   string
	ActorUserID string
	MemberID    string
	RemovedAt   time.Time
}

type CreateProjectInvitationStoreInput struct {
	ID          string
	ProjectID   string
	ActorUserID string
	Email       string
	EmailHash   string
	Role        Role
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type ListProjectInvitationsStoreInput struct {
	ProjectID   string
	ActorUserID string
	Limit       int
}

type ResolveProjectInvitationMemberAccessStoreInput struct {
	ActorUserID  string
	InvitationID string
}

type ResolveProjectInvitationInviteeAccessStoreInput struct {
	ActorUserID  string
	InvitationID string
	EmailHash    string
}

type CancelProjectInvitationStoreInput struct {
	ActorUserID  string
	ProjectID    string
	InvitationID string
	CanceledAt   time.Time
}

type AcceptProjectInvitationStoreInput struct {
	InvitationID string
	ProjectID    string
	MembershipID string
	UserID       string
	EmailHash    string
	AcceptedAt   time.Time
}

type ListProjectInvitationsForActorStoreInput struct {
	ActorUserID     string
	EmailHash       string
	Limit           int
	HasCursor       bool
	CursorCreatedAt time.Time
	CursorID        string
}

type ProjectRecord struct {
	ID                string
	Title             string
	Description       string
	BookCount         int
	ChapterCount      int
	WordCount         int
	Revision          int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         *time.Time
	RestoreUntil      *time.Time
	CurrentMembership ProjectMembershipRecord
}

type MembershipStatus string

const (
	MembershipStatusActive  MembershipStatus = "active"
	MembershipStatusRemoved MembershipStatus = "removed"
)

type ProjectMembershipRecord struct {
	ID          string
	ProjectID   string
	UserID      string
	Email       string
	DisplayName *string
	Role        Role
	Status      MembershipStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ProjectInvitationStatus string

const (
	ProjectInvitationStatusPending  ProjectInvitationStatus = "pending"
	ProjectInvitationStatusAccepted ProjectInvitationStatus = "accepted"
	ProjectInvitationStatusCanceled ProjectInvitationStatus = "canceled"
	ProjectInvitationStatusExpired  ProjectInvitationStatus = "expired"
)

type ProjectInvitationRecord struct {
	ID                 string
	ProjectID          string
	ProjectTitle       string
	InviterUserID      string
	InviterDisplayName string
	InviterEmail       string
	Email              string
	EmailHash          string
	Role               Role
	Status             ProjectInvitationStatus
	ExpiresAt          time.Time
	AcceptedAt         *time.Time
	CanceledAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CurrentMembership  ProjectMembershipRecord
}

type ProjectInvitationMemberAccess struct {
	ProjectID    string
	InvitationID string
	ProjectRole  Role
}

type ProjectInvitationInviteeAccess struct {
	ProjectID    string
	InvitationID string
}
