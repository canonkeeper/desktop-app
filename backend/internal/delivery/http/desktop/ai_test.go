// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	domain "canonkeeper/backend/internal/domain/aisettings"
	desktopdomain "canonkeeper/backend/internal/domain/desktop"
	service "canonkeeper/backend/internal/services/aisettings"

	"github.com/go-chi/chi/v5"
)

type settingsOfTheTest struct{ settings domain.Settings }

func (kept *settingsOfTheTest) AI() domain.Settings { return kept.settings }
func (kept *settingsOfTheTest) KeepAI(settings domain.Settings) error {
	kept.settings = settings
	return nil
}

type keysOfTheTest struct {
	values map[string]string
	refuse bool
}

func (keys *keysOfTheTest) Has(id string) bool                { _, ok := keys.values[id]; return ok }
func (keys *keysOfTheTest) Store() desktopdomain.SecretsStore { return desktopdomain.SecretsInSystem }
func (keys *keysOfTheTest) Save(_ context.Context, id, value string) error {
	if keys.refuse {
		return desktopdomain.ErrSecretNotKept
	}
	keys.values[id] = value
	return nil
}
func (keys *keysOfTheTest) Delete(_ context.Context, id string) error {
	if keys.refuse {
		return desktopdomain.ErrSecretNotKept
	}
	delete(keys.values, id)
	return nil
}

// windowOfTheAI is the router of the window over the use cases of «Свой ИИ» on fakes of settings.json and of the shell.
func windowOfTheAI(settings *settingsOfTheTest, keys *keysOfTheTest) http.Handler {
	return windowOfTheAIWith(service.Read{Settings: settings, Keys: keys}, settings, keys)
}

func windowOfTheAIWith(read service.Read, settings *settingsOfTheTest, keys *keysOfTheTest) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		AI: AI{
			Read: read, Update: service.Update{Settings: settings, Read: read},
			SetKey: service.SetKey{Keys: keys}, DeleteKey: service.DeleteKey{Keys: keys},
		},
		CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	return router
}

func askTheWindow(router http.Handler, method, path, body, cookie string) (int, map[string]any, string) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Origin", "http://127.0.0.1:1")
	request.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var answer map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &answer)
	return response.Code, answer, response.Header().Get("Cache-Control")
}

