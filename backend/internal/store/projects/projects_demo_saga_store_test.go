package projectstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/projects"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// specs/2026-11-09-demo-saga.md DS3.1, DS3.2, DS1.7: the demo saga is made next to the sagas of the limit and is not
// one of them; an owner has one live demo saga, and the store names it; a deleted one frees the place for a new one.
func TestAnOwnerHasOneLiveDemoSaga(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000c11"
	insertProjectStoreUserForTest(t, db, userID, "one-demo-saga@example.com")
	limit := int64(1)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(ctx, tx, userID, []quota.OverrideChange{{Key: quota.KeyProjects, Value: &limit}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	create := func(index int, demo bool) (projects.ProjectRecord, error) {
		return store.CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
			ProjectID:         fmt.Sprintf("00000000-0000-4000-8000-000000000d1%d", index),
			OwnerMembershipID: fmt.Sprintf("00000000-0000-4000-8000-000000000e1%d", index),
			OwnerUserID:       userID, Title: fmt.Sprintf("Saga %d", index), CreatedAt: time.Now(), Demo: demo,
		})
	}
	if _, err := store.DemoProject(ctx, userID); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("the demo saga of an owner without one = %v; want projects.ErrNotFound", err)
	}
	own, err := create(1, false)
	if err != nil || own.Demo {
		t.Fatalf("his own saga = %+v, %v; want a saga without the sign", own, err)
	}
	// The owner stands at the limit of sagas: the demo saga is made all the same, and a saga of his own is not.
	demo, err := create(2, true)
	if err != nil || !demo.Demo {
		t.Fatalf("the demo saga at the limit of sagas = %+v, %v; want it made with the sign", demo, err)
	}
	if exceeded, ok := quota.AsExceeded(func() error { _, err := create(3, false); return err }()); !ok || exceeded.Key != quota.KeyProjects {
		t.Fatalf("a second saga of his own = %+v, %v; want the limit of sagas", exceeded, ok)
	}
	if _, err := create(4, true); !errors.Is(err, projects.ErrConflict) {
		t.Fatalf("a second live demo saga = %v; want projects.ErrConflict", err)
	}
	found, err := store.DemoProject(ctx, userID)
	if err != nil || found.ID != demo.ID || !found.Demo || found.CurrentMembership.Role != projects.RoleOwner {
		t.Fatalf("the demo saga of the owner = %+v, %v; want %s", found, err, demo.ID)
	}
	// Every read of a saga carries the sign.
	read, err := store.GetProject(ctx, userID, demo.ID)
	if err != nil || !read.Demo {
		t.Fatalf("the demo saga read = %+v, %v; want the sign", read, err)
	}
	listed, err := store.ListProjectsForActor(ctx, projects.ListProjectsStoreInput{ActorUserID: userID, Limit: 10})
	if err != nil || len(listed) != 2 {
		t.Fatalf("the sagas of the owner = %+v, %v; want two", listed, err)
	}
	for _, project := range listed {
		if project.Demo != (project.ID == demo.ID) {
			t.Fatalf("the sign of the listed saga %s = %v", project.ID, project.Demo)
		}
	}
	renamed := "Renamed"
	updated, err := store.UpdateProject(ctx, projects.UpdateProjectStoreInput{
		ActorUserID: userID, ProjectID: demo.ID, Title: renamed, UpdatedAt: time.Now(),
	})
	if err != nil || !updated.Demo {
		t.Fatalf("the demo saga renamed = %+v, %v; want the sign kept", updated, err)
	}

	deleteProject := func(projectID string) {
		t.Helper()
		if err := store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
			ActorUserID: userID, ProjectID: projectID,
			TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
		}); err != nil {
			t.Fatalf("DeleteProject() error = %v", err)
		}
	}
	deleteProject(demo.ID)
	if _, err := store.DemoProject(ctx, userID); !errors.Is(err, projects.ErrNotFound) {
		t.Fatalf("the demo saga after its deletion = %v; want projects.ErrNotFound", err)
	}
	deleted, err := store.ListProjectsForActor(ctx, projects.ListProjectsStoreInput{ActorUserID: userID, Limit: 10, State: projects.ProjectListDeleted})
	if err != nil || len(deleted) != 1 || !deleted[0].Demo {
		t.Fatalf("the deleted sagas = %+v, %v; want the demo saga with the sign", deleted, err)
	}
	again, err := create(5, true)
	if err != nil || !again.Demo {
		t.Fatalf("a new demo saga after the deletion = %+v, %v", again, err)
	}
}

// specs/2026-11-09-demo-saga.md DS5.2: a deleted demo saga comes back over the limit of sagas of its owner, but not
// next to a live one — the refusal is a conflict, and the saga stays deleted.
func TestRestoringADemoSagaNextToALiveOneConflicts(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000c21"
	insertProjectStoreUserForTest(t, db, userID, "restore-demo-saga@example.com")
	limit := int64(1)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(ctx, tx, userID, []quota.OverrideChange{{Key: quota.KeyProjects, Value: &limit}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	create := func(index int, demo bool) string {
		t.Helper()
		created, err := store.CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
			ProjectID:         fmt.Sprintf("00000000-0000-4000-8000-000000000d2%d", index),
			OwnerMembershipID: fmt.Sprintf("00000000-0000-4000-8000-000000000e2%d", index),
			OwnerUserID:       userID, Title: fmt.Sprintf("Saga %d", index), CreatedAt: time.Now(), Demo: demo,
		})
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	deleteProject := func(projectID string) {
		t.Helper()
		if err := store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
			ActorUserID: userID, ProjectID: projectID,
			TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
		}); err != nil {
			t.Fatalf("DeleteProject() error = %v", err)
		}
	}
	restore := func(projectID string) (projects.ProjectRecord, error) {
		return store.RestoreProject(ctx, projects.RestoreProjectStoreInput{ActorUserID: userID, ProjectID: projectID})
	}
	create(1, false)
	first := create(2, true)
	deleteProject(first)
	second := create(3, true)

	if _, err := restore(first); !errors.Is(err, projects.ErrConflict) {
		t.Fatalf("the deleted demo saga restored next to a live one = %v; want projects.ErrConflict", err)
	}
	var deleted bool
	if err := db.Pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id = $1`, first).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("the refused restore: deleted = %v, %v", deleted, err)
	}
	// Without a live one it comes back, with the owner at his limit of sagas.
	deleteProject(second)
	restored, err := restore(first)
	if err != nil || !restored.Demo || restored.ID != first {
		t.Fatalf("the deleted demo saga restored = %+v, %v; want it back with the sign", restored, err)
	}
}
