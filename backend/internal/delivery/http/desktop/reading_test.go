// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	aisettings "canonkeeper/backend/internal/domain/aisettings"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
	service "canonkeeper/backend/internal/services/readingconsent"

	"github.com/go-chi/chi/v5"
)

// readingSettingsOfTheTest is «Свой ИИ» of the reading: connected, with or without a calibration.
type readingSettingsOfTheTest struct {
	settings    aisettings.Settings
	calibration float64
	connected   bool
}

func (settings readingSettingsOfTheTest) AI() aisettings.Settings { return settings.settings }
func (settings readingSettingsOfTheTest) Calibration() (float64, bool) {
	return settings.calibration, settings.calibration > 0
}
func (settings readingSettingsOfTheTest) Connected(context.Context) bool { return settings.connected }
func (readingSettingsOfTheTest) SiteHost() string                        { return "site.example.test" }
func (readingSettingsOfTheTest) AccountLimits(context.Context) (service.AccountLimits, bool) {
	return service.AccountLimits{}, false
}

// ownersOfTheTest own the saga «own» of the author «author».
type ownersOfTheTest struct{}

func (ownersOfTheTest) Owned(_ context.Context, actorID, projectID string) error {
	if actorID != "author" || projectID != "own" {
		return service.ErrNotFound
	}
	return nil
}

type sagaOfTheTest struct{ toRead service.ToRead }

func (saga sagaOfTheTest) Inputs(context.Context, string, []service.Chapter) (int64, error) {
	return 0, nil
}

func (saga sagaOfTheTest) ToRead(context.Context, string, bool) (service.ToRead, error) {
	return saga.toRead, nil
}

func windowOfTheReading(settings readingSettingsOfTheTest, toRead service.ToRead) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Reading: Reading{
			Estimate: service.GetEstimate{Settings: settings, Owners: ownersOfTheTest{}, Saga: sagaOfTheTest{toRead: toRead}},
		},
		CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)
	return router
}

