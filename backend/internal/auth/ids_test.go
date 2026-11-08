package auth

import (
	"regexp"
	"testing"
)

func TestSecureIDGeneratorProducesUUIDsAndOpaqueTokens(t *testing.T) {
	ids := NewSecureIDGenerator()
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	userID := ids.NewUserID()
	sessionID := ids.NewSessionID()
	if !uuidPattern.MatchString(userID) {
		t.Fatalf("NewUserID() = %q, want UUIDv4", userID)
	}
	if !uuidPattern.MatchString(sessionID) {
		t.Fatal("NewSessionID() did not return a UUIDv4")
	}
	if userID == sessionID {
		t.Fatal("user and session IDs must be independently generated")
	}

	sessionCookie := ids.NewSessionCookieValue()
	csrfToken := ids.NewCSRFToken()
	if len(sessionCookie) < 32 {
		t.Fatalf("session cookie token length = %d, want at least 32", len(sessionCookie))
	}
	if len(csrfToken) < 32 {
		t.Fatalf("CSRF token length = %d, want at least 32", len(csrfToken))
	}
	if sessionCookie == csrfToken {
		t.Fatal("session cookie token and CSRF token must differ")
	}
}
