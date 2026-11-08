package jobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

const (
	artifactConstraintUserID    = "00000000-0000-4000-8000-000000000a01"
	artifactConstraintProjectID = "00000000-0000-4000-8000-000000000a02"
	artifactConstraintJobID     = "00000000-0000-4000-8000-000000000a03"
	artifactConstraintOwnerID   = "00000000-0000-4000-8000-000000000a04"
	artifactConstraintRefID     = "00000000-0000-4000-8000-000000000a05"
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

func insertSessionForTest(t *testing.T, db *storedb.DB, id, userID, sessionHash, csrfSecretHash string, expiresAt, createdAt time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(
		context.Background(),
		`INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, userID, sessionHash, csrfSecretHash, expiresAt, createdAt,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func hashForAuthStoreTest(t *testing.T, index int) string {
	t.Helper()
	seed := t.Name() + ":" + strconv.Itoa(index) + ":" + auth.NewSecureIDGenerator().NewCSRFToken()
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
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

func insertBookForExportStoreTest(t *testing.T, db *storedb.DB, bookID, projectID, title string, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO books (id, project_id, title, subtitle, cover_color, "order", display_number, status, revision, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, NULL, 1, '1', 'draft', 1, $4, $4)
	`, bookID, projectID, title, now); err != nil {
		t.Fatalf("insert book: %v", err)
	}
}

func insertChapterForExportStoreTest(t *testing.T, db *storedb.DB, chapterID, bookID, title string, order int, status string, revision int64, publishedRevision *int64, deletedAt *time.Time, now time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO chapters (
			id, project_id, book_id, title, "order", status, revision,
			published_revision, published_at, deleted_at,
			governance_retention_started_at, purge_after, purge_deadline,
			created_at, updated_at
		)
		VALUES (
			$1, (SELECT project_id FROM books WHERE id = $2), $2, $3, $4, $5, $6,
			$7, $8, $9,
			$9, CASE WHEN $9::timestamptz IS NULL THEN NULL ELSE $9 + interval '7 days' END,
			CASE WHEN $9::timestamptz IS NULL THEN NULL ELSE $9 + interval '8 days' END,
			$10, $10
		)
	`, chapterID, bookID, title, order, status, revision, publishedRevision, now, deletedAt, now); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

// fenceProjectDeletionForTest создаёт согласованное удалённое состояние без импорта projectstore,
// который замыкает тестовый цикл projectstore → indexingstore → jobstore.
func fenceProjectDeletionForTest(t *testing.T, db *storedb.DB, _, projectID string, deletedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SET session_replication_role = origin`) }()

	deletedAt = deletedAt.UTC().Truncate(time.Microsecond)
	if _, err := conn.Exec(ctx, `
		UPDATE projects
		SET lifecycle_generation = lifecycle_generation + 1,
		    deleted_at = $2::timestamptz,
		    governance_retention_started_at = $2::timestamptz,
		    purge_after = $2::timestamptz + interval '7 days',
		    purge_deadline = $2::timestamptz + interval '8 days',
		    updated_at = $2::timestamptz
		WHERE id = $1
	`, projectID, deletedAt); err != nil {
		t.Fatalf("fence project deletion for jobs: %v", err)
	}
}

func insertProjectScopeArtifactOwner(t *testing.T, tx pgx.Tx, ownerID, refID, artifactID string, refCount int) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO artifact_source_owners (
			id, project_id, artifact_kind, artifact_id, source_view,
			manifest_version, manifest_hash, ref_count, read_state,
			source_identity_state
		) VALUES (
			$1, $2, 'job', $3, NULL,
			1, decode(repeat('11', 32), 'hex'), $4, 'active',
			'scope_only'
		)
	`, ownerID, artifactConstraintProjectID, artifactID, refCount); err != nil {
		t.Fatalf("insert artifact owner: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO artifact_source_refs (
			id, owner_id, project_id, ref_kind, scope_kind, source_view,
			project_generation, source_independent
		) VALUES ($1, $2, $3, 'scope', 'project', NULL, 1, true)
	`, refID, ownerID, artifactConstraintProjectID); err != nil {
		t.Fatalf("insert artifact source ref: %v", err)
	}
}

func setArtifactOwnerManifestHashFromRefs(t *testing.T, tx pgx.Tx, ownerID, projectID string) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `
		UPDATE artifact_source_owners AS owner
		SET manifest_hash = (
			SELECT ck_artifact_manifest_digest(
				owner.artifact_kind, owner.project_id, owner.artifact_id, owner.source_view,
				array_agg(ck_artifact_source_ref_projection(
					ref.ref_kind, ref.scope_kind, ref.project_id, ref.project_generation,
					ref.book_id, ref.book_generation, ref.chapter_id, ref.chapter_generation,
					ref.source_view, ref.source_independent,
					ref.chat_session_id, ref.chat_lifecycle_epoch
				)))
			FROM artifact_source_refs AS ref
			WHERE ref.owner_id = owner.id AND ref.project_id = owner.project_id
		)
		WHERE owner.id = $1 AND owner.project_id = $2
	`, ownerID, projectID); err != nil {
		t.Fatalf("derive artifact manifest hash from refs: %v", err)
	}
}
