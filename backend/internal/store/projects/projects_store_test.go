package projectstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	deletionrepo "canonkeeper/backend/internal/repository/postgres/deletion"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestProjectsStoreImplementsProjectsStoreContract(t *testing.T) {
	var _ projects.Store = (*Store)(nil)
}

func TestProjectsServiceMalformedProjectIDReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	service := projects.NewService(New(nil), nil, nil)
	actorID := "00000000-0000-4000-8000-000000000500"
	malformedProjectID := "not-a-project-uuid"

	tests := []struct {
		name string
		act  func() error
	}{
		{
			name: "get",
			act: func() error {
				_, err := service.GetProject(ctx, actorID, malformedProjectID)
				return err
			},
		},
		{
			name: "update",
			act: func() error {
				_, err := service.UpdateProject(ctx, actorID, malformedProjectID, projects.UpdateProjectInput{})
				return err
			},
		},
		{
			name: "delete",
			act: func() error {
				return service.DeleteProject(ctx, actorID, malformedProjectID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.act(); !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("service %s malformed projectID error = %v, want ErrNotFound", tt.name, err)
			}
		})
	}
}

func TestProjectsStoreMalformedProjectIDReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store := New(nil)
	actorID := "00000000-0000-4000-8000-000000000500"
	malformedProjectID := "not-a-project-uuid"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		act  func() error
	}{
		{
			name: "get",
			act: func() error {
				_, err := store.GetProject(ctx, actorID, malformedProjectID)
				return err
			},
		},
		{
			name: "update",
			act: func() error {
				_, err := store.UpdateProject(ctx, projects.UpdateProjectStoreInput{
					ActorUserID: actorID,
					ProjectID:   malformedProjectID,
					Title:       "Updated",
					Description: "Updated",
					UpdatedAt:   now,
				})
				return err
			},
		},
		{
			name: "delete",
			act: func() error {
				return store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
					ActorUserID: actorID,
					ProjectID:   malformedProjectID,
					TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
					DeletedAt:   now,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.act(); !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("store %s malformed projectID error = %v, want ErrNotFound", tt.name, err)
			}
		})
	}
}

func TestProjectsStoreCreateProjectWithOwnerMembershipPersistsOwnerMembership(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000501"
	projectID := "00000000-0000-4000-8000-000000000601"
	membershipID := "00000000-0000-4000-8000-000000000701"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-owner@example.com")

	project, err := store.CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
		ProjectID:         projectID,
		OwnerMembershipID: membershipID,
		OwnerUserID:       userID,
		Title:             "The White Port",
		Description:       "City notes",
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("CreateProjectWithOwnerMembership() error = %v", err)
	}

	if project.ID != projectID || project.Title != "The White Port" {
		t.Fatal("CreateProjectWithOwnerMembership() returned unexpected project identity")
	}
	if project.CurrentMembership.ID != membershipID || project.CurrentMembership.Role != projects.RoleOwner {
		t.Fatal("CreateProjectWithOwnerMembership() returned unexpected owner membership")
	}

	stored, err := store.GetProject(ctx, userID, projectID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	if stored.CurrentMembership.ID != membershipID || stored.CurrentMembership.Role != projects.RoleOwner {
		t.Fatal("GetProject() did not read the persisted owner membership")
	}
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT project.lifecycle_generation
		FROM projects AS project
		WHERE project.id = $1
	`, projectID).Scan(&generation); err != nil {
		t.Fatalf("read project lifecycle: %v", err)
	}
	if generation != 1 {
		t.Fatalf("project lifecycle generation = %d", generation)
	}
	var catalogRevision int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT catalog_revision
		FROM project_source_views
		WHERE project_id = $1 AND source_view = 'draft' AND project_lifecycle_generation = 1
	`, projectID).Scan(&catalogRevision); err != nil {
		t.Fatalf("read project draft source marker: %v", err)
	}
	if catalogRevision != 1 {
		t.Fatalf("project draft source marker revision = %d, want 1", catalogRevision)
	}
}

