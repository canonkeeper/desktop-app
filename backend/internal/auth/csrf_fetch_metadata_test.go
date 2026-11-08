package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// SEC-15 (specs/2026-10-28-security-hardening.md, R2.3): a page of another site cannot change the
// CSRF secret of a session by sending the browser to GET /auth/csrf; the own site, a typed address
// and a client without Fetch Metadata get a token as before.
func TestGetCSRFTokenRefusesARequestAnotherSiteStarted(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	service := NewService(
		NewMemoryStoreForTest(t),
		NewSequenceIDsForTest(
			[]string{"user-1"},
			[]string{"session-1"},
			[]string{sessionCookieForTest(t, 1)},
			[]string{csrfTokenForTest(t, 1), csrfTokenForTest(t, 2), csrfTokenForTest(t, 3), csrfTokenForTest(t, 4), csrfTokenForTest(t, 5)},
		),
		StaticClockForTest(now),
	)
	session, _ := signedInForTest(t, service, "writer@example.com")
	router := chi.NewRouter()
	newHandlersForTest(service).Mount(router)
	getToken := func(site string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/auth/csrf", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.CookieValue})
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	own := getToken("same-origin")
	if own.Code != http.StatusOK {
		t.Fatalf("same-origin status = %d, want 200; body: %s", own.Code, own.Body.String())
	}
	var body csrfTokenResponseForTest
	if err := json.NewDecoder(own.Body).Decode(&body); err != nil {
		t.Fatalf("decode CSRF response: %v", err)
	}
	for _, site := range []string{"cross-site", "same-site", "Cross-Site"} {
		refused := getToken(site)
		if refused.Code != http.StatusForbidden || !json.Valid(refused.Body.Bytes()) {
			t.Fatalf("%s status = %d, want 403; body: %s", site, refused.Code, refused.Body.String())
		}
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(refused.Body.Bytes(), &problem)
		if problem.Code != "origin.invalid" {
			t.Fatalf("%s problem code = %q, want origin.invalid", site, problem.Code)
		}
	}
	if err := service.ValidateCSRF(t.Context(), session.CookieValue, body.CSRFToken); err != nil {
		t.Fatalf("the token of the open tab stopped working after requests of another site: %v", err)
	}
	for _, site := range []string{"none", ""} {
		if rec := getToken(site); rec.Code != http.StatusOK {
			t.Fatalf("Sec-Fetch-Site %q status = %d, want 200", site, rec.Code)
		}
	}
}