func ownAt(t *testing.T, draft aisettings.OwnDraft) aisettings.Settings {
	t.Helper()
	settings, err := aisettings.Draft{Source: "own", Own: &draft}.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

// specs/2026-11-13-desk-consent.md CN1.3, CN1.6: the estimate of a saga with five chapters without cards and two with them
// counts five and names the stop to offer, the chapters of a sample and the host; a local model says «на этом компьютере»; a
// saga whose chapters are read counts only the names and the layer that wait; another saga is 404, no session 401, no helper
// helper.not_connected.
func TestTheWindowEstimatesTheReading(t *testing.T) {
	rubles, deepseek, reader := "RUB", "deepseek-chat", "deepseek-reasoner"
	in, out := "30", "120"
	settings := readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &deepseek, CardsModel: &reader,
		Advanced: aisettings.AdvancedDraft{Currency: &rubles, PriceIn: &in, PriceOut: &out},
	})}
	toRead := service.ToRead{Books: 1, Carded: 2, CardedCharacters: 20_000}
	for range 5 {
		toRead.Unread = append(toRead.Unread, service.Chapter{ID: "chapter", Hash: "hash", Characters: 10_000})
	}
	window := windowOfTheReading(settings, toRead)
	status, answer, cache := askTheWindow(window, http.MethodGet, "/projects/own/reading/estimate", "", "cookie-of-the-window")
	if status != http.StatusOK || cache != "no-store" {
		t.Fatalf("getReadingEstimate = %d %v", status, answer)
	}
	if answer["chapters"] != float64(5) || answer["unit"] != "RUB" || answer["host"] != "api.deepseek.com" || answer["local"] != false ||
		answer["kind"] != "rough" || answer["sampleChapters"] != float64(3) || answer["amount"] == nil || answer["suggestedStop"] == nil {
		t.Fatalf("the estimate = %v", answer)
	}
	if amount, stop := answer["amount"].(float64), answer["suggestedStop"].(float64); stop < amount {
		t.Fatalf("the stop %v is below the estimate %v", stop, amount)
	}

	ollama := readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{Preset: "ollama", ChatModel: &deepseek, CardsModel: &reader})}
	status, answer, _ = askTheWindow(windowOfTheReading(ollama, toRead), http.MethodGet, "/projects/own/reading/estimate", "", "cookie-of-the-window")
	if status != http.StatusOK || answer["local"] != true || answer["unit"] != "tokens" || answer["amount"] != nil {
		t.Fatalf("the estimate of a local model = %d %v", status, answer)
	}

	read := service.ToRead{Books: 1, Carded: 7, CardedCharacters: 70_000, NamesWait: true, ThreadsWait: true}
	status, answer, _ = askTheWindow(windowOfTheReading(settings, read), http.MethodGet, "/projects/own/reading/estimate", "", "cookie-of-the-window")
	if status != http.StatusOK || answer["chapters"] != float64(0) || answer["namesWait"] != true || answer["threadsWait"] != true ||
		answer["inputTokens"].(float64) <= 0 || answer["sampleChapters"] != float64(0) {
		t.Fatalf("the estimate of the names and the layer = %d %v", status, answer)
	}

	if status, answer, _ = askTheWindow(window, http.MethodGet, "/projects/other/reading/estimate", "", "cookie-of-the-window"); status != http.StatusNotFound {
		t.Fatalf("another saga = %d %v", status, answer)
	}
	if status, answer, _ = askTheWindow(window, http.MethodGet, "/projects/own/reading/estimate", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("without the session = %d %v", status, answer)
	}
	// specs/2026-11-13-desk-account-ai.md AI3.4: the estimate of the account whose limits the site has not told says null.
	account := readingSettingsOfTheTest{connected: true, settings: aisettings.Settings{Source: aisettings.SourceAccount, Account: &aisettings.Account{
		ChatModel: "site-chat", CardsModel: "site-cards", ContextWindow: 400_000, CompactLimit: 300_000,
	}}}
	status, answer, _ = askTheWindow(windowOfTheReading(account, toRead), http.MethodGet, "/projects/own/reading/estimate", "", "cookie-of-the-window")
	estimated, _ := answer["account"].(map[string]any)
	if status != http.StatusOK || estimated == nil || estimated["charactersLimit"] != nil || estimated["workLimit"] != nil || estimated["endsFirst"] != nil {
		t.Fatalf("the estimate of the account without the limits = %d %v", status, answer)
	}
	if _, kept := estimated["charactersLimit"]; !kept {
		t.Fatal("the estimate of the account leaves out charactersLimit")
	}
	settings.connected = false
	if status, answer, _ = askTheWindow(windowOfTheReading(settings, toRead), http.MethodGet, "/projects/own/reading/estimate", "", "cookie-of-the-window"); status != http.StatusConflict || answer["code"] != "helper.not_connected" {
		t.Fatalf("without the helper = %d %v", status, answer)
	}
}

// consentStoreOfTheTest keeps the consent of the saga «own» as the repository does: a change runs over it whole.
type consentStoreOfTheTest struct {
	consent *readingdomain.Consent
	retried bool
}

func (store *consentStoreOfTheTest) Consent(context.Context, string) (*readingdomain.Consent, error) {
	return store.consent, nil
}

func (store *consentStoreOfTheTest) ChangeConsent(_ context.Context, _ string, _ time.Time, change func(*readingdomain.Consent) (readingdomain.Consent, error), retry bool) (readingdomain.Consent, error) {
	kept, err := change(store.consent)
	if err == nil {
		store.consent, store.retried = &kept, retry
	}
	return kept, err
}

type workOfTheTest struct{ queued, canceled int }

func (work *workOfTheTest) Queue(context.Context, string) error          { work.queued++; return nil }
func (work *workOfTheTest) Cancel(context.Context, string, string) error { work.canceled++; return nil }