func TestProjectsStorePhase04CreateRollsBackAfterCatalogMutation(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	userID := ids.NewProjectID()
	projectID := ids.NewProjectID()
	membershipID := ids.NewProjectMembershipID()
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-catalog-rollback@example.test")
	injected := errors.New("project catalog failpoint")
	store := New(db, withDeletionFailpoint(func(point string) error {
		if point == "create_after_catalog_mutation" {
			return injected
		}
		return nil
	}))

	_, err := store.CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
		ProjectID: projectID, OwnerMembershipID: membershipID, OwnerUserID: userID,
		Title: "Rollback project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if !errors.Is(err, injected) {
		t.Fatalf("CreateProjectWithOwnerMembership() error = %v, want injected", err)
	}
	var projectsCount, membershipsCount, markersCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM projects WHERE id = $1),
		  (SELECT count(*) FROM project_memberships WHERE id = $2),
		  (SELECT count(*) FROM project_source_views WHERE project_id = $1)
	`, projectID, membershipID).Scan(&projectsCount, &membershipsCount, &markersCount); err != nil {
		t.Fatal(err)
	}
	if projectsCount != 0 || membershipsCount != 0 || markersCount != 0 {
		t.Fatalf("rolled back project/membership/catalog = %d/%d/%d, want 0/0/0", projectsCount, membershipsCount, markersCount)
	}
}

func TestProjectsStoreCreateFailsClosedWhenSourceCatalogIsUnavailable(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	userID := ids.NewProjectID()
	projectID := ids.NewProjectID()
	membershipID := ids.NewProjectMembershipID()
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "missing-source-catalog@example.test")
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE project_source_views RENAME TO project_source_views_unavailable`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), `ALTER TABLE project_source_views_unavailable RENAME TO project_source_views`); err != nil {
			t.Errorf("restore project_source_views: %v", err)
		}
	})

	_, err := New(db).CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
		ProjectID: projectID, OwnerMembershipID: membershipID, OwnerUserID: userID,
		Title: "Fail closed project", CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	if err == nil {
		t.Fatal("CreateProjectWithOwnerMembership() error = nil, want missing catalog error")
	}
	var projectCount, membershipCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM projects WHERE id = $1),
		  (SELECT count(*) FROM project_memberships WHERE id = $2)
	`, projectID, membershipID).Scan(&projectCount, &membershipCount); err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 || membershipCount != 0 {
		t.Fatalf("project/membership survived failed catalog mutation = %d/%d", projectCount, membershipCount)
	}
}

func TestProjectsStorePhase04DeleteAndRestoreRollBackWithCatalog(t *testing.T) {
	t.Run("delete", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		before := phase04LibrarySourceStateForTest(t, fixture)
		injected := errors.New("project delete failpoint")
		store := New(
			fixture.db,
			withDeletionFailpoint(func(point string) error {
				if point == "after_source_update" {
					return injected
				}
				return nil
			}),
		)
		_, err := store.FenceProjectDeletion(context.Background(), governedProjectDeleteRequestForTest(
			fixture.actorID, fixture.projectID, projects.NewSecureIDGenerator().NewDeletionTombstoneID(), fixture.now.Add(time.Second),
		))
		if !errors.Is(err, injected) {
			t.Fatalf("FenceProjectDeletion() error = %v, want injected", err)
		}
		if after := phase04LibrarySourceStateForTest(t, fixture); after != before {
			t.Fatal("project delete changed source/catalog state after rollback")
		}
	})

	t.Run("restore", func(t *testing.T) {
		fixture := newPhase04LibraryRollbackFixture(t)
		if _, err := New(
			fixture.db,
		).FenceProjectDeletion(context.Background(), governedProjectDeleteRequestForTest(
			fixture.actorID, fixture.projectID, projects.NewSecureIDGenerator().NewDeletionTombstoneID(), fixture.now.Add(time.Second),
		)); err != nil {
			t.Fatalf("delete before project restore rollback: %v", err)
		}
		before := phase04LibrarySourceStateForTest(t, fixture)
		input := restoreProjectInputForTest(fixture.actorID, fixture.projectID)
		injected := errors.New("project restore failpoint")
		store := New(fixture.db, withDeletionFailpoint(func(point string) error {
			if point == "restore_after_source_update" {
				return injected
			}
			return nil
		}))
		if _, err := store.RestoreProject(context.Background(), input); !errors.Is(err, injected) {
			t.Fatalf("RestoreProject() error = %v, want injected", err)
		}
		if after := phase04LibrarySourceStateForTest(t, fixture); after != before {
			t.Fatal("project restore changed source/catalog state after rollback")
		}
	})
}

func TestProjectsStoreGetProjectReturnsActiveProjectForActor(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000504"
	projectID := "00000000-0000-4000-8000-000000000604"
	membershipID := "00000000-0000-4000-8000-000000000704"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-get@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, userID, "Readable Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, now, false)

	project, err := store.GetProject(ctx, userID, projectID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}

	if project.ID != projectID || project.Title != "Readable Project" {
		t.Fatal("GetProject() returned unexpected project identity")
	}
	if project.CurrentMembership.ID != membershipID || project.CurrentMembership.Role != projects.RoleEditor {
		t.Fatal("GetProject() returned unexpected current membership")
	}
}

func TestProjectsStoreGetProjectReturnsNotFoundForNonMember(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000505"
	actorID := "00000000-0000-4000-8000-000000000506"
	projectID := "00000000-0000-4000-8000-000000000605"
	membershipID := "00000000-0000-4000-8000-000000000705"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "project-non-member@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Private Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, ownerID, projects.RoleOwner, now, false)

	_, err := store.GetProject(ctx, actorID, projectID)

	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("GetProject() error = %v, want ErrNotFound", err)
	}
}

func TestProjectsStoreUpdateProjectMutatesActiveProjectForActor(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000507"
	projectID := "00000000-0000-4000-8000-000000000607"
	membershipID := "00000000-0000-4000-8000-000000000707"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-update@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, userID, "Original Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleEditor, createdAt, false)

	project, err := store.UpdateProject(ctx, projects.UpdateProjectStoreInput{
		ActorUserID: userID,
		ProjectID:   projectID,
		Title:       "Updated Project",
		Description: "Updated notes",
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}

	if project.Title != "Updated Project" || project.Description != "Updated notes" {
		t.Fatalf("UpdateProject() returned %#v, want updated metadata", project)
	}
	if project.Revision != 2 {
		t.Fatalf("UpdateProject() revision = %d, want 2", project.Revision)
	}
	if !project.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("UpdateProject() updated_at = %s, want %s", project.UpdatedAt, updatedAt)
	}
}

func TestProjectsStoreUpdateProjectRejectsDirectViewerMutation(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000520"
	viewerID := "00000000-0000-4000-8000-000000000521"
	projectID := "00000000-0000-4000-8000-000000000620"
	membershipID := "00000000-0000-4000-8000-000000000720"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, viewerID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "project-direct-viewer-owner@example.com")
	insertProjectStoreUserForTest(t, db, viewerID, "project-direct-viewer@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Original Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, viewerID, projects.RoleViewer, createdAt, false)

	_, err := store.UpdateProject(ctx, projects.UpdateProjectStoreInput{
		ActorUserID: viewerID,
		ProjectID:   projectID,
		Title:       "Viewer Update",
		Description: "Viewer mutation",
		UpdatedAt:   updatedAt,
	})

	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("UpdateProject() direct viewer error = %v, want ErrNotFound", err)
	}
	var title string
	var description string
	var revision int64
	if err := db.Pool.QueryRow(ctx, "SELECT name, description, revision FROM projects WHERE id = $1", projectID).Scan(&title, &description, &revision); err != nil {
		t.Fatalf("read project after denied viewer update: %v", err)
	}
	if title != "Original Project" || description != "" || revision != 1 {
		t.Fatalf("project after denied viewer update = title %q description %q revision %d, want unchanged", title, description, revision)
	}
}

func TestProjectsStoreUpdateProjectSkipsHiddenAccess(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		removed bool
		deleted bool
	}{
		{name: "removed membership", removed: true},
		{name: "deleted project", deleted: true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			userID := "00000000-0000-4000-8000-00000000050" + string(rune('8'+i))
			projectID := "00000000-0000-4000-8000-00000000060" + string(rune('8'+i))
			membershipID := "00000000-0000-4000-8000-00000000070" + string(rune('8'+i))
			now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			cleanupProjectsStoreRows(t, db, userID, projectID)
			insertProjectStoreUserForTest(t, db, userID, "project-hidden-update-"+string(rune('a'+i))+"@example.com")
			insertProjectStoreProjectForTest(t, db, projectID, userID, "Hidden Project", now)
			role := projects.RoleEditor
			if tt.deleted {
				role = projects.RoleOwner
			}
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, role, now, tt.removed)
			if tt.deleted {
				fenceProjectDeletionForTest(t, db, userID, projectID, now)
			}

			_, err := store.UpdateProject(ctx, projects.UpdateProjectStoreInput{
				ActorUserID: userID,
				ProjectID:   projectID,
				Title:       "Updated",
				Description: "Updated",
				UpdatedAt:   now.Add(time.Hour),
			})

			if !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("UpdateProject() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestProjectsStoreDeleteProjectSoftDeletesActiveProjectForActor(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000510"
	projectID := "00000000-0000-4000-8000-000000000610"
	membershipID := "00000000-0000-4000-8000-000000000710"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	deletedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-delete@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, userID, "Deleted Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, createdAt, false)

	err := store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
		ActorUserID: userID,
		ProjectID:   projectID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
		DeletedAt:   deletedAt,
	})
	if err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}

	var gotDeletedAt time.Time
	var gotPurgeAfter time.Time
	var gotUpdatedAt time.Time
	var gotRevision int64
	if err := db.Pool.QueryRow(ctx, "SELECT deleted_at, purge_after, updated_at, revision FROM projects WHERE id = $1", projectID).Scan(&gotDeletedAt, &gotPurgeAfter, &gotUpdatedAt, &gotRevision); err != nil {
		t.Fatalf("read deleted project: %v", err)
	}
	wantPurgeAfter := deletedAt.Add(deletion.SourceRestoreWindow)
	if !gotDeletedAt.Equal(deletedAt) || !gotPurgeAfter.Equal(wantPurgeAfter) || !gotUpdatedAt.Equal(deletedAt) {
		t.Fatalf("delete timestamps = %s/%s/%s, want %s/%s/%s", gotDeletedAt, gotPurgeAfter, gotUpdatedAt, deletedAt, wantPurgeAfter, deletedAt)
	}
	if gotRevision != 2 {
		t.Fatalf("revision = %d, want 2", gotRevision)
	}

	_, err = store.GetProject(ctx, userID, projectID)
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("GetProject() after delete error = %v, want ErrNotFound", err)
	}
}

func TestProjectsStoreDeleteProjectRejectsDirectEditorMutation(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000522"
	editorID := "00000000-0000-4000-8000-000000000523"
	projectID := "00000000-0000-4000-8000-000000000622"
	membershipID := "00000000-0000-4000-8000-000000000722"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	deletedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, editorID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "project-direct-editor-owner@example.com")
	insertProjectStoreUserForTest(t, db, editorID, "project-direct-editor@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Editor Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, editorID, projects.RoleEditor, createdAt, false)

	err := store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
		ActorUserID: editorID,
		ProjectID:   projectID,
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
		DeletedAt:   deletedAt,
	})

	if !errors.Is(err, projects.ErrForbidden) {
		t.Fatalf("DeleteProject() direct editor error = %v, want ErrForbidden", err)
	}
	var gotDeletedAt pgtype.Timestamptz
	var gotPurgeAfter pgtype.Timestamptz
	var gotRevision int64
	if err := db.Pool.QueryRow(ctx, "SELECT deleted_at, purge_after, revision FROM projects WHERE id = $1", projectID).Scan(&gotDeletedAt, &gotPurgeAfter, &gotRevision); err != nil {
		t.Fatalf("read project after denied editor delete: %v", err)
	}
	if gotDeletedAt.Valid || gotPurgeAfter.Valid || gotRevision != 1 {
		t.Fatalf("project after denied editor delete = deleted_at valid %t purge_after valid %t revision %d, want unchanged", gotDeletedAt.Valid, gotPurgeAfter.Valid, gotRevision)
	}
}

func TestProjectsStoreDeleteProjectSkipsHiddenAccess(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		removed bool
		deleted bool
	}{
		{name: "removed membership", removed: true},
		{name: "deleted project", deleted: true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			userID := "00000000-0000-4000-8000-00000000051" + string(rune('1'+i))
			projectID := "00000000-0000-4000-8000-00000000061" + string(rune('1'+i))
			membershipID := "00000000-0000-4000-8000-00000000071" + string(rune('1'+i))
			now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			cleanupProjectsStoreRows(t, db, userID, projectID)
			insertProjectStoreUserForTest(t, db, userID, "project-hidden-delete-"+string(rune('a'+i))+"@example.com")
			insertProjectStoreProjectForTest(t, db, projectID, userID, "Hidden Project", now)
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, tt.removed)
			if tt.deleted {
				fenceProjectDeletionForTest(t, db, userID, projectID, now)
			}

			err := store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
				ActorUserID: userID,
				ProjectID:   projectID,
				TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
				DeletedAt:   now.Add(time.Hour),
			})

			if !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("DeleteProject() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestProjectsStoreFenceProjectGovernedDeletionCommitsExactFenceAndOutbox(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000000901"
	projectA := "00000000-0000-4000-8000-000000000902"
	projectB := "00000000-0000-4000-8000-000000000903"
	membershipA := "00000000-0000-4000-8000-000000000904"
	membershipB := "00000000-0000-4000-8000-000000000905"
	invitationID := "00000000-0000-4000-8000-000000000906"
	tombstoneID := "00000000-0000-4000-8000-000000000907"
	fencedAt := time.Date(2026, 7, 12, 10, 11, 12, 345000, time.UTC)

	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, actorID, "governed-project-delete@example.test")
	insertProjectStoreProjectForTest(t, db, projectA, actorID, "Governed A", fencedAt.Add(-time.Hour))
	insertProjectStoreProjectForTest(t, db, projectB, actorID, "Governed B", fencedAt.Add(-2*time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipA, projectA, actorID, projects.RoleAdmin, fencedAt.Add(-time.Hour), false)
	insertProjectStoreMembershipForTest(t, db, membershipB, projectB, actorID, projects.RoleOwner, fencedAt.Add(-2*time.Hour), false)
	insertProjectStoreInvitationForTest(
		t, db, invitationID, projectA, actorID,
		"pending-before-delete@example.test", strings.Repeat("9", 64),
		projects.RoleViewer, projects.ProjectInvitationStatusPending,
		fencedAt.Add(24*time.Hour), fencedAt.Add(-time.Minute),
	)
	indexJobID := "00000000-0000-4000-8000-000000000908"
	if _, err := indexingstore.New(db).StartIndexing(ctx, indexing.StartIndexingStoreInput{
		CandidateJobID: indexJobID, IdempotencyRecordID: "00000000-0000-4000-8000-000000000909",
		ActorUserID: actorID, ProjectID: projectA, SourceView: governance.SourceViewDraft,
		SessionWriteProof: sessionProof, ScopeHash: "project-delete-o1-scope",
		OperationID: "project.delete.o1.text_index.v1", IdempotencyKeyHash: "project-delete-o1-key",
		RequestHash: "project-delete-o1-request", IdempotencyStatusCode: 202,
		CreatedAt: fencedAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create current text-index job before project delete: %v", err)
	}

	var projectBBefore string
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(project)::text FROM projects project WHERE id = $1`, projectB).Scan(&projectBBefore); err != nil {
		t.Fatal(err)
	}
	store := New(db)
	request := deletion.SourceDeleteRequest{
		TombstoneID: tombstoneID,
		ActorUserID: actorID,
		ProjectID:   projectA,
		EntityKind:  deletion.EntityProject,
		EntityID:    projectA,
		FencedAt:    fencedAt,
		GovernedWindow: &deletion.GovernedSourceDeleteWindow{
			PurgeAfter:           fencedAt.Add(deletion.SourceRestoreWindow),
			DerivedPurgeDeadline: fencedAt.Add(deletion.DerivedPurgeDeadline),
			SourcePurgeDeadline:  fencedAt.Add(deletion.SourceRestoreWindow + deletion.SourceHardPurgeGrace),
		},
	}

	result, err := store.FenceProjectDeletion(ctx, request)
	if err != nil {
		t.Fatalf("FenceProjectDeletion() error = %v", err)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("FenceProjectDeletion() result invalid: %v", err)
	}
	wantDeleted := governance.SourceGenerationVector{
		ProjectID: projectA, ProjectGeneration: 1,
	}
	wantCurrent := governance.SourceGenerationVector{
		ProjectID: projectA, ProjectGeneration: 2,
	}
	if result.TombstoneID != tombstoneID || result.Deleted != wantDeleted || result.Current != wantCurrent {
		t.Fatalf("FenceProjectDeletion() result = %#v, want tombstone and vectors %#v -> %#v", result, wantDeleted, wantCurrent)
	}

	var deletedAt, retentionStarted, purgeAfter, purgeDeadline pgtype.Timestamptz
	var hardPurgeStarted pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, governance_retention_started_at, purge_after,
		       purge_deadline, hard_purge_started_at, lifecycle_generation
		FROM projects WHERE id = $1
	`, projectA).Scan(&deletedAt, &retentionStarted, &purgeAfter, &purgeDeadline, &hardPurgeStarted, &generation); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Time.Equal(fencedAt) || !retentionStarted.Time.Equal(fencedAt) ||
		!purgeAfter.Time.Equal(request.GovernedWindow.PurgeAfter) ||
		!purgeDeadline.Time.Equal(request.GovernedWindow.SourcePurgeDeadline) ||
		hardPurgeStarted.Valid || generation != 2 {
		t.Fatalf("project lifecycle after fence = deleted %v retention %v purge %v deadline %v hard %v generation %d", deletedAt, retentionStarted, purgeAfter, purgeDeadline, hardPurgeStarted, generation)
	}

	var origin, entityKind, purgeMode, status string
	var selectedGeneration int64
	var tombstoneDeadline time.Time
	if err := db.Pool.QueryRow(ctx, `
		SELECT origin, entity_kind, purge_mode, status, project_generation, purge_deadline
		FROM deletion_tombstones WHERE id = $1 AND project_id = $2
	`, tombstoneID, projectA).Scan(&origin, &entityKind, &purgeMode, &status, &selectedGeneration, &tombstoneDeadline); err != nil {
		t.Fatal(err)
	}
	if origin != "governed_online" || entityKind != "project" || purgeMode != "derived_generation" || status != "pending" || selectedGeneration != 1 || !tombstoneDeadline.Equal(request.GovernedWindow.DerivedPurgeDeadline) {
		t.Fatalf("unexpected project tombstone: origin=%s entity=%s mode=%s status=%s generation=%d deadline=%s", origin, entityKind, purgeMode, status, selectedGeneration, tombstoneDeadline)
	}

	var targets []string
	if err := db.Pool.QueryRow(ctx, `
		SELECT array_agg(target ORDER BY CASE target
			WHEN 'runtime' THEN 1 WHEN 'filesystem' THEN 2
			WHEN 'postgresql' THEN 4 END)
		FROM deletion_target_tasks WHERE tombstone_id = $1 AND project_id = $2
	`, tombstoneID, projectA).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if strings.Join(targets, ",") != "runtime,filesystem,postgresql" {
		t.Fatalf("target order = %v", targets)
	}
	postgresSteps, err := deletion.PostgresStepRegistry(
		governance.DeletionSelectionProject,
		governance.PurgeModeDerivedGeneration,
	)
	if err != nil {
		t.Fatalf("project PostgreSQL step registry: %v", err)
	}
	var stepCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM deletion_target_step_progress
		WHERE tombstone_id = $1 AND project_id = $2 AND target = 'postgresql'
	`, tombstoneID, projectA).Scan(&stepCount); err != nil || stepCount != len(postgresSteps) {
		t.Fatalf("postgresql step count = %d, want %d, err = %v", stepCount, len(postgresSteps), err)
	}

	var invitationStatus string
	var canceledAt pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `SELECT status, canceled_at FROM project_invitations WHERE id = $1`, invitationID).Scan(&invitationStatus, &canceledAt); err != nil {
		t.Fatal(err)
	}
	if invitationStatus != "canceled" || !canceledAt.Valid || !canceledAt.Time.Equal(fencedAt) {
		t.Fatalf("pending invitation not canceled atomically: status=%s canceled_at=%v", invitationStatus, canceledAt)
	}
	var ownerReadState string
	var ownerFencedAt pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `
		SELECT owner.read_state, owner.fenced_at
		FROM jobs AS job
		JOIN artifact_source_owners AS owner
		  ON owner.id = job.artifact_source_owner_id AND owner.project_id = job.project_id
		WHERE job.id = $1 AND job.project_id = $2
	`, indexJobID, projectA).Scan(&ownerReadState, &ownerFencedAt); err != nil {
		t.Fatalf("load existing job owner after project fence: %v", err)
	}
	if ownerReadState != "source_deleted" || !ownerFencedAt.Valid || !ownerFencedAt.Time.Equal(fencedAt) {
		t.Fatalf("project delete owner fence = state %q at %v, want source_deleted at %s", ownerReadState, ownerFencedAt, fencedAt)
	}

	var projectBAfter string
	if err := db.Pool.QueryRow(ctx, `SELECT to_jsonb(project)::text FROM projects project WHERE id = $1`, projectB).Scan(&projectBAfter); err != nil {
		t.Fatal(err)
	}
	if projectBAfter != projectBBefore {
		t.Fatalf("unrelated project changed:\nbefore=%s\nafter=%s", projectBBefore, projectBAfter)
	}
}

func TestProjectsStoreFenceProjectGovernedDeletionRollsBackEveryFailpoint(t *testing.T) {
	failpoints := []string{
		"after_row_lock",
		"after_source_update",
		"after_tombstone_insert",
		"after_target_runtime",
		"after_target_filesystem",
		"after_target_postgresql",
	}
	for _, failpoint := range failpoints {
		t.Run(failpoint, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			actorID := "00000000-0000-4000-8000-000000000911"
			projectID := "00000000-0000-4000-8000-000000000912"
			membershipID := "00000000-0000-4000-8000-000000000913"
			tombstoneID := "00000000-0000-4000-8000-000000000914"
			fencedAt := time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC)
			insertProjectStoreUserForTest(t, db, actorID, "rollback-project-delete@example.test")
			insertProjectStoreProjectForTest(t, db, projectID, actorID, "Rollback", fencedAt.Add(-time.Hour))
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, fencedAt.Add(-time.Hour), false)

			injected := errors.New("injected project deletion failure")
			store := New(
				db,
				withDeletionFailpoint(func(point string) error {
					if point == failpoint {
						return injected
					}
					return nil
				}),
			)
			_, err := store.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID, fencedAt))
			if !errors.Is(err, injected) {
				t.Fatalf("FenceProjectDeletion() error = %v, want injected failure", err)
			}

			assertProjectDeletionRolledBackForTest(t, db, projectID, tombstoneID)
		})
	}
}

func TestProjectsStoreFenceProjectGovernedDeletionReauthorizesLockedMembership(t *testing.T) {
	tests := []struct {
		name    string
		role    projects.Role
		removed bool
		wantErr error
	}{
		{name: "downgraded editor is forbidden", role: projects.RoleEditor, wantErr: projects.ErrForbidden},
		{name: "removed owner is concealed", role: projects.RoleOwner, removed: true, wantErr: projects.ErrNotFound},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db := openCurrentStoreTestDB(t)
			suffix := strconv.Itoa(i + 1)
			actorID := "00000000-0000-4000-8000-00000000093" + suffix
			projectID := "00000000-0000-4000-8000-00000000094" + suffix
			membershipID := "00000000-0000-4000-8000-00000000095" + suffix
			tombstoneID := "00000000-0000-4000-8000-00000000096" + suffix
			fencedAt := time.Date(2026, 7, 12, 11, 30, 0, 0, time.UTC)
			insertProjectStoreUserForTest(t, db, actorID, "reauthorize-project-delete-"+suffix+"@example.test")
			insertProjectStoreProjectForTest(t, db, projectID, actorID, "Reauthorize", fencedAt.Add(-time.Hour))
			insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, tt.role, fencedAt.Add(-time.Hour), tt.removed)
			store := New(db)
			_, err := store.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID, fencedAt))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("FenceProjectDeletion() error = %v, want %v", err, tt.wantErr)
			}
			assertProjectDeletionRolledBackForTest(t, db, projectID, tombstoneID)
		})
	}
}

