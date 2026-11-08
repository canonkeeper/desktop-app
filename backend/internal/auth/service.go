package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	ErrInvalidCSRF     = errors.New("auth: invalid csrf token")
	// ErrEmailNotVerified closes model work to an actor whose address is not verified.
	ErrEmailNotVerified = errors.New("auth: email not verified")
)

const anonymousCSRFTokenExpiration = 10 * time.Minute

type IDGenerator interface {
	NewUserID() string
	NewSessionID() string
	NewSessionCookieValue() string
	NewCSRFToken() string
}

type Clock interface {
	Now() time.Time
}

// Service is the open core of auth: users, sessions, CSRF and the write proof. Sign-in,
// passwords, mail, impersonation and documents live outside it and use it.
type Service struct {
	store Store
	ids   IDGenerator
	clock Clock
	// anonymousCSRFKey signs pre-session CSRF tokens, so they need no shared
	// cache that one client could flood (SEC-03); it lives as long as the process.
	anonymousCSRFKey []byte
}

func NewService(store Store, ids IDGenerator, clock Clock) *Service {
	service := &Service{
		store:            store,
		ids:              ids,
		clock:            clock,
		anonymousCSRFKey: make([]byte, sha256.Size),
	}
	mustReadRandom(service.anonymousCSRFKey)
	return service
}

// NewUser is a user the core creates without a password; a session its build opens
// (CreateSession) signs it in.
type NewUser struct {
	Email         string
	DisplayName   string
	EmailVerified bool
}

// CreateUser stores a new user; the caller gives a normalized address and a display name.
func (s *Service) CreateUser(ctx context.Context, user NewUser) (User, error) {
	input := CreateUserInput{ID: s.ids.NewUserID(), Email: user.Email, DisplayName: user.DisplayName}
	if user.EmailVerified {
		input.EmailVerifiedAt = s.clock.Now()
	}
	record, err := s.store.CreateUser(ctx, input)
	if err != nil {
		return User{}, err
	}
	return UserFromRecord(record), nil
}

func (s *Service) CurrentUser(ctx context.Context, cookieValue string) (User, error) {
	active, err := s.ActiveSession(ctx, cookieValue)
	if err != nil {
		return User{}, err
	}
	return UserFromRecord(active.User), nil
}

// ActiveSession reads the live session of the cookie with its user; no cookie and a
// revoked, rotated or expired session are ErrUnauthenticated.
func (s *Service) ActiveSession(ctx context.Context, cookieValue string) (ActiveSessionRecord, error) {
	if cookieValue == "" {
		return ActiveSessionRecord{}, ErrUnauthenticated
	}
	active, err := s.store.GetActiveSessionByHash(ctx, HashSecret(cookieValue), s.clock.Now())
	if errors.Is(err, ErrSessionNotFound) {
		return ActiveSessionRecord{}, ErrUnauthenticated
	}
	return active, err
}

func (s *Service) CSRFToken(ctx context.Context, cookieValue string) (CSRFToken, error) {
	now := s.clock.Now()
	token := CSRFToken{
		Token:     s.ids.NewCSRFToken(),
		ExpiresAt: now.Add(anonymousCSRFTokenExpiration),
		Rotation:  "refreshed",
	}
	if cookieValue != "" {
		err := s.store.UpdateSessionCSRFByHash(ctx, HashSecret(cookieValue), now, HashSecret(token.Token))
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, ErrSessionNotFound) {
			return CSRFToken{}, err
		}
		token.ClearSessionCookie = true
	}
	token.Token = s.signAnonymousCSRFToken(token.Token, token.ExpiresAt)
	return token, nil
}

func (s *Service) ValidateCSRF(ctx context.Context, cookieValue, csrfToken string) error {
	if csrfToken == "" {
		return ErrInvalidCSRF
	}
	if cookieValue == "" {
		if s.validAnonymousCSRFToken(csrfToken, s.clock.Now()) {
			return nil
		}
		return ErrInvalidCSRF
	}
	active, err := s.store.GetActiveSessionByHash(ctx, HashSecret(cookieValue), s.clock.Now())
	if errors.Is(err, ErrSessionNotFound) {
		return ErrInvalidCSRF
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(active.CSRFSecretHash), []byte(HashSecret(csrfToken))) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

func (s *Service) AuthenticateWrite(ctx context.Context, cookieValue, csrfToken string) (User, SessionWriteProof, error) {
	if cookieValue == "" {
		return User{}, SessionWriteProof{}, ErrUnauthenticated
	}
	sessionHash := HashSecret(cookieValue)
	active, err := s.store.GetActiveSessionByHash(ctx, sessionHash, s.clock.Now())
	if errors.Is(err, ErrSessionNotFound) {
		return User{}, SessionWriteProof{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, SessionWriteProof{}, err
	}
	if csrfToken == "" {
		return User{}, SessionWriteProof{}, ErrInvalidCSRF
	}
	csrfSecretHash := HashSecret(csrfToken)
	if subtle.ConstantTimeCompare([]byte(active.CSRFSecretHash), []byte(csrfSecretHash)) != 1 {
		return User{}, SessionWriteProof{}, ErrInvalidCSRF
	}
	proof := SessionWriteProof{SessionHash: sessionHash, CSRFSecretHash: csrfSecretHash}
	return UserFromRecord(active.User), proof, nil
}

// signAnonymousCSRFToken binds the random nonce and the expiry to the
// process key: "<nonce>.<expiry ms>.<HMAC>".
func (s *Service) signAnonymousCSRFToken(nonce string, expiresAt time.Time) string {
	payload := nonce + "." + strconv.FormatInt(expiresAt.UnixMilli(), 10)
	return payload + "." + s.anonymousCSRFSignature(payload)
}

func (s *Service) validAnonymousCSRFToken(token string, now time.Time) bool {
	cut := strings.LastIndexByte(token, '.')
	if cut < 0 {
		return false
	}
	payload, signature := token[:cut], token[cut+1:]
	if !hmac.Equal([]byte(signature), []byte(s.anonymousCSRFSignature(payload))) {
		return false
	}
	expiresAt, err := strconv.ParseInt(payload[strings.LastIndexByte(payload, '.')+1:], 10, 64)
	return err == nil && now.Before(time.UnixMilli(expiresAt))
}

func (s *Service) anonymousCSRFSignature(payload string) string {
	mac := hmac.New(sha256.New, s.anonymousCSRFKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