// specs/2026-11-13-desk-secrets.md SE2.1–SE2.5: the window reads and replaces the settings of «Свой ИИ», hands over the
// key and has it removed; the key is in no answer; a field that is not valid is refused by its JSON pointer and changes
// nothing; a shell that did not keep the key is ai.key_not_saved; without the session of the window, the 401 of the site.
func TestTheWindowSetsUpItsOwnAI(t *testing.T) {
	settings := &settingsOfTheTest{settings: domain.None()}
	keys := &keysOfTheTest{values: map[string]string{}}
	window := windowOfTheAI(settings, keys)

	status, answer, cache := askTheWindow(window, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window")
	want := map[string]any{"settings": map[string]any{"source": "none", "own": nil}, "keySaved": false, "secretsStore": "system", "appliesAfterAnswer": false, "check": nil, "moneyStopSagas": float64(0), "account": nil}
	if status != http.StatusOK || cache != "no-store" || !reflect.DeepEqual(answer, want) {
		t.Fatalf("getAiSettings = %d %v (%q)", status, answer, cache)
	}

	status, answer, cache = askTheWindow(window, http.MethodPut, "/desktop/ai",
		`{"source":"own","own":{"preset":"deepseek","chatModel":"deepseek-chat","advanced":{"priceInPerMillion":0.27,"parallel":8}}}`, "cookie-of-the-window")
	want = map[string]any{
		"settings": map[string]any{"source": "own", "own": map[string]any{
			"preset": "deepseek", "baseUrl": "https://api.deepseek.com/v1", "protocol": "chat", "chatModel": "deepseek-chat", "cardsModel": "",
			"advanced": map[string]any{
				"contextWindow": float64(128000), "maxOutput": float64(16000), "reasoning": "medium", "parallel": float64(8),
				"priceInPerMillion": 0.27, "priceOutPerMillion": nil, "currency": "USD", "priceCachedInPerMillion": nil,
			},
		}},
		"keySaved": false, "secretsStore": "system", "appliesAfterAnswer": false, "check": nil, "moneyStopSagas": float64(0), "account": nil,
	}
	if status != http.StatusOK || cache != "no-store" || !reflect.DeepEqual(answer, want) {
		t.Fatalf("updateAiSettings = %d %v", status, answer)
	}

	// specs/2026-11-13-desk-consent.md CN1.1: the currency and the price of the cache a write leaves out are kept; null
	// clears the price of the cache.
	advanced := func(body string) map[string]any {
		t.Helper()
		status, answer, _ := askTheWindow(window, http.MethodPut, "/desktop/ai", body, "cookie-of-the-window")
		if status != http.StatusOK {
			t.Fatalf("updateAiSettings %s = %d %v", body, status, answer)
		}
		return answer["settings"].(map[string]any)["own"].(map[string]any)["advanced"].(map[string]any)
	}
	if got := advanced(`{"source":"own","own":{"preset":"deepseek","advanced":{"currency":"RUB","priceCachedInPerMillion":0.5}}}`); got["currency"] != "RUB" || got["priceCachedInPerMillion"] != 0.5 {
		t.Fatalf("the currency and the price of the cache = %v", got)
	}
	if got := advanced(`{"source":"own","own":{"preset":"deepseek","advanced":{"priceInPerMillion":30}}}`); got["currency"] != "RUB" || got["priceCachedInPerMillion"] != 0.5 {
		t.Fatalf("a write without them = %v", got)
	}
	if got := advanced(`{"source":"own","own":{"preset":"deepseek","advanced":{"priceCachedInPerMillion":null}}}`); got["currency"] != "RUB" || got["priceCachedInPerMillion"] != nil {
		t.Fatalf("a write that clears the price of the cache = %v", got)
	}
	if status, _, _ := askTheWindow(window, http.MethodPut, "/desktop/ai", `{"source":"own","own":{"preset":"deepseek","advanced":{"currency":"GBP"}}}`, "cookie-of-the-window"); status != http.StatusBadRequest {
		t.Fatalf("an unknown currency = %d", status)
	}

	for body, pointer := range map[string]string{
		`{"source":"own","own":{"preset":"custom","baseUrl":"https://user:secret@gateway.example.test/v1"}}`: "/own/baseUrl",
		`{"source":"own","own":{"preset":"openai","advanced":{"contextWindow":128000.5}}}`:                   "/own/advanced/contextWindow",
		`{"source":"own","own":{"preset":"openai","advanced":{"priceOutPerMillion":1e2}}}`:                   "/own/advanced/priceOutPerMillion",
		`{"source":"own"}`: "/own",
	} {
		status, answer, _ = askTheWindow(window, http.MethodPut, "/desktop/ai", body, "cookie-of-the-window")
		issues, _ := answer["errors"].([]any)
		if status != http.StatusBadRequest || answer["code"] != "ai.settings_invalid" || len(issues) != 1 ||
			issues[0].(map[string]any)["path"] != pointer || strings.Contains(answer["detail"].(string), "secret") {
			t.Fatalf("%s = %d %v; want the field %s", body, status, answer, pointer)
		}
	}
	if status, answer, _ = askTheWindow(window, http.MethodPut, "/desktop/ai", `{"source":"own","future":1}`, "cookie-of-the-window"); status != http.StatusBadRequest || answer["code"] != "validation.failed" {
		t.Fatalf("an unknown field = %d %v", status, answer)
	}
	if settings.settings.Own == nil || settings.settings.Own.Preset != domain.PresetDeepSeek {
		t.Fatalf("a refused write changed the settings: %+v", settings.settings)
	}

	const key = "key-test-key-of-the-north-pass-7f3a"
	for _, body := range []string{`{"key":"sk test"}`, `{"key":7}`} {
		if status, answer, _ = askTheWindow(window, http.MethodPut, "/desktop/ai/key", body, "cookie-of-the-window"); status != http.StatusBadRequest || answer["code"] != "ai.key_invalid" {
			t.Fatalf("%s = %d %v", body, status, answer)
		}
	}
	if status, _, cache = askTheWindow(window, http.MethodPut, "/desktop/ai/key", `{"key":"`+key+`"}`, "cookie-of-the-window"); status != http.StatusNoContent || cache != "no-store" {
		t.Fatalf("setAiKey = %d", status)
	}
	status, answer, _ = askTheWindow(window, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window")
	if encoded, _ := json.Marshal(answer); status != http.StatusOK || answer["keySaved"] != true || strings.Contains(string(encoded), "north-pass") {
		t.Fatalf("getAiSettings after the key = %d %s", status, encoded)
	}
	keys.refuse = true
	if status, answer, _ = askTheWindow(window, http.MethodPut, "/desktop/ai/key", `{"key":"key-test-another"}`, "cookie-of-the-window"); status != http.StatusServiceUnavailable || answer["code"] != "ai.key_not_saved" {
		t.Fatalf("a key the shell did not keep = %d %v", status, answer)
	}
	if status, answer, _ = askTheWindow(window, http.MethodDelete, "/desktop/ai/key", "", "cookie-of-the-window"); status != http.StatusServiceUnavailable || answer["code"] != "ai.key_not_saved" {
		t.Fatalf("a removal the shell refused = %d %v", status, answer)
	}
	keys.refuse = false
	if status, _, _ = askTheWindow(window, http.MethodDelete, "/desktop/ai/key", "", "cookie-of-the-window"); status != http.StatusNoContent || keys.Has(desktopdomain.SecretOwnAI) {
		t.Fatalf("deleteAiKey = %d", status)
	}

	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/desktop/ai", ""}, {http.MethodPut, "/desktop/ai", `{"source":"none"}`},
		{http.MethodPut, "/desktop/ai/key", `{"key":"` + key + `"}`}, {http.MethodDelete, "/desktop/ai/key", ""},
	} {
		for _, cookie := range []string{"", "cookie-of-another-run"} {
			if status, answer, _ = askTheWindow(window, request.method, request.path, request.body, cookie); status != http.StatusUnauthorized || answer["code"] != "auth.session_required" {
				t.Fatalf("%s %s without the session = %d %v", request.method, request.path, status, answer)
			}
		}
	}
	if keys.Has(desktopdomain.SecretOwnAI) || settings.settings.Source != domain.SourceOwn {
		t.Fatal("a request without the session changed something")
	}
}

// stopsOfTheTest is the port of the sagas under a sum in money: the count of the service of the consent, or its failure.
type stopsOfTheTest struct {
	count int
	err   error
}

func (stops stopsOfTheTest) Count(context.Context) (int, error) { return stops.count, stops.err }

// specs/2026-11-13-desk-consent-web.md RW3.1: getAiSettings and the answer of updateAiSettings say how many sagas of the
// author a change of the currency or the removal of the prices would pause — by the port of the service of the consent; a
// failure of the count is the 500 of the window, not a zero.
func TestTheWindowReadsTheSagasUnderASumInMoney(t *testing.T) {
	settings := &settingsOfTheTest{settings: domain.None()}
	keys := &keysOfTheTest{values: map[string]string{}}
	window := windowOfTheAIWith(service.Read{Settings: settings, Keys: keys, Stops: stopsOfTheTest{count: 2}}, settings, keys)
	status, answer, _ := askTheWindow(window, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window")
	if status != http.StatusOK || answer["moneyStopSagas"] != float64(2) {
		t.Fatalf("getAiSettings = %d %v", status, answer)
	}
	status, answer, _ = askTheWindow(window, http.MethodPut, "/desktop/ai", `{"source":"own","own":{"preset":"deepseek"}}`, "cookie-of-the-window")
	if status != http.StatusOK || answer["moneyStopSagas"] != float64(2) {
		t.Fatalf("updateAiSettings = %d %v", status, answer)
	}
	broken := windowOfTheAIWith(service.Read{Settings: settings, Keys: keys, Stops: stopsOfTheTest{err: errors.New("down")}}, settings, keys)
	if status, answer, _ = askTheWindow(broken, http.MethodGet, "/desktop/ai", "", "cookie-of-the-window"); status != http.StatusInternalServerError || answer["code"] != "server.error" {
		t.Fatalf("a failed count = %d %v", status, answer)
	}
}
