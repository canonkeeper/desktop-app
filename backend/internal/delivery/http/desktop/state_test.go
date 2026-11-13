// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// stateOf is the state of the app as the use case reads it.
type stateOf struct {
	state service.AppState
	err   error
}

func (read stateOf) Execute(context.Context) (service.AppState, error) { return read.state, read.err }

func askTheState(t *testing.T, read stateOf, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}}, State: read,
		CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/desktop/state", nil)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// specs/2026-11-13-desk-settings.md ST2.1, ST2.2, ST4.2, ST4.3: getDesktopState answers the window its version, the data
// directory as the author sees it, the place it takes — the last known one while it is counted — the copies before an
// update with their time and version and the hosts of the network; without the session of the window it is the 401 of
// the site.
func TestTheWindowReadsTheStateOfItsApp(t *testing.T) {
	state := service.AppState{
		Version: "1.2.0", DataDir: `C:\Users\author\AppData\Local\ru.canonkeeper.desktop\data`,
		Usage: service.DiskUsage{Bytes: 1_288_490_188, Known: true},
		Backups: []service.Backup{
			{Made: time.Date(2026, 11, 12, 9, 15, 0, 250_000_000, time.UTC), Version: "1.1.4"},
			{Made: time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)},
		},
		Network: []service.NetworkUse{{Host: "canonkeeper.ru", Purpose: "связь с аккаунтом"}},
	}
	response := askTheState(t, stateOf{state: state}, "cookie-of-the-window")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("getDesktopState = %d %s, Cache-Control %q", response.Code, response.Body, response.Header().Get("Cache-Control"))
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"version": "1.2.0",
		"dataDir": `C:\Users\author\AppData\Local\ru.canonkeeper.desktop\data`,
		"usage":   map[string]any{"bytes": float64(1_288_490_188), "counting": false},
		"backups": []any{
			map[string]any{"madeAt": "2026-11-12T09:15:00.250Z", "version": "1.1.4"},
			map[string]any{"madeAt": "2026-11-02T08:00:00.000Z", "version": nil},
		},
		"network": []any{map[string]any{"host": "canonkeeper.ru", "purpose": "связь с аккаунтом"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the state = %v\nwant %v", got, want)
	}

	// A first count still going on: no place yet, the mark «считается»; a build that did not name its version says none;
	// no copies and no hosts are empty lists.
	counting := askTheState(t, stateOf{state: service.AppState{DataDir: "/home/author/.local/share/canonkeeper", Usage: service.DiskUsage{Counting: true}}}, "cookie-of-the-window")
	got = nil
	if err := json.Unmarshal(counting.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want = map[string]any{
		"version": nil, "dataDir": "/home/author/.local/share/canonkeeper",
		"usage": map[string]any{"bytes": nil, "counting": true}, "backups": []any{}, "network": []any{},
	}
	if counting.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("a state being counted = %d %v", counting.Code, got)
	}

	for _, cookie := range []string{"", "cookie-of-another-run"} {
		refused := askTheState(t, stateOf{state: state}, cookie)
		var problem struct {
			Code string `json:"code"`
		}
		if refused.Code != http.StatusUnauthorized || json.Unmarshal(refused.Body.Bytes(), &problem) != nil || problem.Code != "auth.session_required" {
			t.Fatalf("without the session of the window = %d %s", refused.Code, refused.Body)
		}
	}
	if failed := askTheState(t, stateOf{err: errors.New("backups unreadable")}, "cookie-of-the-window"); failed.Code != http.StatusInternalServerError {
		t.Fatalf("a failed read = %d", failed.Code)
	}
}
