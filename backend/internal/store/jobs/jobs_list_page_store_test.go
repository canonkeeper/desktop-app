package jobstore

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type jobListFixture struct {
	ctx       context.Context
	db        *storedb.DB
	store     *Store
	actorID   string
	projectID string
	now       time.Time
}

func newJobListFixture(t *testing.T) jobListFixture {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	fixture := jobListFixture{
		ctx: context.Background(), db: db, store: New(db),
		actorID:   "00000000-0000-4000-8000-00000000e101",
		projectID: "00000000-0000-4000-8000-00000000e102",
		now:       time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC),
	}
	insertProjectStoreUserForTest(t, db, fixture.actorID, "jobs-list-page@example.com")
	insertProjectStoreUserForTest(t, db, "00000000-0000-4000-8000-00000000e103", "jobs-list-outsider@example.com")
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Jobs List Page", fixture.now)
	insertProjectStoreMembershipForTest(t, db, "00000000-0000-4000-8000-00000000e104", fixture.projectID, fixture.actorID, projects.RoleOwner, fixture.now, false)
	return fixture
}

// create makes a readable Job of the project minute minutes after the fixture time.
func (fixture jobListFixture) create(t *testing.T, minute int, kind jobs.Kind, status jobs.Status, subject jobs.Subject) string {
	t.Helper()
	id := fmt.Sprintf("00000000-0000-4000-8000-0000000e2%03d", minute)
	at := fixture.now.Add(time.Duration(minute) * time.Minute)
	queue, creator := jobs.QueueClassUserDemanded, fixture.actorID
	if kind == jobs.KindChapterCards {
		// Cards follow a text index on their own: an automatic Job without a creator.
		queue, creator = jobs.QueueClassAutomatic, ""
	}
	if _, err := fixture.store.createJobForTest(fixture.ctx, jobs.CreateJobStoreInput{
		ID: id, ProjectID: fixture.projectID, Kind: kind, Status: status, Subject: subject,
		CreatorUserID: creator, QueueClass: queue, MaxAttempts: 3,
		NextAttemptAt: at, CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("create %s %s job: %v", status, kind, err)
	}
	return id
}

func (fixture jobListFixture) textSubject(view governance.SourceView, revision uint64) jobs.Subject {
	return jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: fixture.projectID, SourceView: view, CatalogRevision: revision}
}

func (fixture jobListFixture) cardsSubject(view governance.SourceView) jobs.Subject {
	return jobs.ChapterCardsSubject{Type: jobs.KindChapterCards, ProjectID: fixture.projectID, SourceView: view}
}

func readableJobIDs(page jobs.ProjectJobCandidates) []string {
	ids := make([]string, 0, len(page.Readable))
	for _, job := range page.Readable {
		ids = append(ids, job.ID)
	}
	return ids
}

