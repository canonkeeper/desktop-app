package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/ratelimit"
)

// SEC-17 (specs/2026-10-28-security-hardening.md, R4.1, R4.2): the limiter names who asks in the
// request line — the user of a session; the client address only of an anonymous sign-in,
// registration or password reset, never of any other guest request.
func TestRateLimitMiddlewareNamesTheUserOrTheClientOfASignInInTheRequestLine(t *testing.T) {
	const userID = "0b4c2f2e-2d6c-4f43-9f5d-8b8f3c1a2e11"
	for _, tc := range []struct {
		name       string
		method     string
		path       string
		principal  string
		wantUser   any
		wantClient any
	}{
		{name: "signed in", method: http.MethodGet, path: "/api/v1/projects", principal: userID, wantUser: userID},
		{name: "signed in signs in again", method: http.MethodPost, path: "/api/v1/auth/login", principal: userID, wantUser: userID},
		{name: "guest reads", method: http.MethodGet, path: "/api/v1/auth/csrf"},
		{name: "guest reports an error", method: http.MethodPost, path: "/api/v1/client-errors"},
		{name: "guest asks for a letter", method: http.MethodPost, path: "/api/v1/auth/email-verification/confirm"},
		{name: "sign-in", method: http.MethodPost, path: "/api/v1/auth/login", wantClient: "203.0.113.7"},
		{name: "registration", method: http.MethodPost, path: "/api/v1/auth/register", wantClient: "203.0.113.7"},
		{name: "password reset request", method: http.MethodPost, path: "/api/v1/auth/password-reset", wantClient: "203.0.113.7"},
		{name: "password reset", method: http.MethodPost, path: "/api/v1/auth/password-reset/confirm", wantClient: "203.0.113.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{Limit: 10, Window: time.Minute})
			keyFunc := NewRateLimitKeyFunc(RateLimitKeyOptions{PrincipalID: func(*http.Request) string { return tc.principal }})
			handler := RequestLogger(&output)(RateLimitMiddleware(limiter, keyFunc)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})))
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = "127.0.0.1:41000"
			req.Header.Set("X-Real-IP", "203.0.113.7")

			handler.ServeHTTP(httptest.NewRecorder(), req)

			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("log entry is not JSON: %v\n%s", err, output.String())
			}
			if entry["userId"] != tc.wantUser || entry["client"] != tc.wantClient {
				t.Fatalf("userId, client = %v, %v; want %v, %v; entry=%v", entry["userId"], entry["client"], tc.wantUser, tc.wantClient, entry)
			}
		})
	}
}

// A sign-in flood that the limiter turns away still names its address.
func TestRateLimitedSignInKeepsItsClientAddress(t *testing.T) {
	var output bytes.Buffer
	limiter := ratelimit.NewMemoryLimiter(ratelimit.MemoryLimiterConfig{Limit: 1, Window: time.Minute})
	handler := RequestLogger(&output)(RateLimitMiddleware(limiter, NewRateLimitKeyFunc(RateLimitKeyOptions{}))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.9:41000"
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	var last map[string]any
	if len(lines) != 2 || json.Unmarshal(lines[1], &last) != nil {
		t.Fatalf("request lines = %q", output.String())
	}
	if last["status"] != float64(http.StatusTooManyRequests) || last["client"] != "203.0.113.9" {
		t.Fatalf("turned-away sign-in line = %v, want 429 with its client address", last)
	}
}

func TestClientLoggedOperationsAreRegisteredRoutes(t *testing.T) {
	registered := map[string]bool{}
	for _, route := range RouteCatalog() {
		registered[route.OperationID] = true
	}
	for operation := range clientLoggedOperations {
		if !registered[operation] {
			t.Fatalf("operation %q that keeps the client address is not a registered route", operation)
		}
	}
}
