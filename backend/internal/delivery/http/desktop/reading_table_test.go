// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	aisettings "canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/canon"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
	indexingservice "canonkeeper/backend/internal/services/indexing"
	service "canonkeeper/backend/internal/services/readingconsent"

	"github.com/go-chi/chi/v5"
)

// field reads a value of an answer by its path of keys: "spent.amount", "chapters.0.place".
func field(answer map[string]any, path string) (any, bool) {
	var current any = answer
	for _, key := range strings.Split(path, ".") {
		switch value := current.(type) {
		case map[string]any:
			next, ok := value[key]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			if key != "0" || len(value) == 0 {
				return nil, false
			}
			current = value[0]
		default:
			return nil, false
		}
	}
	return current, true
}

// countsOfTheTable is the projection of the status as the store gives it: the counts of the reading always, and — while
// chapters are left — the preparation with what the reading waits for and «спросить можно уже сейчас» by the indexed text.
type countsOfTheTable struct{ read, total int }

func (counts countsOfTheTable) GetStatus(_ context.Context, input indexingservice.GetStatusInput) (canon.IndexingStatusResponse, error) {
	status := canon.IndexingStatusResponse{Counts: &canon.IndexingPreparation{ChaptersRead: counts.read, ChaptersTotal: counts.total}}
	if counts.read < counts.total {
		status.Preparation = &canon.IndexingPreparation{ChaptersRead: counts.read, ChaptersTotal: counts.total, Askable: true, Waiting: input.Waiting}
	}
	return status, nil
}

type paceOfTheTable int

func (pace paceOfTheTable) CardsSince(context.Context, string, time.Time) (int, error) {
	return int(pace), nil
}

type sampleStoreOfTheTable struct{ job *service.SampleJob }

func (store sampleStoreOfTheTable) LastSample(context.Context, string) (*service.SampleJob, error) {
	return store.job, nil
}
func (store sampleStoreOfTheTable) QueueSample(context.Context, string, string, []service.Chapter, string, time.Time) (string, error) {
	return "", nil
}
func (store sampleStoreOfTheTable) SampleRequests(context.Context, string) ([]service.SampleRequest, error) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	return []service.SampleRequest{{At: start, Duration: 240 * time.Second, Host: "api.deepseek.com", Model: "deepseek-reasoner", Estimated: true,
		Cost: &service.SampleCost{Micros: 4_000_000, Currency: "RUB"}}}, nil
}

type sampleCardsOfTheTable struct{ done int }

func (cards sampleCardsOfTheTable) Cards(_ context.Context, _ string, chapters []service.Chapter) ([]service.SampleCard, error) {
	var shown []service.SampleCard
	for _, chapter := range chapters[:min(cards.done, len(chapters))] {
		shown = append(shown, service.SampleCard{ChapterID: chapter.ID, Place: "Книга 1, гл. 1 „Туманный берег“", Summary: "Ирна возвращается",
			Events: []string{"Возвращение Ирны"}, People: []string{"Ирна"}, Characters: 2_000})
	}
	return shown, nil
}

