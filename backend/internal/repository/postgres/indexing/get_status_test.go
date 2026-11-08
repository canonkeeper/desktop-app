package indexing

import (
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/canon"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
)

var statusTestTime = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)

func statusTextJob(id string, status jobs.Status, view governance.SourceView, revision uint64, created time.Time) jobs.Job {
	return jobs.Job{
		ID: id, Kind: jobs.KindCanonTextIndex, Status: status, CreatedAt: created,
		Subject: jobs.CanonTextIndexSubject{
			Type: jobs.KindCanonTextIndex, ProjectID: "project-1", SourceView: view, CatalogRevision: revision,
		},
	}
}

func statusCardsJob(id string, status jobs.Status, view governance.SourceView, created time.Time) jobs.Job {
	return jobs.Job{
		ID: id, Kind: jobs.KindChapterCards, Status: status, CreatedAt: created,
		Subject: jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: "project-1", SourceView: view},
	}
}

// R5.3: a book is ready when its text is indexed and every chapter has a card; while the cards
// Job runs the version is processing at the first book still missing cards.
func TestBuildStatusResponseTracksTextIndexAndCardsPerBook(t *testing.T) {
	snapshot := statusSnapshot{
		role: projects.RoleViewer,
		views: map[governance.SourceView]statusViewSnapshot{
			governance.SourceViewPublished: {
				revision: 7,
				books: []statusBookSnapshot{
					{id: "book-1", chapters: 10, indexed: 10, carded: 10},
					{id: "book-2", chapters: 8, indexed: 8, carded: 3},
				},
			},
			governance.SourceViewDraft: {revision: 9},
		},
		jobs: []jobs.Job{
			statusTextJob("text-job", jobs.StatusSucceeded, governance.SourceViewPublished, 7, statusTestTime),
			statusCardsJob("cards-job", jobs.StatusRunning, governance.SourceViewPublished, statusTestTime.Add(time.Minute)),
		},
	}

	response, err := buildStatusResponse(snapshot, statusScope{bookID: "book-2", sourceView: governance.SourceViewPublished})
	if err != nil {
		t.Fatal(err)
	}
	if response.Draft != nil {
		t.Fatal("viewer response exposed draft status")
	}
	context := response.Published.Context
	if context == nil || context.State != canon.IndexingStateProcessing || context.CurrentBook == nil ||
		context.CurrentBook.BookID != "book-2" || context.CurrentBook.Position != 2 || context.CurrentBook.Total != 2 {
		t.Fatalf("published = %#v", response.Published)
	}
	assertStatusStep(t, response.Steps, canon.IndexingStepTextIndex, canon.IndexingStepSucceeded)
	cards := assertStatusStep(t, response.Steps, canon.IndexingStepCards, canon.IndexingStepRunning)
	if cards.Counters == nil || *cards.Counters.IndexedChapters != 3 || *cards.Counters.TotalChapters != 8 ||
		cards.Progress == nil || *cards.Progress != 3.0/8 || cards.JobID == nil || *cards.JobID != "cards-job" {
		t.Fatalf("cards step = %#v", cards)
	}
	assertStatusStep(t, response.Steps, canon.IndexingStepReady, canon.IndexingStepPending)

	ready, err := buildStatusResponse(snapshot, statusScope{bookID: "book-1", sourceView: governance.SourceViewPublished})
	if err != nil {
		t.Fatal(err)
	}
	assertStatusStep(t, ready.Steps, canon.IndexingStepCards, canon.IndexingStepSucceeded)
	assertStatusStep(t, ready.Steps, canon.IndexingStepReady, canon.IndexingStepSucceeded)
}

// A version without an active Job and with a book still missing text or cards is partial, and
// only a project editor sees the book to restart.
func TestBuildStatusResponseIsPartialWithoutActiveJob(t *testing.T) {
	views := map[governance.SourceView]statusViewSnapshot{
		governance.SourceViewPublished: {revision: 3, books: []statusBookSnapshot{{id: "book-1", chapters: 4, indexed: 4, carded: 4}}},
		governance.SourceViewDraft: {revision: 4, books: []statusBookSnapshot{
			{id: "book-1", chapters: 4, indexed: 4, carded: 4},
			{id: "book-2", chapters: 5, indexed: 2, carded: 2},
		}},
	}
	jobList := []jobs.Job{
		statusTextJob("old-text", jobs.StatusSucceeded, governance.SourceViewDraft, 3, statusTestTime),
		statusTextJob("failed-text", jobs.StatusFailed, governance.SourceViewDraft, 4, statusTestTime.Add(time.Minute)),
		statusCardsJob("cards", jobs.StatusSucceeded, governance.SourceViewDraft, statusTestTime.Add(2*time.Minute)),
	}
	response, err := buildStatusResponse(statusSnapshot{role: projects.RoleEditor, views: views, jobs: jobList},
		statusScope{bookID: "book-2", sourceView: governance.SourceViewDraft})
	if err != nil {
		t.Fatal(err)
	}
	if response.Published.Context == nil || response.Published.Context.State != canon.IndexingStateReady {
		t.Fatalf("published = %#v", response.Published)
	}
	draft := response.Draft
	if draft == nil || draft.Context == nil || draft.Context.State != canon.IndexingStatePartial ||
		draft.Context.RetryableFailedBook == nil || draft.Context.RetryableFailedBook.BookID != "book-2" {
		t.Fatalf("draft = %#v", draft)
	}
	text := assertStatusStep(t, response.Steps, canon.IndexingStepTextIndex, canon.IndexingStepFailed)
	if !text.CanRetry || text.Counters == nil || *text.Counters.IndexedChapters != 2 || *text.Counters.TotalChapters != 5 {
		t.Fatalf("text step = %#v", text)
	}
	// The finished cards Job covered the older text of this book.
	assertStatusStep(t, response.Steps, canon.IndexingStepCards, canon.IndexingStepPending)

	viewer, err := buildStatusResponse(statusSnapshot{role: projects.RoleViewer, views: views, jobs: jobList}, statusScope{})
	if err != nil {
		t.Fatal(err)
	}
	if viewer.Draft != nil || len(viewer.Steps) != 0 {
		t.Fatalf("viewer response = %#v", viewer)
	}
}

func TestBuildStatusResponseRejectsUnknownBook(t *testing.T) {
	snapshot := statusSnapshot{role: projects.RoleOwner, views: map[governance.SourceView]statusViewSnapshot{
		governance.SourceViewDraft: {revision: 1, books: []statusBookSnapshot{{id: "book-1"}}},
	}}
	if _, err := buildStatusResponse(snapshot, statusScope{bookID: "book-2", sourceView: governance.SourceViewDraft}); err == nil {
		t.Fatal("status of an unknown book was built")
	}
}

func assertStatusStep(t *testing.T, steps []canon.IndexingStep, kind canon.IndexingStepKind, want canon.IndexingStepStatus) canon.IndexingStep {
	t.Helper()
	for _, step := range steps {
		if step.Kind == kind {
			if step.Status != want {
				t.Fatalf("%s status = %s, want %s", kind, step.Status, want)
			}
			return step
		}
	}
	t.Fatalf("missing %s step", kind)
	return canon.IndexingStep{}
}
