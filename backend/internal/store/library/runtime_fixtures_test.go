package librarystore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertProjectStoreUserForTest(t *testing.T, db *storedb.DB, userID, email string) {
	t.Helper()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, $2, 'Project Owner', 'test-password-hash', $3)
	`, userID, email, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertProjectStoreUserWithSessionForTest(t *testing.T, db *storedb.DB, userID, email string) auth.SessionWriteProof {
	t.Helper()
	insertProjectStoreUserForTest(t, db, userID, email)
	proof := auth.SessionWriteProof{
		SessionHash:    fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+userID+":session"))),
		CSRFSecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()+userID+":csrf"))),
	}
	if _, err := db.Pool.Exec(context.Background(), `
		WITH db_clock AS (SELECT clock_timestamp() AS now_at)
		INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, now_at + interval '1 hour', now_at FROM db_clock
	`, userID, proof.SessionHash, proof.CSRFSecretHash); err != nil {
		t.Fatalf("insert active user session: %v", err)
	}
	return proof
}

func insertProjectStoreProjectForTest(t *testing.T, db *storedb.DB, projectID, ownerUserID, title string, now time.Time) {
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

func insertProjectStoreMembershipForTest(t *testing.T, db *storedb.DB, membershipID, projectID, userID string, role projects.Role, now time.Time, removed bool) {
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

func insertIndexingStoreParagraphForTest(t *testing.T, db *storedb.DB, paragraphID, chapterID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (id, chapter_id, revision, variant, kind, "order", markdown, plain_text, word_count, created_at, updated_at)
		VALUES ($1, $2, 1, 'draft', 'paragraph', 1, 'dragon', 'dragon', 1, $3, $3)
	`, paragraphID, chapterID, now); err != nil {
		t.Fatalf("insert paragraph: %v", err)
	}
}

func insertIndexingStoreBookForTest(t *testing.T, db *storedb.DB, bookID, projectID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, "order", display_number, created_at, updated_at)
		VALUES ($1, $2, 'Indexing Book', 1, '1', $3, $3)
	`, bookID, projectID, now); err != nil {
		t.Fatalf("insert book: %v", err)
	}
}

func insertIndexingStoreChapterForTest(t *testing.T, db *storedb.DB, projectID, chapterID, bookID string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapters (id, project_id, book_id, title, "order", revision, created_at, updated_at)
		VALUES ($1, $2, $3, 'Indexing Chapter', 1, 1, $4, $4)
	`, chapterID, projectID, bookID, now); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

type indexingStoreFixtureForTest struct {
	actorID, projectID, membershipID string
	bookID, chapterID, paragraphID   string
	sessionProof                     auth.SessionWriteProof
}

func createIndexingStoreFixtureForTest(t *testing.T, db *storedb.DB, base int) indexingStoreFixtureForTest {
	t.Helper()
	fixture := indexingStoreFixtureForTest{
		actorID: indexingStoreUUIDForTest(base + 1), projectID: indexingStoreUUIDForTest(base + 2),
		membershipID: indexingStoreUUIDForTest(base + 3), bookID: indexingStoreUUIDForTest(base + 4),
		chapterID: indexingStoreUUIDForTest(base + 5), paragraphID: indexingStoreUUIDForTest(base + 6),
	}
	now := indexingStoreTestNow()
	fixture.sessionProof = insertProjectStoreUserWithSessionForTest(t, db, fixture.actorID, fmt.Sprintf("indexing-%d@example.com", base))
	insertProjectStoreProjectForTest(t, db, fixture.projectID, fixture.actorID, "Indexing Project", now)
	insertProjectStoreMembershipForTest(t, db, fixture.membershipID, fixture.projectID, fixture.actorID, projects.RoleEditor, now, false)
	insertIndexingStoreBookForTest(t, db, fixture.bookID, fixture.projectID, now)
	insertIndexingStoreChapterForTest(t, db, fixture.projectID, fixture.chapterID, fixture.bookID, now)
	insertIndexingStoreParagraphForTest(t, db, fixture.paragraphID, fixture.chapterID, now)
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
	return fixture
}

type workerClaimAuthorityForLibraryTest struct {
	db           *storedb.DB
	workerSchema string
}

func newWorkerClaimAuthorityForTest(db *storedb.DB) *workerClaimAuthorityForLibraryTest {
	var schema string
	if err := db.Pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		panic(err)
	}
	return &workerClaimAuthorityForLibraryTest{db: db, workerSchema: pgx.Identifier{schema + "_worker"}.Sanitize()}
}

func (authority *workerClaimAuthorityForLibraryTest) ClaimNextQueuedJobForWorker(ctx context.Context, kinds []jobs.Kind) (jobs.WorkerClaimDecision, error) {
	values := make([]string, len(kinds))
	for index, kind := range kinds {
		values[index] = string(kind)
	}
	var projectID, jobID pgtype.UUID
	var kind, state string
	var incarnation int64
	var exhausted pgtype.Text
	err := authority.db.Pool.QueryRow(ctx, `
		SELECT project_id, work_id, kind, state, claim_incarnation, exhausted_status
		FROM `+authority.workerSchema+`.claim_next_queued_job_for_worker($1, NULL)
	`, values).Scan(&projectID, &jobID, &kind, &state, &incarnation, &exhausted)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.WorkerClaimDecision{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.WorkerClaimDecision{}, err
	}
	decision := jobs.WorkerClaimDecision{Claim: jobs.WorkerClaim{
		ProjectID: projectID.String(), JobID: jobID.String(), ClaimIncarnation: incarnation,
	}}
	if exhausted.Valid {
		decision.ExhaustedStatus = jobs.Status(exhausted.String)
	}
	return decision, nil
}

