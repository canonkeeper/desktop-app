// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// channelOfTheTest is the control channel as the erasure sees it: the messages the backend wrote to the shell.
type channelOfTheTest struct{ erase int }

func (channel *channelOfTheTest) AskErase() error {
	channel.erase++
	return nil
}

// unlinkedAccount is an app without a link with an account: the erasure has nothing to unlink.
type unlinkedAccount struct{}

func (unlinkedAccount) BeforeErase(context.Context) {}

// windowThatErases is the router of the window over the erasure, with a check of the token of a write as the core's.
func windowThatErases(channel *channelOfTheTest) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Erase:    service.EraseData{Shell: channel, Account: unlinkedAccount{}},
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

func askToErase(router http.Handler, body, cookie string, headers map[string]string) (int, string, string) {
	request := httptest.NewRequest(http.MethodPost, "/desktop/erase", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
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

// specs/2026-11-13-desk-erase.md ER1.2: eraseDesktopData asks the shell once, with the word, under the session of the
// window, its strict Origin and its token; anything else asks nothing.
func TestTheWindowAsksTheShellToEraseOnlyWithTheWord(t *testing.T) {
	channel := &channelOfTheTest{}
	window := windowThatErases(channel)
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
		status, body, _ := askToErase(window, `{"confirm":"стереть"}`, refused.cookie, refused.headers)
		if status != refused.status || !strings.Contains(body, refused.code) {
			t.Errorf("%s = %d %s", refused.name, status, body)
		}
	}
	for _, body := range []string{`{"confirm":"стерет"}`, `{"confirm":""}`, `{}`, `{"confirm":"стереть всё"}`} {
		if status, answer, _ := askToErase(window, body, "cookie-of-the-window", guarded); status != http.StatusBadRequest ||
			!strings.Contains(answer, `"code":"desktop.erase_unconfirmed"`) {
			t.Errorf("%s = %d %s", body, status, answer)
		}
	}
	// A body that is not the object of the operation is refused as such, whatever word it carries.
	for _, body := range []string{`{"confirm":7}`, `{"confirm":"стереть","also":"всё"}`, `стереть`} {
		if status, answer, _ := askToErase(window, body, "cookie-of-the-window", guarded); status != http.StatusBadRequest ||
			!strings.Contains(answer, `"code":"validation.failed"`) {
			t.Errorf("%s = %d %s", body, status, answer)
		}
	}
	if channel.erase != 0 {
		t.Fatalf("a refused request asked the shell %d times", channel.erase)
	}

	status, body, cache := askToErase(window, `{"confirm":"  Стереть "}`, "cookie-of-the-window", guarded)
	if status != http.StatusAccepted || body != "" || cache != "no-store" || channel.erase != 1 {
		t.Fatalf("the word = %d %q (%q); the shell asked %d times", status, body, cache, channel.erase)
	}
}

// The shell that did not hear the request: the window learns the erasure did not begin.
type deafChannel struct{}

func (deafChannel) AskErase() error { return context.Canceled }

func TestAnErasureTheShellDidNotHearIsAnError(t *testing.T) {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Erase:    service.EraseData{Shell: deafChannel{}, Account: unlinkedAccount{}},
		CSRF:     func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	status, body, _ := askToErase(router, `{"confirm":"стереть"}`, "cookie-of-the-window", map[string]string{"Origin": "http://127.0.0.1:1"})
	if status != http.StatusInternalServerError || !strings.Contains(body, "server.error") {
		t.Fatalf("a deaf shell = %d %s", status, body)
	}
}
