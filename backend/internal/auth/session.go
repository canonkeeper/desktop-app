package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// NewSession is a session the core opens for a known user; the caller decides how long
// it lives.
type NewSession struct {
	UserID     string
	ExpiresAt  time.Time
	Persistent bool
	// ImpersonatorUserID names the administrator who opens the session on behalf of
	// UserID: its cookie is a child cookie that works only next to its pair (child_session.go).
	ImpersonatorUserID string
}

// CreateSession opens a session of a known user and answers it with its first CSRF token.
func (s *Service) CreateSession(ctx context.Context, session NewSession) (Session, string, error) {
	input, cookieValue, csrfToken := s.PrepareSession(session.UserID, session.ExpiresAt)
	input.Persistent = session.Persistent
	input.ImpersonatorUserID = session.ImpersonatorUserID
	if session.ImpersonatorUserID != "" {
		cookieValue = impersonationChildCookiePrefix + cookieValue
		input.SessionHash = HashSecret(cookieValue)
	}

	record, err := s.store.CreateSession(ctx, input)
	if err != nil {
		return Session{}, "", err
	}

	return SessionFromRecord(record, cookieValue), csrfToken, nil
}

// PrepareSession makes the secrets of a new session of userID for a caller that stores
// it in its own transaction; only their hashes go into the input.
func (s *Service) PrepareSession(userID string, expiresAt time.Time) (CreateSessionInput, string, string) {
	cookieValue := s.ids.NewSessionCookieValue()
	csrfToken := s.ids.NewCSRFToken()
	now := s.clock.Now()

	return CreateSessionInput{
		ID:             s.ids.NewSessionID(),
		UserID:         userID,
		SessionHash:    HashSecret(cookieValue),
		CSRFSecretHash: HashSecret(csrfToken),
		ExpiresAt:      expiresAt,
		CreatedAt:      now,
	}, cookieValue, csrfToken
}

func SessionFromRecord(record SessionRecord, cookieValue string) Session {
	return Session{
		ID:          record.ID,
		CookieValue: cookieValue,
		ExpiresAt:   record.ExpiresAt,
		Persistent:  record.Persistent,
	}
}

// HashSecret is the stored form of a session cookie, a CSRF token and an account link.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
