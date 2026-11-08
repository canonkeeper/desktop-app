package canonstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

func insertProjectStoreUserForTest(t *testing.T, db *Store, userID, email string) {
	t.Helper()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, $2, 'Project Owner', 'test-password-hash', $3)
	`, userID, email, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertProjectStoreProjectForTest(t *testing.T, db *Store, projectID, ownerUserID, title string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO projects (id, owner_user_id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, '', $4, $4)
	`, projectID, ownerUserID, title, now); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO project_source_views (project_id, source_view, project_lifecycle_generation, catalog_revision)
		SELECT id, 'draft', lifecycle_generation, 1 FROM projects WHERE id = $1
	`, projectID); err != nil {
		t.Fatalf("insert project draft source marker: %v", err)
	}
}

func insertProjectStoreMembershipForTest(t *testing.T, db *Store, membershipID, projectID, userID string, role projects.Role, now time.Time, removed bool) {
	t.Helper()
	var removedAt *time.Time
	if removed {
		removedAt = &now
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO project_memberships (id, project_id, user_id, role, removed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, membershipID, projectID, userID, string(role), removedAt, now); err != nil {
		t.Fatalf("insert project membership: %v", err)
	}
}

func insertIndexingStoreBookForTest(t *testing.T, db *Store, bookID, projectID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
		VALUES ($1, $2, 'Indexing Book', 1, '1', $3, $3)
	`, bookID, projectID, now); err != nil {
		t.Fatalf("insert book: %v", err)
	}
}

func insertIndexingStoreChapterForTest(t *testing.T, db *Store, projectID, chapterID, bookID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapters (id, project_id, book_id, title, "order", revision, created_at, updated_at)
		VALUES ($1, $2, $3, 'Indexing Chapter', 1, 1, $4, $4)
	`, chapterID, projectID, bookID, now); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

func insertIndexingStoreParagraphForTest(t *testing.T, db *Store, paragraphID, chapterID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at)
		VALUES ($1, $2, 1, 'draft', 'paragraph', 1, 'dragon', 'dragon', 1, $3, $3)
	`, paragraphID, chapterID, now); err != nil {
		t.Fatalf("insert paragraph: %v", err)
	}
}

type indexingStoreFixtureForTest struct {
	actorID, projectID, membershipID, bookID, chapterID, paragraphID string
}

func createIndexingStoreFixtureForTest(t *testing.T, db *Store, base int) indexingStoreFixtureForTest {
	t.Helper()
	fixture := indexingStoreFixtureForTest{
		actorID: indexingStoreUUIDForTest(base + 1), projectID: indexingStoreUUIDForTest(base + 2),
		membershipID: indexingStoreUUIDForTest(base + 3), bookID: indexingStoreUUIDForTest(base + 4),
		chapterID: indexingStoreUUIDForTest(base + 5), paragraphID: indexingStoreUUIDForTest(base + 6),
	}
	now := indexingStoreTestNow()
	insertProjectStoreUserForTest(t, db, fixture.actorID, fmt.Sprintf("indexing-%d@example.com", base))
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Indexing Project", now)
	insertProjectStoreMembershipForTest(t, db, fixture.membershipID, fixture.projectID, fixture.actorID, projects.RoleEditor, now, false)
	insertIndexingStoreBookForTest(t, db, fixture.bookID, fixture.projectID, now)
	insertIndexingStoreChapterForTest(t, db, fixture.projectID, fixture.chapterID, fixture.bookID, now)
	insertIndexingStoreParagraphForTest(t, db, fixture.paragraphID, fixture.chapterID, now)
	seedIndexingCatalogForTest(t, db, fixture)
	return fixture
}

