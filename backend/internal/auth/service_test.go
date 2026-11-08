package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCreateUserStoresAUserWithoutPassword(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	service := NewService(store, StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"), StaticClockForTest(now))

	unverified, err := service.CreateUser(ctx, NewUser{Email: "writer@example.com", DisplayName: "Canon Writer"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if unverified.ID != "user-1" || unverified.Email != "writer@example.com" || unverified.DisplayName != "Canon Writer" || unverified.EmailVerified {
		t.Fatalf("CreateUser() = %+v, want an unverified user-1", unverified)
	}
	if stored := store.usersByID["user-1"]; !stored.EmailVerifiedAt.IsZero() {
		t.Fatalf("stored verification = %s, want none", stored.EmailVerifiedAt)
	}

	service = NewService(store, StaticIDsWithGeneratedSecretsForTest(t, "user-2", "session-2"), StaticClockForTest(now))
	verified, err := service.CreateUser(ctx, NewUser{Email: "author@example.com", DisplayName: "Автор", EmailVerified: true})
	if err != nil {
		t.Fatalf("CreateUser(verified) error = %v", err)
	}
	if !verified.EmailVerified || !store.usersByID["user-2"].EmailVerifiedAt.Equal(now) {
		t.Fatalf("CreateUser(verified) = %+v, stored %s, want verified at %s", verified, store.usersByID["user-2"].EmailVerifiedAt, now)
	}
}

func TestCreateSessionOpensASessionOfAKnownUser(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	ids := StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1")
	service := NewService(store, ids, StaticClockForTest(now))
	user := createUserForTest(t, service, "writer@example.com")

	session, csrfToken, err := service.CreateSession(ctx, NewSession{UserID: user.ID, ExpiresAt: now.Add(time.Hour), Persistent: true})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if session.ID != "session-1" || session.CookieValue != ids.cookieValue || csrfToken != ids.csrfToken ||
		!session.ExpiresAt.Equal(now.Add(time.Hour)) || !session.Persistent {
		t.Fatalf("CreateSession() = %+v/%q", session, csrfToken)
	}
	stored := store.sessionsByID["session-1"]
	if stored.SessionHash != HashSecret(ids.cookieValue) || stored.CSRFSecretHash != HashSecret(ids.csrfToken) || !stored.CreatedAt.Equal(now) || stored.ImpersonatorUserID != "" {
		t.Fatalf("stored session = %+v, want only the hashes of its secrets", stored)
	}
	if current, err := service.CurrentUser(ctx, session.CookieValue); err != nil || current.ID != user.ID {
		t.Fatalf("CurrentUser() = %+v, %v", current, err)
	}
}

func TestCreateSessionOnBehalfOfAUserMarksItsCookieAsAChild(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	ids := StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1")
	service := NewService(store, ids, StaticClockForTest(now))
	user := createUserForTest(t, service, "writer@example.com")

	child, _, err := service.CreateSession(ctx, NewSession{UserID: user.ID, ExpiresAt: now.Add(time.Hour), ImpersonatorUserID: "admin-1"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if !IsImpersonationChildCookie(child.CookieValue) || child.CookieValue != "imp_"+ids.cookieValue {
		t.Fatalf("child cookie = %q, want the marked cookie", child.CookieValue)
	}
	stored := store.sessionsByID["session-1"]
	if stored.SessionHash != HashSecret(child.CookieValue) || stored.ImpersonatorUserID != "admin-1" || stored.Persistent {
		t.Fatalf("stored child session = %+v", stored)
	}
}

func TestCurrentUserReturnsUserForActiveSession(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	service := NewService(
		store,
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	)
	session, _ := signedInForTest(t, service, "writer@example.com")

	user, err := service.CurrentUser(ctx, session.CookieValue)
	if err != nil {
		t.Fatalf("CurrentUser() error = %v", err)
	}
	if user.ID != "user-1" {
		t.Fatalf("User.ID = %q, want user-1", user.ID)
	}
	if user.Email != "writer@example.com" {
		t.Fatalf("User.Email = %q, want writer@example.com", user.Email)
	}
	if user.EmailVerified {
		t.Fatal("User.EmailVerified = true, want false before the verification link")
	}
	if _, err := service.CurrentUser(ctx, ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("CurrentUser(no cookie) error = %v, want ErrUnauthenticated", err)
	}
	if _, err := service.CurrentUser(ctx, sessionCookieForTest(t, 2)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("CurrentUser(unknown cookie) error = %v, want ErrUnauthenticated", err)
	}
}

func TestCurrentUserCarriesEmailVerificationStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	)
	user, err := service.CreateUser(ctx, NewUser{Email: "writer@example.com", DisplayName: "Canon Writer", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := service.CreateSession(ctx, NewSession{UserID: user.ID, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	current, err := service.CurrentUser(ctx, session.CookieValue)
	if err != nil {
		t.Fatalf("CurrentUser() error = %v", err)
	}
	if !current.EmailVerified {
		t.Fatal("User.EmailVerified = false, want true")
	}
}

func TestValidateCSRFChecksSessionBoundToken(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	)
	session, csrfToken := signedInForTest(t, service, "writer@example.com")

	if err := service.ValidateCSRF(ctx, session.CookieValue, csrfToken); err != nil {
		t.Fatalf("ValidateCSRF() error = %v", err)
	}
	if err := service.ValidateCSRF(ctx, session.CookieValue, csrfTokenForTest(t, 2)); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("ValidateCSRF() error = %v, want ErrInvalidCSRF", err)
	}
}

func TestAuthenticateWriteReturnsOnlyValidatedSessionProof(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	)
	session, csrfToken := signedInForTest(t, service, "writer@example.com")

	user, proof, err := service.AuthenticateWrite(ctx, session.CookieValue, csrfToken)
	if err != nil {
		t.Fatalf("AuthenticateWrite() error = %v", err)
	}
	if user.ID != "user-1" || !proof.Valid() || proof.SessionHash == session.CookieValue || proof.CSRFSecretHash == csrfToken {
		t.Fatalf("AuthenticateWrite() = %#v/%#v", user, proof)
	}
	if _, _, err := service.AuthenticateWrite(ctx, session.CookieValue, csrfTokenForTest(t, 2)); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("AuthenticateWrite(wrong csrf) error = %v, want ErrInvalidCSRF", err)
	}
	if _, _, err := service.AuthenticateWrite(ctx, "expired-session", ""); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("AuthenticateWrite(expired session without csrf) error = %v, want ErrUnauthenticated", err)
	}
	if _, _, err := service.AuthenticateWrite(ctx, session.CookieValue, ""); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("AuthenticateWrite(active session without csrf) error = %v, want ErrInvalidCSRF", err)
	}
	encoded, err := json.Marshal(proof)
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("SessionWriteProof JSON = %q, %v", encoded, err)
	}
}

