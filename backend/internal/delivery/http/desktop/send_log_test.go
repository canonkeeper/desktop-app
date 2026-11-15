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
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/modeljournal"
	service "canonkeeper/backend/internal/services/desktop"

	"github.com/go-chi/chi/v5"
)

// journalOfTheTest is the journal store as the operations of the window see it: one question and one check.
type journalOfTheTest struct {
	cleared  int
	clearAt  *time.Time
	failures error
}

const questionKey = "11111111-1111-4111-8111-111111111111|2026-11-13"

func (journal *journalOfTheTest) Rows(_ context.Context, query modeljournal.RowsQuery) ([]modeljournal.Row, *modeljournal.Cursor, error) {
	if query.Zone != "Europe/Moscow" {
		return nil, nil, modeljournal.ErrZone
	}
	question := modeljournal.Row{
		Key: questionKey, Kind: modeljournal.KindQuestion, Start: time.Date(2026, 11, 13, 9, 40, 0, 0, time.UTC), Last: time.Date(2026, 11, 13, 9, 41, 0, 0, time.UTC),
		ProjectID: "saga", SagaTitle: "Северный предел", ChatTitle: "Кто стоит на заставе?", Recipients: []string{"provider.example.test"},
		Bytes: 6200, Sendings: 2, Named: disclosure.Named{Question: true, Chapters: []disclosure.Chapter{{ID: "c12", Precision: disclosure.Paragraphs, Paragraphs: []disclosure.Range{{From: 3, To: 9}}}}},
		Own: &modeljournal.OwnNumbers{Requests: 2, Tokens: 48000, Costs: []modeljournal.Cost{{Currency: "RUB", Micros: 3_000_000}}},
	}
	check := modeljournal.Row{
		Key: "22222222-2222-4222-8222-222222222222|2026-11-13", Kind: modeljournal.KindCheck, Start: time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC),
		Last: time.Date(2026, 11, 13, 9, 31, 0, 0, time.UTC), Recipients: []string{"provider.example.test"}, Bytes: 1200, Sendings: 6,
	}
	if query.Kind == modeljournal.KindCheck {
		return []modeljournal.Row{check}, nil, nil
	}
	return []modeljournal.Row{question, check}, nil, nil
}

func (journal *journalOfTheTest) Sendings(_ context.Context, key, zone string, _ *modeljournal.SendingCursor, _ int) (modeljournal.Kind, []modeljournal.SendingLine, *modeljournal.SendingCursor, error) {
	if zone != "Europe/Moscow" {
		return "", nil, nil, modeljournal.ErrZone
	}
	if key != questionKey {
		return modeljournal.KindCheck, nil, nil, nil
	}
	return modeljournal.KindQuestion, []modeljournal.SendingLine{
		{ID: "s1", At: time.Date(2026, 11, 13, 9, 40, 0, 0, time.UTC), Recipient: "provider.example.test", Bytes: 2100, Named: disclosure.Named{Question: true}},
		{ID: "s2", At: time.Date(2026, 11, 13, 9, 41, 0, 0, time.UTC), Recipient: "provider.example.test", Bytes: 4100, Outcome: "ответ оборвался",
			Named: disclosure.Named{Chapters: []disclosure.Chapter{{ID: "c12", Precision: disclosure.Paragraphs, Paragraphs: []disclosure.Range{{From: 3, To: 9}}}}}},
	}, nil, nil
}

func (journal *journalOfTheTest) SendingsOf(ctx context.Context, keys []string, zone string) (map[string][]modeljournal.SendingLine, error) {
	all := map[string][]modeljournal.SendingLine{}
	for _, key := range keys {
		_, lines, _, err := journal.Sendings(ctx, key, zone, nil, 0)
		if err != nil {
			return nil, err
		}
		all[key] = lines
	}
	return all, nil
}

func (journal *journalOfTheTest) Places(context.Context, disclosure.Named) (modeljournal.Places, error) {
	return modeljournal.Places{Chapters: map[string]modeljournal.ChapterPlace{
		"c12": {State: modeljournal.PlaceLive, BookID: "b2", Book: 2, Number: "12", Title: "Белый порт"},
	}}, nil
}

func (journal *journalOfTheTest) Sagas(context.Context) (int, error) { return 1, nil }

func (journal *journalOfTheTest) Clear(context.Context) error {
	journal.cleared++
	return journal.failures
}

func (journal *journalOfTheTest) Local(_ context.Context, _ string, moments []time.Time) (map[int64]time.Time, error) {
	clock := map[int64]time.Time{}
	for _, moment := range moments {
		clock[moment.UnixMicro()] = moment.Add(3 * time.Hour)
	}
	return clock, nil
}

func (journal *journalOfTheTest) LogClearedAt() *time.Time { return journal.clearAt }

func (journal *journalOfTheTest) KeepLogCleared(at time.Time) error {
	journal.clearAt = &at
	return nil
}

