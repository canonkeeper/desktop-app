package authstore

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	*storedb.DB
}

func New(db *storedb.DB) *Store {
	return &Store{DB: db}
}

// CreateUser stores a user without a password: no hash matches the empty one, so only a
// session its build opens signs the user in.
func (s *Store) CreateUser(ctx context.Context, input auth.CreateUserInput) (auth.UserRecord, error) {
	userID, err := storedb.ParseUUID(input.ID)
	if err != nil {
		return auth.UserRecord{}, err
	}
	verifiedAt := pgtype.Timestamptz{}
	if !input.EmailVerifiedAt.IsZero() {
		verifiedAt = storedb.Timestamptz(input.EmailVerifiedAt)
	}
	row, err := sqlc.New(s.Pool).CreateUser(ctx, sqlc.CreateUserParams{
		ID: userID, Email: input.Email, DisplayName: input.DisplayName, EmailVerifiedAt: verifiedAt,
	})
	if err != nil {
		return auth.UserRecord{}, err
	}
	return UserRecordFromCreateUser(row), nil
}

func (s *Store) CreateSession(ctx context.Context, input auth.CreateSessionInput) (auth.SessionRecord, error) {
	return InsertSession(ctx, sqlc.New(s.Pool), input)
}

// InsertSession stores a session through queries, so a caller can open it inside its own
// transaction.
func InsertSession(ctx context.Context, queries *sqlc.Queries, input auth.CreateSessionInput) (auth.SessionRecord, error) {
	sessionID, err := storedb.ParseUUID(input.ID)
	if err != nil {
		return auth.SessionRecord{}, err
	}
	userID, err := storedb.ParseUUID(input.UserID)
	if err != nil {
		return auth.SessionRecord{}, err
	}
	impersonator, err := optionalUUID(input.ImpersonatorUserID)
	if err != nil {
		return auth.SessionRecord{}, err
	}
	session, err := queries.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:                 sessionID,
		UserID:             userID,
		SessionHash:        input.SessionHash,
		CsrfSecretHash:     input.CSRFSecretHash,
		ExpiresAt:          storedb.Timestamptz(input.ExpiresAt),
		CreatedAt:          storedb.Timestamptz(input.CreatedAt),
		Persistent:         input.Persistent,
		ImpersonatorUserID: impersonator,
	})
	if err != nil {
		return auth.SessionRecord{}, err
	}
	return SessionRecord(session.ID, session.UserID, session.ExpiresAt, session.CreatedAt, session.Persistent), nil
}

func (s *Store) GetActiveSessionByHash(ctx context.Context, sessionHash string, now time.Time) (auth.ActiveSessionRecord, error) {
	row, err := sqlc.New(s.Pool).GetActiveSessionByHash(ctx, sqlc.GetActiveSessionByHashParams{
		SessionHash: sessionHash,
		Now:         storedb.Timestamptz(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ActiveSessionRecord{}, auth.ErrSessionNotFound
	}
	if err != nil {
		return auth.ActiveSessionRecord{}, err
	}
	return activeSessionRecord(row), nil
}

func (s *Store) UpdateSessionCSRFByHash(ctx context.Context, sessionHash string, now time.Time, csrfSecretHash string) error {
	rowsAffected, err := sqlc.New(s.Pool).UpdateSessionCSRFByHash(ctx, sqlc.UpdateSessionCSRFByHashParams{
		CsrfSecretHash: csrfSecretHash,
		SessionHash:    sessionHash,
		Now:            storedb.Timestamptz(now),
	})
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return auth.ErrSessionNotFound
	}
	return nil
}

// UserIDByEmail finds the live user of a normalized address: the desktop finds its local author by it
// (specs/2026-11-07-desktop-app.md DA2.2).
func (s *Store) UserIDByEmail(ctx context.Context, email string) (string, bool, error) {
	id, err := sqlc.New(s.Pool).GetUserIDByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return storedb.UUIDString(id), true, nil
}

// RevokeUserSessions revokes every session of the user.
func (s *Store) RevokeUserSessions(ctx context.Context, userID string, revokedAt time.Time) error {
	id, err := storedb.ParseUUID(userID)
	if err != nil {
		return auth.ErrUnauthenticated
	}
	return sqlc.New(s.Pool).RevokeUserSessions(ctx, sqlc.RevokeUserSessionsParams{
		RevokedAt: storedb.Timestamptz(revokedAt), UserID: id,
	})
}

func UserRecordFromCreateUser(row sqlc.CreateUserRow) auth.UserRecord {
	return auth.UserRecord{
		ID:            row.ID.String(),
		Email:         row.Email,
		EmailVerified: row.EmailVerifiedAt.Valid,
		DisplayName:   row.DisplayName,
		CreatedAt:     row.CreatedAt.Time,
		TermsVersion:  row.TermsVersion.String, ConsentVersion: row.PersonalDataConsentVersion.String,
	}
}

func SessionRecord(id, userID pgtype.UUID, expiresAt, createdAt pgtype.Timestamptz, persistent bool) auth.SessionRecord {
	return auth.SessionRecord{
		ID:         id.String(),
		UserID:     userID.String(),
		ExpiresAt:  expiresAt.Time,
		CreatedAt:  createdAt.Time,
		Persistent: persistent,
	}
}

func activeSessionRecord(row sqlc.GetActiveSessionByHashRow) auth.ActiveSessionRecord {
	return auth.ActiveSessionRecord{
		User: auth.UserRecord{
			ID:            row.UserID.String(),
			Email:         row.UserEmail,
			EmailVerified: row.UserEmailVerifiedAt.Valid,
			DisplayName:   row.UserDisplayName,
			CreatedAt:     row.UserCreatedAt.Time,
			TermsVersion:  row.UserTermsVersion.String, ConsentVersion: row.UserPersonalDataConsentVersion.String,
		},
		Session:        SessionRecord(row.SessionID, row.SessionUserID, row.SessionExpiresAt, row.SessionCreatedAt, row.SessionPersistent),
		CSRFSecretHash: row.SessionCsrfSecretHash,
	}
}

// optionalUUID maps an empty id to NULL.
func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return storedb.ParseUUID(value)
}
