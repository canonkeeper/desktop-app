package projects

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCreateProjectCreatesOwnerMembership(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	service := NewService(
		store,
		StaticIDsForTest("project-1", "membership-1"),
		StaticClockForTest(now),
	)

	project, err := service.CreateProject(ctx, "user-1", CreateProjectInput{
		Title:       "The White Port",
		Description: "City notes",
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}

	if project.ID != "project-1" {
		t.Fatalf("Project.ID = %q, want project-1", project.ID)
	}
	if project.Title != "The White Port" {
		t.Fatalf("Project.Title = %q, want The White Port", project.Title)
	}
	if project.Description != "City notes" {
		t.Fatalf("Project.Description = %q, want City notes", project.Description)
	}
	if project.BookCount != 0 || project.ChapterCount != 0 || project.WordCount != 0 {
		t.Fatalf("Project counts = %d/%d/%d, want all zero", project.BookCount, project.ChapterCount, project.WordCount)
	}
	if project.Revision != 1 {
		t.Fatalf("Project.Revision = %d, want 1", project.Revision)
	}
	if !project.CreatedAt.Equal(now) || !project.UpdatedAt.Equal(now) {
		t.Fatalf("Project timestamps = %s/%s, want %s", project.CreatedAt, project.UpdatedAt, now)
	}

	membership := project.CurrentMembership
	if membership.ID != "membership-1" {
		t.Fatalf("Membership.ID = %q, want membership-1", membership.ID)
	}
	if membership.ProjectID != "project-1" {
		t.Fatalf("Membership.ProjectID = %q, want project-1", membership.ProjectID)
	}
	if membership.UserID != "user-1" {
		t.Fatalf("Membership.UserID = %q, want user-1", membership.UserID)
	}
	if membership.Role != RoleOwner {
		t.Fatalf("Membership.Role = %q, want owner", membership.Role)
	}
	if membership.Status != MembershipStatusActive {
		t.Fatalf("Membership.Status = %q, want active", membership.Status)
	}
	if !membership.CreatedAt.Equal(now) || !membership.UpdatedAt.Equal(now) {
		t.Fatalf("Membership timestamps = %s/%s, want %s", membership.CreatedAt, membership.UpdatedAt, now)
	}

	storedMembership := store.ActiveMembershipForTest(t, "project-1", "user-1")
	if storedMembership.ID != "membership-1" || storedMembership.Role != RoleOwner {
		t.Fatal("owner membership was not persisted")
	}
	if store.CreateProjectWithOwnerMembershipCallCountForTest() != 1 {
		t.Fatalf("CreateProjectWithOwnerMembership calls = %d, want 1", store.CreateProjectWithOwnerMembershipCallCountForTest())
	}
}

func TestCreateProjectValidationRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input CreateProjectInput
	}{
		{
			name:  "empty title",
			input: CreateProjectInput{Title: ""},
		},
		{
			name:  "blank title",
			input: CreateProjectInput{Title: "   "},
		},
		{
			name:  "overlong title",
			input: CreateProjectInput{Title: strings.Repeat("a", 161)},
		},
		{
			name: "overlong description",
			input: CreateProjectInput{
				Title:       "The White Port",
				Description: strings.Repeat("a", 1001),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			service := NewService(
				store,
				StaticIDsForTest("project-1", "membership-1"),
				StaticClockForTest(time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)),
			)

			_, err := service.CreateProject(context.Background(), "user-1", tt.input)

			if !errors.Is(err, ErrValidation) {
				t.Fatalf("CreateProject() error = %v, want ErrValidation", err)
			}
			if store.CreateProjectWithOwnerMembershipCallCountForTest() != 0 {
				t.Fatal("CreateProject() persisted invalid input")
			}
		})
	}
}

func TestCreateProjectValidationAcceptsOpenAPILimits(t *testing.T) {
	store := NewMemoryStoreForTest(t)
	service := NewService(
		store,
		StaticIDsForTest("project-1", "membership-1"),
		StaticClockForTest(time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)),
	)

	project, err := service.CreateProject(context.Background(), "user-1", CreateProjectInput{
		Title:       strings.Repeat("a", 160),
		Description: strings.Repeat("b", 1000),
	})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.Title != strings.Repeat("a", 160) {
		t.Fatal("CreateProject() changed a boundary-length title")
	}
	if project.Description != strings.Repeat("b", 1000) {
		t.Fatal("CreateProject() changed a boundary-length description")
	}
}

func TestGetProjectNonMemberReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	_, err := service.GetProject(ctx, "user-2", "project-1")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject() error = %v, want ErrNotFound", err)
	}
}

func TestGetProjectReadsActiveProjectForEveryMemberRole(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleViewer, RoleEditor, RoleAdmin, RoleOwner} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", role, now)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			project, err := service.GetProject(ctx, "user-1", "project-1")
			if err != nil {
				t.Fatalf("GetProject() error = %v", err)
			}
			if project.ID != "project-1" || project.CurrentMembership.Role != role {
				t.Fatalf("GetProject() returned %#v, want project with current %s membership", project, role)
			}
		})
	}
}

func TestGetProjectHidesDeletedProjectsAndRemovedMemberships(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		arrange func(*testing.T, *memoryStoreForTest)
	}{
		{
			name: "deleted project",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.SoftDeleteProjectForTest(t, "project-1", now)
			},
		},
		{
			name: "removed membership",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.RemoveMembershipForTest(t, "project-1", "user-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleOwner, now)
			tt.arrange(t, store)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			_, err := service.GetProject(ctx, "user-1", "project-1")

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetProject() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestUpdateProjectViewerReturnsForbidden(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleViewer, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	_, err := service.UpdateProject(ctx, "user-1", "project-1", UpdateProjectInput{
		Title: stringPtrForTest("Changed"),
	})

	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpdateProject() error = %v, want ErrForbidden", err)
	}
	if store.UpdateProjectCallCountForTest() != 0 {
		t.Fatal("UpdateProject() persisted a viewer mutation")
	}
}

func TestUpdateProjectNonMemberReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	_, err := service.UpdateProject(ctx, "user-2", "project-1", UpdateProjectInput{
		Title: stringPtrForTest("Changed"),
	})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateProject() error = %v, want ErrNotFound", err)
	}
	if store.UpdateProjectCallCountForTest() != 0 {
		t.Fatal("UpdateProject() persisted a non-member mutation")
	}
}