func TestProjectsStoreListDeletedAndRestoreProjectKeepsCurrentGeneration(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	actorID := "00000000-0000-4000-8000-000000000981"
	projectID := "00000000-0000-4000-8000-000000000982"
	membershipID := "00000000-0000-4000-8000-000000000983"
	tombstoneID := "00000000-0000-4000-8000-000000000984"
	invitationID := "00000000-0000-4000-8000-000000000989"
	oldGenerationJobID := "00000000-0000-4000-8000-000000000994"
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	sessionProof := insertProjectStoreUserWithSessionForTest(t, db, actorID, "restore-project@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Project", fencedAt.Add(-time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, fencedAt.Add(-time.Hour), false)
	insertProjectStoreInvitationForTest(
		t, db, invitationID, projectID, actorID,
		"restore-canceled@example.test", strings.Repeat("8", 64),
		projects.RoleViewer, projects.ProjectInvitationStatusPending,
		fencedAt.Add(24*time.Hour), fencedAt.Add(-time.Minute),
	)
	if _, err := indexingstore.New(db).StartIndexing(ctx, indexing.StartIndexingStoreInput{
		CandidateJobID: oldGenerationJobID, IdempotencyRecordID: "00000000-0000-4000-8000-000000000995",
		ActorUserID: actorID, ProjectID: projectID, SourceView: governance.SourceViewDraft,
		SessionWriteProof: sessionProof, ScopeHash: "restore-old-job-scope",
		OperationID: "restore.old.text_index.v1", IdempotencyKeyHash: "restore-old-job-key",
		RequestHash: "restore-old-job-request", IdempotencyStatusCode: 202,
		CreatedAt: fencedAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create old-generation job before delete: %v", err)
	}
	store := New(db)
	if _, err := store.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID, fencedAt)); err != nil {
		t.Fatalf("delete before restore: %v", err)
	}

	deleted, err := store.ListProjectsForActor(ctx, projects.ListProjectsStoreInput{
		ActorUserID: actorID,
		State:       projects.ProjectListDeleted,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListProjectsForActor(deleted) error = %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != projectID || deleted[0].DeletedAt == nil || deleted[0].RestoreUntil == nil {
		t.Fatalf("deleted projects = %#v", deleted)
	}
	if !deleted[0].DeletedAt.Equal(fencedAt) || !deleted[0].RestoreUntil.Equal(fencedAt.Add(deletion.SourceRestoreWindow)) {
		t.Fatalf("deleted project clocks = deleted %v restore %v", deleted[0].DeletedAt, deleted[0].RestoreUntil)
	}

	restored, err := store.RestoreProject(ctx, projects.RestoreProjectStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
	})
	if err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if restored.ID != projectID || restored.DeletedAt != nil || restored.RestoreUntil != nil {
		t.Fatalf("RestoreProject() project = %#v", restored)
	}

	var generation int64
	var deletedAt, retentionStarted, purgeAfter, purgeDeadline pgtype.Timestamptz
	if err := db.Pool.QueryRow(ctx, `
		SELECT lifecycle_generation, deleted_at, governance_retention_started_at,
		       purge_after, purge_deadline
		FROM projects WHERE id = $1
	`, projectID).Scan(&generation, &deletedAt, &retentionStarted, &purgeAfter, &purgeDeadline); err != nil {
		t.Fatal(err)
	}
	if generation != 2 || deletedAt.Valid || retentionStarted.Valid || purgeAfter.Valid || purgeDeadline.Valid {
		t.Fatalf("restored lifecycle = generation %d deleted=%v retention=%v purge=%v deadline=%v", generation, deletedAt, retentionStarted, purgeAfter, purgeDeadline)
	}

	var intentView, intentStatus, payloadState string
	var intentRevision, catalogRevision int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT job.subject_json->>'sourceView', job.status, job.governance_payload_state,
		       (job.subject_json->>'catalogRevision')::bigint, source.catalog_revision
		FROM jobs AS job
		JOIN project_source_views AS source
		  ON source.project_id = job.project_id
		 AND source.source_view = job.subject_json->>'sourceView'
		WHERE job.project_id = $1 AND job.kind = 'canon_text_index' AND job.status = 'queued'
	`, projectID).Scan(&intentView, &intentStatus, &payloadState, &intentRevision, &catalogRevision); err != nil {
		t.Fatal(err)
	}
	if intentView != "draft" || intentStatus != "queued" || payloadState != "active" || intentRevision != catalogRevision {
		t.Fatalf("restore text intent = view %q status %q payload %q revision %d, catalog %d",
			intentView, intentStatus, payloadState, intentRevision, catalogRevision)
	}
	jobsStore := jobstore.New(db)
	if _, err := jobsStore.ResolveJobAccessForActor(ctx, jobs.ResolveJobAccessForActorStoreInput{
		ActorUserID: actorID, JobID: oldGenerationJobID,
	}); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("ResolveJobAccessForActor(old generation after restore) error = %v, want ErrNotFound", err)
	}
	var oldTombstoneCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE id = $1`, tombstoneID).Scan(&oldTombstoneCount); err != nil || oldTombstoneCount != 1 {
		t.Fatalf("old tombstone count = %d, err = %v", oldTombstoneCount, err)
	}
	var invitationStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM project_invitations WHERE id = $1`, invitationID).Scan(&invitationStatus); err != nil || invitationStatus != "canceled" {
		t.Fatalf("restored invitation status = %q, err = %v", invitationStatus, err)
	}
	_, err = store.RestoreProject(ctx, projects.RestoreProjectStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
	})
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("second public RestoreProject() error = %v, want ErrNotFound", err)
	}
	var queuedIntentCount int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM jobs
		WHERE project_id = $1 AND kind = 'canon_text_index' AND status = 'queued'
	`, projectID).Scan(&queuedIntentCount); err != nil || queuedIntentCount != 1 {
		t.Fatalf("queued text intents after repeat restore = %d, err = %v", queuedIntentCount, err)
	}
}

func TestProjectsStoreRestoreProjectRollsBackAfterSourceUpdate(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	actorID := ids.NewProjectID()
	projectID := ids.NewProjectID()
	membershipID := ids.NewProjectMembershipID()
	tombstoneID := ids.NewDeletionTombstoneID()
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "restore-after-source-update@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Rollback", fencedAt.Add(-time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, fencedAt.Add(-time.Hour), false)
	deleteStore := New(db)
	if _, err := deleteStore.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID, fencedAt)); err != nil {
		t.Fatalf("delete before rollback test: %v", err)
	}

	input := restoreProjectInputForTest(actorID, projectID)
	sentinel := errors.New("restore failpoint after source update")
	restoreStore := New(db, withDeletionFailpoint(func(point string) error {
		if point == "restore_after_source_update" {
			return sentinel
		}
		return nil
	}))
	if _, err := restoreStore.RestoreProject(ctx, input); !errors.Is(err, sentinel) {
		t.Fatalf("RestoreProject() error = %v, want %v", err, sentinel)
	}
	assertProjectRestoreRolledBackForTest(t, db, input)
}

func TestProjectsStoreRestoreProjectReauthorizesCurrentMembership(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	actorID := ids.NewProjectID()
	projectID := ids.NewProjectID()
	membershipID := ids.NewProjectMembershipID()
	tombstoneID := ids.NewDeletionTombstoneID()
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserForTest(t, db, actorID, "restore-reauthorize@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Reauthorize", fencedAt.Add(-time.Hour))
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, fencedAt.Add(-time.Hour), false)
	store := New(db)
	if _, err := store.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID, fencedAt)); err != nil {
		t.Fatalf("delete before reauthorization test: %v", err)
	}

	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'editor' WHERE id = $1`, membershipID); err != nil {
		t.Fatal(err)
	}
	editorInput := restoreProjectInputForTest(actorID, projectID)
	if _, err := store.RestoreProject(ctx, editorInput); !errors.Is(err, projects.ErrForbidden) {
		t.Fatalf("RestoreProject(editor) error = %v, want ErrForbidden", err)
	}
	assertProjectRestoreRolledBackForTest(t, db, editorInput)

	if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, membershipID); err != nil {
		t.Fatal(err)
	}
	removedInput := restoreProjectInputForTest(actorID, projectID)
	if _, err := store.RestoreProject(ctx, removedInput); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("RestoreProject(removed) error = %v, want ErrNotFound", err)
	}
	assertProjectRestoreRolledBackForTest(t, db, removedInput)
}

func TestProjectsStoreRestoreProjectRejectsExpiredWindowAndTerminalFence(t *testing.T) {
	t.Run("expired at strict boundary", func(t *testing.T) {
		ctx := context.Background()
		db := openCurrentStoreTestDB(t)
		ids := projects.NewSecureIDGenerator()
		actorID := ids.NewProjectID()
		projectID := ids.NewProjectID()
		membershipID := ids.NewProjectMembershipID()
		tombstoneID := ids.NewDeletionTombstoneID()
		fencedAt := time.Now().UTC().Truncate(time.Microsecond)
		insertProjectStoreUserForTest(t, db, actorID, "restore-expired@example.test")
		insertProjectStoreProjectForTest(t, db, projectID, actorID, "Restore Expired", fencedAt.Add(-time.Hour))
		insertProjectStoreMembershipForTest(t, db, membershipID, projectID, actorID, projects.RoleOwner, fencedAt.Add(-time.Hour), false)
		store := New(db)
		if _, err := store.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID, fencedAt)); err != nil {
			t.Fatalf("delete before expiry test: %v", err)
		}
		conn, err := db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			conn.Release()
			t.Fatal(err)
		}
		_, updateErr := conn.Exec(ctx, `
			WITH boundary AS (SELECT clock_timestamp() AS now_at)
			UPDATE projects
			SET deleted_at = boundary.now_at - interval '7 days',
			    governance_retention_started_at = boundary.now_at - interval '7 days',
			    purge_after = boundary.now_at,
			    purge_deadline = boundary.now_at + interval '24 hours'
			FROM boundary WHERE id = $1
		`, projectID)
		_, resetErr := conn.Exec(ctx, `SET session_replication_role = origin`)
		conn.Release()
		if updateErr != nil || resetErr != nil {
			t.Fatalf("move project to purge boundary: update=%v reset=%v", updateErr, resetErr)
		}

		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'editor' WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		editorInput := restoreProjectInputForTest(actorID, projectID)
		if _, err := store.RestoreProject(ctx, editorInput); !errors.Is(err, projects.ErrForbidden) {
			t.Fatalf("RestoreProject(expired editor) error = %v, want ErrForbidden", err)
		}
		assertProjectRestoreRolledBackForTest(t, db, editorInput)

		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		removedInput := restoreProjectInputForTest(actorID, projectID)
		if _, err := store.RestoreProject(ctx, removedInput); !errors.Is(err, projects.ErrNotFound) {
			t.Fatalf("RestoreProject(expired removed member) error = %v, want ErrNotFound", err)
		}
		assertProjectRestoreRolledBackForTest(t, db, removedInput)

		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'owner', removed_at = NULL WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		input := restoreProjectInputForTest(actorID, projectID)
		if _, err := store.RestoreProject(ctx, input); !errors.Is(err, projects.ErrConflict) {
			t.Fatalf("RestoreProject(expired) error = %v, want ErrConflict", err)
		}
		assertProjectRestoreRolledBackForTest(t, db, input)

		conn, err = db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			conn.Release()
			t.Fatal(err)
		}
		_, updateErr = conn.Exec(ctx, `UPDATE projects SET hard_purge_started_at = clock_timestamp() WHERE id = $1`, projectID)
		_, resetErr = conn.Exec(ctx, `SET session_replication_role = origin`)
		conn.Release()
		if updateErr != nil || resetErr != nil {
			t.Fatalf("move project to terminal state: update=%v reset=%v", updateErr, resetErr)
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET role = 'editor' WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		terminalEditorInput := restoreProjectInputForTest(actorID, projectID)
		if _, err := store.RestoreProject(ctx, terminalEditorInput); !errors.Is(err, projects.ErrForbidden) {
			t.Fatalf("RestoreProject(terminal editor) error = %v, want ErrForbidden", err)
		}
		assertProjectRestoreRolledBackForTest(t, db, terminalEditorInput)
		if _, err := db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at = clock_timestamp() WHERE id = $1`, membershipID); err != nil {
			t.Fatal(err)
		}
		terminalRemovedInput := restoreProjectInputForTest(actorID, projectID)
		if _, err := store.RestoreProject(ctx, terminalRemovedInput); !errors.Is(err, projects.ErrNotFound) {
			t.Fatalf("RestoreProject(terminal removed member) error = %v, want ErrNotFound", err)
		}
		assertProjectRestoreRolledBackForTest(t, db, terminalRemovedInput)
	})

}

