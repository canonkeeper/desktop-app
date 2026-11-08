package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func newHandlersForTest(service *Service) Handlers {
	return NewHandlers(service, false)
}

func TestGetCSRFTokenReturnsNoStoreBootstrap(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	ids := StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1")
	handler := newHandlersForTest(NewService(
		NewMemoryStoreForTest(t),
		ids,
		StaticClockForTest(now),
	))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	rec := httptest.NewRecorder()

	handler.GetCSRFToken(rec, req)

	result := rec.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	if result.Header.Get("cache-control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", result.Header.Get("cache-control"))
	}
	var body csrfTokenResponseForTest
	if err := json.NewDecoder(result.Body).Decode(&body); err != nil {
		t.Fatalf("decode CSRF response: %v", err)
	}
	if !strings.HasPrefix(body.CSRFToken, ids.csrfToken+".") {
		t.Fatal("CSRF response did not use generated token")
	}
	if !body.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("CSRF expiresAt = %s, want %s", body.ExpiresAt, now.Add(10*time.Minute))
	}
	if body.Rotation != "refreshed" {
		t.Fatalf("rotation = %q, want refreshed", body.Rotation)
	}
}

func TestGetCSRFTokenClearsStaleSessionCookie(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	)
	router := chi.NewRouter()
	newHandlersForTest(service).Mount(router)
	csrfReq := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
	csrfReq.AddCookie(&http.Cookie{Name: "ck_session", Value: sessionCookieForTest(t, 2)})
	csrfRec := httptest.NewRecorder()

	router.ServeHTTP(csrfRec, csrfReq)

	csrfResult := csrfRec.Result()
	defer csrfResult.Body.Close()
	if csrfResult.StatusCode != http.StatusOK {
		t.Fatalf("csrf status = %d, want %d", csrfResult.StatusCode, http.StatusOK)
	}
	clearedCookie := findCookieForTest(t, csrfResult.Cookies(), "ck_session")
	if clearedCookie.Value != "" {
		t.Fatal("stale session cookie clear response must use an empty value")
	}
	if clearedCookie.MaxAge >= 0 {
		t.Fatalf("stale session cookie MaxAge = %d, want negative", clearedCookie.MaxAge)
	}
	var body csrfTokenResponseForTest
	if err := json.NewDecoder(csrfResult.Body).Decode(&body); err != nil {
		t.Fatalf("decode CSRF response: %v", err)
	}
	if err := service.ValidateCSRF(t.Context(), "", body.CSRFToken); err != nil {
		t.Fatalf("token after a stale cookie is not an anonymous token: %v", err)
	}
}

func TestGetCSRFTokenWithSessionAuthorizesSessionMutation(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		NewSequenceIDsForTest(
			[]string{"user-1"},
			[]string{"session-1"},
			[]string{sessionCookieForTest(t, 1)},
			[]string{csrfTokenForTest(t, 1), csrfTokenForTest(t, 2)},
		),
		StaticClockForTest(now),
	)
	session, _ := signedInForTest(t, service, "writer@example.com")
	handler := newHandlersForTest(service)
	csrfReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	csrfReq.AddCookie(&http.Cookie{Name: "ck_session", Value: session.CookieValue})
	csrfRec := httptest.NewRecorder()

	handler.GetCSRFToken(csrfRec, csrfReq)

	csrfResult := csrfRec.Result()
	defer csrfResult.Body.Close()
	var body csrfTokenResponseForTest
	if err := json.NewDecoder(csrfResult.Body).Decode(&body); err != nil {
		t.Fatalf("decode CSRF response: %v", err)
	}
	next := handler.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	mutationReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	mutationReq.AddCookie(&http.Cookie{Name: "ck_session", Value: session.CookieValue})
	mutationReq.Header.Set("X-CSRF-Token", body.CSRFToken)
	mutationRec := httptest.NewRecorder()

	next.ServeHTTP(mutationRec, mutationReq)

	if mutationRec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", mutationRec.Code, http.StatusNoContent)
	}
}

