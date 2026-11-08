package authstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
)

func TestAuthStoreImplementsAuthStoreContract(t *testing.T) {
	var _ auth.Store = (*Store)(nil)
}

// A user the core creates has no password; its session reads the verified address and ends
// with every other session of the user.
func TestAuthStoreCreatesAUserWithoutPasswordAndRevokesItsSessions(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	userID := "00000000-0000-4000-8000-000000000521"
	sessionHash := hashForAuthStoreTest(t, 1)

	user, err := store.CreateUser(ctx, auth.CreateUserInput{
		ID: userID, Email: "local@desktop.invalid", DisplayName: "Автор", EmailVerifiedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.ID != userID || user.Email != "local@desktop.invalid" || user.DisplayName != "Автор" || !user.EmailVerified ||
		user.TermsVersion != "" || user.ConsentVersion != "" {
		t.Fatalf("CreateUser() = %+v", user)
	}
	var passwordHash string
	if err := db.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&passwordHash); err != nil || passwordHash != "" {
		t.Fatalf("stored password hash = %q, %v; want none", passwordHash, err)
	}
	if _, err := store.CreateSession(ctx, auth.CreateSessionInput{
		ID: "00000000-0000-4000-8000-000000000621", UserID: userID, SessionHash: sessionHash,
		CSRFSecretHash: hashForAuthStoreTest(t, 2), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	active, err := store.GetActiveSessionByHash(ctx, sessionHash, now)
	if err != nil || active.User.ID != userID || !active.User.EmailVerified {
		t.Fatalf("GetActiveSessionByHash() = %+v, %v", active, err)
	}

	if err := store.RevokeUserSessions(ctx, userID, now); err != nil {
		t.Fatalf("RevokeUserSessions() error = %v", err)
	}
	if _, err := store.GetActiveSessionByHash(ctx, sessionHash, now); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("revoked GetActiveSessionByHash() error = %v, want ErrSessionNotFound", err)
	}
}

func TestProductRuntimeRoleCanUseAuthenticationAndProjectQueries(t *testing.T) {
	ctx := context.Background()
	db := openCurrentStoreTestDB(t)
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL ROLE canonkeeper_product_runtime`); err != nil {
		t.Fatal(err)
	}
	queries := sqlc.New(tx)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	userID, err := storedb.ParseUUID("00000000-0000-4000-8000-000000000501")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := storedb.ParseUUID("00000000-0000-4000-8000-000000000601")
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := storedb.ParseUUID("00000000-0000-4000-8000-000000000701")
	if err != nil {
		t.Fatal(err)
	}
	membershipID, err := storedb.ParseUUID("00000000-0000-4000-8000-000000000801")
	if err != nil {
		t.Fatal(err)
	}

	const email = "product-runtime@example.com"
	createdUser, err := queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:              userID,
		Email:           email,
		DisplayName:     "Product Runtime",
		PasswordHash:    passwordHashForAuthStoreTest(t),
		EmailVerifiedAt: storedb.Timestamptz(now),
	})
	if err != nil {
		t.Fatal(err)
	}
	if storedb.UUIDString(createdUser.ID) != storedb.UUIDString(userID) || createdUser.Email != email {
		t.Fatal("product runtime returned an unexpected user identity")
	}

	sessionHash := hashForAuthStoreTest(t, 20)
	createdSession, err := queries.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:             sessionID,
		UserID:         userID,
		SessionHash:    sessionHash,
		CsrfSecretHash: hashForAuthStoreTest(t, 21),
		ExpiresAt:      storedb.Timestamptz(now.Add(time.Hour)),
		CreatedAt:      storedb.Timestamptz(now),
	})
	if err != nil {
		t.Fatal(err)
	}
	activeSession, err := queries.GetActiveSessionByHash(ctx, sqlc.GetActiveSessionByHashParams{
		SessionHash: sessionHash,
		Now:         storedb.Timestamptz(now),
	})
	if err != nil {
		t.Fatal(err)
	}
	if storedb.UUIDString(createdSession.ID) != storedb.UUIDString(sessionID) ||
		storedb.UUIDString(activeSession.SessionID) != storedb.UUIDString(sessionID) ||
		storedb.UUIDString(activeSession.UserID) != storedb.UUIDString(userID) || activeSession.UserEmail != email {
		t.Fatal("product runtime returned an unexpected session identity")
	}

	if _, err := queries.CreateProject(ctx, sqlc.CreateProjectParams{
		ID:          projectID,
		OwnerUserID: userID,
		Name:        "Product Runtime Project",
		Description: "Runtime role access regression",
		CreatedAt:   storedb.Timestamptz(now),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateProjectMembership(ctx, sqlc.CreateProjectMembershipParams{
		ID:        membershipID,
		ProjectID: projectID,
		UserID:    userID,
		Role:      "owner",
		CreatedAt: storedb.Timestamptz(now),
	}); err != nil {
		t.Fatal(err)
	}
	projects, err := queries.ListProjectsForActor(ctx, sqlc.ListProjectsForActorParams{
		ActorUserID:     userID,
		HasCursor:       false,
		CursorCreatedAt: storedb.Timestamptz(now),
		CursorID:        projectID,
		LimitCount:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || storedb.UUIDString(projects[0].ID) != storedb.UUIDString(projectID) || projects[0].MembershipRole != "owner" {
		t.Fatalf("projects = %#v", projects)
	}
}

// passwordHashForAuthStoreTest is a stored password hash for rows the test inserts; the core
// never reads it.
func passwordHashForAuthStoreTest(t *testing.T) string {
	t.Helper()
	return hashForAuthStoreTest(t, 0)
}

func hashForAuthStoreTest(t *testing.T, index int) string {
	t.Helper()
	seed := t.Name() + ":" + strconv.Itoa(index) + ":" + auth.NewSecureIDGenerator().NewCSRFToken()
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}