func seedIndexingCatalogForTest(t *testing.T, db *Store, fixture indexingStoreFixtureForTest) {
	t.Helper()
	if err := db.WithTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO book_source_members (
			  project_id, source_view, project_lifecycle_generation,
			  book_id, book_lifecycle_generation, title
			) VALUES ($1, 'draft', 1, $2, 1, 'Indexing Book')
		`, fixture.projectID, fixture.bookID); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `
			INSERT INTO chapter_source_members (
			  project_id, source_view, project_lifecycle_generation,
			  book_id, book_lifecycle_generation,
			  chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order
			) VALUES ($1, 'draft', 1, $2, 1, $3, 1, 1, 'Indexing Chapter', 1)
		`, fixture.projectID, fixture.bookID, fixture.chapterID)
		return err
	}); err != nil {
		t.Fatalf("seed indexing catalog: %v", err)
	}
}

type canonicalIndexStoreFixture struct {
	db                                                                   *Store
	store                                                                *indexingstore.Store
	claim                                                                jobs.WorkerClaim
	actorID, projectID, bookID, chapterID, siblingChapterID, paragraphID string
	view                                                                 governance.SourceView
	now                                                                  time.Time
}

func newCanonicalIndexStoreFixture(t *testing.T, base int, view governance.SourceView) canonicalIndexStoreFixture {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	baseFixture := createIndexingStoreFixtureForTest(t, db, base)
	fixture := canonicalIndexStoreFixture{
		db: db, store: indexingstore.New(db.DB),
		actorID: baseFixture.actorID, projectID: baseFixture.projectID, bookID: baseFixture.bookID,
		chapterID: baseFixture.chapterID, paragraphID: baseFixture.paragraphID,
		siblingChapterID: indexingStoreUUIDForTest(base + 10), view: view, now: indexingStoreTestNow(),
	}
	insertIndexingStoreChapterForTest(t, db, fixture.projectID, fixture.siblingChapterID, fixture.bookID, fixture.now)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE chapters SET "order"=2 WHERE id=$1`, fixture.siblingChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO chapter_source_members (project_id, source_view, project_lifecycle_generation, book_id, book_lifecycle_generation, chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order) VALUES ($1,'draft',1,$2,1,$3,1,1,'Sibling',2)`, fixture.projectID, fixture.bookID, fixture.siblingChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO project_source_views (project_id,source_view,project_lifecycle_generation,catalog_revision) VALUES ($1,'published',1,1)`, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,'published',1,$2,1,'Indexing Book')`, fixture.projectID, fixture.bookID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,'published',1,$2,1,$3,1,1,'Indexing Chapter',1),($1,'published',1,$2,1,$4,1,1,'Sibling',2)`, fixture.projectID, fixture.bookID, fixture.chapterID, fixture.siblingChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO chapter_paragraphs (id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at) SELECT id,chapter_id,revision,'published',kind,"order",markdown,plain_text,word_count,created_at,updated_at FROM chapter_paragraphs WHERE chapter_id=$1 AND variant='draft'`, fixture.chapterID); err != nil {
		t.Fatal(err)
	}
	fixture.claim = addCanonicalIndexClaimForTest(t, fixture, 11)
	return fixture
}

func addCanonicalIndexClaimForTest(t *testing.T, fixture canonicalIndexStoreFixture, offset int) jobs.WorkerClaim {
	t.Helper()
	view, err := governance.PresentSourceView(fixture.view)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
		ProjectID: fixture.projectID, ProjectGeneration: 1,
	}, view, governance.AbsentChatArm())
	if err != nil {
		t.Fatal(err)
	}
	jobID := indexingStoreUUIDForTest(8000 + offset + int(fixture.view[0])*100)
	if err := fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, _, err := jobstore.New(fixture.db.DB).UpsertCanonTextIndexIntent(context.Background(), tx, jobs.CanonTextIndexIntentInput{
			JobID: jobID, CreateIfMissing: true,
			Subject: jobs.CanonTextIndexSubject{
				Type: jobs.KindCanonTextIndex, ProjectID: fixture.projectID,
				SourceView: fixture.view, CatalogRevision: 1,
			},
			CreatorUserID: fixture.actorID,
			Source:        jobs.JobSourceManifest{SourceView: view, Refs: []governance.ArtifactSourceRef{ref}},
			CreatedAt:     fixture.now,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(context.Background(), `UPDATE jobs SET status='running', claim_incarnation=1, claimed_at=$3 WHERE project_id=$1 AND id=$2`, fixture.projectID, jobID, fixture.now); err != nil {
		t.Fatal(err)
	}
	return jobs.WorkerClaim{ProjectID: fixture.projectID, JobID: jobID, ClaimIncarnation: 1}
}

func indexingStoreUUIDForTest(value int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
}

func indexingStoreTestNow() time.Time {
	return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
}

type governedChapterFixtureForTest struct {
	db                         *Store
	actorID, projectID, bookID string
	chapterID, membershipID    string
	fencedAt                   time.Time
}

func newGovernedChapterFixtureForTest(t *testing.T, role projects.Role, removed bool) governedChapterFixtureForTest {
	t.Helper()
	db := openCurrentStoreTestDB(t)
	ids := projects.NewSecureIDGenerator()
	fixture := governedChapterFixtureForTest{
		db: db, actorID: ids.NewProjectID(), projectID: ids.NewProjectID(),
		bookID: ids.NewProjectID(), chapterID: ids.NewProjectID(),
		membershipID: ids.NewProjectMembershipID(), fencedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	insertProjectStoreUserForTest(t, db, fixture.actorID, fixture.actorID+"@chapter-fixture.example.test")
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Governed Chapter Project", fixture.fencedAt.Add(-3*time.Hour))
	insertProjectStoreMembershipForTest(t, db, fixture.membershipID, fixture.projectID, fixture.actorID, role, fixture.fencedAt.Add(-3*time.Hour), removed)
	insertIndexingStoreBookForTest(t, db, fixture.bookID, fixture.projectID, fixture.fencedAt.Add(-2*time.Hour))
	insertIndexingStoreChapterForTest(t, db, fixture.projectID, fixture.chapterID, fixture.bookID, fixture.fencedAt.Add(-time.Hour))
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO book_source_members (
			project_id, source_view, project_lifecycle_generation,
			book_id, book_lifecycle_generation, title
		) VALUES ($1, 'draft', 1, $2, 1, 'Governed Chapter Book')
	`, fixture.projectID, fixture.bookID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_source_members (
			project_id, source_view, project_lifecycle_generation,
			book_id, book_lifecycle_generation,
			chapter_id, chapter_lifecycle_generation, source_revision, title, chapter_order
		) VALUES ($1, 'draft', 1, $2, 1, $3, 1, 1, 'Governed Chapter', 1)
	`, fixture.projectID, fixture.bookID, fixture.chapterID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fenceChapterDeletionForTest(t *testing.T, fixture governedChapterFixtureForTest) {
	t.Helper()
	ctx := context.Background()
	err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		var projectGeneration, bookGeneration, chapterGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT p.lifecycle_generation, b.lifecycle_generation, c.lifecycle_generation
			FROM projects AS p
			JOIN books AS b ON b.project_id = p.id
			JOIN chapters AS c ON c.project_id = p.id AND c.book_id = b.id
			JOIN project_memberships AS m ON m.project_id = p.id
			WHERE p.id = $1 AND p.deleted_at IS NULL
			  AND b.id = $2 AND b.deleted_at IS NULL
			  AND c.id = $3 AND c.deleted_at IS NULL
			  AND m.user_id = $4 AND m.removed_at IS NULL
			  AND m.role IN ('owner', 'admin', 'editor')
			FOR UPDATE OF p, b, c, m
		`, fixture.projectID, fixture.bookID, fixture.chapterID, fixture.actorID).Scan(
			&projectGeneration, &bookGeneration, &chapterGeneration,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE chapters
			SET deleted_at = $4,
			    governance_retention_started_at = $4,
			    purge_after = $5,
			    purge_deadline = $6,
			    lifecycle_generation = lifecycle_generation + 1,
			    revision = revision + 1,
			    updated_at = $4
			WHERE id = $1 AND book_id = $2 AND project_id = $3
		`, fixture.chapterID, fixture.bookID, fixture.projectID, fixture.fencedAt,
			fixture.fencedAt.Add(deletion.SourceRestoreWindow),
			fixture.fencedAt.Add(deletion.SourceRestoreWindow+deletion.SourceHardPurgeGrace)); err != nil {
			return err
		}
		vector := governance.SourceGenerationVector{
			ProjectID: fixture.projectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
			BookID: fixture.bookID, BookGeneration: governance.LifecycleGeneration(bookGeneration),
			ChapterID: fixture.chapterID, ChapterGeneration: governance.LifecycleGeneration(chapterGeneration),
		}
		if _, err := insertSourceDeletionTombstoneForTest(
			ctx, tx, governance.DeletionSelectionChapter, vector, fixture.fencedAt,
		); err != nil {
			return err
		}
		if err := fixture.db.RevokeExactSourceArtifactOwnersAtFence(
			ctx, tx, governance.DeletionSelectionChapter, vector, fixture.fencedAt,
		); err != nil {
			return err
		}
		err := fixture.db.ScrubCanonicalDerivedSourceInTx(
			ctx, tx, governance.DeletionSelectionChapter, vector, fixture.fencedAt,
		)
		return err
	})
	if err != nil {
		t.Fatalf("fence chapter deletion: %v", err)
	}
}
