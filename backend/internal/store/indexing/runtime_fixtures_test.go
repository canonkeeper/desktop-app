package indexingstore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/projects"
	storedb "canonkeeper/backend/internal/store/db"
)

func insertProjectStoreUserWithSessionForTest(t *testing.T, db *storedb.DB, userID, email string) auth.SessionWriteProof {
	t.Helper()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO users (id, email, display_name, password_hash, email_verified_at)
		VALUES ($1, $2, 'Project Owner', 'test-password-hash', $3)
	`, userID, email, now); err != nil {
		t.Fatalf("insert user: %v", err)
	}
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
		SELECT id, 'draft', lifecycle_generation, 1 FROM projects WHERE id=$1
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