func (authority *workerClaimAuthorityForLibraryTest) ClaimNextImportExpiryForWorker(ctx context.Context) (jobs.WorkerClaim, error) {
	var projectID, jobID pgtype.UUID
	var incarnation int64
	err := authority.db.Pool.QueryRow(ctx, `
		SELECT project_id, job_id, claim_incarnation
		FROM `+authority.workerSchema+`.claim_next_import_expiry_for_worker()
	`).Scan(&projectID, &jobID, &incarnation)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.WorkerClaim{}, jobs.ErrNotFound
	}
	return jobs.WorkerClaim{ProjectID: projectID.String(), JobID: jobID.String(), ClaimIncarnation: incarnation}, err
}

func cleanupProjectsStoreRows(t *testing.T, db *storedb.DB, userID, projectID string) {
	t.Helper()
	cleanup := func() {
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_invitations WHERE project_id=$1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM project_memberships WHERE project_id=$1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", projectID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM sessions WHERE user_id=$1", userID)
		_, _ = db.Pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	}
	t.Cleanup(cleanup)
	cleanup()
}

func countJobsStoreRowsForTest(t *testing.T, db *storedb.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func indexingStoreUUIDForTest(value int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
}

func indexingStoreTestNow() time.Time {
	return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
}

func advanceProjectLifecycleForLibraryTest(t *testing.T, db *storedb.DB, projectID string, changedAt time.Time) {
	t.Helper()
	withTestReplicaRole(t, db, func(ctx context.Context, conn *pgxpool.Conn) {
		if _, err := conn.Exec(ctx, `
			UPDATE projects
			SET lifecycle_generation=lifecycle_generation+1,
			    deleted_at=$2, governance_retention_started_at=$2,
			    purge_after=$2::timestamptz+interval '7 days',
			    purge_deadline=$2::timestamptz+interval '8 days'
			WHERE id=$1
		`, projectID, changedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `
			UPDATE projects
			SET deleted_at=NULL, governance_retention_started_at=NULL,
			    purge_after=NULL, purge_deadline=NULL, updated_at=$2
			WHERE id=$1
		`, projectID, changedAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `
			UPDATE project_source_views AS view
			SET project_lifecycle_generation=project.lifecycle_generation,
			    catalog_revision=view.catalog_revision+1
			FROM projects AS project
			WHERE view.project_id=$1 AND project.id=view.project_id
		`, projectID); err != nil {
			t.Fatal(err)
		}
	})
}

func deleteProjectForLibraryTest(ctx context.Context, db *storedb.DB, projectID string, deletedAt time.Time) error {
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET session_replication_role=replica`); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SET session_replication_role=origin`) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var generation int64
	if err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation FROM projects
		WHERE id=$1 AND deleted_at IS NULL
		FOR UPDATE
	`, projectID).Scan(&generation); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE projects
		SET lifecycle_generation=$2+1,
		    deleted_at=$3, governance_retention_started_at=$3,
		    purge_after=$3::timestamptz+interval '7 days',
		    purge_deadline=$3::timestamptz+interval '8 days'
		WHERE id=$1 AND lifecycle_generation=$2
	`, projectID, generation, deletedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockCanonTextTerminalProjectForLibraryTest(ctx context.Context, tx pgx.Tx, projectID pgtype.UUID) (int64, error) {
	var generation int64
	err := tx.QueryRow(ctx, `
		SELECT lifecycle_generation FROM projects
		WHERE id=$1 AND deleted_at IS NULL
		FOR SHARE
	`, projectID).Scan(&generation)
	return generation, err
}

func lockCanonTextTerminalCatalogForLibraryTest(
	ctx context.Context,
	tx pgx.Tx,
	projectID pgtype.UUID,
	projectGeneration int64,
	subject jobs.CanonTextIndexSubject,
) error {
	for _, query := range []string{
		`SELECT id FROM books WHERE project_id=$1 AND deleted_at IS NULL ORDER BY "order",id FOR SHARE`,
		`SELECT chapter.id FROM chapters AS chapter
		 JOIN books AS book ON book.id=chapter.book_id AND book.project_id=chapter.project_id AND book.deleted_at IS NULL
		 WHERE chapter.project_id=$1 AND chapter.deleted_at IS NULL
		 ORDER BY book."order",chapter."order",chapter.id FOR SHARE OF chapter`,
	} {
		rows, err := tx.Query(ctx, query, projectID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id pgtype.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	var revision int64
	return tx.QueryRow(ctx, `
		SELECT catalog_revision FROM project_source_views
		WHERE project_id=$1 AND source_view=$2
		  AND project_lifecycle_generation=$3 AND catalog_revision=$4
		FOR SHARE
	`, projectID, string(subject.SourceView), projectGeneration, int64(subject.CatalogRevision)).Scan(&revision)
}