// specs/2026-11-13-desk-consent.md CN4.1: every word, number and button of the boards 44·27, 44·28 and of the table Data
// comes from a field the handlers of the slice answer — the operations of the reading and the status of the saga — in its
// state; the page counts nothing. Each row of the table of the spec is a row here.
func TestEveryWordOfTheBoardsHasItsField(t *testing.T) {
	rubles, deepseek, reader, in, out := "RUB", "deepseek-chat", "deepseek-reasoner", "30", "120"
	priced := readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{
		Preset: "deepseek", ChatModel: &deepseek, CardsModel: &reader,
		Advanced: aisettings.AdvancedDraft{Currency: &rubles, PriceIn: &in, PriceOut: &out},
	})}
	plain := readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{Preset: "deepseek", ChatModel: &deepseek, CardsModel: &reader})}
	local := readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{Preset: "ollama", ChatModel: &deepseek, CardsModel: &reader})}
	unread := make([]service.Chapter, 612)
	for i := range unread {
		unread[i] = service.Chapter{ID: "c", Hash: "h", Characters: 10_000}
	}
	stop, step := int64(1_000_000_000), int64(1_000_000_000)
	since := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	consentOf := func(state readingdomain.State, pause readingdomain.Pause, unit readingdomain.Unit, spent int64) *readingdomain.Consent {
		return &readingdomain.Consent{
			Source: readingdomain.SourceOwn, Host: "api.deepseek.com", State: state, Pause: pause, Unit: unit, StopAt: &stop, Step: &step,
			Since: since, ResumedAt: since, Spent: readingdomain.Spent{Micros: spent, Tokens: aisettings.Tokens{Input: spent / 2, Output: spent / 2}, Estimated: true},
		}
	}
	window := func(settings readingSettingsOfTheTest, toRead service.ToRead, consent *readingdomain.Consent, sample *service.SampleJob, done int) http.Handler {
		store := &consentStoreOfTheTest{consent: consent}
		samples := service.Samples{
			Settings: settings, Owners: ownersOfTheTest{}, Consents: store, Saga: sagaOfTheTest{toRead: toRead},
			Store: sampleStoreOfTheTable{job: sample}, Cards: sampleCardsOfTheTable{done: done}, Now: time.Now,
		}
		router := chi.NewRouter()
		Handlers{
			Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
			Reading: Reading{
				Estimate: service.GetEstimate{Settings: settings, Owners: ownersOfTheTest{}, Saga: sagaOfTheTest{toRead: toRead}, Samples: samples},
				Consent:  service.GetConsent{Settings: settings, Owners: ownersOfTheTest{}, Consents: store},
				Sample:   samples,
				Unit:     func() readingdomain.Unit { return readingdomain.UnitOf(settings.settings.Own.Advanced) },
			},
			CSRF: func(next http.Handler) http.Handler { return next }, AllowedOrigins: []string{"http://127.0.0.1:1"},
		}.Mount(router)
		return router
	}
	status := func(settings readingSettingsOfTheTest, toRead service.ToRead, consent *readingdomain.Consent, read int) map[string]any {
		t.Helper()
		reading := service.Status{Settings: settings, Consents: &consentStoreOfTheTest{consent: consent}, Saga: sagaOfTheTest{toRead: toRead},
			Pace: paceOfTheTable(100), Now: func() time.Time { return since.Add(time.Hour) }}
		answer, err := indexingservice.NewService(countsOfTheTable{read: read, total: 612}).WithReading(reading).GetStatus(context.Background(),
			indexingservice.GetStatusInput{ActorUserID: "author", ProjectID: "own"})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(answer)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		return decoded
	}
	get := func(router http.Handler, path string) map[string]any {
		t.Helper()
		code, answer, _ := askTheWindow(router, http.MethodGet, path, "", "cookie-of-the-window")
		if code != http.StatusOK {
			t.Fatalf("%s = %d %v", path, code, answer)
		}
		return answer
	}
	sampleJob := func(status, code string) *service.SampleJob {
		return &service.SampleJob{JobID: "sample", Status: status, ErrorCode: code, Host: "api.deepseek.com",
			Chapters: []service.Chapter{{ID: "c1", Hash: "h1"}, {ID: "c2", Hash: "h2"}, {ID: "c3", Hash: "h3"}}}
	}

	rubleUnit := readingdomain.Unit(aisettings.CurrencyRUB)
	for _, row := range []struct {
		name   string
		answer map[string]any
		fields map[string]any // nil — the field is there with any value
	}{
		{"44·27 строка на полке", status(priced, service.ToRead{Unread: unread}, nil, 0),
			map[string]any{"reading.state": "waiting_consent", "reading.chaptersTotal": float64(612), "reading.chaptersRead": float64(0),
				"preparation.waiting": "consent"}},
		{"44·27 с ценами", get(window(priced, service.ToRead{Unread: unread, Books: 13}, nil, nil, 0), "/projects/own/reading/estimate"),
			map[string]any{"chapters": float64(612), "host": "api.deepseek.com", "local": false, "unit": "RUB", "kind": "rough",
				"sampleChapters": float64(3), "inputTokens": nil, "outputTokens": nil, "amount": nil, "seconds": nil, "suggestedStop": nil}},
		{"44·27 без цен", get(window(plain, service.ToRead{Unread: unread, Books: 13}, nil, nil, 0), "/projects/own/reading/estimate"),
			map[string]any{"unit": "tokens", "amount": "null", "suggestedStop": nil}},
		{"44·27 проба идёт", get(window(priced, service.ToRead{Unread: unread}, nil, sampleJob("running", ""), 1), "/projects/own/reading/sample"),
			map[string]any{"state": "running", "done": float64(1), "total": float64(3)}},
		{"44·27 проба готова", get(window(priced, service.ToRead{Unread: unread}, nil, sampleJob("succeeded", ""), 3), "/projects/own/reading/sample"),
			map[string]any{"state": "succeeded", "total": float64(3), "seconds": float64(240), "spent.amount": float64(4_000_000), "spent.unit": "RUB",
				"spent.approximate": true, "chapters.0.place": "Книга 1, гл. 1 „Туманный берег“", "chapters.0.summary": nil, "chapters.0.events": nil,
				"chapters.0.people": nil}},
		{"44·28 идёт", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 240_000_000), 212),
			map[string]any{"reading.state": "reading", "reading.chaptersRead": float64(212), "reading.chaptersTotal": float64(612),
				"reading.remainingSeconds": nil, "reading.spent": float64(240_000_000), "reading.approximate": true, "reading.stopAt": float64(stop),
				"reading.unit": "RUB", "preparation.askable": true, "preparation.waiting": "absent"}},
		{"44·28 пауза автора", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StatePaused, readingdomain.PauseAuthor, rubleUnit, 0), 212),
			map[string]any{"reading.state": "paused", "reading.pause": "author", "preparation.waiting": "paused"}},
		{"44·28 сумма", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StatePaused, readingdomain.PauseSum, rubleUnit, stop), 212),
			map[string]any{"reading.pause": "sum", "reading.spent": float64(stop), "reading.stopAt": float64(stop), "reading.step": float64(step)}},
		{"44·28 провайдер отказал", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StatePaused, readingdomain.PauseFunds, rubleUnit, 0), 212),
			map[string]any{"reading.pause": "funds"}},
		{"44·28 нет связи", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StatePaused, readingdomain.PauseConnection, rubleUnit, 0), 212),
			map[string]any{"reading.pause": "connection"}},
		{"44·28 готово", status(priced, service.ToRead{}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 612),
			map[string]any{"reading": "absent"}},
		{"Data ключ", status(priced, service.ToRead{Unread: unread[:1]}, consentOf(readingdomain.StatePaused, readingdomain.PauseKey, rubleUnit, 0), 611),
			map[string]any{"reading.pause": "key"}},
		{"Data модель", status(priced, service.ToRead{Unread: unread[:1]}, consentOf(readingdomain.StatePaused, readingdomain.PauseModel, rubleUnit, 0), 611),
			map[string]any{"reading.pause": "model"}},
		{"Data цены изменились", status(plain, service.ToRead{Unread: unread[:1]}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 611),
			map[string]any{"reading.state": "paused", "reading.pause": "prices"}},
		{"Data главы прочитаны", status(priced, service.ToRead{NamesWait: true}, consentOf(readingdomain.StatePaused, readingdomain.PauseSum, rubleUnit, stop), 612),
			map[string]any{"reading.state": "paused", "reading.namesWait": true, "reading.threadsWait": false}},
		// specs/2026-11-15-desk-reading-rest.md RR2.1, RR2.2: what did not come together — the names, the threads, both, with
		// the chapters — is done with its reasons; while the chapters are read, the names and the threads that wait say what
		// is brought together.
		{"Data имена не сведены", status(priced, service.ToRead{NamesUnread: "unreadable"}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 612),
			map[string]any{"reading.state": "done", "reading.unreadRest.names": "unreadable", "reading.unreadRest.threads": "null", "reading.unread": "null"}},
		{"Data нити не сведены", status(priced, service.ToRead{ThreadsUnread: "no_answer"}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 612),
			map[string]any{"reading.state": "done", "reading.unreadRest.names": "null", "reading.unreadRest.threads": "no_answer"}},
		{"Data главы и имена не сведены", status(priced, service.ToRead{Marked: []service.Unread{{ID: "c", Hash: "h", Reason: "cut_answer"}}, NamesUnread: "refused", ThreadsUnread: "cut_answer"},
			consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 611),
			map[string]any{"reading.state": "done", "reading.unread.chapters": float64(1), "reading.unreadRest.names": "refused", "reading.unreadRest.threads": "cut_answer"}},
		{"Data сводит сюжетные нити", status(priced, service.ToRead{ThreadsWait: true, NamesUnread: "unreadable"}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 612),
			map[string]any{"reading.state": "reading", "reading.namesWait": false, "reading.threadsWait": true, "reading.unreadRest.names": "unreadable"}},
		{"Data главы ещё читаются", status(priced, service.ToRead{Unread: unread[:1]}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 611),
			map[string]any{"reading.state": "reading", "reading.namesWait": false, "reading.threadsWait": false, "reading.unreadRest": "null"}},
		{"Data согласие не действует", status(readingSettingsOfTheTest{connected: true, settings: ownAt(t, aisettings.OwnDraft{Preset: "openai", ChatModel: &deepseek, CardsModel: &reader})},
			service.ToRead{Unread: unread}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 0),
			map[string]any{"reading.state": "waiting_consent", "reading.reason": "provider_changed"}},
		{"Data главы не прочитались", status(priced, service.ToRead{Marked: []service.Unread{{ID: "c", Hash: "h", Reason: "cut_answer"}}}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 611),
			map[string]any{"reading.state": "done", "reading.unread.chapters": float64(1), "reading.unread.reason": "cut_answer"}},
		{"Data главы не прочитались: ответ не разобран", status(priced, service.ToRead{Marked: []service.Unread{{ID: "c", Hash: "h", Reason: "unreadable"}}}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 611),
			map[string]any{"reading.state": "done", "reading.unread.chapters": float64(1), "reading.unread.reason": "unreadable"}},
		{"Data счёт без цен", status(plain, service.ToRead{Unread: unread[:1]}, consentOf(readingdomain.StateAllowed, "", readingdomain.UnitTokens, 0), 611),
			map[string]any{"reading.unit": "tokens", "reading.spent": nil, "reading.stopAt": nil}},
		{"Data модель на этом компьютере", get(window(local, service.ToRead{Unread: unread}, nil, nil, 0), "/projects/own/reading/estimate"),
			map[string]any{"local": true}},
		{"Data проба не получилась", get(window(priced, service.ToRead{Unread: unread}, nil, sampleJob("failed", "reading.funds"), 0), "/projects/own/reading/sample"),
			map[string]any{"state": "failed", "failure": "funds"}},
		// specs/2026-11-15-desk-reading-no-answer.md Data: the provider does not answer — the pause, with the chapters read and
		// with what is left to bring together; the chapters it did not take; its sample says «ответ помощника не пришёл».
		{"Data провайдер не отвечает", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StatePaused, readingdomain.PauseNoAnswer, rubleUnit, 0), 212),
			map[string]any{"reading.state": "paused", "reading.pause": "no_answer", "reading.chaptersRead": float64(212), "preparation.waiting": "paused"}},
		{"Data провайдер не отвечает, главы прочитаны", status(priced, service.ToRead{NamesWait: true, ThreadsWait: true}, consentOf(readingdomain.StatePaused, readingdomain.PauseNoAnswer, rubleUnit, 0), 612),
			map[string]any{"reading.state": "paused", "reading.pause": "no_answer", "reading.namesWait": true, "reading.threadsWait": true}},
		{"Data провайдер не принял главы", status(priced, service.ToRead{Marked: []service.Unread{{ID: "c", Hash: "h", Reason: "refused"}}}, consentOf(readingdomain.StateAllowed, "", rubleUnit, 0), 611),
			map[string]any{"reading.state": "done", "reading.unread.chapters": float64(1), "reading.unread.reason": "refused"}},
		{"Data проба без ответа провайдера", get(window(priced, service.ToRead{Unread: unread}, nil, sampleJob("failed", "reading.no_answer"), 0), "/projects/own/reading/sample"),
			map[string]any{"state": "failed", "failure": "other"}},
		{"Data проба остановлена", get(window(priced, service.ToRead{Unread: unread}, nil, sampleJob("canceled", ""), 1), "/projects/own/reading/sample"),
			map[string]any{"state": "canceled", "done": float64(1), "total": float64(3)}},
		{"Data пауза «сумма», потрачено меньше", status(priced, service.ToRead{Unread: unread[:400]}, consentOf(readingdomain.StatePaused, readingdomain.PauseSum, rubleUnit, 980_000_000), 212),
			map[string]any{"reading.pause": "sum", "reading.spent": float64(980_000_000), "reading.approximate": true}},
	} {
		for path, want := range row.fields {
			value, ok := field(row.answer, path)
			switch want {
			case nil:
				if !ok || value == nil {
					t.Errorf("%s: no %s in %v", row.name, path, row.answer)
				}
			case "absent":
				if ok {
					t.Errorf("%s: %s = %v; want none", row.name, path, value)
				}
			case "null":
				if !ok || value != nil {
					t.Errorf("%s: %s = %v; want null", row.name, path, value)
				}
			default:
				if !ok || value != want {
					t.Errorf("%s: %s = %v; want %v", row.name, path, value, want)
				}
			}
		}
	}
}