func TestUpdateProjectEditorUpdatesMutableFields(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleEditor, createdAt)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(updatedAt))

	project, err := service.UpdateProject(ctx, "user-1", "project-1", UpdateProjectInput{
		Title:          stringPtrForTest("  The Black Port  "),
		DescriptionSet: true,
		Description:    stringPtrForTest("New notes"),
	})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if project.Title != "The Black Port" {
		t.Fatalf("Project.Title = %q, want trimmed title", project.Title)
	}
	if project.Description != "New notes" {
		t.Fatalf("Project.Description = %q, want New notes", project.Description)
	}
	if project.Revision != 2 {
		t.Fatalf("Project.Revision = %d, want 2", project.Revision)
	}
	if !project.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("Project.UpdatedAt = %s, want %s", project.UpdatedAt, updatedAt)
	}

	project, err = service.UpdateProject(ctx, "user-1", "project-1", UpdateProjectInput{
		DescriptionSet: true,
		Description:    nil,
	})
	if err != nil {
		t.Fatalf("UpdateProject(description null) error = %v", err)
	}
	if project.Description != "" {
		t.Fatalf("Project.Description after null = %q, want empty string", project.Description)
	}
	if project.Revision != 3 {
		t.Fatalf("Project.Revision after null description = %d, want 3", project.Revision)
	}
}

func TestUpdateProjectAdminAndOwnerCanMutate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleAdmin, RoleOwner} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", role, now)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			project, err := service.UpdateProject(ctx, "user-1", "project-1", UpdateProjectInput{
				Title: stringPtrForTest("Changed"),
			})
			if err != nil {
				t.Fatalf("UpdateProject() error = %v", err)
			}
			if project.Title != "Changed" {
				t.Fatalf("Project.Title = %q, want Changed", project.Title)
			}
		})
	}
}

func TestUpdateProjectValidatesOpenAPIRequest(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		input UpdateProjectInput
	}{
		{
			name:  "empty title",
			input: UpdateProjectInput{Title: stringPtrForTest("")},
		},
		{
			name:  "blank title",
			input: UpdateProjectInput{Title: stringPtrForTest("   ")},
		},
		{
			name:  "overlong title",
			input: UpdateProjectInput{Title: stringPtrForTest(strings.Repeat("a", 161))},
		},
		{
			name: "overlong description",
			input: UpdateProjectInput{
				DescriptionSet: true,
				Description:    stringPtrForTest(strings.Repeat("a", 1001)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleEditor, now)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			_, err := service.UpdateProject(ctx, "user-1", "project-1", tt.input)

			if !errors.Is(err, ErrValidation) {
				t.Fatalf("UpdateProject() error = %v, want ErrValidation", err)
			}
			if store.UpdateProjectCallCountForTest() != 0 {
				t.Fatal("UpdateProject() persisted invalid input")
			}
		})
	}
}

func TestUpdateProjectEmptyInputReturnsCurrentProject(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleEditor, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now.Add(time.Hour)))

	project, err := service.UpdateProject(ctx, "user-1", "project-1", UpdateProjectInput{})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	if project.Title != "The White Port" || project.Description != "City notes" || project.Revision != 1 {
		t.Fatalf("UpdateProject() returned %#v, want unchanged current project", project)
	}
	if store.UpdateProjectCallCountForTest() != 0 {
		t.Fatal("UpdateProject() persisted an empty update")
	}
}

func TestUpdateProjectHidesDeletedProjectsAndRemovedMemberships(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		arrange func(*testing.T, *memoryStoreForTest)
	}{
		{
			name: "deleted project",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.SoftDeleteProjectForTest(t, "project-1", now)
			},
		},
		{
			name: "removed membership",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.RemoveMembershipForTest(t, "project-1", "user-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleEditor, now)
			tt.arrange(t, store)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			_, err := service.UpdateProject(ctx, "user-1", "project-1", UpdateProjectInput{
				Title: stringPtrForTest("Changed"),
			})

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("UpdateProject() error = %v, want ErrNotFound", err)
			}
			if store.UpdateProjectCallCountForTest() != 0 {
				t.Fatal("UpdateProject() persisted a hidden project mutation")
			}
		})
	}
}

func TestDeleteProjectOwnerAndAdminSoftDelete(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	deletedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleAdmin, RoleOwner} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", role, createdAt)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(deletedAt))

			err := service.DeleteProject(ctx, "user-1", "project-1")
			if err != nil {
				t.Fatalf("DeleteProject() error = %v", err)
			}
			if !store.DeletedAtForTest(t, "project-1").Equal(deletedAt) {
				t.Fatalf("deleted_at was not set to %s", deletedAt)
			}
			if got := store.StoredProjectForTest(t, "project-1").Revision; got != 2 {
				t.Fatalf("project revision = %d, want 2", got)
			}
			if got := store.LastDeleteProjectInputForTest(t).TombstoneID; got != "00000000-0000-4000-8000-000000000999" {
				t.Fatalf("delete tombstone id = %q", got)
			}

			_, err = service.GetProject(ctx, "user-1", "project-1")
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetProject() after delete error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestDeleteProjectViewerAndEditorReturnForbidden(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleViewer, RoleEditor} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", role, now)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			err := service.DeleteProject(ctx, "user-1", "project-1")

			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("DeleteProject() error = %v, want ErrForbidden", err)
			}
			if store.DeleteProjectCallCountForTest() != 0 {
				t.Fatal("DeleteProject() persisted a forbidden delete")
			}
		})
	}
}

func TestDeleteProjectNonMemberReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	err := service.DeleteProject(ctx, "user-2", "project-1")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteProject() error = %v, want ErrNotFound", err)
	}
	if store.DeleteProjectCallCountForTest() != 0 {
		t.Fatal("DeleteProject() persisted a non-member delete")
	}
}

func TestDeleteProjectHidesDeletedProjectsAndRemovedMemberships(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		arrange func(*testing.T, *memoryStoreForTest)
	}{
		{
			name: "deleted project",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.SoftDeleteProjectForTest(t, "project-1", now)
			},
		},
		{
			name: "removed membership",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.RemoveMembershipForTest(t, "project-1", "user-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleOwner, now)
			tt.arrange(t, store)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			err := service.DeleteProject(ctx, "user-1", "project-1")

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("DeleteProject() error = %v, want ErrNotFound", err)
			}
			if store.DeleteProjectCallCountForTest() != 0 {
				t.Fatal("DeleteProject() persisted a hidden project delete")
			}
		})
	}
}

func TestActorCollectionListProjectsReturnsOnlyActiveVisibleProjects(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-active", "Active", "", now)
	store.AddMembershipForTest(t, "membership-active", "project-active", "user-1", RoleEditor, now)
	store.AddProjectForTest(t, "project-removed", "Removed", "", now)
	store.AddMembershipForTest(t, "membership-removed", "project-removed", "user-1", RoleViewer, now)
	store.RemoveMembershipForTest(t, "project-removed", "user-1")
	store.AddProjectForTest(t, "project-deleted", "Deleted", "", now)
	store.AddMembershipForTest(t, "membership-deleted", "project-deleted", "user-1", RoleOwner, now)
	store.SoftDeleteProjectForTest(t, "project-deleted", now)
	store.AddProjectForTest(t, "project-foreign", "Foreign", "", now)
	store.AddMembershipForTest(t, "membership-foreign", "project-foreign", "user-2", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	page, err := service.ListProjects(ctx, "user-1", ListProjectsInput{Limit: 50})
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}

	if len(page.Data) != 1 || page.Data[0].ID != "project-active" || page.Data[0].CurrentMembership.Role != RoleEditor {
		t.Fatalf("ListProjects() = %#v, want only active visible project", page.Data)
	}
	if page.Meta.HasMore || page.Meta.NextCursor != nil {
		t.Fatalf("ListProjects() meta = %#v, want terminal page", page.Meta)
	}
}