// UC1, UC2: a page is chosen by tenant, kind, the active filter and the cursor before the read
// check; the page reports its candidates and the last one for the next page.
func TestListProjectJobsPagesByFilterBeforeTheReadCheck(t *testing.T) {
	fixture := newJobListFixture(t)
	textOld := fixture.create(t, 1, jobs.KindCanonTextIndex, jobs.StatusSucceeded, fixture.textSubject(governance.SourceViewDraft, 1))
	cards := fixture.create(t, 2, jobs.KindChapterCards, jobs.StatusRunning, fixture.cardsSubject(governance.SourceViewDraft))
	importJob := fixture.create(t, 3, jobs.KindImport, jobs.StatusQueued, workerClaimSubjectForTest(jobs.KindImport, fixture.projectID))
	textNew := fixture.create(t, 4, jobs.KindCanonTextIndex, jobs.StatusFailed, fixture.textSubject(governance.SourceViewDraft, 1))

	list := func(input jobs.ListProjectJobsStoreInput) jobs.ProjectJobCandidates {
		t.Helper()
		if input.ActorUserID == "" {
			input.ActorUserID = fixture.actorID
		}
		input.ProjectID = fixture.projectID
		page, err := fixture.store.ListProjectJobs(fixture.ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if page := list(jobs.ListProjectJobsStoreInput{ActiveOnly: true, Limit: 10}); page.Count != 2 ||
		!slices.Equal(readableJobIDs(page), []string{importJob, cards}) {
		t.Fatalf("active page = %#v, want the queued import and the running cards", page)
	}
	first := list(jobs.ListProjectJobsStoreInput{Limit: 2})
	if first.Count != 2 || !slices.Equal(readableJobIDs(first), []string{textNew, importJob}) ||
		first.LastID != importJob || !first.LastCreatedAt.Equal(fixture.now.Add(3*time.Minute)) {
		t.Fatalf("first page = %#v, want the two newest Jobs ending at the import", first)
	}
	rest := list(jobs.ListProjectJobsStoreInput{Limit: 2, HasCursor: true, CursorAt: first.LastCreatedAt, CursorID: first.LastID})
	if !slices.Equal(readableJobIDs(rest), []string{cards, textOld}) {
		t.Fatalf("second page = %#v", rest)
	}
	if page := list(jobs.ListProjectJobsStoreInput{Kind: jobs.KindCanonTextIndex, Limit: 10}); !slices.Equal(readableJobIDs(page), []string{textNew, textOld}) {
		t.Fatalf("text index page = %#v", page)
	}
	if page := list(jobs.ListProjectJobsStoreInput{ActorUserID: "00000000-0000-4000-8000-00000000e103", Limit: 10}); page.Count != 0 {
		t.Fatalf("outsider page = %#v, want no candidates", page)
	}
}

// UC3: the indexing status reads, per current text version, the latest text index Job of its
// catalog revision, the latest cards Job in work and the latest cards Job, and nothing else.
func TestListIndexingStatusJobsReadsTheLatestJobOfEachGroup(t *testing.T) {
	fixture := newJobListFixture(t)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		UPDATE project_source_views SET catalog_revision = 2 WHERE project_id = $1 AND source_view = 'draft'
	`, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `
		INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision)
		SELECT id, 'published', lifecycle_generation, 1 FROM projects WHERE id = $1
	`, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	fixture.create(t, 1, jobs.KindCanonTextIndex, jobs.StatusSucceeded, fixture.textSubject(governance.SourceViewDraft, 2))
	textDraft := fixture.create(t, 2, jobs.KindCanonTextIndex, jobs.StatusFailed, fixture.textSubject(governance.SourceViewDraft, 2))
	fixture.create(t, 3, jobs.KindCanonTextIndex, jobs.StatusSucceeded, fixture.textSubject(governance.SourceViewDraft, 1))
	textPublished := fixture.create(t, 4, jobs.KindCanonTextIndex, jobs.StatusSucceeded, fixture.textSubject(governance.SourceViewPublished, 1))
	fixture.create(t, 5, jobs.KindChapterCards, jobs.StatusSucceeded, fixture.cardsSubject(governance.SourceViewDraft))
	cardsActive := fixture.create(t, 6, jobs.KindChapterCards, jobs.StatusRunning, fixture.cardsSubject(governance.SourceViewDraft))
	cardsLatest := fixture.create(t, 7, jobs.KindChapterCards, jobs.StatusSucceeded, fixture.cardsSubject(governance.SourceViewDraft))
	fixture.create(t, 8, jobs.KindImport, jobs.StatusQueued, workerClaimSubjectForTest(jobs.KindImport, fixture.projectID))

	read := func(actorID, bookID string) []string {
		t.Helper()
		rows, err := sqlc.New(fixture.db.Pool).ListIndexingStatusJobs(fixture.ctx, sqlc.ListIndexingStatusJobsParams{
			ProjectID: mustParseUUIDForTest(t, fixture.projectID), ActorUserID: mustParseUUIDForTest(t, actorID), BookID: bookID,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, storedb.UUIDString(row.Job.ID))
		}
		return ids
	}
	if got, want := read(fixture.actorID, ""), []string{cardsLatest, cardsActive, textPublished, textDraft}; !slices.Equal(got, want) {
		t.Fatalf("status jobs = %v, want %v", got, want)
	}
	// An import without a result names no book.
	if got := read(fixture.actorID, "00000000-0000-4000-8000-00000000e1ff"); len(got) != 4 {
		t.Fatalf("status jobs with a book = %v, want no import", got)
	}
	if got := read("00000000-0000-4000-8000-00000000e103", ""); len(got) != 0 {
		t.Fatalf("outsider status jobs = %v, want none", got)
	}
}

func mustParseUUIDForTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := storedb.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
