package projectstore

import (
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/projects"
)

func TestProjectsStoreActiveProjectRoleScopesActorToExactProject(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	store := New(db)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	userA := "00000000-0000-4000-8000-000000000811"
	userB := "00000000-0000-4000-8000-000000000812"
	projectA := "00000000-0000-4000-8000-000000000821"
	projectB := "00000000-0000-4000-8000-000000000822"
	cleanupProjectsStoreRows(t, db, userA, projectA)
	cleanupProjectsStoreRows(t, db, userB, projectB)
	insertProjectStoreUserForTest(t, db, userA, "access-a@example.com")
	insertProjectStoreUserForTest(t, db, userB, "access-b@example.com")
	insertProjectStoreProjectForTest(t, db, projectA, userA, "Project A", now)
	insertProjectStoreProjectForTest(t, db, projectB, userB, "Project B", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000831", projectA, userA, projects.RoleOwner, now, false)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000832", projectB, userB, projects.RoleEditor, now, false)

	role, err := store.ActiveProjectRole(t.Context(), userB, projectB)
	if err != nil {
		t.Fatalf("ActiveProjectRole() error = %v", err)
	}
	if role != projects.RoleEditor {
		t.Fatalf("ActiveProjectRole() role = %q, want editor", role)
	}

	_, err = store.ActiveProjectRole(t.Context(), userB, projectA)
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("ActiveProjectRole(cross-project) error = %v, want ErrNotFound", err)
	}
}

func TestProjectsStoreActiveProjectRoleConcealsRemovedMemberAndDeletedProject(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	store := New(db)
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		userID    string
		projectID string
		memberID  string
		removed   bool
		deleted   bool
	}{
		{name: "removed member", userID: "00000000-0000-4000-8000-000000000841", projectID: "00000000-0000-4000-8000-000000000851", memberID: "00000000-0000-4000-8000-000000000861", removed: true},
		{name: "deleted project", userID: "00000000-0000-4000-8000-000000000842", projectID: "00000000-0000-4000-8000-000000000852", memberID: "00000000-0000-4000-8000-000000000862", deleted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanupProjectsStoreRows(t, db, tt.userID, tt.projectID)
			insertProjectStoreUserForTest(t, db, tt.userID, tt.name+"@example.com")
			insertProjectStoreProjectForTest(t, db, tt.projectID, tt.userID, tt.name, now)
			insertProjectStoreMembershipForTest(t, db, tt.memberID, tt.projectID, tt.userID, projects.RoleOwner, now, tt.removed)
			if tt.deleted {
				fenceProjectDeletionForTest(t, db, tt.userID, tt.projectID, now)
			}

			_, err := store.ActiveProjectRole(t.Context(), tt.userID, tt.projectID)
			if !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("ActiveProjectRole() error = %v, want ErrNotFound", err)
			}
		})
	}
}
