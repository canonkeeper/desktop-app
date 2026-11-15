// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/desktop"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

type accessOfTheTest struct{ state service.ProgramAccessState }

func (access *accessOfTheTest) Get(context.Context) (service.ProgramAccessState, error) { return access.state, nil }
func (access *accessOfTheTest) Update(_ context.Context, enabled bool) (service.ProgramAccessState, error) {
	access.state.Enabled, access.state.Open = enabled, enabled
	access.state.Ever = access.state.Ever || enabled
	return access.state, nil
}

type extensionOfTheTest struct{}

func (extensionOfTheTest) Execute(context.Context) (string, []byte, error) {
	return "CanonKeeper-1.4.0.mcpb", []byte("PK the package"), nil
}

func windowOfThePrograms(access ProgramAccess, extension ClaudeDesktopExtension) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Programs: Programs{Access: access, Extension: extension},
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

func askWindow(router http.Handler, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	if csrf {
		request.Header.Set("Origin", "http://127.0.0.1:1")
		request.Header.Set("X-CSRF-Token", "token-of-the-window")
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// specs/2026-11-15-desk-mcp.md MC3.1–MC3.3: getProgramAccess tells the reading, its commands of the system, the programs and the
// last reading; updateProgramAccess turns it on under the strict Origin and the token of the window; the file of the extension
// goes as an attachment; a computer without it is 404.
func TestTheOperationsOfTheReadingByThePrograms(t *testing.T) {
	commands := domain.Commands(domain.Bridge{Program: "/usr/bin/canonkeeper-mcp"}, "/home/author/данные", false)
	at := time.Date(2026, 11, 13, 11, 5, 0, 0, time.UTC)
	access := &accessOfTheTest{state: service.ProgramAccessState{
		Commands: &commands, Programs: []service.ProgramSeen{{Name: "Claude Code", At: at, Matches: false}}, LastRead: &service.ProgramSeen{Name: "Codex", At: at},
	}}
	router := windowOfThePrograms(access, extensionOfTheTest{})
	response := askWindow(router, http.MethodGet, "/desktop/programs", "", false)
	var state map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil || state["system"] != "linux" || state["enabled"] != false ||
		state["commands"].(map[string]any)["desktopFile"] == nil || state["lastRead"].(map[string]any)["name"] != "Codex" ||
		state["programs"].([]any)[0].(map[string]any)["matches"] != false || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("getProgramAccess: %d %s", response.Code, response.Body.String())
	}
	if response := askWindow(router, http.MethodPut, "/desktop/programs", `{"enabled":true}`, false); response.Code != http.StatusForbidden {
		t.Fatalf("updateProgramAccess without the window: %d", response.Code)
	}
	if response := askWindow(router, http.MethodPut, "/desktop/programs", `{}`, true); response.Code != http.StatusBadRequest {
		t.Fatalf("updateProgramAccess without the choice: %d", response.Code)
	}
	response = askWindow(router, http.MethodPut, "/desktop/programs", `{"enabled":true}`, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":true`) || !strings.Contains(response.Body.String(), `"open":true`) {
		t.Fatalf("updateProgramAccess: %d %s", response.Code, response.Body.String())
	}
	response = askWindow(router, http.MethodGet, "/desktop/programs/claude-desktop-extension", "", false)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" ||
		!strings.Contains(response.Header().Get("Content-Disposition"), "CanonKeeper-1.4.0.mcpb") || response.Body.String() != "PK the package" {
		t.Fatalf("the extension: %d %v", response.Code, response.Header())
	}
	none := windowOfThePrograms(access, service.ClaudeDesktopExtension{Windows: false})
	if response := askWindow(none, http.MethodGet, "/desktop/programs/claude-desktop-extension", "", false); response.Code != http.StatusNotFound {
		t.Fatalf("no extension on Linux: %d", response.Code)
	}
}
