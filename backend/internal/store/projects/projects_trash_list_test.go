package projectstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/projects"
	trashrepo "canonkeeper/backend/internal/repository/postgres/trash"
)

// specs/2026-11-11-ux7-trash.md TB1.1, TB1.2: a deleted saga is listed in «Недавно удалённые» for its owner with its term,
// also when it was his only one, and not for an editor of it; a restored saga is not listed.
func TestRecentlyDeletedListsTheSagasOfTheAuthor(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	ownerID, editorID := "00000000-0000-4000-8000-000000071401", "00000000-0000-4000-8000-000000071402"
	projectID := "00000000-0000-4000-8000-000000071403"
	fencedAt := time.Now().UTC().Truncate(time.Microsecond)
	insertProjectStoreUserWithSessionForTest(t, db, ownerID, "trash-owner@example.test")
	insertProjectStoreUserWithSessionForTest(t, db, editorID, "trash-editor@example.test")
	insertProjectStoreProjectForTest(t, db, projectID, ownerID, "Северный берег", fencedAt.Add(-time.Hour))
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000071404", projectID, ownerID, projects.RoleOwner, fencedAt.Add(-time.Hour), false)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-000000071405", projectID, editorID, projects.RoleEditor, fencedAt.Add(-time.Hour), false)
	store := New(db)
	if _, err := store.FenceProjectDeletion(ctx, governedProjectDeleteRequestForTest(ownerID, projectID, "00000000-0000-4000-8000-000000071406", fencedAt)); err != nil {
		t.Fatalf("delete the saga: %v", err)
	}
	trash := trashrepo.New(db)
	sources, err := trash.ListDeletedSources(ctx, ownerID, 100)
	if err != nil || sources.Sagas.Total != 1 || len(sources.Sagas.Items) != 1 {
		t.Fatalf("the owner's list = %+v, %v", sources, err)
	}
	saga := sources.Sagas.Items[0]
	if saga.ID != projectID || saga.Title != "Северный берег" || !saga.DeletedAt.Equal(fencedAt) || !saga.RestoreUntil.Equal(fencedAt.Add(7*24*time.Hour)) {
		t.Fatalf("the deleted saga = %+v", saga)
	}
	if editor, err := trash.ListDeletedSources(ctx, editorID, 100); err != nil || editor.Sagas.Total != 0 {
		t.Fatalf("the editor's list = %+v, %v", editor, err)
	}
	if _, err := store.RestoreProject(ctx, projects.RestoreProjectStoreInput{ActorUserID: ownerID, ProjectID: projectID}); err != nil {
		t.Fatal(err)
	}
	if after, err := trash.ListDeletedSources(ctx, ownerID, 100); err != nil || after.Sagas.Total != 0 {
		t.Fatalf("the list after «Вернуть» = %+v, %v", after, err)
	}
}
