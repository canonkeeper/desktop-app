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
	domain "canonkeeper/backend/internal/domain/desktop"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// updatesOfTheTest is an update of the app that a phase holds.
type updatesOfTheTest struct {
	update domain.AppUpdate
	asked  []string
}

func (updates *updatesOfTheTest) Update() domain.AppUpdate { return updates.update }

func (updates *updatesOfTheTest) command(name string, allowed bool) error {
	if !allowed {
		return domain.ErrUpdateState
	}
	updates.asked = append(updates.asked, name)
	return nil
}

func (updates *updatesOfTheTest) Check(context.Context) error {
	return updates.command("check", domain.MayCheck(updates.update.Phase, updates.update.Kind))
}

func (updates *updatesOfTheTest) Install(context.Context) error {
	return updates.command("install", domain.MayInstall(updates.update))
}

func (updates *updatesOfTheTest) Cancel(context.Context) error {
	return updates.command("cancel", domain.MayCancel(updates.update.Phase))
}

func (updates *updatesOfTheTest) KeepDaily(daily bool) error {
	updates.update.Daily = daily
	updates.asked = append(updates.asked, "daily")
	return nil
}

func windowUpdated(updates *updatesOfTheTest) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Updates: Updates{
			Read: service.ReadAppUpdate{Updates: updates}, Check: service.CheckAppUpdate{Updates: updates},
			Install: service.InstallAppUpdate{Updates: updates}, Cancel: service.CancelAppUpdate{Updates: updates},
			SetDaily: service.SetAppUpdateDaily{Updates: updates},
		},
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

func updateRequest(router http.Handler, method, path, body, cookie string, headers map[string]string) (int, string) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
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

// specs/2026-11-13-desk-updates.md UB1.7: getAppUpdate to the session of the window; the writes behind its strict Origin,
// the token of a write and the session; an operation the phase does not allow is 409 desktop.update_state.
func TestTheUpdateOfTheAppIsReadAndAskedOnlyByTheWindowInItsPhase(t *testing.T) {
	updates := &updatesOfTheTest{update: domain.AppUpdate{
		Version: "1.2.0", Daily: true, Kind: domain.UpdateDeb, Phase: domain.UpdateIdle, Found: domain.UpdateAvailable,
		Available: &domain.UpdateRelease{Version: "1.3.0", Notes: "Что нового"},
	}}
	router := windowUpdated(updates)
	window := "cookie-of-the-window"
	if status, _ := updateRequest(router, http.MethodGet, "/desktop/update", "", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("getAppUpdate without the session = %d", status)
	}
	status, body := updateRequest(router, http.MethodGet, "/desktop/update", "", window, nil)
	var read map[string]any
	if status != http.StatusOK || json.Unmarshal([]byte(body), &read) != nil {
		t.Fatalf("getAppUpdate = %d %s", status, body)
	}
	want := map[string]any{
		"version": "1.2.0", "daily": true, "kind": "deb", "phase": "idle", "found": "available",
		"available": map[string]any{"version": "1.3.0", "notes": "Что нового"}, "percent": nil, "refusal": nil, "package": nil, "checkedAt": nil,
	}
	if encoded, expected := mustJSON(read), mustJSON(want); encoded != expected {
		t.Fatalf("getAppUpdate = %s; want %s", encoded, expected)
	}
	write := map[string]string{"Origin": "http://127.0.0.1:1", "X-CSRF-Token": "token-of-the-window"}
	for _, path := range []string{"/desktop/update/check", "/desktop/update/install", "/desktop/update/cancel"} {
		for name, refused := range map[string]struct {
			cookie  string
			headers map[string]string
			status  int
		}{
			"no session": {"", write, http.StatusUnauthorized},
			"no Origin":  {window, map[string]string{"X-CSRF-Token": "token-of-the-window"}, http.StatusForbidden},
			"no token":   {window, map[string]string{"Origin": "http://127.0.0.1:1"}, http.StatusForbidden},
		} {
			if status, body := updateRequest(router, http.MethodPost, path, "", refused.cookie, refused.headers); status != refused.status {
				t.Fatalf("%s %s = %d %s; want %d", path, name, status, body, refused.status)
			}
		}
	}
	if len(updates.asked) != 0 {
		t.Fatalf("a refused write asked %v", updates.asked)
	}
	// Nothing to cancel while a version is only found.
	if status, body := updateRequest(router, http.MethodPost, "/desktop/update/cancel", "", window, write); status != http.StatusConflict || !strings.Contains(body, "desktop.update_state") {
		t.Fatalf("cancelAppUpdate of nothing = %d %s", status, body)
	}
	if status, body := updateRequest(router, http.MethodPost, "/desktop/update/install", "", window, write); status != http.StatusOK {
		t.Fatalf("installAppUpdate = %d %s", status, body)
	}
	updates.update.Phase = domain.UpdateInstalling
	for _, path := range []string{"/desktop/update/check", "/desktop/update/install", "/desktop/update/cancel"} {
		if status, body := updateRequest(router, http.MethodPost, path, "", window, write); status != http.StatusConflict || !strings.Contains(body, "desktop.update_state") {
			t.Fatalf("%s while installing = %d %s", path, status, body)
		}
	}
	// The daily check, on or off.
	if status, body := updateRequest(router, http.MethodPut, "/desktop/update/settings", `{"daily":"no"}`, window, write); status != http.StatusBadRequest {
		t.Fatalf("updateAppUpdateSettings with a wrong value = %d %s", status, body)
	}
	if status, body := updateRequest(router, http.MethodPut, "/desktop/update/settings", `{"daily":false}`, window, write); status != http.StatusOK || !strings.Contains(body, `"daily":false`) {
		t.Fatalf("updateAppUpdateSettings = %d %s", status, body)
	}
	if strings.Join(updates.asked, ",") != "install,daily" {
		t.Fatalf("the window asked %v", updates.asked)
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
