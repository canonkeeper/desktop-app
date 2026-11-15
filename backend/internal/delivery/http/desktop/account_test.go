// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/accountlink"
	service "canonkeeper/backend/internal/services/accountlink"
	desktopservice "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// startOfTheTest records the intent of the starts it was asked.
type startOfTheTest struct{ intents []bool }

func (start *startOfTheTest) Execute(_ context.Context, chooseHelper bool) (service.State, error) {
	start.intents = append(start.intents, chooseHelper)
	return service.State{
		Phase:   domain.PhaseWaiting,
		Waiting: &domain.Waiting{Address: "https://site.example.test/link?state=x", Expires: time.Date(2026, 11, 15, 12, 10, 0, 0, time.UTC)},
	}, nil
}

type readOfTheTest struct{ state service.State }

func (read readOfTheTest) Execute(context.Context, bool) service.State { return read.state }

func windowLinked(start *startOfTheTest, read service.State) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		State: desktopservice.ReadAppState{
			Usage: noUsage{}, Backups: noBackups{}, Network: func() []desktopservice.NetworkUse { return nil },
		},
		Account: Account{Start: start, Read: readOfTheTest{state: read}},
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

func startLinkWith(router http.Handler, body string) (int, string) {
	request := httptest.NewRequest(http.MethodPost, "/desktop/account/link", strings.NewReader(body))
	// The window and the tests of the process send the type of JSON with an empty body too.
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:1")
	request.Header.Set("X-CSRF-Token", "token-of-the-window")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

// specs/2026-11-15-desk-account-link-web.md AW3.2: startAccountLink takes the intent to choose the helper of the account in
// its optional body — without a body there is none —, and a body that is not its object is refused and starts nothing.
func TestTheStartOfALinkTakesTheIntentOfTheAuthor(t *testing.T) {
	start := &startOfTheTest{}
	router := windowLinked(start, service.State{})
	for _, body := range []string{``, `{"chooseHelper":true}`, `{"chooseHelper":false}`} {
		if status, answer := startLinkWith(router, body); status != http.StatusOK || !strings.Contains(answer, `"state":"waiting"`) {
			t.Fatalf("%q: startAccountLink = %d %s", body, status, answer)
		}
	}
	for _, body := range []string{`{}`, `{"chooseHelper":"yes"}`, `{"chooseHelper":true,"more":1}`, `[]`} {
		if status, answer := startLinkWith(router, body); status != http.StatusBadRequest {
			t.Fatalf("%q: startAccountLink = %d %s", body, status, answer)
		}
	}
	if len(start.intents) != 3 || start.intents[0] || !start.intents[1] || start.intents[2] {
		t.Fatalf("the starts heard the intents %v", start.intents)
	}
}

// AW1.6: getAccount says a link that ran out as failed with the reason expired.
func TestALinkThatRanOutIsSaidExpired(t *testing.T) {
	router := windowLinked(&startOfTheTest{}, service.State{Phase: domain.PhaseFailed, Failure: domain.FailedExpired})
	request := httptest.NewRequest(http.MethodGet, "/desktop/account", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var state struct {
		State   string  `json:"state"`
		Failure *string `json:"failure"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil || state.State != "failed" || state.Failure == nil || *state.Failure != "expired" {
		t.Fatalf("getAccount = %d %s", response.Code, response.Body)
	}
}

type finishOfTheTest struct {
	outcome service.Outcome
	err     error
}

func (finish finishOfTheTest) Execute(context.Context, string, string) (service.Outcome, error) {
	return finish.outcome, finish.err
}

// AW1.6, specs/2026-11-13-desk-account.md AC1.2: the pages of the return that did not link send the author back to the app
// to link it again — they name no store (the app says where the link was not kept, by passStore) and no button (after a
// failure the app says «Связать снова», not «Связать с аккаунтом»).
func TestThePagesOfAReturnThatDidNotLinkSendTheAuthorBackToTheApp(t *testing.T) {
	cases := []struct {
		name   string
		finish finishOfTheTest
		status int
		words  string
	}{
		{"a pass not kept", finishOfTheTest{outcome: service.NotSaved}, http.StatusServiceUnavailable,
			"Связь не сохранилась на этом компьютере. Вернитесь в CanonKeeper и свяжите приложение ещё раз"},
		{"the exchange failed", finishOfTheTest{outcome: service.ExchangeFailed}, http.StatusBadGateway,
			"Связать не получилось. Вернитесь в CanonKeeper и свяжите приложение ещё раз"},
		{"the return failed", finishOfTheTest{err: errors.New("the shell left")}, http.StatusInternalServerError,
			"Связать не получилось. Вернитесь в CanonKeeper и свяжите приложение ещё раз"},
		{"a state no link waits for", finishOfTheTest{err: domain.ErrStateInvalid}, http.StatusForbidden,
			"Эта ссылка связи уже не действует. Вернитесь в CanonKeeper и свяжите приложение ещё раз"},
	}
	for _, page := range cases {
		callback := LinkCallback{Finish: page.finish}
		response := httptest.NewRecorder()
		callback.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/desktop/link/callback?code=c&state=s", nil))
		body := response.Body.String()
		if response.Code != page.status || !strings.Contains(body, page.words) ||
			strings.Contains(body, "хранилище") || strings.Contains(body, "Связать с аккаунтом") || strings.Contains(body, "нажмите") {
			t.Fatalf("%s: the page of the return = %d %s", page.name, response.Code, body)
		}
	}
}
