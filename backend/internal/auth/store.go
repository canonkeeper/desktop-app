package auth

import (
	"context"
	"errors"
	"time"
)

var ErrSessionNotFound = errors.New("auth: session not found")

// Store is the persistence of users and sessions the core needs; sign-in, passwords,
// account links and documents keep their own store outside the core.
type Store interface {
	CreateUser(context.Context, CreateUserInput) (UserRecord, error)
	CreateSession(context.Context, CreateSessionInput) (SessionRecord, error)
	GetActiveSessionByHash(context.Context, string, time.Time) (ActiveSessionRecord, error)
	UpdateSessionCSRFByHash(context.Context, string, time.Time, string) error
}

// CreateUserInput is a user the core creates: no password, so only a session its build
// opens signs it in. A zero EmailVerifiedAt leaves the address unverified.
type CreateUserInput struct {
	ID              string
	Email           string
	DisplayName     string
	EmailVerifiedAt time.Time
}

type UserRecord struct {
	ID            string
	Email         string
	EmailVerified bool
	DisplayName   string
	CreatedAt     time.Time
	// TermsVersion and ConsentVersion are the editions of the site's documents the user
	// accepted; the core only carries them from the users row.
	TermsVersion   string
	ConsentVersion string
}

type CreateSessionInput struct {
	ID             string
	UserID         string
	SessionHash    string
	CSRFSecretHash string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	Persistent     bool
	// ImpersonatorUserID names the administrator who opened this session on behalf of
	// UserID; empty for the user's own sign-in (impersonation journal, UC5).
	ImpersonatorUserID string
}

type SessionRecord struct {
	ID         string
	UserID     string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	Persistent bool
}

type ActiveSessionRecord struct {
	User           UserRecord
	Session        SessionRecord
	CSRFSecretHash string
}

func UserFromRecord(record UserRecord) User {
	return User{
		ID:            record.ID,
		Email:         record.Email,
		EmailVerified: record.EmailVerified,
		DisplayName:   record.DisplayName,
		CreatedAt:     record.CreatedAt,
	}
}