func TestSessionWriteProofRequiresBothHashes(t *testing.T) {
	if (SessionWriteProof{}).Valid() || (SessionWriteProof{SessionHash: "session"}).Valid() ||
		(SessionWriteProof{CSRFSecretHash: "csrf"}).Valid() ||
		!(SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"}).Valid() {
		t.Fatal("SessionWriteProof.Valid() accepted an incomplete proof")
	}
}

// SEC-03 / R1.4: a flood of anonymous CSRF requests from one client must not
// invalidate the token another client is about to submit.
func TestAnonymousCSRFTokenSurvivesFloodOfOtherAnonymousTokens(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		&countingIDsForTest{},
		StaticClockForTest(now),
	)

	honest, err := service.CSRFToken(ctx, "")
	if err != nil {
		t.Fatalf("CSRFToken() error = %v", err)
	}
	for range 5000 {
		if _, err := service.CSRFToken(ctx, ""); err != nil {
			t.Fatalf("CSRFToken() error = %v", err)
		}
	}

	if err := service.ValidateCSRF(ctx, "", honest.Token); err != nil {
		t.Fatalf("ValidateCSRF(honest token after flood) error = %v, want nil", err)
	}
}

func TestAnonymousCSRFTokenRejectsForgedExpiredAndForeignTokens(t *testing.T) {
	ctx := context.Background()
	clock := &manualClockForTest{now: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	service := NewService(NewMemoryStoreForTest(t), &countingIDsForTest{}, clock)
	other := NewService(NewMemoryStoreForTest(t), &countingIDsForTest{}, clock)

	token, err := service.CSRFToken(ctx, "")
	if err != nil {
		t.Fatalf("CSRFToken() error = %v", err)
	}
	foreign, err := other.CSRFToken(ctx, "")
	if err != nil {
		t.Fatalf("other CSRFToken() error = %v", err)
	}
	forgedExpiry := strings.Replace(token.Token, "."+strconv.FormatInt(token.ExpiresAt.UnixMilli(), 10)+".",
		"."+strconv.FormatInt(token.ExpiresAt.Add(time.Hour).UnixMilli(), 10)+".", 1)
	for name, candidate := range map[string]string{
		"foreign":       foreign.Token,
		"forged expiry": forgedExpiry,
		"truncated":     token.Token[:len(token.Token)-1],
		"unsigned":      csrfTokenForTest(t, 1),
	} {
		if candidate == token.Token {
			t.Fatalf("%s candidate equals the issued token", name)
		}
		if err := service.ValidateCSRF(ctx, "", candidate); !errors.Is(err, ErrInvalidCSRF) {
			t.Fatalf("ValidateCSRF(%s) error = %v, want ErrInvalidCSRF", name, err)
		}
	}
	if err := service.ValidateCSRF(ctx, "", token.Token); err != nil {
		t.Fatalf("ValidateCSRF(issued) error = %v, want nil", err)
	}

	clock.Advance(anonymousCSRFTokenExpiration)
	if err := service.ValidateCSRF(ctx, "", token.Token); !errors.Is(err, ErrInvalidCSRF) {
		t.Fatalf("ValidateCSRF(expired) error = %v, want ErrInvalidCSRF", err)
	}
}

func createUserForTest(t testing.TB, service *Service, email string) User {
	t.Helper()
	user, err := service.CreateUser(context.Background(), NewUser{Email: email, DisplayName: "Canon Writer"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	return user
}

// signedInForTest creates a user and opens a 12-hour session of it.
func signedInForTest(t testing.TB, service *Service, email string) (Session, string) {
	t.Helper()
	user := createUserForTest(t, service, email)
	session, csrfToken, err := service.CreateSession(context.Background(), NewSession{
		UserID: user.ID, ExpiresAt: service.clock.Now().Add(12 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return session, csrfToken
}

type memoryStoreForTest struct {
	usersByID    map[string]CreateUserInput
	sessionsByID map[string]CreateSessionInput
}

func NewMemoryStoreForTest(t *testing.T) *memoryStoreForTest {
	t.Helper()

	return &memoryStoreForTest{
		usersByID:    make(map[string]CreateUserInput),
		sessionsByID: make(map[string]CreateSessionInput),
	}
}

func (s *memoryStoreForTest) CreateUser(_ context.Context, input CreateUserInput) (UserRecord, error) {
	s.usersByID[input.ID] = input
	return userRecordFromTestUser(input), nil
}

func (s *memoryStoreForTest) CreateSession(_ context.Context, input CreateSessionInput) (SessionRecord, error) {
	s.sessionsByID[input.ID] = input
	return memorySessionRecordForTest(input), nil
}

func (s *memoryStoreForTest) GetActiveSessionByHash(_ context.Context, sessionHash string, now time.Time) (ActiveSessionRecord, error) {
	session, err := s.activeSessionByHash(sessionHash, now)
	if err != nil {
		return ActiveSessionRecord{}, err
	}
	user, ok := s.usersByID[session.UserID]
	if !ok {
		return ActiveSessionRecord{}, ErrSessionNotFound
	}
	return ActiveSessionRecord{
		User:           userRecordFromTestUser(user),
		Session:        memorySessionRecordForTest(session),
		CSRFSecretHash: session.CSRFSecretHash,
	}, nil
}

func (s *memoryStoreForTest) UpdateSessionCSRFByHash(_ context.Context, sessionHash string, now time.Time, csrfSecretHash string) error {
	session, err := s.activeSessionByHash(sessionHash, now)
	if err != nil {
		return err
	}
	session.CSRFSecretHash = csrfSecretHash
	s.sessionsByID[session.ID] = session
	return nil
}

func (s *memoryStoreForTest) activeSessionByHash(sessionHash string, now time.Time) (CreateSessionInput, error) {
	if sessionHash == "" {
		return CreateSessionInput{}, ErrSessionNotFound
	}
	for _, session := range s.sessionsByID {
		if session.SessionHash == sessionHash && session.ExpiresAt.After(now) {
			return session, nil
		}
	}
	return CreateSessionInput{}, ErrSessionNotFound
}

func userRecordFromTestUser(user CreateUserInput) UserRecord {
	return UserRecord{
		ID:            user.ID,
		Email:         user.Email,
		EmailVerified: !user.EmailVerifiedAt.IsZero(),
		DisplayName:   user.DisplayName,
	}
}

func memorySessionRecordForTest(session CreateSessionInput) SessionRecord {
	return SessionRecord{
		ID: session.ID, UserID: session.UserID, ExpiresAt: session.ExpiresAt,
		CreatedAt: session.CreatedAt, Persistent: session.Persistent,
	}
}

func sessionCookieForTest(t *testing.T, index int) string {
	t.Helper()
	return secretValueForTest(t, "session", index)
}

func csrfTokenForTest(t *testing.T, index int) string {
	t.Helper()
	return secretValueForTest(t, "csrf", index)
}

func StaticIDsWithGeneratedSecretsForTest(t *testing.T, userID, sessionID string) staticIDsForTest {
	t.Helper()
	return staticIDsForTest{
		userID: userID, sessionID: sessionID, cookieValue: sessionCookieForTest(t, 1), csrfToken: csrfTokenForTest(t, 1),
	}
}

func secretValueForTest(t *testing.T, label string, index int) string {
	t.Helper()
	seed := t.Name() + ":" + label + ":" + strconv.Itoa(index)
	sum := sha256.Sum256([]byte(seed))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type countingIDsForTest struct {
	next int
}

func (ids *countingIDsForTest) NewUserID() string {
	ids.next++
	return "user-" + strconv.Itoa(ids.next)
}

func (ids *countingIDsForTest) NewSessionID() string {
	ids.next++
	return "session-" + strconv.Itoa(ids.next)
}

func (ids *countingIDsForTest) NewSessionCookieValue() string {
	ids.next++
	return NewSecureIDGenerator().NewSessionCookieValue()
}

func (ids *countingIDsForTest) NewCSRFToken() string {
	ids.next++
	return NewSecureIDGenerator().NewCSRFToken()
}

type staticIDsForTest struct {
	userID      string
	sessionID   string
	cookieValue string
	csrfToken   string
}

func (ids staticIDsForTest) NewUserID() string {
	return ids.userID
}

func (ids staticIDsForTest) NewSessionID() string {
	return ids.sessionID
}

func (ids staticIDsForTest) NewSessionCookieValue() string {
	return ids.cookieValue
}

func (ids staticIDsForTest) NewCSRFToken() string {
	return ids.csrfToken
}

type staticClockForTest struct {
	now time.Time
}

func StaticClockForTest(now time.Time) staticClockForTest {
	return staticClockForTest{now: now}
}

func (clock staticClockForTest) Now() time.Time {
	return clock.now
}

type manualClockForTest struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *manualClockForTest) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *manualClockForTest) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}
