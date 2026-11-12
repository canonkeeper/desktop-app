// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"

	"github.com/go-chi/chi/v5"
)

// windowSessions is the session of one window: its cookie names the local author.
type windowSessions struct {
	cookie string
	user   auth.User
	err    error
}

func (sessions windowSessions) CurrentUser(_ context.Context, cookieValue string) (auth.User, error) {
	if sessions.err != nil {
		return auth.User{}, sessions.err
	}
	if cookieValue == "" || cookieValue != sessions.cookie {
		return auth.User{}, auth.ErrUnauthenticated
	}
	return sessions.user, nil
}

func askWhoIsTheAuthor(t *testing.T, sessions windowSessions, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	Handlers{
		Sessions: sessions, CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// specs/2026-11-12-desk-window.md WN1.1: getCurrentUser of the desktop answers the local author of the session of the window in
// the form User — verified, nothing to accept — and a request without the session the same 401 as the site.
func TestTheWindowKnowsItsAuthor(t *testing.T) {
	created := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	sessions := windowSessions{cookie: "cookie-of-the-window", user: auth.User{
		ID: "6a1f0c52-0000-4000-8000-000000000001", Email: "local@desktop.invalid", DisplayName: "Автор", EmailVerified: true, CreatedAt: created,
	}}

	response := askWhoIsTheAuthor(t, sessions, "cookie-of-the-window")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("getCurrentUser = %d, Cache-Control %q", response.Code, response.Header().Get("Cache-Control"))
	}
	var user map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": "6a1f0c52-0000-4000-8000-000000000001", "email": "local@desktop.invalid", "displayName": "Автор",
		"emailVerified": true, "documentsAccepted": true, "avatarUrl": nil, "createdAt": "2026-11-12T09:30:00.000Z",
	}
	if len(user) != len(want) {
		t.Fatalf("the author = %v, want %v", user, want)
	}
	for field, value := range want {
		if user[field] != value {
			t.Fatalf("the author has %s = %v, want %v", field, user[field], value)
		}
	}

	for _, cookie := range []string{"", "cookie-of-another-run"} {
		refused := askWhoIsTheAuthor(t, sessions, cookie)
		var problem struct {
			Code string `json:"code"`
		}
		if refused.Code != http.StatusUnauthorized || json.Unmarshal(refused.Body.Bytes(), &problem) != nil ||
			problem.Code != "auth.session_required" || refused.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("without the session of the window = %d %s", refused.Code, refused.Body)
		}
	}

	if failed := askWhoIsTheAuthor(t, windowSessions{err: errors.New("database down")}, "cookie-of-the-window"); failed.Code != http.StatusInternalServerError {
		t.Fatalf("a failed read = %d", failed.Code)
	}
}