// specs/2026-11-13-desk-consent.md CN3.2: the owner gives the consent with a sum — the Jobs of the reading are queued —,
// pauses it — they are canceled —, continues it «ещё на X» over what was spent with the count kept; a sum not above what
// was spent is refused by its field; a pause without a consent is 409; another saga 404; a write without the guards of the
// window 403.
func TestTheWindowGivesAndPausesTheReading(t *testing.T) {
	rubles, deepseek, reader, in, out := "RUB", "deepseek-chat", "deepseek-reasoner", "30", "120"
	settings := readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &deepseek, CardsModel: &reader,
		Advanced: aisettings.AdvancedDraft{Currency: &rubles, PriceIn: &in, PriceOut: &out},
	})}
	store, work := &consentStoreOfTheTest{}, &workOfTheTest{}
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Reading: Reading{
			Consent:    service.GetConsent{Settings: settings, Owners: ownersOfTheTest{}, Consents: store},
			SetConsent: service.SetConsent{Settings: settings, Owners: ownersOfTheTest{}, Store: store, Work: work, Now: func() time.Time { return now }},
			Unit:       func() readingdomain.Unit { return readingdomain.Unit(aisettings.CurrencyRUB) },
		},
		CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
	}.Mount(router)

	status, answer, _ := askTheWindow(router, http.MethodGet, "/projects/own/reading/consent", "", "cookie-of-the-window")
	if status != http.StatusOK || answer["state"] != "none" || answer["unit"] != "RUB" || answer["source"] != "own" {
		t.Fatalf("the consent before = %d %v", status, answer)
	}
	if status, answer, _ = askTheWindow(router, http.MethodPut, "/projects/own/reading/consent", `{"state":"paused"}`, "cookie-of-the-window"); status != http.StatusConflict || answer["code"] != "reading.no_consent" {
		t.Fatalf("a pause without a consent = %d %v", status, answer)
	}
	status, answer, _ = askTheWindow(router, http.MethodPut, "/projects/own/reading/consent", `{"state":"allowed","stopAt":1000000000}`, "cookie-of-the-window")
	// The answer of the write names who reads the books as the read of the consent does (specs/2026-11-13-desk-account-ai.md B2).
	if status != http.StatusOK || answer["state"] != "allowed" || answer["host"] != "api.deepseek.com" || answer["stopAt"] != float64(1_000_000_000) ||
		answer["step"] != float64(1_000_000_000) || answer["source"] != "own" || work.queued != 1 {
		t.Fatalf("the consent = %d %v, queued %d", status, answer, work.queued)
	}
	store.consent.Spent.Micros, store.consent.State, store.consent.Pause = 1_000_000_000, readingdomain.StatePaused, readingdomain.PauseSum
	if status, answer, _ = askTheWindow(router, http.MethodPut, "/projects/own/reading/consent", `{"state":"allowed","stopAt":900000000}`, "cookie-of-the-window"); status != http.StatusBadRequest || answer["code"] != "reading.invalid" {
		t.Fatalf("a sum below what was spent = %d %v", status, answer)
	}
	status, answer, _ = askTheWindow(router, http.MethodPut, "/projects/own/reading/consent", `{"state":"allowed","continueBy":500000000,"retryUnread":true}`, "cookie-of-the-window")
	spent, _ := answer["spent"].(map[string]any)
	if status != http.StatusOK || answer["stopAt"] != float64(1_500_000_000) || answer["step"] != float64(500_000_000) || spent["amount"] != float64(1_000_000_000) ||
		answer["since"] != "2026-11-13T09:00:00Z" || !store.retried || work.queued != 2 {
		t.Fatalf("«ещё на X» = %d %v", status, answer)
	}
	if status, answer, _ = askTheWindow(router, http.MethodPut, "/projects/own/reading/consent", `{"state":"paused"}`, "cookie-of-the-window"); status != http.StatusOK || answer["pause"] != "author" || answer["source"] != "own" || work.canceled != 1 {
		t.Fatalf("the pause of the author = %d %v", status, answer)
	}
	if status, answer, _ = askTheWindow(router, http.MethodPut, "/projects/other/reading/consent", `{"state":"allowed"}`, "cookie-of-the-window"); status != http.StatusNotFound {
		t.Fatalf("another saga = %d %v", status, answer)
	}
	request := httptest.NewRequest(http.MethodPut, "/projects/own/reading/consent", strings.NewReader(`{"state":"allowed"}`))
	request.Header.Set("Origin", "http://evil.example.test")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("a write of another origin = %d", response.Code)
	}
}