func TestActorCollectionProjectsStoreUsesCursorAndFiltersInactiveProjects(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	actorID := "00000000-0000-4000-8000-000000000570"
	activeProjectID := "00000000-0000-4000-8000-000000000670"
	newestActiveProjectID := "00000000-0000-4000-8000-000000000673"
	removedProjectID := "00000000-0000-4000-8000-000000000671"
	deletedProjectID := "00000000-0000-4000-8000-000000000672"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, actorID, activeProjectID)
	cleanupProjectsStoreRows(t, db, actorID, newestActiveProjectID)
	cleanupProjectsStoreRows(t, db, actorID, removedProjectID)
	cleanupProjectsStoreRows(t, db, actorID, deletedProjectID)
	insertProjectStoreUserForTest(t, db, actorID, "list-projects-actor@example.com")
	insertProjectStoreProjectForTest(t, db, activeProjectID, actorID, "Active Project", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000770", activeProjectID, actorID, projects.RoleEditor, now, false)
	insertProjectStoreProjectForTest(t, db, newestActiveProjectID, actorID, "Newest Active Project", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000773", newestActiveProjectID, actorID, projects.RoleViewer, now, false)
	insertProjectStoreProjectForTest(t, db, removedProjectID, actorID, "Removed Project", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000771", removedProjectID, actorID, projects.RoleViewer, now, true)
	insertProjectStoreProjectForTest(t, db, deletedProjectID, actorID, "Deleted Project", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000772", deletedProjectID, actorID, projects.RoleOwner, now, false)
	fenceProjectDeletionForTest(t, db, actorID, deletedProjectID, now.Add(time.Hour))

	firstPage, err := store.ListProjectsForActor(ctx, projects.ListProjectsStoreInput{
		ActorUserID: actorID,
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListProjectsForActor() first page error = %v", err)
	}
	if len(firstPage) != 1 || firstPage[0].ID != newestActiveProjectID || firstPage[0].CurrentMembership.Role != projects.RoleViewer {
		t.Fatalf("ListProjectsForActor() first page = %#v, want newest active project", firstPage)
	}

	secondPage, err := store.ListProjectsForActor(ctx, projects.ListProjectsStoreInput{
		ActorUserID:     actorID,
		Limit:           1,
		HasCursor:       true,
		CursorCreatedAt: firstPage[0].CreatedAt,
		CursorID:        firstPage[0].ID,
	})
	if err != nil {
		t.Fatalf("ListProjectsForActor() second page error = %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != activeProjectID || secondPage[0].CurrentMembership.Role != projects.RoleEditor {
		t.Fatalf("ListProjectsForActor() second page = %#v, want older active project only", secondPage)
	}
}

func TestProjectsStoreListProjectMembersReturnsActiveMembersWithPII(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000571"
	memberID := "00000000-0000-4000-8000-000000000572"
	removedID := "00000000-0000-4000-8000-000000000573"
	projectID := "00000000-0000-4000-8000-000000000673"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, memberID, projectID)
	cleanupProjectsStoreRows(t, db, removedID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "member-list-owner@example.com")
	insertProjectStoreUserForTest(t, db, memberID, "member-list-reader@example.test")
	insertProjectStoreUserForTest(t, db, removedID, "member-list-removed@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Member List Project", now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000773", projectID, ownerID, projects.RoleOwner, now, false)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000774", projectID, memberID, projects.RoleViewer, now, false)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000000775", projectID, removedID, projects.RoleEditor, now, true)

	members, err := store.ListProjectMembers(ctx, projects.ListProjectMembersStoreInput{
		ProjectID:   projectID,
		ActorUserID: ownerID,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("ListProjectMembers() error = %v", err)
	}

	if len(members) != 2 {
		t.Fatalf("ListProjectMembers() returned %d members, want owner and active member", len(members))
	}
	member := projectMemberByUserIDForTest(t, members, memberID)
	if member.Email != "member-list-reader@example.test" || member.DisplayName == nil || *member.DisplayName != "Project Owner" || member.Role != projects.RoleViewer {
		t.Fatalf("active member = %#v, want email/displayName/viewer", member)
	}
	if projectMemberContainsUserForTest(members, removedID) {
		t.Fatal("ListProjectMembers() returned removed member")
	}
}

func TestProjectsStoreMemberRoleUpdateAndRemovalRepeatAuthorization(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000574"
	viewerID := "00000000-0000-4000-8000-000000000575"
	memberID := "00000000-0000-4000-8000-000000000576"
	otherMemberID := "00000000-0000-4000-8000-000000000577"
	projectID := "00000000-0000-4000-8000-000000000674"
	otherProjectID := "00000000-0000-4000-8000-000000000675"
	ownerMembershipID := "00000000-0000-4000-8000-000000000776"
	viewerMembershipID := "00000000-0000-4000-8000-000000000777"
	memberMembershipID := "00000000-0000-4000-8000-000000000778"
	otherMembershipID := "00000000-0000-4000-8000-000000000779"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, viewerID, projectID)
	cleanupProjectsStoreRows(t, db, memberID, projectID)
	cleanupProjectsStoreRows(t, db, otherMemberID, otherProjectID)
	insertProjectStoreUserForTest(t, db, ownerID, "member-mutate-owner@example.com")
	insertProjectStoreUserForTest(t, db, viewerID, "member-mutate-viewer@example.com")
	insertProjectStoreUserForTest(t, db, memberID, "member-mutate-target@example.test")
	insertProjectStoreUserForTest(t, db, otherMemberID, "member-mutate-other@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Member Mutate Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreMembershipForTest(t, db, viewerMembershipID, projectID, viewerID, projects.RoleViewer, createdAt, false)
	insertProjectStoreMembershipForTest(t, db, memberMembershipID, projectID, memberID, projects.RoleViewer, createdAt, false)
	insertProjectStoreProjectForTest(t, db, otherProjectID, otherMemberID, "Other Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, otherMembershipID, otherProjectID, otherMemberID, projects.RoleViewer, createdAt, false)

	_, err := store.UpdateProjectMemberRole(ctx, projects.UpdateProjectMemberRoleStoreInput{
		ActorUserID: ownerID,
		ProjectID:   projectID,
		MemberID:    otherMembershipID,
		Role:        projects.RoleEditor,
		UpdatedAt:   updatedAt,
	})
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("UpdateProjectMemberRole() cross-project error = %v, want ErrNotFound", err)
	}

	_, err = store.UpdateProjectMemberRole(ctx, projects.UpdateProjectMemberRoleStoreInput{
		ActorUserID: viewerID,
		ProjectID:   projectID,
		MemberID:    memberMembershipID,
		Role:        projects.RoleEditor,
		UpdatedAt:   updatedAt,
	})
	if !errors.Is(err, projects.ErrForbidden) {
		t.Fatalf("UpdateProjectMemberRole() viewer error = %v, want ErrForbidden", err)
	}

	updated, err := store.UpdateProjectMemberRole(ctx, projects.UpdateProjectMemberRoleStoreInput{
		ActorUserID: ownerID,
		ProjectID:   projectID,
		MemberID:    memberMembershipID,
		Role:        projects.RoleEditor,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		t.Fatalf("UpdateProjectMemberRole() owner error = %v", err)
	}
	if updated.Role != projects.RoleEditor || updated.Email != "member-mutate-target@example.test" {
		t.Fatalf("updated membership = %#v, want editor with PII", updated)
	}

	if err := store.RemoveProjectMember(ctx, projects.RemoveProjectMemberStoreInput{
		ActorUserID: ownerID,
		ProjectID:   projectID,
		MemberID:    memberMembershipID,
		RemovedAt:   updatedAt.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RemoveProjectMember() error = %v", err)
	}
	_, err = store.GetProject(ctx, memberID, projectID)
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("GetProject() after member removal error = %v, want ErrNotFound", err)
	}
}

func TestPermissionLossRemovalRetryRecognizesRemovedMember(t *testing.T) {
	removedAt := time.Now().UTC().Truncate(time.Microsecond)
	locked := lockedProjectMemberMutation{
		targetRemovedAt: pgtype.Timestamptz{Time: removedAt, Valid: true},
	}
	if !locked.removalRetry() {
		t.Fatal("removed member was not recognized as cleanup retry")
	}
}

func TestProjectsStorePermissionLossEnqueuesNativeRootDeletion(t *testing.T) {
	t.Run("editor downgrade enqueues exact actor intent", func(t *testing.T) {
		ctx := context.Background()
		db, ownerID, memberUserID, projectID, memberID, createdAt := newPermissionLossFixtureForTest(t, projects.RoleEditor)
		updatedAt := createdAt.Add(time.Hour)
		store := New(db)

		updated, err := store.UpdateProjectMemberRole(ctx, projects.UpdateProjectMemberRoleStoreInput{
			ActorUserID: ownerID, ProjectID: projectID, MemberID: memberID,
			Role: projects.RoleViewer, UpdatedAt: updatedAt,
		})
		if err != nil {
			t.Fatalf("UpdateProjectMemberRole() error = %v", err)
		}
		if updated.Role != projects.RoleViewer || !updated.UpdatedAt.Equal(updatedAt) {
			t.Fatalf("updated membership = %#v", updated)
		}
		tombstoneID := assertPermissionLossIntentForTest(t, db, projectID, memberUserID, updatedAt)
		completePermissionLossNoDataTargetsForTest(t, db, memberUserID)
		retried, err := store.UpdateProjectMemberRole(ctx, projects.UpdateProjectMemberRoleStoreInput{
			ActorUserID: ownerID, ProjectID: projectID, MemberID: memberID,
			Role: projects.RoleViewer, UpdatedAt: updatedAt.Add(time.Minute),
		})
		if err != nil || !retried.UpdatedAt.Equal(updatedAt) {
			t.Fatalf("exact downgrade retry = %#v, %v", retried, err)
		}
		if retryID := assertPermissionLossIntentForTest(t, db, projectID, memberUserID, updatedAt); retryID != tombstoneID {
			t.Fatalf("downgrade retry tombstone = %q, want %q", retryID, tombstoneID)
		}
	})

	t.Run("member removal commits with durable intent", func(t *testing.T) {
		ctx := context.Background()
		db, ownerID, memberUserID, projectID, memberID, createdAt := newPermissionLossFixtureForTest(t, projects.RoleEditor)
		removedAt := createdAt.Add(time.Hour)
		store := New(db)

		err := store.RemoveProjectMember(ctx, projects.RemoveProjectMemberStoreInput{
			ActorUserID: ownerID, ProjectID: projectID, MemberID: memberID, RemovedAt: removedAt,
		})
		if err != nil {
			t.Fatalf("RemoveProjectMember() error = %v", err)
		}
		var role string
		var removedAtDB pgtype.Timestamptz
		if err := db.Pool.QueryRow(ctx, `
			SELECT role, removed_at FROM project_memberships
			WHERE id = $1 AND project_id = $2
		`, memberID, projectID).Scan(&role, &removedAtDB); err != nil {
			t.Fatal(err)
		}
		if role != string(projects.RoleEditor) || !removedAtDB.Valid || !removedAtDB.Time.Equal(removedAt) {
			t.Fatalf("permission loss did not commit before cleanup: role=%q removed=%v", role, removedAtDB)
		}
		tombstoneID := assertPermissionLossIntentForTest(t, db, projectID, memberUserID, removedAt)
		if err := store.RemoveProjectMember(ctx, projects.RemoveProjectMemberStoreInput{
			ActorUserID: ownerID, ProjectID: projectID, MemberID: memberID, RemovedAt: removedAt.Add(time.Minute),
		}); err != nil {
			t.Fatalf("exact RemoveProjectMember() retry error = %v", err)
		}
		if retryID := assertPermissionLossIntentForTest(t, db, projectID, memberUserID, removedAt); retryID != tombstoneID {
			t.Fatalf("removal retry tombstone = %q, want %q", retryID, tombstoneID)
		}
	})

	t.Run("permission-preserving and metadata mutations do not enqueue deletion", func(t *testing.T) {
		ctx := context.Background()
		db, ownerID, memberUserID, projectID, memberID, createdAt := newPermissionLossFixtureForTest(t, projects.RoleAdmin)
		store := New(db)

		if _, err := store.UpdateProjectMemberRole(ctx, projects.UpdateProjectMemberRoleStoreInput{
			ActorUserID: ownerID, ProjectID: projectID, MemberID: memberID,
			Role: projects.RoleEditor, UpdatedAt: createdAt.Add(time.Hour),
		}); err != nil {
			t.Fatalf("UpdateProjectMemberRole() error = %v", err)
		}
		if _, err := store.UpdateProject(ctx, projects.UpdateProjectStoreInput{
			ActorUserID: ownerID, ProjectID: projectID, Title: "Renamed",
			Description: "metadata only", UpdatedAt: createdAt.Add(2 * time.Hour),
		}); err != nil {
			t.Fatalf("UpdateProject() error = %v", err)
		}
		var intents int
		if err := db.Pool.QueryRow(ctx, `
			SELECT count(*)::int FROM deletion_tombstones
			WHERE project_id = $1 AND permission_loss_actor_user_id = $2
		`, projectID, memberUserID).Scan(&intents); err != nil {
			t.Fatal(err)
		}
		if intents != 0 {
			t.Fatalf("permission loss intents = %d, want 0", intents)
		}
	})
}

func TestProjectsStoreCreateAndListProjectInvitationPersistsContractFields(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	inviterID := "00000000-0000-4000-8000-000000000524"
	projectID := "00000000-0000-4000-8000-000000000624"
	membershipID := "00000000-0000-4000-8000-000000000724"
	invitationID := "00000000-0000-4000-8000-000000000824"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(7 * 24 * time.Hour)
	cleanupProjectsStoreRows(t, db, inviterID, projectID)
	insertProjectStoreUserForTest(t, db, inviterID, "project-inviter@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, inviterID, "Invitation Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, inviterID, projects.RoleOwner, now, false)

	invitation, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
		ID:          invitationID,
		ProjectID:   projectID,
		ActorUserID: inviterID,
		Email:       "invitee@example.test",
		EmailHash:   strings.Repeat("a", 64),
		Role:        projects.RoleEditor,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateProjectInvitation() error = %v", err)
	}

	if invitation.ID != invitationID || invitation.ProjectID != projectID || invitation.ProjectTitle != "Invitation Project" {
		t.Fatalf("invitation project fields = %q/%q/%q, want contract project context", invitation.ID, invitation.ProjectID, invitation.ProjectTitle)
	}
	if invitation.InviterDisplayName != "Project Owner" || invitation.InviterEmail != "project-inviter@example.com" {
		t.Fatalf("inviter fields = %q/%q, want Project Owner/project-inviter@example.com", invitation.InviterDisplayName, invitation.InviterEmail)
	}
	if invitation.Email != "invitee@example.test" || invitation.EmailHash != strings.Repeat("a", 64) || invitation.Role != projects.RoleEditor || invitation.Status != projects.ProjectInvitationStatusPending {
		t.Fatalf("invitation role/status = %s/%s, want editor/pending", invitation.Role, invitation.Status)
	}
	if !invitation.ExpiresAt.Equal(expiresAt) || !invitation.CreatedAt.Equal(now) || !invitation.UpdatedAt.Equal(now) {
		t.Fatalf("invitation timestamps = %s/%s/%s, want %s/%s/%s", invitation.ExpiresAt, invitation.CreatedAt, invitation.UpdatedAt, expiresAt, now, now)
	}

	invitations, err := store.ListProjectInvitations(ctx, projects.ListProjectInvitationsStoreInput{
		ProjectID:   projectID,
		ActorUserID: inviterID,
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("ListProjectInvitations() error = %v", err)
	}
	if len(invitations) != 1 || invitations[0].ID != invitationID {
		t.Fatalf("ListProjectInvitations() IDs = %v, want [%s]", projectInvitationIDsForTest(invitations), invitationID)
	}
}

func TestProjectsStoreListInvitationRejectsDirectUnauthorizedActors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name          string
		actorID       string
		projectID     string
		actorRole     projects.Role
		hasMembership bool
		removed       bool
		deleted       bool
		wantErr       error
	}{
		{
			name:          "viewer",
			actorID:       "00000000-0000-4000-8000-000000000540",
			projectID:     "00000000-0000-4000-8000-000000000640",
			actorRole:     projects.RoleViewer,
			hasMembership: true,
			wantErr:       projects.ErrForbidden,
		},
		{
			name:      "non-member",
			actorID:   "00000000-0000-4000-8000-000000000541",
			projectID: "00000000-0000-4000-8000-000000000641",
			wantErr:   projects.ErrNotFound,
		},
		{
			name:          "removed-admin",
			actorID:       "00000000-0000-4000-8000-000000000542",
			projectID:     "00000000-0000-4000-8000-000000000642",
			actorRole:     projects.RoleAdmin,
			hasMembership: true,
			removed:       true,
			wantErr:       projects.ErrNotFound,
		},
		{
			name:          "deleted-project",
			actorID:       "00000000-0000-4000-8000-000000000543",
			projectID:     "00000000-0000-4000-8000-000000000643",
			actorRole:     projects.RoleOwner,
			hasMembership: true,
			deleted:       true,
			wantErr:       projects.ErrNotFound,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			ownerID := "00000000-0000-4000-8000-000000000544"
			ownerMembershipID := "00000000-0000-4000-8000-00000000074" + string(rune('0'+i))
			actorMembershipID := "00000000-0000-4000-8000-00000000084" + string(rune('0'+i))
			invitationID := "00000000-0000-4000-8000-00000000094" + string(rune('0'+i))
			now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			cleanupProjectsStoreRows(t, db, ownerID, tt.projectID)
			cleanupProjectsStoreRows(t, db, tt.actorID, tt.projectID)
			insertProjectStoreUserForTest(t, db, ownerID, "direct-list-owner-"+tt.name+"@example.com")
			insertProjectStoreUserForTest(t, db, tt.actorID, "direct-list-actor-"+tt.name+"@example.com")
			insertProjectStoreProjectForTest(t, db, tt.projectID, ownerID, "Direct List Project", now)
			insertProjectStoreMembershipForTest(t, db, ownerMembershipID, tt.projectID, ownerID, projects.RoleOwner, now, false)
			if tt.hasMembership {
				insertProjectStoreMembershipForTest(t, db, actorMembershipID, tt.projectID, tt.actorID, tt.actorRole, now, tt.removed)
			}
			insertProjectStoreInvitationForTest(t, db, invitationID, tt.projectID, ownerID, "direct-list-"+tt.name+"@example.test", strings.Repeat("7", 64), projects.RoleViewer, projects.ProjectInvitationStatusPending, now.Add(14*24*time.Hour), now)
			if tt.deleted {
				fenceProjectDeletionForTest(t, db, ownerID, tt.projectID, now.Add(time.Hour))
			}

			_, err := store.ListProjectInvitations(ctx, projects.ListProjectInvitationsStoreInput{
				ProjectID:   tt.projectID,
				ActorUserID: tt.actorID,
				Limit:       20,
			})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ListProjectInvitations() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestProjectsStoreCreateInvitationRejectsDirectUnauthorizedActors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name          string
		actorID       string
		role          projects.Role
		hasMembership bool
		wantErr       error
		suffix        string
		hashSeed      string
	}{
		{name: "editor", actorID: "00000000-0000-4000-8000-000000000526", role: projects.RoleEditor, hasMembership: true, wantErr: projects.ErrForbidden, suffix: "526", hashSeed: "e"},
		{name: "viewer", actorID: "00000000-0000-4000-8000-000000000527", role: projects.RoleViewer, hasMembership: true, wantErr: projects.ErrForbidden, suffix: "527", hashSeed: "f"},
		{name: "non-member", actorID: "00000000-0000-4000-8000-000000000528", hasMembership: false, wantErr: projects.ErrNotFound, suffix: "528", hashSeed: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			ownerID := "00000000-0000-4000-8000-000000000525"
			projectID := "00000000-0000-4000-8000-000000000" + tt.suffix
			ownerMembershipID := "00000000-0000-4000-8000-000000000725"
			actorMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "9"
			invitationID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "8"
			now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			cleanupProjectsStoreRows(t, db, ownerID, projectID)
			cleanupProjectsStoreRows(t, db, tt.actorID, projectID)
			insertProjectStoreUserForTest(t, db, ownerID, "direct-create-owner-"+tt.suffix+"@example.com")
			insertProjectStoreUserForTest(t, db, tt.actorID, "direct-create-actor-"+tt.suffix+"@example.com")
			insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Direct Create Project", now)
			insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, now, false)
			if tt.hasMembership {
				insertProjectStoreMembershipForTest(t, db, actorMembershipID, projectID, tt.actorID, tt.role, now, false)
			}

			_, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
				ID:          invitationID,
				ProjectID:   projectID,
				ActorUserID: tt.actorID,
				Email:       "blocked-" + tt.suffix + "@example.test",
				EmailHash:   strings.Repeat(tt.hashSeed, 64),
				Role:        projects.RoleViewer,
				ExpiresAt:   now.Add(14 * 24 * time.Hour),
				CreatedAt:   now,
			})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateProjectInvitation() error = %v, want %v", err, tt.wantErr)
			}
			if got := projectInvitationCountForProjectForTest(t, db, projectID); got != 0 {
				t.Fatalf("invitation count after denied direct create = %d, want 0", got)
			}
		})
	}
}