func journalWindow(journal *journalOfTheTest) http.Handler {
	router := chi.NewRouter()
	Handlers{
		Sessions: windowSessions{cookie: "cookie-of-the-window", user: auth.User{ID: "author"}},
		Log:      service.SendLog{Store: journal, Clears: journal, Now: func() time.Time { return time.Date(2026, 11, 13, 11, 5, 0, 0, time.UTC) }},
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

func journalCall(t *testing.T, router http.Handler, method, target string, signed bool, origin string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	if signed {
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "cookie-of-the-window"})
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", "token-of-the-window")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// specs/2026-11-14-desk-journal.md JR2.2–JR2.6: the window reads the rows of the journal with the words of what went, the
// sendings of a row and the file of a choice, and clears the journal; without its session nothing is read (401), a clearing
// from another Origin is refused (403), a zone the journal does not know is 400. No key of a chapter reaches the window.
func TestTheWindowReadsSavesAndClearsTheJournal(t *testing.T) {
	journal := &journalOfTheTest{}
	router := journalWindow(journal)
	if response := journalCall(t, router, http.MethodGet, "/desktop/log?zone=Europe/Moscow", false, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("without the session = %d", response.Code)
	}
	if response := journalCall(t, router, http.MethodGet, "/desktop/log?zone=Nowhere/Land", true, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("a zone nobody knows = %d", response.Code)
	}
	if response := journalCall(t, router, http.MethodGet, "/desktop/log?zone=Europe/Moscow&kind=letter", true, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("a kind nobody knows = %d", response.Code)
	}
	response := journalCall(t, router, http.MethodGet, "/desktop/log?zone=Europe/Moscow", true, "")
	var page struct {
		Rows []struct {
			Key, Kind, WhatWent string
			Chat                *struct{ Title *string }
			Saga                *struct{ Title string }
			Own                 *struct{ Requests int }
		} `json:"rows"`
		Next      *string `json:"next"`
		ClearedAt *string `json:"clearedAt"`
		Sagas     int     `json:"sagas"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Rows) != 2 || page.Sagas != 1 || page.Next != nil {
		t.Fatalf("listSendLog = %d %s", response.Code, response.Body)
	}
	if question := page.Rows[0]; question.WhatWent != "ваш вопрос; Книга 2, гл. 12 „Белый порт“, абзацы 3–9" || question.Chat == nil || *question.Chat.Title != "Кто стоит на заставе?" ||
		question.Saga == nil || question.Own == nil || question.Own.Requests != 2 {
		t.Fatalf("the question = %+v", question)
	}
	if check := page.Rows[1]; check.WhatWent != "без вашего текста" || check.Chat != nil || check.Saga != nil {
		t.Fatalf("the check = %+v", check)
	}
	if strings.Contains(response.Body.String(), "c12") || strings.Contains(response.Body.String(), "¶") {
		t.Fatal("the window got a key of a chapter")
	}
	response = journalCall(t, router, http.MethodGet, "/desktop/log/sendings?row="+questionKey+"&zone=Europe/Moscow", true, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"outcome":"ответ оборвался"`) || !strings.Contains(response.Body.String(), `"whatWent":"ваш вопрос"`) {
		t.Fatalf("listSendLogSendings = %d %s", response.Code, response.Body)
	}
	response = journalCall(t, router, http.MethodGet, "/desktop/log/file?zone=Europe/Moscow", true, "")
	var file struct{ FileName, Text string }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &file) != nil || file.FileName != "canonkeeper-journal-2026-11-13.txt" ||
		!strings.Contains(file.Text, "13 ноября 2026, 12:40 · Вопрос · ваш вопрос; Книга 2, гл. 12 „Белый порт“, абзацы 3–9 · provider.example.test") ||
		!strings.Contains(file.Text, "13 ноября 2026, 12:41 · Книга 2, гл. 12 „Белый порт“, абзацы 3–9 · 4 КБ · ответ оборвался") {
		t.Fatalf("getSendLogFile = %d %s", response.Code, response.Body)
	}
	if response := journalCall(t, router, http.MethodDelete, "/desktop/log", true, "http://evil.example.test"); response.Code != http.StatusForbidden || journal.cleared != 0 {
		t.Fatalf("a clearing from another Origin = %d, cleared %d", response.Code, journal.cleared)
	}
	response = journalCall(t, router, http.MethodDelete, "/desktop/log", true, "http://127.0.0.1:1")
	if response.Code != http.StatusOK || journal.cleared != 1 || !strings.Contains(response.Body.String(), `"clearedAt":"2026-11-13T11:05:00.000Z"`) {
		t.Fatalf("clearSendLog = %d %s", response.Code, response.Body)
	}
	response = journalCall(t, router, http.MethodGet, "/desktop/log?zone=Europe/Moscow", true, "")
	if !strings.Contains(response.Body.String(), `"clearedAt":"2026-11-13T11:05:00.000Z"`) {
		t.Fatalf("the cleared journal = %s", response.Body)
	}
}