func TestActorCollectionListProjectsUsesStableSessionBoundCursor(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	for _, project := range []struct {
		id        string
		createdAt time.Time
	}{
		{id: "project-a", createdAt: createdAt.Add(-time.Minute)},
		{id: "project-b", createdAt: createdAt},
		{id: "project-c", createdAt: createdAt},
	} {
		store.AddProjectForTest(t, project.id, project.id, "", project.createdAt)
		store.AddMembershipForTest(t, "membership-"+project.id, project.id, "user-1", RoleViewer, project.createdAt)
	}
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(createdAt))

	first, err := service.ListProjects(ctx, "user-1", ListProjectsInput{Limit: 1})
	if err != nil {
		t.Fatalf("ListProjects() first page error = %v", err)
	}
	if got := projectIDsForTest(first.Data); !slices.Equal(got, []string{"project-c"}) {
		t.Fatalf("ListProjects() first page IDs = %v, want [project-c]", got)
	}
	if !first.Meta.HasMore || first.Meta.NextCursor == nil || *first.Meta.NextCursor == "" {
		t.Fatalf("ListProjects() first page meta = %#v, want a next cursor", first.Meta)
	}

	second, err := service.ListProjects(ctx, "user-1", ListProjectsInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil {
		t.Fatalf("ListProjects() second page error = %v", err)
	}
	if got := projectIDsForTest(second.Data); !slices.Equal(got, []string{"project-b"}) {
		t.Fatalf("ListProjects() second page IDs = %v, want [project-b]", got)
	}
	if !second.Meta.HasMore || second.Meta.NextCursor == nil {
		t.Fatalf("ListProjects() second page meta = %#v, want a next cursor", second.Meta)
	}

	third, err := service.ListProjects(ctx, "user-1", ListProjectsInput{Limit: 1, Cursor: *second.Meta.NextCursor})
	if err != nil {
		t.Fatalf("ListProjects() third page error = %v", err)
	}
	if got := projectIDsForTest(third.Data); !slices.Equal(got, []string{"project-a"}) {
		t.Fatalf("ListProjects() third page IDs = %v, want [project-a]", got)
	}
	if third.Meta.HasMore || third.Meta.NextCursor != nil {
		t.Fatalf("ListProjects() third page meta = %#v, want terminal page", third.Meta)
	}

	if _, err := service.ListProjects(ctx, "user-2", ListProjectsInput{Limit: 1, Cursor: *first.Meta.NextCursor}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjects() cross-session cursor error = %v, want ErrValidation", err)
	}
	if _, err := service.ListProjects(ctx, "user-1", ListProjectsInput{Limit: 1, Cursor: "not-a-cursor"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjects() malformed cursor error = %v, want ErrValidation", err)
	}
}

func TestListProjectsDeletedStateUsesDeletedClocksAndDedicatedCursor(t *testing.T) {
	ctx := context.Background()
	deletedAt := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	for _, fixture := range []struct {
		projectID string
		role      Role
		deletedAt time.Time
	}{
		{projectID: "project-old", role: RoleAdmin, deletedAt: deletedAt.Add(-time.Hour)},
		{projectID: "project-new", role: RoleOwner, deletedAt: deletedAt},
		{projectID: "project-editor", role: RoleEditor, deletedAt: deletedAt.Add(time.Hour)},
	} {
		store.AddProjectForTest(t, fixture.projectID, fixture.projectID, "", fixture.deletedAt.Add(-time.Hour))
		store.AddMembershipForTest(t, "membership-"+fixture.projectID, fixture.projectID, "user-1", fixture.role, fixture.deletedAt.Add(-time.Hour))
		store.SoftDeleteProjectForTest(t, fixture.projectID, fixture.deletedAt)
	}
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(deletedAt))

	first, err := service.ListProjects(ctx, "user-1", ListProjectsInput{State: ProjectListDeleted, Limit: 1})
	if err != nil {
		t.Fatalf("ListProjects(deleted) first page error = %v", err)
	}
	if got := projectIDsForTest(first.Data); !slices.Equal(got, []string{"project-new"}) {
		t.Fatalf("ListProjects(deleted) first page IDs = %v", got)
	}
	if first.Data[0].DeletedAt == nil || !first.Data[0].DeletedAt.Equal(deletedAt) ||
		first.Data[0].RestoreUntil == nil || !first.Data[0].RestoreUntil.Equal(deletedAt.Add(7*24*time.Hour)) {
		t.Fatalf("ListProjects(deleted) clocks = deleted %v restore %v", first.Data[0].DeletedAt, first.Data[0].RestoreUntil)
	}
	if !first.Meta.HasMore || first.Meta.NextCursor == nil {
		t.Fatalf("ListProjects(deleted) first page meta = %#v", first.Meta)
	}

	second, err := service.ListProjects(ctx, "user-1", ListProjectsInput{
		State: ProjectListDeleted, Limit: 1, Cursor: *first.Meta.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListProjects(deleted) second page error = %v", err)
	}
	if got := projectIDsForTest(second.Data); !slices.Equal(got, []string{"project-old"}) {
		t.Fatalf("ListProjects(deleted) second page IDs = %v", got)
	}
	if second.Meta.HasMore || second.Meta.NextCursor != nil {
		t.Fatalf("ListProjects(deleted) second page meta = %#v", second.Meta)
	}

	activeStore := NewMemoryStoreForTest(t)
	for _, id := range []string{"active-a", "active-b"} {
		activeStore.AddProjectForTest(t, id, id, "", deletedAt)
		activeStore.AddMembershipForTest(t, "membership-"+id, id, "user-1", RoleOwner, deletedAt)
	}
	activeService := NewService(activeStore, StaticIDsForTest("", ""), StaticClockForTest(deletedAt))
	activePage, err := activeService.ListProjects(ctx, "user-1", ListProjectsInput{Limit: 1})
	if err != nil || activePage.Meta.NextCursor == nil {
		t.Fatalf("ListProjects(active) cursor = %#v, err = %v", activePage.Meta, err)
	}
	if _, err := service.ListProjects(ctx, "user-1", ListProjectsInput{
		State: ProjectListDeleted, Limit: 1, Cursor: *activePage.Meta.NextCursor,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjects(deleted) active cursor error = %v, want ErrValidation", err)
	}
}

func TestListProjectsRejectsUnknownState(t *testing.T) {
	service := NewService(NewMemoryStoreForTest(t), StaticIDsForTest("", ""), StaticClockForTest(time.Now()))
	if _, err := service.ListProjects(context.Background(), "user-1", ListProjectsInput{State: "archived"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListProjects() error = %v, want ErrValidation", err)
	}
}

func TestRestoreProjectPassesActorAndProjectToStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "Project", "", now.Add(-time.Hour))
	store.AddMembershipForTest(t, "membership-1", "project-1", "user-1", RoleOwner, now.Add(-time.Hour))
	store.SoftDeleteProjectForTest(t, "project-1", now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	restored, err := service.RestoreProject(ctx, "user-1", "project-1")
	if err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if restored.ID != "project-1" {
		t.Fatalf("RestoreProject() = %#v", restored)
	}
	want := RestoreProjectStoreInput{
		ActorUserID: "user-1",
		ProjectID:   "project-1",
	}
	if store.lastRestoreProjectInput != want {
		t.Fatalf("RestoreProject() input = %#v, want %#v", store.lastRestoreProjectInput, want)
	}
}

func TestListProjectMembersOwnerAndAdminReturnActiveMembersWithPII(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleOwner, RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "", now)
			store.AddMembershipForTest(t, "membership-actor", "project-1", "user-1", role, now)
			store.AddMembershipForTest(t, "membership-reader", "project-1", "user-2", RoleViewer, now)
			store.AddUserEmailForTest(t, "user-2", "reader@example.test")
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			members, err := service.ListProjectMembers(ctx, "user-1", "project-1", ListProjectMembersInput{Limit: 50})
			if err != nil {
				t.Fatalf("ListProjectMembers() error = %v", err)
			}
			if len(members) != 2 {
				t.Fatalf("ListProjectMembers() returned %d members, want 2", len(members))
			}
			reader := memberByIDForTest(t, members, "membership-reader")
			if reader.Email != "reader@example.test" || reader.Role != RoleViewer || reader.Status != MembershipStatusActive {
				t.Fatalf("reader member = %#v, want active viewer with email", reader)
			}
		})
	}
}