func TestProjectsStoreCreateInvitationRejectsDuplicatePendingInvite(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000529"
	projectID := "00000000-0000-4000-8000-000000000629"
	membershipID := "00000000-0000-4000-8000-000000000729"
	firstInvitationID := "00000000-0000-4000-8000-000000000829"
	secondInvitationID := "00000000-0000-4000-8000-000000000929"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	emailHash := strings.Repeat("b", 64)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "duplicate-invite-owner@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Duplicate Invite Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, ownerID, projects.RoleOwner, now, false)
	if _, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
		ID:          firstInvitationID,
		ProjectID:   projectID,
		ActorUserID: ownerID,
		Email:       "duplicate@example.test",
		EmailHash:   emailHash,
		Role:        projects.RoleViewer,
		ExpiresAt:   now.Add(14 * 24 * time.Hour),
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateProjectInvitation() first error = %v", err)
	}

	_, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
		ID:          secondInvitationID,
		ProjectID:   projectID,
		ActorUserID: ownerID,
		Email:       "duplicate@example.test",
		EmailHash:   emailHash,
		Role:        projects.RoleEditor,
		ExpiresAt:   now.Add(14 * 24 * time.Hour),
		CreatedAt:   now,
	})

	if !errors.Is(err, projects.ErrConflict) {
		t.Fatalf("CreateProjectInvitation() duplicate error = %v, want ErrConflict", err)
	}
	if got := projectInvitationCountForProjectForTest(t, db, projectID); got != 1 {
		t.Fatalf("invitation count after duplicate create = %d, want 1", got)
	}
}

func TestProjectsStoreCreateInvitationRejectsExistingActiveMemberEmail(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000530"
	memberID := "00000000-0000-4000-8000-000000000531"
	projectID := "00000000-0000-4000-8000-000000000630"
	ownerMembershipID := "00000000-0000-4000-8000-000000000730"
	memberMembershipID := "00000000-0000-4000-8000-000000000731"
	invitationID := "00000000-0000-4000-8000-000000000830"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, memberID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "active-member-owner@example.com")
	insertProjectStoreUserForTest(t, db, memberID, "member@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Active Member Project", now)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, now, false)
	insertProjectStoreMembershipForTest(t, db, memberMembershipID, projectID, memberID, projects.RoleViewer, now, false)

	_, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
		ID:          invitationID,
		ProjectID:   projectID,
		ActorUserID: ownerID,
		Email:       "member@example.test",
		EmailHash:   strings.Repeat("c", 64),
		Role:        projects.RoleEditor,
		ExpiresAt:   now.Add(14 * 24 * time.Hour),
		CreatedAt:   now,
	})

	if !errors.Is(err, projects.ErrConflict) {
		t.Fatalf("CreateProjectInvitation() active member error = %v, want ErrConflict", err)
	}
	if got := projectInvitationCountForProjectForTest(t, db, projectID); got != 0 {
		t.Fatalf("invitation count after active member create = %d, want 0", got)
	}
}

func TestProjectsStoreCreateInvitationAllowsRemovedMemberEmail(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000532"
	memberID := "00000000-0000-4000-8000-000000000533"
	projectID := "00000000-0000-4000-8000-000000000632"
	ownerMembershipID := "00000000-0000-4000-8000-000000000732"
	memberMembershipID := "00000000-0000-4000-8000-000000000733"
	invitationID := "00000000-0000-4000-8000-000000000832"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, memberID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "removed-member-owner@example.com")
	insertProjectStoreUserForTest(t, db, memberID, "removed-member@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Removed Member Project", now)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, now, false)
	insertProjectStoreMembershipForTest(t, db, memberMembershipID, projectID, memberID, projects.RoleViewer, now.Add(-time.Hour), true)

	invitation, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
		ID:          invitationID,
		ProjectID:   projectID,
		ActorUserID: ownerID,
		Email:       "removed-member@example.test",
		EmailHash:   strings.Repeat("d", 64),
		Role:        projects.RoleViewer,
		ExpiresAt:   now.Add(14 * 24 * time.Hour),
		CreatedAt:   now,
	})

	if err != nil {
		t.Fatalf("CreateProjectInvitation() removed member error = %v", err)
	}
	if invitation.ID != invitationID || invitation.Status != projects.ProjectInvitationStatusPending {
		t.Fatalf("invitation id/status = %q/%s, want created pending", invitation.ID, invitation.Status)
	}
}

func TestActorCollectionProjectsStoreListInvitationsRejectsUnownedUnverifiedAndDisabledActors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		actorID    string
		actorEmail string
		verified   bool
		disabled   bool
		suffix     string
	}{
		{name: "wrong actor", actorID: "00000000-0000-4000-8000-000000000547", actorEmail: "wrong-list@example.test", verified: true, suffix: "647"},
		{name: "unverified actor", actorID: "00000000-0000-4000-8000-000000000548", actorEmail: "invitee-list-unverified@example.test", suffix: "648"},
		{name: "disabled actor", actorID: "00000000-0000-4000-8000-000000000549", actorEmail: "invitee-list-disabled@example.test", verified: true, disabled: true, suffix: "649"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			ownerID := "00000000-0000-4000-8000-000000000550"
			projectID := "00000000-0000-4000-8000-000000000" + tt.suffix
			ownerMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "0"
			invitationID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "1"
			createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			inviteeEmail := "invitee-list-" + tt.suffix + "@example.test"
			if tt.name != "wrong actor" {
				inviteeEmail = tt.actorEmail
			}
			emailHash := emailHashForProjectStoreTest(inviteeEmail)
			cleanupProjectsStoreRows(t, db, ownerID, projectID)
			cleanupProjectsStoreRows(t, db, tt.actorID, projectID)
			insertProjectStoreUserForTest(t, db, ownerID, "list-my-owner-"+tt.suffix+"@example.com")
			insertProjectStoreUserWithStateForTest(t, db, tt.actorID, tt.actorEmail, tt.verified, tt.disabled, createdAt)
			insertProjectStoreProjectForTest(t, db, projectID, ownerID, "List My Project", createdAt)
			insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
			insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, inviteeEmail, emailHash, projects.RoleViewer, projects.ProjectInvitationStatusPending, createdAt.Add(14*24*time.Hour), createdAt)

			invitations, err := store.ListProjectInvitationsForActor(ctx, projects.ListProjectInvitationsForActorStoreInput{
				ActorUserID: tt.actorID,
				EmailHash:   emailHash,
				Limit:       20,
			})
			if err != nil {
				t.Fatalf("ListProjectInvitationsForActor() error = %v", err)
			}
			if len(invitations) != 0 {
				t.Fatalf("ListProjectInvitationsForActor() exposed %d invitations for %s, want 0", len(invitations), tt.name)
			}
		})
	}
}

