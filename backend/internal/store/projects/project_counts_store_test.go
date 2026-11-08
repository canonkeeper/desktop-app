package projectstore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/projects"
)

func TestProjectsStoreReadsCountsFromCurrentLibraryContent(t *testing.T) {
	fixture := newPhase04LibraryRollbackFixture(t)
	store := New(fixture.db)

	project, err := store.GetProject(context.Background(), fixture.actorID, fixture.projectID)
	if err != nil {
		t.Fatalf("GetProject() error = %v", err)
	}
	assertProjectCounts(t, project)

	listed, err := store.ListProjectsForActor(context.Background(), projects.ListProjectsStoreInput{
		ActorUserID: fixture.actorID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListProjectsForActor() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListProjectsForActor() returned %d projects, want 1", len(listed))
	}
	assertProjectCounts(t, listed[0])

	updated, err := store.UpdateProject(context.Background(), projects.UpdateProjectStoreInput{
		ActorUserID: fixture.actorID,
		ProjectID:   fixture.projectID,
		Title:       "Updated project",
		UpdatedAt:   fixture.now.Add(1),
	})
	if err != nil {
		t.Fatalf("UpdateProject() error = %v", err)
	}
	assertProjectCounts(t, updated)
}

func assertProjectCounts(t *testing.T, project projects.ProjectRecord) {
	t.Helper()
	if project.BookCount != 1 || project.ChapterCount != 1 || project.WordCount != 1 {
		t.Fatalf("project counts = %d/%d/%d, want 1/1/1", project.BookCount, project.ChapterCount, project.WordCount)
	}
}