func TestListProjectMembersViewerAndEditorReturnForbidden(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleViewer, RoleEditor} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "", now)
			store.AddMembershipForTest(t, "membership-actor", "project-1", "user-1", role, now)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			_, err := service.ListProjectMembers(ctx, "user-1", "project-1", ListProjectMembersInput{Limit: 50})

			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("ListProjectMembers() error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestListProjectMembersNonMemberReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-1", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	_, err := service.ListProjectMembers(ctx, "user-2", "project-1", ListProjectMembersInput{Limit: 50})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListProjectMembers() error = %v, want ErrNotFound", err)
	}
}

func TestUpdateProjectMemberRoleAdminUpdatesNonOwner(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "", createdAt)
	store.AddMembershipForTest(t, "membership-admin", "project-1", "user-1", RoleAdmin, createdAt)
	store.AddMembershipForTest(t, "membership-viewer", "project-1", "user-2", RoleViewer, createdAt)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(updatedAt))

	membership, err := service.UpdateProjectMemberRole(ctx, "user-1", "project-1", "membership-viewer", UpdateProjectMemberRoleInput{Role: RoleEditor})
	if err != nil {
		t.Fatalf("UpdateProjectMemberRole() error = %v", err)
	}

	if membership.Role != RoleEditor || !membership.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated membership = %#v, want editor updated at %s", membership, updatedAt)
	}
	stored := store.ActiveMembershipForTest(t, "project-1", "user-2")
	if stored.Role != RoleEditor {
		t.Fatalf("stored role = %s, want editor", stored.Role)
	}
}

func TestUpdateProjectMemberRoleRejectsOwnerTargetAndOwnerRole(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		memberID string
		role     Role
	}{
		{name: "target owner", memberID: "membership-owner", role: RoleViewer},
		{name: "requested owner role", memberID: "membership-viewer", role: RoleOwner},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "", now)
			store.AddMembershipForTest(t, "membership-owner", "project-1", "user-1", RoleOwner, now)
			store.AddMembershipForTest(t, "membership-viewer", "project-1", "user-2", RoleViewer, now)
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			_, err := service.UpdateProjectMemberRole(ctx, "user-1", "project-1", tt.memberID, UpdateProjectMemberRoleInput{Role: tt.role})

			if !errors.Is(err, ErrConflict) {
				t.Fatalf("UpdateProjectMemberRole() error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestUpdateProjectMemberRoleNonAdminForbiddenAndCrossProjectNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		actorRole   Role
		projectID   string
		memberID    string
		wantErr     error
		crossMember bool
	}{
		{name: "viewer", actorRole: RoleViewer, projectID: "project-1", memberID: "membership-target", wantErr: ErrForbidden},
		{name: "editor", actorRole: RoleEditor, projectID: "project-1", memberID: "membership-target", wantErr: ErrForbidden},
		{name: "cross project", actorRole: RoleOwner, projectID: "project-1", memberID: "membership-cross", wantErr: ErrNotFound, crossMember: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "", now)
			store.AddMembershipForTest(t, "membership-actor", "project-1", "user-1", tt.actorRole, now)
			store.AddMembershipForTest(t, "membership-target", "project-1", "user-2", RoleViewer, now)
			if tt.crossMember {
				store.AddProjectForTest(t, "project-2", "Other", "", now)
				store.AddMembershipForTest(t, "membership-cross", "project-2", "user-3", RoleViewer, now)
			}
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			_, err := service.UpdateProjectMemberRole(ctx, "user-1", tt.projectID, tt.memberID, UpdateProjectMemberRoleInput{Role: RoleEditor})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("UpdateProjectMemberRole() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRemoveProjectMemberAdminRemovesAccessImmediately(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	removedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "", createdAt)
	store.AddMembershipForTest(t, "membership-admin", "project-1", "user-1", RoleAdmin, createdAt)
	store.AddMembershipForTest(t, "membership-viewer", "project-1", "user-2", RoleViewer, createdAt)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(removedAt))

	err := service.RemoveProjectMember(ctx, "user-1", "project-1", "membership-viewer")
	if err != nil {
		t.Fatalf("RemoveProjectMember() error = %v", err)
	}

	membership, ok := store.MembershipForTest("project-1", "user-2")
	if !ok || membership.Status != MembershipStatusRemoved || !membership.UpdatedAt.Equal(removedAt) {
		t.Fatalf("stored membership = %#v ok %t, want removed at %s", membership, ok, removedAt)
	}
	_, err = service.GetProject(ctx, "user-2", "project-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject() after removal error = %v, want ErrNotFound", err)
	}
}