func TestActorCollectionProjectsStoreListInvitationsUsesCursorAndReturnsOnlyPublicFields(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000556"
	inviteeID := "00000000-0000-4000-8000-000000000557"
	projectID := "00000000-0000-4000-8000-000000000656"
	newestProjectID := "00000000-0000-4000-8000-000000000657"
	ownerMembershipID := "00000000-0000-4000-8000-000000000756"
	newestOwnerMembershipID := "00000000-0000-4000-8000-000000000757"
	invitationID := "00000000-0000-4000-8000-000000000856"
	newestInvitationID := "00000000-0000-4000-8000-000000000857"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(14 * 24 * time.Hour)
	email := "list-owned@example.test"
	emailHash := emailHashForProjectStoreTest(email)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, ownerID, newestProjectID)
	cleanupProjectsStoreRows(t, db, inviteeID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "list-owned-owner@example.com")
	insertProjectStoreUserForTest(t, db, inviteeID, email)
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Owned Invitation Project", createdAt)
	insertProjectStoreProjectForTest(t, db, newestProjectID, ownerID, "Newest Invitation Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreMembershipForTest(t, db, newestOwnerMembershipID, newestProjectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, email, emailHash, projects.RoleAdmin, projects.ProjectInvitationStatusPending, expiresAt, createdAt)
	insertProjectStoreInvitationForTest(t, db, newestInvitationID, newestProjectID, ownerID, email, emailHash, projects.RoleViewer, projects.ProjectInvitationStatusPending, expiresAt, createdAt)

	firstPage, err := store.ListProjectInvitationsForActor(ctx, projects.ListProjectInvitationsForActorStoreInput{
		ActorUserID: inviteeID,
		EmailHash:   emailHash,
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("ListProjectInvitationsForActor() first page error = %v", err)
	}
	if len(firstPage) != 1 || firstPage[0].ID != newestInvitationID {
		t.Fatalf("ListProjectInvitationsForActor() first page IDs = %v, want [%s]", projectInvitationIDsForTest(firstPage), newestInvitationID)
	}

	secondPage, err := store.ListProjectInvitationsForActor(ctx, projects.ListProjectInvitationsForActorStoreInput{
		ActorUserID:     inviteeID,
		EmailHash:       emailHash,
		Limit:           1,
		HasCursor:       true,
		CursorCreatedAt: firstPage[0].CreatedAt,
		CursorID:        firstPage[0].ID,
	})
	if err != nil {
		t.Fatalf("ListProjectInvitationsForActor() second page error = %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != invitationID {
		t.Fatalf("ListProjectInvitationsForActor() second page IDs = %v, want [%s]", projectInvitationIDsForTest(secondPage), invitationID)
	}
	invitation := secondPage[0]
	if invitation.ID != invitationID || invitation.ProjectID != projectID || invitation.ProjectTitle != "Owned Invitation Project" {
		t.Fatalf("invitation project fields = %q/%q/%q, want expected ID/project/title", invitation.ID, invitation.ProjectID, invitation.ProjectTitle)
	}
	if invitation.InviterDisplayName != "Project Owner" || invitation.InviterEmail != "list-owned-owner@example.com" {
		t.Fatalf("public inviter fields = %q/%q, want owner display name/email", invitation.InviterDisplayName, invitation.InviterEmail)
	}
	if invitation.Email != email || invitation.Role != projects.RoleAdmin || invitation.Status != projects.ProjectInvitationStatusPending {
		t.Fatalf("invitation email/role/status = %q/%s/%s, want owned pending admin invitation", invitation.Email, invitation.Role, invitation.Status)
	}
	if invitation.InviterUserID != "" || invitation.EmailHash != "" || invitation.CanceledAt != nil {
		t.Fatalf("non-public invitation fields leaked: inviterUserID=%q emailHash=%q canceledAt=%v", invitation.InviterUserID, invitation.EmailHash, invitation.CanceledAt)
	}
	if !invitation.ExpiresAt.Equal(expiresAt) || !invitation.CreatedAt.Equal(createdAt) || !invitation.UpdatedAt.Equal(createdAt) {
		t.Fatalf("invitation timestamps = %s/%s/%s, want %s/%s/%s", invitation.ExpiresAt, invitation.CreatedAt, invitation.UpdatedAt, expiresAt, createdAt, createdAt)
	}
}

func TestProjectsStoreResolveInvitationInviteeAccessRejectsUnownedUnverifiedDisabledAndDeleted(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		actorID    string
		actorEmail string
		verified   bool
		disabled   bool
		deleted    bool
		suffix     string
	}{
		{name: "wrong actor", actorID: "00000000-0000-4000-8000-000000000558", actorEmail: "wrong-preflight@example.test", verified: true, suffix: "658"},
		{name: "unverified actor", actorID: "00000000-0000-4000-8000-000000000559", actorEmail: "preflight-unverified@example.test", suffix: "659"},
		{name: "disabled actor", actorID: "00000000-0000-4000-8000-000000000560", actorEmail: "preflight-disabled@example.test", verified: true, disabled: true, suffix: "660"},
		{name: "deleted project", actorID: "00000000-0000-4000-8000-000000000561", actorEmail: "preflight-deleted@example.test", verified: true, deleted: true, suffix: "661"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			ownerID := "00000000-0000-4000-8000-000000000562"
			projectID := "00000000-0000-4000-8000-000000000" + tt.suffix
			ownerMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "3"
			invitationID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "4"
			createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			invitationEmail := tt.actorEmail
			if tt.name == "wrong actor" {
				invitationEmail = "preflight-owned@example.test"
			}
			emailHash := emailHashForProjectStoreTest(invitationEmail)
			cleanupProjectsStoreRows(t, db, ownerID, projectID)
			cleanupProjectsStoreRows(t, db, tt.actorID, projectID)
			insertProjectStoreUserForTest(t, db, ownerID, "preflight-owner-"+tt.suffix+"@example.com")
			insertProjectStoreUserWithStateForTest(t, db, tt.actorID, tt.actorEmail, tt.verified, tt.disabled, createdAt)
			insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Preflight Project", createdAt)
			insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
			insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, invitationEmail, emailHash, projects.RoleViewer, projects.ProjectInvitationStatusPending, createdAt.Add(14*24*time.Hour), createdAt)
			if tt.deleted {
				fenceProjectDeletionForTest(t, db, ownerID, projectID, createdAt.Add(time.Hour))
			}

			_, err := store.ResolveProjectInvitationInviteeAccessForActor(ctx, projects.ResolveProjectInvitationInviteeAccessStoreInput{
				ActorUserID:  tt.actorID,
				InvitationID: invitationID,
				EmailHash:    emailHash,
			})

			if !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("ResolveProjectInvitationInviteeAccessForActor() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestProjectsStoreAcceptInvitationRejectsMismatchedUserEmailHash(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000548"
	inviteeID := "00000000-0000-4000-8000-000000000549"
	wrongUserID := "00000000-0000-4000-8000-000000000550"
	projectID := "00000000-0000-4000-8000-000000000648"
	ownerMembershipID := "00000000-0000-4000-8000-000000000748"
	newMembershipID := "00000000-0000-4000-8000-000000000749"
	invitationID := "00000000-0000-4000-8000-000000000848"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	acceptedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	emailHash := emailHashForProjectStoreTest("accept-invitee@example.test")
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, inviteeID, projectID)
	cleanupProjectsStoreRows(t, db, wrongUserID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "accept-mismatch-owner@example.com")
	insertProjectStoreUserForTest(t, db, inviteeID, "accept-invitee@example.test")
	insertProjectStoreUserForTest(t, db, wrongUserID, "accept-wrong@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Accept Mismatch Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, "accept-invitee@example.test", emailHash, projects.RoleEditor, projects.ProjectInvitationStatusPending, acceptedAt.Add(time.Hour), createdAt)

	_, err := store.AcceptProjectInvitation(ctx, projects.AcceptProjectInvitationStoreInput{
		InvitationID: invitationID,
		ProjectID:    projectID,
		MembershipID: newMembershipID,
		UserID:       wrongUserID,
		EmailHash:    emailHash,
		AcceptedAt:   acceptedAt,
	})

	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("AcceptProjectInvitation() mismatched user error = %v, want ErrNotFound", err)
	}
	if membershipCountForProjectUserForTest(t, db, projectID, wrongUserID) != 0 {
		t.Fatal("AcceptProjectInvitation() created membership for mismatched user")
	}
	status, acceptedAtStored := projectInvitationStateForTest(t, db, invitationID)
	if status != projects.ProjectInvitationStatusPending || acceptedAtStored.Valid {
		t.Fatalf("invitation after denied accept = %s accepted valid %t, want pending false", status, acceptedAtStored.Valid)
	}
}

func TestProjectsStoreAcceptInvitationRejectsUnverifiedAndDisabledUsers(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		userID   string
		verified bool
		disabled bool
		suffix   string
	}{
		{name: "unverified", userID: "00000000-0000-4000-8000-000000000551", suffix: "651"},
		{name: "disabled", userID: "00000000-0000-4000-8000-000000000552", verified: true, disabled: true, suffix: "652"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			ownerID := "00000000-0000-4000-8000-000000000553"
			projectID := "00000000-0000-4000-8000-000000000" + tt.suffix
			ownerMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "0"
			newMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "1"
			invitationID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "2"
			createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			acceptedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
			email := "accept-" + tt.name + "@example.test"
			emailHash := emailHashForProjectStoreTest(email)
			cleanupProjectsStoreRows(t, db, ownerID, projectID)
			cleanupProjectsStoreRows(t, db, tt.userID, projectID)
			insertProjectStoreUserForTest(t, db, ownerID, "accept-state-owner-"+tt.suffix+"@example.com")
			insertProjectStoreUserWithStateForTest(t, db, tt.userID, email, tt.verified, tt.disabled, createdAt)
			insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Accept State Project", createdAt)
			insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
			insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, email, emailHash, projects.RoleEditor, projects.ProjectInvitationStatusPending, acceptedAt.Add(time.Hour), createdAt)

			_, err := store.AcceptProjectInvitation(ctx, projects.AcceptProjectInvitationStoreInput{
				InvitationID: invitationID,
				ProjectID:    projectID,
				MembershipID: newMembershipID,
				UserID:       tt.userID,
				EmailHash:    emailHash,
				AcceptedAt:   acceptedAt,
			})

			if !errors.Is(err, projects.ErrNotFound) {
				t.Fatalf("AcceptProjectInvitation() %s user error = %v, want ErrNotFound", tt.name, err)
			}
			if membershipCountForProjectUserForTest(t, db, projectID, tt.userID) != 0 {
				t.Fatalf("AcceptProjectInvitation() created membership for %s user", tt.name)
			}
			status, acceptedAtStored := projectInvitationStateForTest(t, db, invitationID)
			if status != projects.ProjectInvitationStatusPending || acceptedAtStored.Valid {
				t.Fatalf("invitation after denied %s accept = %s accepted valid %t, want pending false", tt.name, status, acceptedAtStored.Valid)
			}
		})
	}
}

func TestProjectsStoreAcceptInvitationRejectsDeletedProject(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000554"
	userID := "00000000-0000-4000-8000-000000000555"
	projectID := "00000000-0000-4000-8000-000000000654"
	ownerMembershipID := "00000000-0000-4000-8000-000000000754"
	newMembershipID := "00000000-0000-4000-8000-000000000755"
	invitationID := "00000000-0000-4000-8000-000000000854"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	acceptedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	email := "accept-deleted@example.test"
	emailHash := emailHashForProjectStoreTest(email)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "accept-deleted-owner@example.com")
	insertProjectStoreUserForTest(t, db, userID, email)
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Accept Deleted Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, email, emailHash, projects.RoleEditor, projects.ProjectInvitationStatusPending, acceptedAt.Add(time.Hour), createdAt)
	fenceProjectDeletionForTest(t, db, ownerID, projectID, acceptedAt.Add(-time.Minute))

	_, err := store.AcceptProjectInvitation(ctx, projects.AcceptProjectInvitationStoreInput{
		InvitationID: invitationID,
		ProjectID:    projectID,
		MembershipID: newMembershipID,
		UserID:       userID,
		EmailHash:    emailHash,
		AcceptedAt:   acceptedAt,
	})

	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("AcceptProjectInvitation() deleted project error = %v, want ErrNotFound", err)
	}
	if membershipCountForProjectUserForTest(t, db, projectID, userID) != 0 {
		t.Fatal("AcceptProjectInvitation() created membership for deleted project")
	}
	status, acceptedAtStored := projectInvitationStateForTest(t, db, invitationID)
	if status != projects.ProjectInvitationStatusCanceled || acceptedAtStored.Valid {
		t.Fatalf("invitation after deleted project accept = %s accepted valid %t, want canceled false", status, acceptedAtStored.Valid)
	}
}

func TestProjectsStoreRegisteredBackendUserCanAcceptInvitation(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	projectStore := New(db)
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	ownerID := "00000000-0000-4000-8000-000000000570"
	inviteeID := "00000000-0000-4000-8000-000000000571"
	projectID := "00000000-0000-4000-8000-000000000670"
	ownerMembershipID := "00000000-0000-4000-8000-000000000770"
	email := "registered-invitee@example.test"
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, inviteeID, projectID)
	cleanupAuthStoreRows(t, db, inviteeID)
	insertProjectStoreUserForTest(t, db, ownerID, "registered-invite-owner@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Registered Invite Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	projectService := projects.NewService(projectStore, projects.NewSecureIDGenerator(), authStoreClockForTest{now: createdAt})
	invitation, err := projectService.CreateProjectInvitation(ctx, ownerID, projectID, projects.CreateProjectInvitationInput{
		Email: email,
		Role:  projects.RoleEditor,
	})
	if err != nil {
		t.Fatalf("CreateProjectInvitation() error = %v", err)
	}

	authService := auth.NewService(
		authstore.New(db),
		&authStoreIDsForTest{next: 570},
		authStoreClockForTest{now: createdAt},
	)
	// Accepting an invitation needs a verified address.
	created, err := authService.CreateUser(ctx, auth.NewUser{Email: email, DisplayName: "Registered Invitee", EmailVerified: true})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	session, _, err := authService.CreateSession(ctx, auth.NewSession{UserID: created.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	user, err := authService.CurrentUser(ctx, session.CookieValue)
	if err != nil || !user.EmailVerified {
		t.Fatalf("CurrentUser() of the verified user = %+v, %v", user, err)
	}

	membership, err := projectService.AcceptProjectInvitation(ctx, projects.AuthenticatedUser{
		ID:            user.ID,
		Email:         user.Email,
		EmailVerified: user.EmailVerified,
	}, invitation.ID)
	if err != nil {
		t.Fatalf("AcceptProjectInvitation() error = %v", err)
	}
	if user.ID != inviteeID {
		t.Fatalf("registered user ID = %s, want %s", user.ID, inviteeID)
	}
	if membership.ProjectID != projectID || membership.UserID != inviteeID || membership.Role != projects.RoleEditor || membership.Status != projects.MembershipStatusActive {
		t.Fatalf("membership = %#v, want active editor membership for registered invitee", membership)
	}
	if status := projectInvitationStatusForTest(t, db, invitation.ID); status != projects.ProjectInvitationStatusAccepted {
		t.Fatalf("invitation status = %s, want accepted", status)
	}
}

func TestProjectsStoreAcceptInvitationReactivatesRemovedMember(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000545"
	memberID := "00000000-0000-4000-8000-000000000546"
	projectID := "00000000-0000-4000-8000-000000000645"
	ownerMembershipID := "00000000-0000-4000-8000-000000000745"
	memberMembershipID := "00000000-0000-4000-8000-000000000746"
	newMembershipID := "00000000-0000-4000-8000-000000000747"
	invitationID := "00000000-0000-4000-8000-000000000845"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	acceptedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	emailHash := strings.Repeat("8", 64)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, memberID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "accept-removed-owner@example.com")
	insertProjectStoreUserForTest(t, db, memberID, "accept-removed-member@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Accept Removed Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreMembershipForTest(t, db, memberMembershipID, projectID, memberID, projects.RoleViewer, createdAt, true)
	insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, "accept-removed-member@example.test", emailHash, projects.RoleAdmin, projects.ProjectInvitationStatusPending, acceptedAt.Add(time.Hour), createdAt)

	_, err := store.AcceptProjectInvitation(ctx, projects.AcceptProjectInvitationStoreInput{
		InvitationID: invitationID,
		ProjectID:    "00000000-0000-4000-8000-000000000646",
		MembershipID: newMembershipID,
		UserID:       memberID,
		EmailHash:    emailHash,
		AcceptedAt:   acceptedAt,
	})
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("AcceptProjectInvitation(other project) error = %v, want ErrNotFound", err)
	}
	if status := projectInvitationStatusForTest(t, db, invitationID); status != projects.ProjectInvitationStatusPending {
		t.Fatalf("invitation status after cross-project accept = %s, want pending", status)
	}

	membership, err := store.AcceptProjectInvitation(ctx, projects.AcceptProjectInvitationStoreInput{
		InvitationID: invitationID,
		ProjectID:    projectID,
		MembershipID: newMembershipID,
		UserID:       memberID,
		EmailHash:    emailHash,
		AcceptedAt:   acceptedAt,
	})
	if err != nil {
		t.Fatalf("AcceptProjectInvitation() removed member error = %v", err)
	}

	if membership.ID != memberMembershipID || membership.ProjectID != projectID || membership.UserID != memberID {
		t.Fatalf("membership identity = %#v, want reactivated existing membership", membership)
	}
	if membership.Role != projects.RoleAdmin || membership.Status != projects.MembershipStatusActive {
		t.Fatalf("membership role/status = %s/%s, want admin/active", membership.Role, membership.Status)
	}
	if !membership.CreatedAt.Equal(createdAt) || !membership.UpdatedAt.Equal(acceptedAt) {
		t.Fatalf("membership timestamps = %s/%s, want %s/%s", membership.CreatedAt, membership.UpdatedAt, createdAt, acceptedAt)
	}

	var storedID string
	var storedRole string
	var removedAt pgtype.Timestamptz
	var storedCreatedAt time.Time
	var storedUpdatedAt time.Time
	if err := db.Pool.QueryRow(ctx, "SELECT id, role, removed_at, created_at, updated_at FROM project_memberships WHERE project_id = $1 AND user_id = $2", projectID, memberID).Scan(&storedID, &storedRole, &removedAt, &storedCreatedAt, &storedUpdatedAt); err != nil {
		t.Fatalf("read reactivated membership: %v", err)
	}
	if storedID != memberMembershipID || storedRole != string(projects.RoleAdmin) || removedAt.Valid || !storedCreatedAt.Equal(createdAt) || !storedUpdatedAt.Equal(acceptedAt) {
		t.Fatalf("stored membership = %s/%s removed %t %s/%s, want existing admin active %s/%s", storedID, storedRole, removedAt.Valid, storedCreatedAt, storedUpdatedAt, createdAt, acceptedAt)
	}
	if status := projectInvitationStatusForTest(t, db, invitationID); status != projects.ProjectInvitationStatusAccepted {
		t.Fatalf("invitation status = %s, want accepted", status)
	}
}

