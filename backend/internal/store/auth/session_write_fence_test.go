package authstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestImpersonationChildSessionWriteProofUsesTargetIdentity(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	now := time.Now().UTC()
	mustUUID := func(value string) pgtype.UUID {
		id, err := storedb.ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	adminID := mustUUID("00000000-0000-4000-8000-000000000911")
	targetID := mustUUID("00000000-0000-4000-8000-000000000912")
	adminSessionID := mustUUID("00000000-0000-4000-8000-000000000913")
	childSessionID := mustUUID("00000000-0000-4000-8000-000000000914")
	adminProof := auth.SessionWriteProof{SessionHash: hashForAuthStoreTest(t, 31), CSRFSecretHash: hashForAuthStoreTest(t, 32)}
	childProof := auth.SessionWriteProof{SessionHash: hashForAuthStoreTest(t, 33), CSRFSecretHash: hashForAuthStoreTest(t, 34)}

	for _, user := range []struct {
		id    pgtype.UUID
		email string
		name  string
	}{
		{id: adminID, email: "impersonation-admin@example.test", name: "Admin"},
		{id: targetID, email: "impersonation-target@example.test", name: "Target"},
	} {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO users (id, email, display_name, password_hash, email_verified_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5, $5)
		`, user.id, user.email, user.name, passwordHashForAuthStoreTest(t), now); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []struct {
		id     pgtype.UUID
		userID pgtype.UUID
		proof  auth.SessionWriteProof
	}{
		{id: adminSessionID, userID: adminID, proof: adminProof},
		{id: childSessionID, userID: targetID, proof: childProof},
	} {
		if _, err := db.Pool.Exec(ctx, `
			INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, session.id, session.userID, session.proof.SessionHash, session.proof.CSRFSecretHash, now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lockedSessionID, err := LockSessionWriteProof(ctx, tx, targetID, childProof)
	if err != nil {
		t.Fatal(err)
	}
	if lockedSessionID != childSessionID {
		t.Fatalf("locked session = %s, want child session %s", lockedSessionID, childSessionID)
	}
	if _, err := LockSessionWriteProof(ctx, tx, targetID, adminProof); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("admin proof with target actor error = %v, want ErrUnauthenticated", err)
	}
}

// R1.4.3: model work needs a verified address; a revoked session stays unauthenticated.
func TestLockVerifiedSessionWriteProofRequiresVerifiedAddress(t *testing.T) {
	db := openCurrentStoreTestDB(t)
	ctx := context.Background()
	insertUserForTest(t, db, fenceUserID, "fence@example.test", "Writer", passwordHashForAuthStoreTest(t))
	proof := auth.SessionWriteProof{SessionHash: hashForAuthStoreTest(t, 1), CSRFSecretHash: hashForAuthStoreTest(t, 2)}
	insertSessionForTest(t, db, fenceSessionID, fenceUserID, proof.SessionHash, proof.CSRFSecretHash, time.Now().Add(time.Hour), fenceNow)
	userID := mustUUIDForTest(t, fenceUserID)
	lock := func() error {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = LockVerifiedSessionWriteProof(ctx, tx, userID, proof)
		return err
	}
	if err := lock(); !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("unverified fence error = %v, want ErrEmailNotVerified", err)
	}
	markUserEmailVerifiedForTest(t, db, fenceUserID, fenceNow)
	if err := lock(); err != nil {
		t.Fatalf("verified fence error = %v", err)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE id = $1", fenceSessionID); err != nil {
		t.Fatal(err)
	}
	if err := lock(); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked fence error = %v, want ErrUnauthenticated", err)
	}
}

const (
	fenceUserID    = "00000000-0000-4000-8000-000000000921"
	fenceSessionID = "00000000-0000-4000-8000-000000000922"
)

var fenceNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func mustUUIDForTest(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	id, err := storedb.ParseUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertUserForTest(t *testing.T, db *storedb.DB, id, email, displayName, passwordHash string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO users (id, email, display_name, password_hash) VALUES ($1, $2, $3, $4)`,
		id, email, displayName, passwordHash); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func markUserEmailVerifiedForTest(t *testing.T, db *storedb.DB, userID string, verifiedAt time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), "UPDATE users SET email_verified_at = $1 WHERE id = $2", verifiedAt, userID); err != nil {
		t.Fatalf("mark user email verified: %v", err)
	}
}

func insertSessionForTest(t *testing.T, db *storedb.DB, id, userID, sessionHash, csrfSecretHash string, expiresAt, createdAt time.Time) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(),
		`INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, userID, sessionHash, csrfSecretHash, expiresAt, createdAt); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}