func TestRemoveProjectMemberRejectsOwnerTarget(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-1", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

	err := service.RemoveProjectMember(ctx, "user-1", "project-1", "membership-owner")

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("RemoveProjectMember() owner error = %v, want ErrConflict", err)
	}
}

func TestRemoveProjectMemberNonAdminForbiddenAndCrossProjectNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		actorRole   Role
		projectID   string
		memberID    string
		wantErr     error
		crossMember bool
	}{
		{name: "viewer", actorRole: RoleViewer, projectID: "project-1", memberID: "membership-target", wantErr: ErrForbidden},
		{name: "editor", actorRole: RoleEditor, projectID: "project-1", memberID: "membership-target", wantErr: ErrForbidden},
		{name: "cross project", actorRole: RoleOwner, projectID: "project-1", memberID: "membership-cross", wantErr: ErrNotFound, crossMember: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "", now)
			store.AddMembershipForTest(t, "membership-actor", "project-1", "user-1", tt.actorRole, now)
			store.AddMembershipForTest(t, "membership-target", "project-1", "user-2", RoleViewer, now)
			if tt.crossMember {
				store.AddProjectForTest(t, "project-2", "Other", "", now)
				store.AddMembershipForTest(t, "membership-cross", "project-2", "user-3", RoleViewer, now)
			}
			service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))

			err := service.RemoveProjectMember(ctx, "user-1", tt.projectID, tt.memberID)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RemoveProjectMember() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

type memoryUserStateForTest struct {
	email       string
	displayName string
	verified    bool
	disabled    bool
}

type memoryStoreForTest struct {
	projectsByID                          map[string]ProjectRecord
	membershipsByProjectIDAndUserID       map[string]ProjectMembershipRecord
	userStatesByID                        map[string]memoryUserStateForTest
	invitationsByID                       map[string]ProjectInvitationRecord
	deletedProjectIDs                     map[string]bool
	deletedAtByProjectID                  map[string]time.Time
	createProjectWithOwnerMembershipCalls int
	updateProjectCalls                    int
	deleteProjectCalls                    int
	createProjectInvitationCalls          int
	lastProjectInvitationInput            CreateProjectInvitationStoreInput
	lastDeleteProjectInput                DeleteProjectStoreInput
	lastRestoreProjectInput               RestoreProjectStoreInput
	lastCancelInvitationInput             CancelProjectInvitationStoreInput
	lastAcceptInvitationInput             AcceptProjectInvitationStoreInput
}

func NewMemoryStoreForTest(t *testing.T) *memoryStoreForTest {
	t.Helper()

	return &memoryStoreForTest{
		projectsByID:                    make(map[string]ProjectRecord),
		membershipsByProjectIDAndUserID: make(map[string]ProjectMembershipRecord),
		userStatesByID:                  make(map[string]memoryUserStateForTest),
		invitationsByID:                 make(map[string]ProjectInvitationRecord),
		deletedProjectIDs:               make(map[string]bool),
		deletedAtByProjectID:            make(map[string]time.Time),
	}
}

func (s *memoryStoreForTest) CreateProjectWithOwnerMembership(_ context.Context, input CreateProjectStoreInput) (ProjectRecord, error) {
	s.createProjectWithOwnerMembershipCalls++

	membership := ProjectMembershipRecord{
		ID:        input.OwnerMembershipID,
		ProjectID: input.ProjectID,
		UserID:    input.OwnerUserID,
		Role:      RoleOwner,
		Status:    MembershipStatusActive,
		CreatedAt: input.CreatedAt,
		UpdatedAt: input.CreatedAt,
	}
	project := ProjectRecord{
		ID:                input.ProjectID,
		Title:             input.Title,
		Description:       input.Description,
		Demo:              input.Demo,
		BookCount:         0,
		ChapterCount:      0,
		WordCount:         0,
		Revision:          1,
		CreatedAt:         input.CreatedAt,
		UpdatedAt:         input.CreatedAt,
		CurrentMembership: membership,
	}
	s.projectsByID[project.ID] = project
	s.membershipsByProjectIDAndUserID[membershipKeyForTest(project.ID, membership.UserID)] = membership

	return project, nil
}

func (s *memoryStoreForTest) GetProject(_ context.Context, actorUserID, projectID string) (ProjectRecord, error) {
	project, ok := s.activeProjectForActor(projectID, actorUserID)
	if !ok {
		return ProjectRecord{}, ErrNotFound
	}
	return project, nil
}

func (s *memoryStoreForTest) DemoProject(_ context.Context, ownerUserID string) (ProjectRecord, error) {
	for _, project := range s.projectsByID {
		if project.Demo && project.DeletedAt == nil && project.CurrentMembership.UserID == ownerUserID && project.CurrentMembership.Role == RoleOwner {
			return project, nil
		}
	}
	return ProjectRecord{}, ErrNotFound
}

func (s *memoryStoreForTest) UpdateProject(_ context.Context, input UpdateProjectStoreInput) (ProjectRecord, error) {
	project, ok := s.activeProjectForActor(input.ProjectID, input.ActorUserID)
	if !ok {
		return ProjectRecord{}, ErrNotFound
	}
	s.updateProjectCalls++
	project.Title = input.Title
	project.Description = input.Description
	project.Revision++
	project.UpdatedAt = input.UpdatedAt
	s.projectsByID[input.ProjectID] = project
	return project, nil
}

func (s *memoryStoreForTest) DeleteProject(_ context.Context, input DeleteProjectStoreInput) error {
	project, ok := s.activeProjectForActor(input.ProjectID, input.ActorUserID)
	if !ok {
		return ErrNotFound
	}
	s.deleteProjectCalls++
	s.lastDeleteProjectInput = input
	project.Revision++
	project.UpdatedAt = input.DeletedAt
	s.projectsByID[input.ProjectID] = project
	s.deletedProjectIDs[input.ProjectID] = true
	s.deletedAtByProjectID[input.ProjectID] = input.DeletedAt
	return nil
}

func (s *memoryStoreForTest) RestoreProject(_ context.Context, input RestoreProjectStoreInput) (ProjectRecord, error) {
	s.lastRestoreProjectInput = input
	project, ok := s.projectsByID[input.ProjectID]
	if !ok || !s.deletedProjectIDs[input.ProjectID] {
		return ProjectRecord{}, ErrNotFound
	}
	membership, ok := s.membershipsByProjectIDAndUserID[membershipKeyForTest(input.ProjectID, input.ActorUserID)]
	if !ok || membership.Status != MembershipStatusActive {
		return ProjectRecord{}, ErrNotFound
	}
	if membership.Role != RoleAdmin && membership.Role != RoleOwner {
		return ProjectRecord{}, ErrForbidden
	}
	delete(s.deletedProjectIDs, input.ProjectID)
	delete(s.deletedAtByProjectID, input.ProjectID)
	project.CurrentMembership = membership
	return project, nil
}

