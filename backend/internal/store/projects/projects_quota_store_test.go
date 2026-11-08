package projectstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/projects"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// R4.1, R4.2: a writer over the project limit cannot create or restore a project; a deleted
// project does not count.
func TestProjectsQuotaLimitsCreateAndRestore(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	userID := "00000000-0000-4000-8000-000000000c01"
	insertProjectStoreUserForTest(t, db, userID, "project-quota@example.com")
	limit := int64(1)
	if err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := quotastore.SetOverridesInTx(ctx, tx, userID, []quota.OverrideChange{{Key: quota.KeyProjects, Value: &limit}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	create := func(index int) error {
		_, err := store.CreateProjectWithOwnerMembership(ctx, projects.CreateProjectStoreInput{
			ProjectID:         fmt.Sprintf("00000000-0000-4000-8000-000000000d0%d", index),
			OwnerMembershipID: fmt.Sprintf("00000000-0000-4000-8000-000000000e0%d", index),
			OwnerUserID:       userID, Title: fmt.Sprintf("Quota %d", index), CreatedAt: time.Now(),
		})
		return err
	}
	if err := create(1); err != nil {
		t.Fatal(err)
	}
	err := create(2)
	if exceeded, ok := quota.AsExceeded(err); !ok || exceeded.Key != quota.KeyProjects {
		t.Fatalf("second project error = %v, want quota.projects", err)
	}
	if err := store.DeleteProject(ctx, projects.DeleteProjectStoreInput{
		ActorUserID: userID, ProjectID: "00000000-0000-4000-8000-000000000d01",
		TombstoneID: projects.NewSecureIDGenerator().NewDeletionTombstoneID(), DeletedAt: time.Now(),
	}); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if err := create(3); err != nil {
		t.Fatalf("a deleted project counted against the limit: %v", err)
	}
	_, err = store.RestoreProject(ctx, projects.RestoreProjectStoreInput{
		ActorUserID: userID, ProjectID: "00000000-0000-4000-8000-000000000d01",
	})
	if exceeded, ok := quota.AsExceeded(err); !ok || exceeded.Key != quota.KeyProjects {
		t.Fatalf("restore error = %v, want quota.projects", err)
	}
	var deleted bool
	if err := db.Pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM projects WHERE id = '00000000-0000-4000-8000-000000000d01'`).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("refused restore: deleted = %v, %v", deleted, err)
	}
}