func TestProjectsStoreCreateInvitationExpiresPriorPendingInviteBeforeConflictCheck(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000534"
	projectID := "00000000-0000-4000-8000-000000000634"
	membershipID := "00000000-0000-4000-8000-000000000734"
	oldInvitationID := "00000000-0000-4000-8000-000000000834"
	newInvitationID := "00000000-0000-4000-8000-000000000934"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := createdAt.Add(15 * 24 * time.Hour)
	emailHash := strings.Repeat("e", 64)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, "expired-invite-owner@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Expired Invite Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreInvitationForTest(t, db, oldInvitationID, projectID, ownerID, "expired@example.test", emailHash, projects.RoleViewer, projects.ProjectInvitationStatusPending, createdAt.Add(14*24*time.Hour), createdAt)

	invitation, err := store.CreateProjectInvitation(ctx, projects.CreateProjectInvitationStoreInput{
		ID:          newInvitationID,
		ProjectID:   projectID,
		ActorUserID: ownerID,
		Email:       "expired@example.test",
		EmailHash:   emailHash,
		Role:        projects.RoleEditor,
		ExpiresAt:   now.Add(14 * 24 * time.Hour),
		CreatedAt:   now,
	})

	if err != nil {
		t.Fatalf("CreateProjectInvitation() after expired pending error = %v", err)
	}
	if invitation.ID != newInvitationID {
		t.Fatalf("new invitation ID = %q, want %s", invitation.ID, newInvitationID)
	}
	if status := projectInvitationStatusForTest(t, db, oldInvitationID); status != projects.ProjectInvitationStatusExpired {
		t.Fatalf("old invitation status = %s, want expired", status)
	}
	if got := projectInvitationCountForProjectForTest(t, db, projectID); got != 2 {
		t.Fatalf("invitation count after replacing expired pending = %d, want 2", got)
	}
}

func TestProjectsStoreCancelInvitationRejectsDirectUnauthorizedActors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name          string
		actorID       string
		role          projects.Role
		hasMembership bool
		wantErr       error
		suffix        string
	}{
		{name: "editor", actorID: "00000000-0000-4000-8000-000000000536", role: projects.RoleEditor, hasMembership: true, wantErr: projects.ErrForbidden, suffix: "636"},
		{name: "viewer", actorID: "00000000-0000-4000-8000-000000000537", role: projects.RoleViewer, hasMembership: true, wantErr: projects.ErrForbidden, suffix: "637"},
		{name: "non-member", actorID: "00000000-0000-4000-8000-000000000538", hasMembership: false, wantErr: projects.ErrNotFound, suffix: "638"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openCurrentStoreTestDB(t)
			store := New(db)
			ownerID := "00000000-0000-4000-8000-000000000535"
			projectID := "00000000-0000-4000-8000-000000000" + tt.suffix
			ownerMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "0"
			actorMembershipID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "1"
			invitationID := "00000000-0000-4000-8000-000000000" + tt.suffix[:2] + "2"
			now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
			cleanupProjectsStoreRows(t, db, ownerID, projectID)
			cleanupProjectsStoreRows(t, db, tt.actorID, projectID)
			insertProjectStoreUserForTest(t, db, ownerID, "direct-cancel-owner-"+tt.suffix+"@example.com")
			insertProjectStoreUserForTest(t, db, tt.actorID, "direct-cancel-actor-"+tt.suffix+"@example.com")
			insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Direct Cancel Project", now)
			insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, now, false)
			if tt.hasMembership {
				insertProjectStoreMembershipForTest(t, db, actorMembershipID, projectID, tt.actorID, tt.role, now, false)
			}
			insertProjectStoreInvitationForTest(t, db, invitationID, projectID, ownerID, "cancel-target-"+tt.suffix+"@example.test", strings.Repeat("f", 64), projects.RoleViewer, projects.ProjectInvitationStatusPending, now.Add(14*24*time.Hour), now)

			_, err := store.CancelProjectInvitation(ctx, projects.CancelProjectInvitationStoreInput{
				ActorUserID:  tt.actorID,
				ProjectID:    projectID,
				InvitationID: invitationID,
				CanceledAt:   now.Add(time.Hour),
			})

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CancelProjectInvitation() error = %v, want %v", err, tt.wantErr)
			}
			if status := projectInvitationStatusForTest(t, db, invitationID); status != projects.ProjectInvitationStatusPending {
				t.Fatalf("invitation status after denied cancel = %s, want pending", status)
			}
		})
	}
}

func TestProjectsStoreCancelInvitationRechecksAndUsesExactProject(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	ownerID := "00000000-0000-4000-8000-000000000580"
	projectID := "00000000-0000-4000-8000-000000000680"
	otherProjectID := "00000000-0000-4000-8000-000000000681"
	projectMembershipID := "00000000-0000-4000-8000-000000000780"
	otherMembershipID := "00000000-0000-4000-8000-000000000781"
	invitationID := "00000000-0000-4000-8000-000000000880"
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	canceledAt := createdAt.Add(time.Hour)
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	cleanupProjectsStoreRows(t, db, ownerID, otherProjectID)
	insertProjectStoreUserForTest(t, db, ownerID, "cancel-exact-owner@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Cancel Exact Project", createdAt)
	insertProjectStoreProjectForTest(t, db, otherProjectID, ownerID, "Cancel Other Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, projectMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreMembershipForTest(t, db, otherMembershipID, otherProjectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreInvitationForTest(
		t, db, invitationID, projectID, ownerID, "cancel-exact-target@example.test",
		strings.Repeat("9", 64), projects.RoleViewer, projects.ProjectInvitationStatusPending,
		createdAt.Add(14*24*time.Hour), createdAt,
	)

	_, err := store.CancelProjectInvitation(ctx, projects.CancelProjectInvitationStoreInput{
		ActorUserID: ownerID, ProjectID: otherProjectID, InvitationID: invitationID, CanceledAt: canceledAt,
	})
	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("CancelProjectInvitation(other project) error = %v, want ErrNotFound", err)
	}
	if status := projectInvitationStatusForTest(t, db, invitationID); status != projects.ProjectInvitationStatusPending {
		t.Fatalf("invitation status after cross-project cancel = %s, want pending", status)
	}

	canceled, err := store.CancelProjectInvitation(ctx, projects.CancelProjectInvitationStoreInput{
		ActorUserID: ownerID, ProjectID: projectID, InvitationID: invitationID, CanceledAt: canceledAt,
	})
	if err != nil {
		t.Fatalf("CancelProjectInvitation() error = %v", err)
	}
	if canceled.ProjectID != projectID || canceled.ID != invitationID || canceled.Status != projects.ProjectInvitationStatusCanceled {
		t.Fatalf("canceled invitation = %#v, want exact project invitation canceled", canceled)
	}
	if status := projectInvitationStatusForTest(t, db, invitationID); status != projects.ProjectInvitationStatusCanceled {
		t.Fatalf("stored invitation status = %s, want canceled", status)
	}
}

func TestProjectsStoreActiveProjectRoleSkipsRemovedMemberships(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000502"
	projectID := "00000000-0000-4000-8000-000000000602"
	membershipID := "00000000-0000-4000-8000-000000000702"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-removed@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, userID, "Removed Membership Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, true)

	_, err := store.ActiveProjectRole(ctx, userID, projectID)

	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("ActiveProjectRole() error = %v, want ErrNotFound", err)
	}
}

func TestProjectsStoreActiveProjectRoleSkipsDeletedProjects(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000503"
	projectID := "00000000-0000-4000-8000-000000000603"
	membershipID := "00000000-0000-4000-8000-000000000703"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	cleanupProjectsStoreRows(t, db, userID, projectID)
	insertProjectStoreUserForTest(t, db, userID, "project-deleted@example.com")
	insertProjectStoreProjectForTest(t, db, projectID, userID, "Deleted Project", now)
	insertProjectStoreMembershipForTest(t, db, membershipID, projectID, userID, projects.RoleOwner, now, false)
	fenceProjectDeletionForTest(t, db, userID, projectID, now)

	_, err := store.ActiveProjectRole(ctx, userID, projectID)

	if !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("ActiveProjectRole() error = %v, want ErrNotFound", err)
	}
}

func cleanupProjectsStoreRows(t *testing.T, db *storedb.DB, userID, projectID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM deletion_tombstones WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_invitations WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_memberships WHERE project_id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM deletion_tombstones WHERE project_id = $1", projectID)
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_invitations WHERE project_id = $1", projectID)
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_memberships WHERE project_id = $1", projectID)
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", projectID)
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
}

func insertProjectStoreUserForTest(t *testing.T, db *storedb.DB, userID, email string) {
	t.Helper()

	insertProjectStoreUserWithStateForTest(t, db, userID, email, true, false, time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC))
}

func insertProjectStoreUserWithSessionForTest(t *testing.T, db *storedb.DB, userID, email string) auth.SessionWriteProof {
	t.Helper()

	insertProjectStoreUserForTest(t, db, userID, email)
	proof := auth.SessionWriteProof{
		SessionHash:    hashForAuthStoreTest(t, 101),
		CSRFSecretHash: hashForAuthStoreTest(t, 102),
	}
	if _, err := db.Pool.Exec(context.Background(), `
		WITH db_clock AS (SELECT clock_timestamp() AS now_at)
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, now_at + interval '1 hour', now_at
		FROM db_clock
	`, userID, proof.SessionHash, proof.CSRFSecretHash); err != nil {
		t.Fatalf("insert active user session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM sessions WHERE session_hash = $1", proof.SessionHash)
	})
	return proof
}

func insertProjectStoreUserWithStateForTest(t *testing.T, db *storedb.DB, userID, email string, verified, disabled bool, now time.Time) {
	t.Helper()

	var emailVerifiedAt *time.Time
	if verified {
		emailVerifiedAt = &now
	}
	var disabledAt *time.Time
	if disabled {
		disabledAt = &now
	}

	_, err := db.Pool.Exec(
		context.Background(),
		`INSERT INTO users (id, email, display_name, password_hash, email_verified_at, disabled_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID,
		email,
		"Project Owner",
		"test-password-hash",
		emailVerifiedAt,
		disabledAt,
	)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertProjectStoreProjectForTest(t *testing.T, db *storedb.DB, projectID, ownerUserID, title string, now time.Time) {
	t.Helper()

	_, err := db.Pool.Exec(
		context.Background(),
		`INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		projectID,
		ownerUserID,
		title,
		"",
		now,
	)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	var catalogInstalled bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT to_regclass('project_source_views') IS NOT NULL`).Scan(&catalogInstalled); err != nil {
		t.Fatalf("detect synthetic source catalog: %v", err)
	}
	if catalogInstalled {
		if _, err := db.Pool.Exec(context.Background(), `
			INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision)
			SELECT id, 'draft', lifecycle_generation, 1 FROM projects WHERE id = $1
		`, projectID); err != nil {
			t.Fatalf("insert project draft source marker: %v", err)
		}
	}
}

func insertProjectStoreMembershipForTest(t *testing.T, db *storedb.DB, membershipID, projectID, userID string, role projects.Role, now time.Time, removed bool) {
	t.Helper()

	var removedAt *time.Time
	if removed {
		removedAt = &now
	}
	_, err := db.Pool.Exec(
		context.Background(),
		`INSERT INTO project_memberships (id, project_id, user_id, role, removed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		membershipID,
		projectID,
		userID,
		string(role),
		removedAt,
		now,
	)
	if err != nil {
		t.Fatalf("insert project membership: %v", err)
	}
}