func (s *memoryStoreForTest) ListProjectsForActor(_ context.Context, input ListProjectsStoreInput) ([]ProjectRecord, error) {
	var records []ProjectRecord
	for projectID := range s.projectsByID {
		project := s.projectsByID[projectID]
		membership, hasMembership := s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, input.ActorUserID)]
		if !hasMembership || membership.Status != MembershipStatusActive {
			continue
		}
		position := project.CreatedAt
		if input.State == ProjectListDeleted {
			deletedAt, deleted := s.deletedAtByProjectID[projectID]
			if !deleted || (membership.Role != RoleAdmin && membership.Role != RoleOwner) {
				continue
			}
			position = deletedAt
			deletedClock := deletedAt
			restore := deletedAt.Add(7 * 24 * time.Hour)
			project.DeletedAt = &deletedClock
			project.RestoreUntil = &restore
		} else if s.deletedProjectIDs[projectID] {
			continue
		}
		if input.HasCursor && !collectionItemFollowsCursorForTest(position, project.ID, input.CursorCreatedAt, input.CursorID) {
			continue
		}
		project.CurrentMembership = membership
		records = append(records, project)
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i].CreatedAt, records[j].CreatedAt
		if input.State == ProjectListDeleted {
			left, right = *records[i].DeletedAt, *records[j].DeletedAt
		}
		if left.Equal(right) {
			return records[i].ID > records[j].ID
		}
		return left.After(right)
	})
	if input.Limit > 0 && len(records) > input.Limit {
		records = records[:input.Limit]
	}
	return records, nil
}

func (s *memoryStoreForTest) ListProjectMembers(_ context.Context, input ListProjectMembersStoreInput) ([]ProjectMembershipRecord, error) {
	project, ok := s.activeProjectForActor(input.ProjectID, input.ActorUserID)
	if !ok {
		return nil, ErrNotFound
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return nil, ErrForbidden
	}

	var members []ProjectMembershipRecord
	for _, membership := range s.membershipsByProjectIDAndUserID {
		if membership.ProjectID == input.ProjectID && membership.Status == MembershipStatusActive {
			members = append(members, s.membershipWithPII(membership))
		}
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].ID < members[j].ID
	})
	if input.Limit > 0 && len(members) > input.Limit {
		members = members[:input.Limit]
	}
	return members, nil
}

func (s *memoryStoreForTest) UpdateProjectMemberRole(_ context.Context, input UpdateProjectMemberRoleStoreInput) (ProjectMembershipRecord, error) {
	project, ok := s.activeProjectForActor(input.ProjectID, input.ActorUserID)
	if !ok {
		return ProjectMembershipRecord{}, ErrNotFound
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return ProjectMembershipRecord{}, ErrForbidden
	}
	if input.Role == RoleOwner {
		return ProjectMembershipRecord{}, ErrConflict
	}
	membership, key, ok := s.activeMembershipByID(input.ProjectID, input.MemberID)
	if !ok {
		return ProjectMembershipRecord{}, ErrNotFound
	}
	if membership.Role == RoleOwner {
		return ProjectMembershipRecord{}, ErrConflict
	}

	membership.Role = input.Role
	membership.UpdatedAt = input.UpdatedAt
	s.membershipsByProjectIDAndUserID[key] = membership
	return s.membershipWithPII(membership), nil
}

func (s *memoryStoreForTest) RemoveProjectMember(_ context.Context, input RemoveProjectMemberStoreInput) error {
	project, ok := s.activeProjectForActor(input.ProjectID, input.ActorUserID)
	if !ok {
		return ErrNotFound
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return ErrForbidden
	}
	membership, key, ok := s.activeMembershipByID(input.ProjectID, input.MemberID)
	if !ok {
		return ErrNotFound
	}
	if membership.Role == RoleOwner {
		return ErrConflict
	}

	membership.Status = MembershipStatusRemoved
	membership.UpdatedAt = input.RemovedAt
	s.membershipsByProjectIDAndUserID[key] = membership
	return nil
}

func (s *memoryStoreForTest) CreateProjectInvitation(_ context.Context, input CreateProjectInvitationStoreInput) (ProjectInvitationRecord, error) {
	project, ok := s.projectsByID[input.ProjectID]
	if !ok || s.deletedProjectIDs[input.ProjectID] {
		return ProjectInvitationRecord{}, ErrNotFound
	}
	for _, invitation := range s.invitationsByID {
		if invitation.ProjectID == input.ProjectID &&
			invitation.EmailHash == input.EmailHash &&
			invitation.Status == ProjectInvitationStatusPending &&
			invitation.AcceptedAt == nil &&
			invitation.CanceledAt == nil &&
			input.CreatedAt.Before(invitation.ExpiresAt) {
			return ProjectInvitationRecord{}, ErrConflict
		}
	}
	for _, membership := range s.membershipsByProjectIDAndUserID {
		userState, ok := s.userStatesByID[membership.UserID]
		if membership.ProjectID == input.ProjectID &&
			membership.Status == MembershipStatusActive &&
			ok &&
			userState.email == input.Email {
			return ProjectInvitationRecord{}, ErrConflict
		}
	}
	s.createProjectInvitationCalls++
	s.lastProjectInvitationInput = input
	invitation := ProjectInvitationRecord{
		ID:            input.ID,
		ProjectID:     input.ProjectID,
		ProjectTitle:  project.Title,
		InviterUserID: input.ActorUserID,
		Email:         input.Email,
		EmailHash:     input.EmailHash,
		Role:          input.Role,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     input.ExpiresAt,
		CreatedAt:     input.CreatedAt,
		UpdatedAt:     input.CreatedAt,
	}
	s.invitationsByID[invitation.ID] = invitation
	return invitation, nil
}

func (s *memoryStoreForTest) ListProjectInvitations(_ context.Context, input ListProjectInvitationsStoreInput) ([]ProjectInvitationRecord, error) {
	project, ok := s.activeProjectForActor(input.ProjectID, input.ActorUserID)
	if !ok || !CanManageMembers(project.CurrentMembership.Role) {
		if ok {
			return nil, ErrForbidden
		}
		return nil, ErrNotFound
	}
	var invitations []ProjectInvitationRecord
	for _, invitation := range s.invitationsByID {
		if invitation.ProjectID == input.ProjectID {
			invitations = append(invitations, invitation)
		}
	}
	return invitations, nil
}

