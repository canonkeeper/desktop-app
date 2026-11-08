package indexingstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type storeFixture struct {
	db                                                                   *storedb.DB
	store                                                                *Store
	claim                                                                jobs.WorkerClaim
	actorID, projectID, bookID, chapterID, siblingChapterID, paragraphID string
	view                                                                 governance.SourceView
	now                                                                  time.Time
}

func newStoreFixture(t *testing.T, base int, view governance.SourceView, tracers ...pgx.QueryTracer) storeFixture {
	t.Helper()
	db := openCurrentStoreTestDB(t, tracers...)
	baseFixture := createIndexingStoreFixtureForTest(t, db, base)
	f := storeFixture{db: db, store: New(db), actorID: baseFixture.actorID, projectID: baseFixture.projectID, bookID: baseFixture.bookID, chapterID: baseFixture.chapterID, paragraphID: baseFixture.paragraphID, siblingChapterID: indexingStoreUUIDForTest(base + 10), view: view, now: indexingStoreTestNow()}
	insertIndexingStoreChapterForTest(t, db, f.projectID, f.siblingChapterID, f.bookID, f.now)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE chapters SET "order"=2 WHERE id=$1`, f.siblingChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO chapter_source_members (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation, chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order) VALUES ($1,'draft',1,$2,1,$3,1,1,'Sibling',2)`, f.projectID, f.bookID, f.siblingChapterID); err != nil {
		t.Fatal(err)
	}
	{
		if _, err := db.Pool.Exec(context.Background(), `INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'published',1,1)`, f.projectID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(context.Background(), `INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,'published',1,$2,1,'Indexing Book')`, f.projectID, f.bookID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(context.Background(), `INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,'published',1,$2,1,$3,1,1,'Indexing Chapter',1),($1,'published',1,$2,1,$4,1,1,'Sibling',2)`, f.projectID, f.bookID, f.chapterID, f.siblingChapterID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(context.Background(), `INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at) SELECT id,chapter_id,revision,'published',kind,"order",markdown,plain_text,word_count,created_at,updated_at FROM chapter_paragraphs WHERE chapter_id=$1 AND variant='draft'`, f.chapterID); err != nil {
			t.Fatal(err)
		}
	}
	f.claim = addCanonicalIndexClaimForTest(t, f, 11)
	return f
}

func addCanonicalIndexClaimForTest(t *testing.T, f storeFixture, offset int) jobs.WorkerClaim {
	t.Helper()
	viewArm, err := governance.PresentSourceView(f.view)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{ProjectID: f.projectID, ProjectGeneration: 1}, viewArm, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	jobID := indexingStoreUUIDForTest(8000 + offset + int(f.view[0])*100)
	err = f.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, _, err := jobstore.New(f.db).UpsertCanonTextIndexIntent(context.Background(), tx, jobs.CanonTextIndexIntentInput{JobID: jobID, CreateIfMissing: true, Subject: jobs.CanonTextIndexSubject{Type: jobs.KindCanonTextIndex, ProjectID: f.projectID, SourceView: f.view, CatalogRevision: 1}, CreatorUserID: f.actorID, Source: jobs.JobSourceManifest{SourceView: viewArm, Refs: []governance.ArtifactSourceRef{ref}}, CreatedAt: f.now})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE jobs SET status='running', claim_incarnation=1, claimed_at=$3 WHERE project_id=$1 AND id=$2`, f.projectID, jobID, f.now); err != nil {
		t.Fatal(err)
	}
	return jobs.WorkerClaim{ProjectID: f.projectID, JobID: jobID, ClaimIncarnation: 1}
}

func (f storeFixture) snapshot(t *testing.T) CanonTextChapterSnapshot {
	t.Helper()
	descriptors, err := f.store.ListCanonTextChapterDescriptorsForClaim(context.Background(), f.claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		if descriptor.ChapterID == f.chapterID {
			snapshot, err := f.store.LoadCanonTextChapterSnapshotForClaim(context.Background(), f.claim, descriptor)
			if err != nil {
				t.Fatal(err)
			}
			return snapshot
		}
	}
	t.Fatal("chapter descriptor missing")
	return CanonTextChapterSnapshot{}
}

func indexCurrentCanonTextChaptersForTest(t *testing.T, ctx context.Context, fixture storeFixture) {
	t.Helper()
	descriptors, err := fixture.store.ListCanonTextChapterDescriptorsForClaim(ctx, fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		snapshot, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(ctx, fixture.claim, descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: fixture.now,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCanonicalIndexRows(t *testing.T, f storeFixture, view governance.SourceView, chapterID string, wantRows int) {
	t.Helper()
	var rows, states int
	if err := f.db.Pool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM chapter_text_search_rows WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3),
		  (SELECT count(*) FROM chapter_text_index_state WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3)
	`, f.projectID, view, chapterID).Scan(&rows, &states); err != nil {
		t.Fatal(err)
	}
	if rows != wantRows || states != wantRows {
		t.Fatalf("rows/state = %d/%d, want %d/%d", rows, states, wantRows, wantRows)
	}
}

func assertNoCanonicalIndexRows(t *testing.T, f storeFixture) {
	t.Helper()
	var rows, states int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM chapter_text_search_rows WHERE project_id=$1),(SELECT count(*) FROM chapter_text_index_state WHERE project_id=$1)`, f.projectID).Scan(&rows, &states); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || states != 0 {
		t.Fatalf("rows/state = %d/%d, want 0/0", rows, states)
	}
}

func canonicalChapterRows(t *testing.T, f storeFixture) []canonicalStoredSet {
	t.Helper()
	var row canonicalStoredSet
	if err := f.db.Pool.QueryRow(context.Background(), `
		SELECT row.source_revision, row.title, row.content
		FROM chapter_text_search_rows AS row
		JOIN chapter_text_index_state AS state USING (project_id, source_view, chapter_id)
		WHERE row.project_id=$1 AND row.source_view=$2 AND row.chapter_id=$3
	`, f.projectID, f.view, f.chapterID).Scan(&row.sourceRevision, &row.title, &row.content); err != nil {
		t.Fatal(err)
	}
	return []canonicalStoredSet{row}
}

type canonicalStoredSet struct {
	sourceRevision int64
	title          string
	content        string
}

func readCanonicalStoredSet(t *testing.T, conn *pgxpool.Conn, f storeFixture) canonicalStoredSet {
	t.Helper()
	var set canonicalStoredSet
	if err := conn.QueryRow(context.Background(), `
		SELECT row.source_revision, row.title, row.content
		FROM chapter_text_search_rows AS row
		JOIN chapter_text_index_state AS state USING (project_id, source_view, chapter_id)
		WHERE row.project_id=$1 AND row.source_view=$2 AND row.chapter_id=$3
	`, f.projectID, f.view, f.chapterID).Scan(&set.sourceRevision, &set.title, &set.content); err != nil {
		t.Fatal(err)
	}
	return set
}

func canonicalStoredSetForSnapshot(snapshot CanonTextChapterSnapshot) canonicalStoredSet {
	contents := make([]string, len(snapshot.Paragraphs))
	for index := range snapshot.Paragraphs {
		contents[index] = snapshot.Paragraphs[index].Content
	}
	return canonicalStoredSet{sourceRevision: int64(snapshot.SourceRevision), title: "Indexing Chapter", content: strings.Join(contents, "\n\n")}
}

func waitForCanonicalDBContender(t *testing.T, ctx context.Context, db *storedb.DB, result <-chan error) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for db.Pool.Stat().AcquiredConns() < 2 {
		select {
		case err := <-result:
			t.Fatalf("concurrent operation completed before lock release: %v", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("concurrent operation did not contend for a database lock")
		}
	}
}

func jobStatusForStoreTest(t *testing.T, db *storedb.DB, jobID string) jobs.Status {
	t.Helper()
	var status string
	if err := db.Pool.QueryRow(context.Background(), "SELECT status FROM jobs WHERE id = $1", jobID).Scan(&status); err != nil {
		t.Fatalf("read job status: %v", err)
	}
	return jobs.Status(status)
}

func assertCanonicalCoverageForTest(t *testing.T, got, want jobs.TextCoverage) {
	t.Helper()
	if got != want {
		t.Fatalf("coverage = %#v, want %#v", got, want)
	}
}