func insertProjectStoreInvitationForTest(
	t *testing.T,
	db *storedb.DB,
	invitationID string,
	projectID string,
	inviterUserID string,
	email string,
	emailHash string,
	role projects.Role,
	status projects.ProjectInvitationStatus,
	expiresAt time.Time,
	createdAt time.Time,
) {
	t.Helper()

	_, err := db.Pool.Exec(
		context.Background(),
		`INSERT INTO project_invitations (id, project_id, inviter_user_id, email, email_hash, role, status, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		invitationID,
		projectID,
		inviterUserID,
		email,
		emailHash,
		string(role),
		string(status),
		expiresAt,
		createdAt,
	)
	if err != nil {
		t.Fatalf("insert project invitation: %v", err)
	}
}

func projectInvitationCountForProjectForTest(t *testing.T, db *storedb.DB, projectID string) int {
	t.Helper()

	var count int
	if err := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM project_invitations WHERE project_id = $1", projectID).Scan(&count); err != nil {
		t.Fatalf("count project invitations: %v", err)
	}
	return count
}

func projectInvitationStatusForTest(t *testing.T, db *storedb.DB, invitationID string) projects.ProjectInvitationStatus {
	t.Helper()

	var status string
	if err := db.Pool.QueryRow(context.Background(), "SELECT status FROM project_invitations WHERE id = $1", invitationID).Scan(&status); err != nil {
		t.Fatalf("read project invitation status: %v", err)
	}
	return projects.ProjectInvitationStatus(status)
}

func projectInvitationStateForTest(t *testing.T, db *storedb.DB, invitationID string) (projects.ProjectInvitationStatus, pgtype.Timestamptz) {
	t.Helper()

	var status string
	var acceptedAt pgtype.Timestamptz
	if err := db.Pool.QueryRow(context.Background(), "SELECT status, accepted_at FROM project_invitations WHERE id = $1", invitationID).Scan(&status, &acceptedAt); err != nil {
		t.Fatalf("read project invitation state: %v", err)
	}
	return projects.ProjectInvitationStatus(status), acceptedAt
}

func membershipCountForProjectUserForTest(t *testing.T, db *storedb.DB, projectID, userID string) int {
	t.Helper()

	var count int
	if err := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM project_memberships WHERE project_id = $1 AND user_id = $2", projectID, userID).Scan(&count); err != nil {
		t.Fatalf("count project memberships: %v", err)
	}
	return count
}

func emailHashForProjectStoreTest(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func projectInvitationIDsForTest(invitations []projects.ProjectInvitationRecord) []string {
	ids := make([]string, len(invitations))
	for i, invitation := range invitations {
		ids[i] = invitation.ID
	}
	return ids
}

func projectMemberByUserIDForTest(t *testing.T, members []projects.ProjectMembershipRecord, userID string) projects.ProjectMembershipRecord {
	t.Helper()
	for _, member := range members {
		if member.UserID == userID {
			return member
		}
	}
	t.Fatalf("missing project member for user %q in %#v", userID, members)
	return projects.ProjectMembershipRecord{}
}

func projectMemberContainsUserForTest(members []projects.ProjectMembershipRecord, userID string) bool {
	for _, member := range members {
		if member.UserID == userID {
			return true
		}
	}
	return false
}

// fenceProjectDeletionForTest удаляет проект через действующий governed lifecycle.
func fenceProjectDeletionForTest(t *testing.T, db *storedb.DB, actorID, projectID string, deletedAt time.Time) {
	t.Helper()
	deletedAt = deletedAt.UTC().Truncate(time.Microsecond)
	_, err := New(db).FenceProjectDeletion(context.Background(), governedProjectDeleteRequestForTest(
		actorID,
		projectID,
		projects.NewSecureIDGenerator().NewDeletionTombstoneID(),
		deletedAt,
	))
	if err != nil {
		t.Fatalf("fence project deletion: %v", err)
	}
}

func newPermissionLossFixtureForTest(
	t *testing.T,
	memberRole projects.Role,
) (*storedb.DB, string, string, string, string, time.Time) {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	ownerID := ids.NewProjectID()
	memberUserID := ids.NewProjectID()
	projectID := ids.NewProjectID()
	ownerMembershipID := ids.NewProjectMembershipID()
	memberID := ids.NewProjectMembershipID()
	createdAt := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", memberUserID)
	})
	cleanupProjectsStoreRows(t, db, ownerID, projectID)
	insertProjectStoreUserForTest(t, db, ownerID, ownerID+"@permission-owner.example.test")
	insertProjectStoreUserForTest(t, db, memberUserID, memberUserID+"@permission-member.example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Permission Loss Project", createdAt)
	insertProjectStoreMembershipForTest(t, db, ownerMembershipID, projectID, ownerID, projects.RoleOwner, createdAt, false)
	insertProjectStoreMembershipForTest(t, db, memberID, projectID, memberUserID, memberRole, createdAt, false)
	return db, ownerID, memberUserID, projectID, memberID, createdAt
}

func assertPermissionLossIntentForTest(
	t *testing.T,
	db *storedb.DB,
	projectID string,
	actorUserID string,
	fencedAt time.Time,
) string {
	t.Helper()
	var tombstoneID, persistedActor string
	var targetCount int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT tombstone.id::text, tombstone.permission_loss_actor_user_id::text, count(task.target)::int
		FROM deletion_tombstones AS tombstone
		JOIN deletion_target_tasks AS task
		  ON task.tombstone_id = tombstone.id AND task.project_id = tombstone.project_id
		WHERE tombstone.project_id = $1
		  AND tombstone.permission_loss_actor_user_id = $2
		  AND tombstone.fenced_at = $3
		GROUP BY tombstone.id, tombstone.permission_loss_actor_user_id
	`, projectID, actorUserID, fencedAt).Scan(&tombstoneID, &persistedActor, &targetCount); err != nil {
		t.Fatalf("durable permission-loss intent: %v", err)
	}
	if persistedActor != actorUserID || targetCount != 3 {
		t.Fatalf("permission-loss intent = %q/%d, want %q/3", persistedActor, targetCount, actorUserID)
	}
	return tombstoneID
}

func completePermissionLossNoDataTargetsForTest(t *testing.T, db *storedb.DB, actorUserID string) {
	t.Helper()
	ctx := context.Background()
	store := deletionrepo.New(db)
	type completedTarget struct {
		claim   deletion.TargetClaim
		request deletion.TargetRequest
		result  deletion.TargetResult
	}
	completed := make([]completedTarget, 0, 3)
	for index, target := range []governance.DeletionTarget{
		governance.TargetFilesystem, governance.TargetRuntime, governance.TargetPostgreSQL,
	} {
		var owner governance.Digest
		owner[0] = byte(index + 1)
		claimed := claimPermissionLossDeletionTargetForTest(ctx, t, db, owner, target)
		request, err := store.LoadDeletionTargetRequestForClaim(ctx, claimed.Claim)
		if err != nil || request.Tombstone.Identity.PermissionLossActorUserID != actorUserID {
			t.Fatalf("load %q actor = %q, %v", target, request.Tombstone.Identity.PermissionLossActorUserID, err)
		}
		handler := governance.HandlerRuntimeDirectSDKV1
		if target == governance.TargetFilesystem {
			handler = governance.HandlerFilesystemOSRootV1
		} else if target == governance.TargetPostgreSQL {
			handler = governance.HandlerPostgresExactInventoryV1
		}
		result := deletion.TargetResult{
			Disposition: deletion.DispositionNoData, HandlerVersion: handler, Reason: governance.ReasonNoData,
		}
		if _, err := store.CompleteDeletionTargetForClaim(ctx, claimed.Claim, result); err != nil {
			t.Fatalf("complete %q no_data: %v", target, err)
		}
		completed = append(completed, completedTarget{claim: claimed.Claim, request: request, result: result})
	}
	for _, target := range completed {
		receipt, err := store.FindCommittedDeletionTargetReceipt(ctx, target.claim)
		validateErr := receipt.ValidateFor(target.request, target.result)
		if err != nil || validateErr != nil {
			t.Fatalf("replay %q receipt: %#v, find=%v validate=%v", target.claim.Target, receipt, err, validateErr)
		}
	}
}

func claimPermissionLossDeletionTargetForTest(
	ctx context.Context,
	t *testing.T,
	db *storedb.DB,
	owner governance.Digest,
	target governance.DeletionTarget,
) deletion.ClaimedTarget {
	t.Helper()
	schema := testutil.IsolatedPostgresSchema(t, db)
	query := strings.ReplaceAll(`
		SELECT project_id, tombstone_id, target, state, attempt,
		       claim_incarnation, lease_owner_digest, lease_expires_at
		FROM canonkeeper_worker.claim_next_deletion_target_task_for_worker($1, $2, $3)
	`, "canonkeeper_worker.", pgx.Identifier{schema + "_worker"}.Sanitize()+".")
	var (
		projectID, tombstoneID pgtype.UUID
		claimedTarget          string
		state                  string
		attempt, incarnation   int64
		persistedOwner         []byte
		leaseExpiresAt         pgtype.Timestamptz
	)
	err := db.Pool.QueryRow(ctx, query,
		owner[:], int32(deletion.DeletionTargetLeaseTTL.Seconds()), true,
	).Scan(&projectID, &tombstoneID, &claimedTarget, &state, &attempt, &incarnation, &persistedOwner, &leaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim %q: no target work available", target)
	}
	if err != nil {
		t.Fatalf("claim %q: %v", target, err)
	}
	var storedOwner governance.Digest
	copy(storedOwner[:], persistedOwner)
	claimed := deletion.ClaimedTarget{
		Claim: deletion.TargetClaim{
			ProjectID: projectID.String(), TombstoneID: tombstoneID.String(),
			Target: governance.DeletionTarget(claimedTarget), ClaimIncarnation: uint64(incarnation),
			LeaseOwnerDigest: storedOwner, ObservedLeaseExpiry: leaseExpiresAt.Time.UTC(),
		},
		Attempt: uint32(attempt), State: deletion.TargetState(state),
	}
	if claimed.Claim.Target != target || claimed.Claim.LeaseOwnerDigest != owner ||
		claimed.Validate(time.Now().UTC()) != nil {
		t.Fatalf("claim %q = %#v", target, claimed)
	}
	return claimed
}

func cleanupAuthStoreRows(t *testing.T, db *storedb.DB, userID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM sessions WHERE user_id = $1", userID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM sessions WHERE user_id = $1", userID)
	_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", userID)
}

func hashForAuthStoreTest(t *testing.T, index int) string {
	t.Helper()
	seed := t.Name() + ":" + strconv.Itoa(index) + ":" + auth.NewSecureIDGenerator().NewCSRFToken()
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

type authStoreIDsForTest struct {
	next int
}

func (ids *authStoreIDsForTest) NewUserID() string {
	ids.next++
	return "00000000-0000-4000-8000-" + leftPadIntForTest(ids.next)
}

func (ids *authStoreIDsForTest) NewSessionID() string {
	ids.next++
	return "00000000-0000-4000-8000-" + leftPadIntForTest(ids.next+100)
}

func (ids *authStoreIDsForTest) NewSessionCookieValue() string {
	ids.next++
	return auth.NewSecureIDGenerator().NewSessionCookieValue()
}

func (ids *authStoreIDsForTest) NewCSRFToken() string {
	ids.next++
	return auth.NewSecureIDGenerator().NewCSRFToken()
}

func leftPadIntForTest(value int) string {
	raw := strconv.Itoa(value)
	return strings.Repeat("0", 12-len(raw)) + raw
}

type authStoreClockForTest struct {
	now time.Time
}

func (clock authStoreClockForTest) Now() time.Time {
	return clock.now
}

func governedProjectDeleteRequestForTest(actorID, projectID, tombstoneID string, fencedAt time.Time) deletion.SourceDeleteRequest {
	return deletion.SourceDeleteRequest{
		TombstoneID: tombstoneID,
		ActorUserID: actorID,
		ProjectID:   projectID,
		EntityKind:  deletion.EntityProject,
		EntityID:    projectID,
		FencedAt:    fencedAt,
		GovernedWindow: &deletion.GovernedSourceDeleteWindow{
			PurgeAfter:           fencedAt.Add(deletion.SourceRestoreWindow),
			DerivedPurgeDeadline: fencedAt.Add(deletion.DerivedPurgeDeadline),
			SourcePurgeDeadline:  fencedAt.Add(deletion.SourceRestoreWindow + deletion.SourceHardPurgeGrace),
		},
	}
}

func assertProjectDeletionRolledBackForTest(t *testing.T, db *storedb.DB, projectID, tombstoneID string) {
	t.Helper()
	ctx := context.Background()
	var deletedAt pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, lifecycle_generation FROM projects WHERE id = $1
	`, projectID).Scan(&deletedAt, &generation); err != nil {
		t.Fatal(err)
	}
	if deletedAt.Valid || generation != 1 {
		t.Fatalf("project mutation survived rollback: deleted_at=%v generation=%d", deletedAt, generation)
	}
	var tombstoneCount, targetCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE id = $1`, tombstoneID).Scan(&tombstoneCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM deletion_target_tasks WHERE tombstone_id = $1`, tombstoneID).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 0 || targetCount != 0 {
		t.Fatalf("deletion outbox survived rollback: tombstones=%d targets=%d", tombstoneCount, targetCount)
	}
}

func restoreProjectInputForTest(actorID, projectID string) projects.RestoreProjectStoreInput {
	return projects.RestoreProjectStoreInput{
		ActorUserID: actorID,
		ProjectID:   projectID,
	}
}

func assertProjectRestoreRolledBackForTest(t *testing.T, db *storedb.DB, input projects.RestoreProjectStoreInput) {
	t.Helper()
	ctx := context.Background()
	var deletedAt pgtype.Timestamptz
	var generation int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT deleted_at, lifecycle_generation FROM projects WHERE id = $1
	`, input.ProjectID).Scan(&deletedAt, &generation); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid || generation != 2 {
		t.Fatalf("restore mutation survived rollback: deleted_at=%v generation=%d", deletedAt, generation)
	}
}