func (s *memoryStoreForTest) ResolveProjectInvitationMemberAccessForActor(_ context.Context, input ResolveProjectInvitationMemberAccessStoreInput) (ProjectInvitationMemberAccess, error) {
	invitation, ok := s.invitationsByID[input.InvitationID]
	if !ok {
		return ProjectInvitationMemberAccess{}, ErrNotFound
	}
	project, ok := s.activeProjectForActor(invitation.ProjectID, input.ActorUserID)
	if !ok {
		return ProjectInvitationMemberAccess{}, ErrNotFound
	}
	return ProjectInvitationMemberAccess{
		ProjectID:    invitation.ProjectID,
		InvitationID: invitation.ID,
		ProjectRole:  project.CurrentMembership.Role,
	}, nil
}

func (s *memoryStoreForTest) ResolveProjectInvitationInviteeAccessForActor(_ context.Context, input ResolveProjectInvitationInviteeAccessStoreInput) (ProjectInvitationInviteeAccess, error) {
	invitation, ok := s.invitationsByID[input.InvitationID]
	if !ok || !s.invitationVisibleToInvitee(invitation, input.ActorUserID, input.EmailHash) {
		return ProjectInvitationInviteeAccess{}, ErrNotFound
	}
	return ProjectInvitationInviteeAccess{ProjectID: invitation.ProjectID, InvitationID: invitation.ID}, nil
}

func (s *memoryStoreForTest) CancelProjectInvitation(_ context.Context, input CancelProjectInvitationStoreInput) (ProjectInvitationRecord, error) {
	invitation, ok := s.invitationsByID[input.InvitationID]
	if !ok || invitation.ProjectID != input.ProjectID {
		return ProjectInvitationRecord{}, ErrNotFound
	}
	project, ok := s.activeProjectForActor(invitation.ProjectID, input.ActorUserID)
	if !ok {
		return ProjectInvitationRecord{}, ErrNotFound
	}
	if !CanManageMembers(project.CurrentMembership.Role) {
		return ProjectInvitationRecord{}, ErrForbidden
	}
	if invitation.Status != ProjectInvitationStatusPending {
		return ProjectInvitationRecord{}, ErrConflict
	}
	s.lastCancelInvitationInput = input
	invitation.Status = ProjectInvitationStatusCanceled
	invitation.CanceledAt = &input.CanceledAt
	invitation.UpdatedAt = input.CanceledAt
	s.invitationsByID[input.InvitationID] = invitation
	return invitation, nil
}

func (s *memoryStoreForTest) AcceptProjectInvitation(_ context.Context, input AcceptProjectInvitationStoreInput) (ProjectMembershipRecord, error) {
	invitation, ok := s.invitationsByID[input.InvitationID]
	if !ok || invitation.ProjectID != input.ProjectID || !s.invitationVisibleToInvitee(invitation, input.UserID, input.EmailHash) {
		return ProjectMembershipRecord{}, ErrNotFound
	}
	if invitation.Status != ProjectInvitationStatusPending ||
		invitation.AcceptedAt != nil ||
		invitation.CanceledAt != nil ||
		!input.AcceptedAt.Before(invitation.ExpiresAt) {
		return ProjectMembershipRecord{}, ErrConflict
	}
	s.lastAcceptInvitationInput = input
	key := membershipKeyForTest(invitation.ProjectID, input.UserID)
	membership, exists := s.membershipsByProjectIDAndUserID[key]
	if exists {
		if membership.Status == MembershipStatusActive {
			return ProjectMembershipRecord{}, ErrConflict
		}
		membership.Role = invitation.Role
		membership.Status = MembershipStatusActive
		membership.UpdatedAt = input.AcceptedAt
	} else {
		membership = ProjectMembershipRecord{
			ID:        input.MembershipID,
			ProjectID: invitation.ProjectID,
			UserID:    input.UserID,
			Role:      invitation.Role,
			Status:    MembershipStatusActive,
			CreatedAt: input.AcceptedAt,
			UpdatedAt: input.AcceptedAt,
		}
	}
	invitation.Status = ProjectInvitationStatusAccepted
	invitation.AcceptedAt = &input.AcceptedAt
	invitation.UpdatedAt = input.AcceptedAt
	s.membershipsByProjectIDAndUserID[key] = membership
	s.invitationsByID[input.InvitationID] = invitation
	return membership, nil
}

func (s *memoryStoreForTest) ListProjectInvitationsForActor(_ context.Context, input ListProjectInvitationsForActorStoreInput) ([]ProjectInvitationRecord, error) {
	actorEmail, ok := s.activeVerifiedUserEmail(input.ActorUserID)
	if !ok {
		return nil, nil
	}
	var invitations []ProjectInvitationRecord
	for _, invitation := range s.invitationsByID {
		if invitation.EmailHash == input.EmailHash &&
			invitation.Email == actorEmail &&
			!s.deletedProjectIDs[invitation.ProjectID] &&
			(!input.HasCursor || collectionItemFollowsCursorForTest(invitation.CreatedAt, invitation.ID, input.CursorCreatedAt, input.CursorID)) {
			invitations = append(invitations, invitation)
		}
	}
	sort.Slice(invitations, func(i, j int) bool {
		if invitations[i].CreatedAt.Equal(invitations[j].CreatedAt) {
			return invitations[i].ID > invitations[j].ID
		}
		return invitations[i].CreatedAt.After(invitations[j].CreatedAt)
	})
	if input.Limit > 0 && len(invitations) > input.Limit {
		invitations = invitations[:input.Limit]
	}
	return invitations, nil
}

func collectionItemFollowsCursorForTest(createdAt time.Time, id string, cursorCreatedAt time.Time, cursorID string) bool {
	return createdAt.Before(cursorCreatedAt) || createdAt.Equal(cursorCreatedAt) && id < cursorID
}

func (s *memoryStoreForTest) invitationVisibleToInvitee(invitation ProjectInvitationRecord, userID, emailHash string) bool {
	userEmail, ok := s.activeVerifiedUserEmail(userID)
	return ok &&
		!s.deletedProjectIDs[invitation.ProjectID] &&
		invitation.EmailHash == emailHash &&
		invitation.Email == userEmail
}

func (s *memoryStoreForTest) activeVerifiedUserEmail(userID string) (string, bool) {
	userState, ok := s.userStatesByID[userID]
	if !ok || !userState.verified || userState.disabled {
		return "", false
	}
	return userState.email, true
}

func (s *memoryStoreForTest) activeProjectForActor(projectID, userID string) (ProjectRecord, bool) {
	if s.deletedProjectIDs[projectID] {
		return ProjectRecord{}, false
	}
	project, ok := s.projectsByID[projectID]
	if !ok {
		return ProjectRecord{}, false
	}
	membership, ok := s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, userID)]
	if !ok || membership.Status != MembershipStatusActive {
		return ProjectRecord{}, false
	}
	project.CurrentMembership = membership
	return project, true
}

func (s *memoryStoreForTest) activeMembershipByID(projectID, memberID string) (ProjectMembershipRecord, string, bool) {
	for key, membership := range s.membershipsByProjectIDAndUserID {
		if membership.ID == memberID && membership.ProjectID == projectID && membership.Status == MembershipStatusActive {
			return membership, key, true
		}
	}
	return ProjectMembershipRecord{}, "", false
}

