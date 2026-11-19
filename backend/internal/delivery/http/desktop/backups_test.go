// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/desktop"

	"github.com/go-chi/chi/v5"
)

// backupsOfTheTest is the removal of the copies: how many times it ran, and the refusal of an update that is installed.
type backupsOfTheTest struct {
	removed  int
	updating bool
}

func (backups *backupsOfTheTest) Execute() error {
	if backups.updating {
		return domain.ErrUpdating
	}
	backups.removed++
	return nil
}

func askToDeleteBackups(router http.Handler, cookie string, headers map[string]string) (int, string, string) {
	request := httptest.NewRequest(http.MethodDelete, "/desktop/backups", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code, response.Body.String(), response.Header().Get("Cache-Control")
}

// specs/2026-11-19-desk-trust.md TW3.5: deleteDesktopBackups removes the copies under the session of the window, its strict
// Origin and its token, and answers 204; while an update is installed it is 409 desktop.updating; anything else removes
// nothing.
func TestTheWindowDeletesTheCopiesUnderItsGuards(t *testing.T) {
	backups := &backupsOfTheTest{}
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Backups:  backups,
		CSRF: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-CSRF-Token") != "token-of-the-window" {
					http.Error(w, "csrf", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
			})
		},
		AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	guarded := map[string]string{"Origin": "http://127.0.0.1:1", "X-CSRF-Token": "token-of-the-window"}
	for _, refused := range []struct {
		name    string
		cookie  string
		headers map[string]string
		status  int
		code    string
	}{
		{"without the session", "", guarded, http.StatusUnauthorized, "auth.session_required"},
		{"with the session of another run", "cookie-of-another-run", guarded, http.StatusUnauthorized, "auth.session_required"},
		{"without the Origin", "cookie-of-the-window", map[string]string{"X-CSRF-Token": "token-of-the-window"}, http.StatusForbidden, ""},
		{"from another origin", "cookie-of-the-window", map[string]string{"Origin": "http://evil.example", "X-CSRF-Token": "token-of-the-window"}, http.StatusForbidden, ""},
		{"without the token", "cookie-of-the-window", map[string]string{"Origin": "http://127.0.0.1:1"}, http.StatusForbidden, ""},
	} {
		if status, body, _ := askToDeleteBackups(router, refused.cookie, refused.headers); status != refused.status || !strings.Contains(body, refused.code) {
			t.Errorf("%s = %d %s", refused.name, status, body)
		}
	}
	if backups.removed != 0 {
		t.Fatalf("a refused request removed the copies %d times", backups.removed)
	}
	backups.updating = true
	if status, body, _ := askToDeleteBackups(router, "cookie-of-the-window", guarded); status != http.StatusConflict ||
		!strings.Contains(body, `"code":"desktop.updating"`) || backups.removed != 0 {
		t.Fatalf("while an update is installed = %d %s, removed %d", status, body, backups.removed)
	}
	backups.updating = false
	status, body, cache := askToDeleteBackups(router, "cookie-of-the-window", guarded)
	if status != http.StatusNoContent || body != "" || cache != "no-store" || backups.removed != 1 {
		t.Fatalf("the removal = %d %q (%q), removed %d", status, body, cache, backups.removed)
	}
}

// A removal that failed is an error the window says, not a success.
type failingBackups struct{}

func (failingBackups) Execute() error { return errors.New("the folder is held") }

func TestARemovalOfTheCopiesThatFailedIsAnError(t *testing.T) {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}}, Backups: failingBackups{},
		CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	if status, body, _ := askToDeleteBackups(router, "cookie-of-the-window", map[string]string{"Origin": "http://127.0.0.1:1"}); status != http.StatusInternalServerError ||
		!strings.Contains(body, "server.error") {
		t.Fatalf("a failed removal = %d %s", status, body)
	}
}