func TestAuthCSRFMiddlewareRejectsMismatchedSessionToken(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	)
	session, _ := signedInForTest(t, service, "writer@example.com")
	handler := newHandlersForTest(service)
	nextCalled := false
	next := handler.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "ck_session", Value: session.CookieValue})
	req.Header.Set("X-CSRF-Token", csrfTokenForTest(t, 2))
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called with mismatched CSRF token")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestAuthCSRFMiddlewareRejectsAnonymousArbitraryToken(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	handler := newHandlersForTest(NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	))
	next := handler.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("X-CSRF-Token", csrfTokenForTest(t, 2))
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAuthCSRFMiddlewareAllowsAnonymousBootstrapToken(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	handler := newHandlersForTest(NewService(
		NewMemoryStoreForTest(t),
		StaticIDsWithGeneratedSecretsForTest(t, "user-1", "session-1"),
		StaticClockForTest(now),
	))
	csrfReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	csrfRec := httptest.NewRecorder()
	handler.GetCSRFToken(csrfRec, csrfReq)
	csrfResult := csrfRec.Result()
	defer csrfResult.Body.Close()
	var body csrfTokenResponseForTest
	if err := json.NewDecoder(csrfResult.Body).Decode(&body); err != nil {
		t.Fatalf("decode CSRF response: %v", err)
	}
	next := handler.CSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("X-CSRF-Token", body.CSRFToken)
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestSessionCookieOutlivesTheBrowserOnlyForAPersistentSession(t *testing.T) {
	expiresAt := time.Date(2026, 12, 8, 12, 0, 0, 0, time.UTC)
	handler := NewHandlers(nil, true)

	persistent := handler.SessionCookie(Session{CookieValue: "cookie", ExpiresAt: expiresAt, Persistent: true})
	if persistent.Name != SessionCookieName || persistent.Value != "cookie" || persistent.Path != "/" || !persistent.HttpOnly ||
		!persistent.Secure || persistent.SameSite != http.SameSiteLaxMode || !persistent.Expires.Equal(expiresAt) {
		t.Fatalf("persistent cookie = %+v", persistent)
	}
	browser := handler.SessionCookie(Session{CookieValue: "cookie", ExpiresAt: expiresAt})
	if !browser.Expires.IsZero() || browser.MaxAge != 0 {
		t.Fatalf("browser-session cookie = %+v, want no Expires or Max-Age", browser)
	}
	cleared := handler.ClearedSessionCookie()
	if cleared.Name != SessionCookieName || cleared.Value != "" || cleared.MaxAge >= 0 || !cleared.Secure || !cleared.HttpOnly {
		t.Fatalf("cleared cookie = %+v", cleared)
	}
}

func TestMarkedImpersonationChildCookieRequiresExactPairOnProductRequests(t *testing.T) {
	childCookie := "imp_" + sessionCookieForTest(t, 2)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: childCookie})
	if got := SessionCookieValue(request); got != "" {
		t.Fatalf("marked child without pair = %q, want unauthenticated", got)
	}

	request.AddCookie(&http.Cookie{
		Name:  ImpersonatorCookieName,
		Value: EncodeImpersonationPair(sessionCookieForTest(t, 1), childCookie),
	})
	if got := SessionCookieValue(request); got != childCookie {
		t.Fatalf("marked child with exact pair = %q, want %q", got, childCookie)
	}
}

type csrfTokenResponseForTest struct {
	CSRFToken string    `json:"csrfToken"`
	ExpiresAt time.Time `json:"expiresAt"`
	Rotation  string    `json:"rotation"`
}

func findCookieForTest(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %q", name)
	return nil
}

type sequenceIDsForTest struct {
	userIDs      []string
	sessionIDs   []string
	cookieValues []string
	csrfTokens   []string
}

func NewSequenceIDsForTest(userIDs, sessionIDs, cookieValues, csrfTokens []string) *sequenceIDsForTest {
	return &sequenceIDsForTest{
		userIDs:      slices.Clone(userIDs),
		sessionIDs:   slices.Clone(sessionIDs),
		cookieValues: slices.Clone(cookieValues),
		csrfTokens:   slices.Clone(csrfTokens),
	}
}

func (ids *sequenceIDsForTest) NewUserID() string {
	return popFirstForTest(&ids.userIDs)
}

func (ids *sequenceIDsForTest) NewSessionID() string {
	return popFirstForTest(&ids.sessionIDs)
}

func (ids *sequenceIDsForTest) NewSessionCookieValue() string {
	return popFirstForTest(&ids.cookieValues)
}

func (ids *sequenceIDsForTest) NewCSRFToken() string {
	return popFirstForTest(&ids.csrfTokens)
}

func popFirstForTest(values *[]string) string {
	value := (*values)[0]
	*values = (*values)[1:]
	return value
}
