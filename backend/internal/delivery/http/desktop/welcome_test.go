// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// welcomeOfTheTest is settings.json as the welcome sees it.
type welcomeOfTheTest struct {
	dismissed    bool
	checkUpdates bool
	writes       int
}

func (settings *welcomeOfTheTest) Welcome() (bool, bool) {
	return !settings.dismissed, settings.checkUpdates
}

func (settings *welcomeOfTheTest) DismissWelcome(checkUpdates bool) error {
	settings.dismissed, settings.checkUpdates = true, checkUpdates
	settings.writes++
	return nil
}

func windowWelcomed(settings *welcomeOfTheTest) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		State: service.ReadAppState{
			Usage: noUsage{}, Backups: noBackups{}, Network: func() []service.NetworkUse { return nil }, Welcome: settings,
		},
		Welcome: service.DismissWelcome{Settings: settings},
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
	return router
}

type noUsage struct{}

func (noUsage) Measure(context.Context) service.DiskUsage { return service.DiskUsage{} }

type noBackups struct{}

func (noBackups) List() ([]service.Backup, error) { return nil, nil }

func welcomeState(t *testing.T, router http.Handler) (bool, bool) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/desktop/state", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var state struct {
		Welcome      *bool `json:"welcome"`
		CheckUpdates *bool `json:"checkUpdates"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil || state.Welcome == nil || state.CheckUpdates == nil {
		t.Fatalf("getDesktopState = %d %s", response.Code, response.Body)
	}
	return *state.Welcome, *state.CheckUpdates
}

func dismiss(router http.Handler, body, cookie string, headers map[string]string) (int, string) {
	request := httptest.NewRequest(http.MethodPost, "/desktop/welcome", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

// specs/2026-11-13-desk-upgrade.md UG5.1: getDesktopState says welcome until the author closed «Добро пожаловать» and the
// choice to check for updates once a day; dismissWelcome takes the choice and marks the welcome as seen, under the
// session of the window, its strict Origin and the token of a write. A body without the choice is refused and marks
// nothing.
func TestTheWelcomeIsShownUntilTheAuthorClosesItAndKeepsTheChoice(t *testing.T) {
	settings := &welcomeOfTheTest{checkUpdates: true}
	router := windowWelcomed(settings)
	if welcome, check := welcomeState(t, router); !welcome || !check {
		t.Fatalf("the first state = welcome %v, checkUpdates %v", welcome, check)
	}
	write := map[string]string{"Origin": "http://127.0.0.1:1", "X-CSRF-Token": "token-of-the-window"}
	for name, refused := range map[string]struct {
		body, cookie string
		headers      map[string]string
		status       int
	}{
		"no session":   {`{"checkUpdates":false}`, "", write, http.StatusUnauthorized},
		"no Origin":    {`{"checkUpdates":false}`, "cookie-of-the-window", map[string]string{"X-CSRF-Token": "token-of-the-window"}, http.StatusForbidden},
		"no token":     {`{"checkUpdates":false}`, "cookie-of-the-window", map[string]string{"Origin": "http://127.0.0.1:1"}, http.StatusForbidden},
		"no choice":    {`{}`, "cookie-of-the-window", write, http.StatusBadRequest},
		"not a choice": {`{"checkUpdates":"no"}`, "cookie-of-the-window", write, http.StatusBadRequest},
		"more":         {`{"checkUpdates":false,"source":"x"}`, "cookie-of-the-window", write, http.StatusBadRequest},
	} {
		if status, body := dismiss(router, refused.body, refused.cookie, refused.headers); status != refused.status {
			t.Fatalf("%s: dismissWelcome = %d %s; want %d", name, status, body, refused.status)
		}
	}
	if settings.writes != 0 {
		t.Fatalf("a refused dismissWelcome wrote %d times", settings.writes)
	}
	if status, body := dismiss(router, `{"checkUpdates":false}`, "cookie-of-the-window", write); status != http.StatusNoContent || body != "" {
		t.Fatalf("dismissWelcome = %d %s", status, body)
	}
	if welcome, check := welcomeState(t, router); welcome || check {
		t.Fatalf("the state after dismissWelcome = welcome %v, checkUpdates %v", welcome, check)
	}
}