func (s *memoryStoreForTest) membershipWithPII(membership ProjectMembershipRecord) ProjectMembershipRecord {
	userState, ok := s.userStatesByID[membership.UserID]
	if !ok {
		return membership
	}
	membership.Email = userState.email
	if userState.displayName != "" {
		displayName := userState.displayName
		membership.DisplayName = &displayName
	}
	return membership
}

func (s *memoryStoreForTest) AddProjectForTest(t *testing.T, projectID, title, description string, now time.Time) {
	t.Helper()

	s.projectsByID[projectID] = ProjectRecord{
		ID:           projectID,
		Title:        title,
		Description:  description,
		BookCount:    0,
		ChapterCount: 0,
		WordCount:    0,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func (s *memoryStoreForTest) AddMembershipForTest(t *testing.T, membershipID, projectID, userID string, role Role, now time.Time) {
	t.Helper()

	s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, userID)] = ProjectMembershipRecord{
		ID:        membershipID,
		ProjectID: projectID,
		UserID:    userID,
		Role:      role,
		Status:    MembershipStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *memoryStoreForTest) AddUserEmailForTest(t *testing.T, userID, email string) {
	t.Helper()

	s.AddUserEmailWithStateForTest(t, userID, email, true, false)
}

func (s *memoryStoreForTest) AddUserEmailWithStateForTest(t *testing.T, userID, email string, verified, disabled bool) {
	t.Helper()

	s.userStatesByID[userID] = memoryUserStateForTest{
		email:       normalizeInvitationEmail(email),
		displayName: "Project Owner",
		verified:    verified,
		disabled:    disabled,
	}
}

func (s *memoryStoreForTest) RemoveMembershipForTest(t *testing.T, projectID, userID string) {
	t.Helper()

	membership, ok := s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, userID)]
	if !ok {
		t.Fatal("expected membership to remove")
	}
	membership.Status = MembershipStatusRemoved
	s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, userID)] = membership
}

func (s *memoryStoreForTest) SoftDeleteProjectForTest(t *testing.T, projectID string, deletedAt time.Time) {
	t.Helper()

	if _, ok := s.projectsByID[projectID]; !ok {
		t.Fatal("expected project to soft delete")
	}
	s.deletedProjectIDs[projectID] = true
	s.deletedAtByProjectID[projectID] = deletedAt
}

func (s *memoryStoreForTest) StoredProjectForTest(t *testing.T, projectID string) ProjectRecord {
	t.Helper()

	project, ok := s.projectsByID[projectID]
	if !ok {
		t.Fatal("expected stored project")
	}
	return project
}

func (s *memoryStoreForTest) DeletedAtForTest(t *testing.T, projectID string) time.Time {
	t.Helper()

	deletedAt, ok := s.deletedAtByProjectID[projectID]
	if !ok {
		t.Fatal("expected project to be soft deleted")
	}
	return deletedAt
}

func (s *memoryStoreForTest) ActiveMembershipForTest(t *testing.T, projectID, userID string) ProjectMembershipRecord {
	t.Helper()

	membership, ok := s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, userID)]
	if !ok {
		t.Fatal("expected active membership was not created")
	}
	return membership
}

func (s *memoryStoreForTest) MembershipForTest(projectID, userID string) (ProjectMembershipRecord, bool) {
	membership, ok := s.membershipsByProjectIDAndUserID[membershipKeyForTest(projectID, userID)]
	return membership, ok
}

func (s *memoryStoreForTest) AddProjectInvitationForTest(t *testing.T, invitation ProjectInvitationRecord) ProjectInvitationRecord {
	t.Helper()

	s.invitationsByID[invitation.ID] = invitation
	return invitation
}

func (s *memoryStoreForTest) ProjectInvitationForTest(t *testing.T, invitationID string) ProjectInvitationRecord {
	t.Helper()

	invitation, ok := s.invitationsByID[invitationID]
	if !ok {
		t.Fatal("expected stored invitation")
	}
	return invitation
}

func (s *memoryStoreForTest) CreateProjectWithOwnerMembershipCallCountForTest() int {
	return s.createProjectWithOwnerMembershipCalls
}

func (s *memoryStoreForTest) UpdateProjectCallCountForTest() int {
	return s.updateProjectCalls
}

func (s *memoryStoreForTest) DeleteProjectCallCountForTest() int {
	return s.deleteProjectCalls
}

func (s *memoryStoreForTest) LastDeleteProjectInputForTest(t *testing.T) DeleteProjectStoreInput {
	t.Helper()
	if s.lastDeleteProjectInput.ProjectID == "" {
		t.Fatal("expected project delete input")
	}
	return s.lastDeleteProjectInput
}

func (s *memoryStoreForTest) CreateProjectInvitationCallCountForTest() int {
	return s.createProjectInvitationCalls
}

func (s *memoryStoreForTest) LastProjectInvitationEmailHashForTest(t *testing.T) string {
	t.Helper()
	if s.lastProjectInvitationInput.EmailHash == "" {
		t.Fatal("expected project invitation create input")
	}
	return s.lastProjectInvitationInput.EmailHash
}

func stringPtrForTest(value string) *string {
	return &value
}

func memberByIDForTest(t *testing.T, members []ProjectMembershipRecord, id string) ProjectMembershipRecord {
	t.Helper()
	for _, member := range members {
		if member.ID == id {
			return member
		}
	}
	t.Fatalf("missing member %q in %#v", id, members)
	return ProjectMembershipRecord{}
}

func membershipKeyForTest(projectID, userID string) string {
	return projectID + ":" + userID
}

func projectIDsForTest(projects []ProjectRecord) []string {
	ids := make([]string, len(projects))
	for i, project := range projects {
		ids[i] = project.ID
	}
	return ids
}

type staticIDsForTest struct {
	projectID    string
	membershipID string
}

func StaticIDsForTest(projectID, membershipID string) staticIDsForTest {
	return staticIDsForTest{
		projectID:    projectID,
		membershipID: membershipID,
	}
}

func (ids staticIDsForTest) NewProjectID() string {
	return ids.projectID
}

func (ids staticIDsForTest) NewProjectMembershipID() string {
	return ids.membershipID
}

func (ids staticIDsForTest) NewProjectInvitationID() string {
	return "invitation-1"
}

func (ids staticIDsForTest) NewDeletionTombstoneID() string {
	return "00000000-0000-4000-8000-000000000999"
}

type staticClockForTest struct {
	now time.Time
}

func StaticClockForTest(now time.Time) staticClockForTest {
	return staticClockForTest{now: now}
}

func (clock staticClockForTest) Now() time.Time {
	return clock.now
}
