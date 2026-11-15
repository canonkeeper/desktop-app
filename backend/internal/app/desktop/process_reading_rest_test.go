// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/coresteps"
)

// readingJobsSince counts the Jobs of the cards and of the layer of the saga queued after a moment.
func (window readingWindow) readingJobsSince(after time.Time) int {
	window.t.Helper()
	var count int
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	if err := connection.QueryRow(context.Background(), `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind IN ('chapter_cards', 'saga_layer') AND created_at > $2`,
		window.project, after).Scan(&count); err != nil {
		window.t.Fatal(err)
	}
	return count
}

// asked waits until no Job of the cards or of the layer of the saga waits or runs, and the stub heard the calls of the names
// and of the books it waits for.
func (window readingWindow) asked(names, books int, timeout time.Duration) {
	window.t.Helper()
	deadline := time.Now().Add(timeout)
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	for {
		var live int
		err := connection.QueryRow(context.Background(), `
			SELECT count(*) FROM jobs WHERE project_id = $1 AND kind IN ('chapter_cards', 'saga_layer') AND status IN ('queued', 'running')`,
			window.project).Scan(&live)
		if err == nil && live == 0 && window.stub.steps(coresteps.Names) >= names && window.stub.steps(coresteps.LayerBook) >= books {
			return
		}
		if time.Now().After(deadline) {
			window.t.Fatalf("the reading did not settle at %d names and %d books:%s", names, books, window.journey())
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// specs/2026-11-15-desk-reading-rest.md RR1.2–RR1.4, RR1.7–RR1.9 on the built backend of the app, a saga of one book and a
// provider that answers the register of names with a word and the question of a book of the layer with rubbish: once the
// chapters are read the names are asked once and the book three times; two restarts, the settings saved without another
// model or limit of an answer and «Пауза» → «Продолжить» add no request and no Job of the cards or of the layer, and the
// status is done with what did not come together; «Повторить» asks the names once and the book three times again; another
// limit of an answer does the same without «Повторить»; the count of the consent grows by these requests alone.
func TestTheNamesAndTheThreadsThatDidNotComeTogetherAreNotPaidAgain(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал"}
	stub := &readingStub{titles: titles, names: "Готово", book: "мусор"}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	advanced := map[string]any{"parallel": 1, "maxOutput": 1000}
	connect(t, window, server.URL, advanced)
	saga.setConsent(map[string]any{"state": "allowed"})
	saga.asked(1, 3, 5*time.Minute)
	const perRequest = stubInput + stubOutput
	names, books := stub.steps(coresteps.Names), stub.steps(coresteps.LayerBook)
	if names != 1 || books != 3 {
		t.Fatalf("after the chapters: %d names and %d books asked; want 1 and 3%s", names, books, saga.journey())
	}
	unreadRest := func() {
		t.Helper()
		status := saga.status()
		if status.Reading == nil || status.Reading.State != "done" || status.Reading.UnreadRest == nil ||
			status.Reading.UnreadRest.Names == nil || *status.Reading.UnreadRest.Names != "unreadable" ||
			status.Reading.UnreadRest.Threads == nil || *status.Reading.UnreadRest.Threads != "unreadable" ||
			status.Reading.NamesWait || status.Reading.ThreadsWait {
			t.Fatalf("the status = %+v%s", status.Reading, saga.journey())
		}
	}
	unreadRest()
	spent := saga.consent().Spent.Amount
	if spent != int64(stub.reading("")*perRequest) {
		t.Fatalf("the count %d for %d requests", spent, stub.reading(""))
	}

	// Nothing of it pays again without the author: two restarts, the settings saved as they are, a pause and «Продолжить».
	quiet := time.Now()
	for range 2 {
		process.stop()
		process = startBackend(t, binary, resources, data, nil)
		window = openWindow(t, process)
		saga.client = window
		time.Sleep(3 * time.Second)
	}
	connect(t, window, server.URL, advanced)
	saga.setConsent(map[string]any{"state": "paused"})
	saga.setConsent(map[string]any{"state": "allowed"})
	time.Sleep(3 * time.Second)
	if jobs := saga.readingJobsSince(quiet); jobs != 0 || stub.steps(coresteps.Names) != names || stub.steps(coresteps.LayerBook) != books {
		t.Fatalf("without the author: %d Jobs, %d names and %d books asked%s", jobs, stub.steps(coresteps.Names), stub.steps(coresteps.LayerBook), saga.journey())
	}
	unreadRest()

	// «Повторить» asks once more: the names once, the book three times, under the same count.
	saga.setConsent(map[string]any{"state": "allowed", "retryUnread": true})
	saga.asked(names+1, books+3, 5*time.Minute)
	if stub.steps(coresteps.Names) != names+1 || stub.steps(coresteps.LayerBook) != books+3 {
		t.Fatalf("«Повторить» asked %d names and %d books", stub.steps(coresteps.Names)-names, stub.steps(coresteps.LayerBook)-books)
	}
	unreadRest()
	if after := saga.consent().Spent.Amount; after != spent+4*perRequest {
		t.Fatalf("the count after «Повторить» = %d; want %d", after, spent+4*perRequest)
	}

	// Another limit of an answer is another helper: one attempt without «Повторить».
	advanced["maxOutput"] = 2000
	connect(t, window, server.URL, advanced)
	saga.asked(names+2, books+6, 5*time.Minute)
	if stub.steps(coresteps.Names) != names+2 || stub.steps(coresteps.LayerBook) != books+6 {
		t.Fatalf("another limit asked %d names and %d books", stub.steps(coresteps.Names)-names-1, stub.steps(coresteps.LayerBook)-books-3)
	}
	unreadRest()
	if after := saga.consent().Spent.Amount; after != spent+8*perRequest || after != int64(stub.reading("")*perRequest) {
		t.Fatalf("the count after another limit = %d for %d requests", after, stub.reading(""))
	}
	process.stop()
}

// restMarks are the marks of the names and the threads of the saga, «kind view» each.
func (window readingWindow) restMarks() []string {
	window.t.Helper()
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	rows, err := connection.Query(context.Background(),
		`SELECT kind || ' ' || source_view FROM reading_unread_rest WHERE project_id = $1 ORDER BY kind, source_view`, window.project)
	if err != nil {
		window.t.Fatal(err)
	}
	defer rows.Close()
	var marks []string
	for rows.Next() {
		var mark string
		if err := rows.Scan(&mark); err != nil {
			window.t.Fatal(err)
		}
		marks = append(marks, mark)
	}
	if err := rows.Err(); err != nil {
		window.t.Fatal(err)
	}
	return marks
}

// readingEstimate is what getReadingEstimate answers of what is left to read.
type readingEstimate struct {
	Chapters    int   `json:"chapters"`
	InputTokens int64 `json:"inputTokens"`
	NamesWait   bool  `json:"namesWait"`
	ThreadsWait bool  `json:"threadsWait"`
}

func (window readingWindow) estimate() readingEstimate {
	window.t.Helper()
	var estimate readingEstimate
	window.read("/api/v1/projects/"+window.project+"/reading/estimate", &estimate)
	return estimate
}

// specs/2026-11-15-desk-reading-rest.md RR1.4, RR2.2 on the built backend of the app: the status and the estimate read the
// marks of a chapter that did not read and of names that did not come together by the one rule of the policy. Under the
// consent of the helper of the failure the saga is done with them and the estimate counts nothing; at another host of the
// provider no consent acts, so the marks do not act — the chapter waits as unread, the status waits for the consent
// («Прочитать книги…» of the page) and the estimate counts the chapter and the names after it; back at the helper of the
// failure the marks act again.
func TestTheMarksActOnlyUnderTheConsentOfTheirHelper(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал"}
	stub := &readingStub{titles: titles, cut: titles[1], names: "Готово"}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	defer process.stop()
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	advanced := map[string]any{"parallel": 1, "maxOutput": 1000}
	connect(t, window, server.URL, advanced)
	saga.setConsent(map[string]any{"state": "allowed"})
	// The layer waits for the chapter that did not read: the names alone are asked.
	saga.asked(1, 0, 5*time.Minute)
	marked := func(at string) {
		t.Helper()
		status := saga.status()
		if status.Reading == nil || status.Reading.State != "done" || status.Reading.Unread == nil || status.Reading.Unread.Chapters != 1 ||
			status.Reading.Unread.Reason != "cut_answer" || status.Reading.UnreadRest == nil || status.Reading.UnreadRest.Names == nil ||
			*status.Reading.UnreadRest.Names != "unreadable" || status.Reading.NamesWait || status.Reading.ThreadsWait {
			t.Fatalf("%s: the status = %+v%s", at, status.Reading, saga.journey())
		}
		if estimate := saga.estimate(); estimate.Chapters != 0 || estimate.InputTokens != 0 || estimate.NamesWait || estimate.ThreadsWait {
			t.Fatalf("%s: the estimate counts what the marks keep: %+v", at, estimate)
		}
	}
	marked("under the consent")
	// Two text versions, one set of names: the Job of the draft asked them and marked them by its version; the Job of the
	// published text paid nothing for the same set, and the status — of the published text — reads the mark of the draft
	// (RR1.4: kept by version, acting by content).
	if marks := saga.restMarks(); len(marks) != 1 || marks[0] != "names draft" || stub.steps(coresteps.Names) != 1 {
		t.Fatalf("the marks of the rest = %v, %d names asked; want the one of the draft%s", marks, stub.steps(coresteps.Names), saga.journey())
	}

	// Another host of the provider: no consent acts for it, and the marks with it — the chapter is to read again.
	other := &readingStub{titles: titles}
	otherServer := httptest.NewServer(other)
	defer otherServer.Close()
	connect(t, window, otherServer.URL, advanced)
	status := saga.status()
	if status.Reading == nil || status.Reading.State != "waiting_consent" || status.Reading.Reason == nil || *status.Reading.Reason != "provider_changed" ||
		status.Reading.Unread != nil || status.Reading.UnreadRest != nil || status.Reading.ChaptersRead != 1 || status.Reading.ChaptersTotal != 2 {
		t.Fatalf("at another host: the status = %+v; want the chapter waiting with the consent", status.Reading)
	}
	if estimate := saga.estimate(); estimate.Chapters != 1 || estimate.InputTokens == 0 {
		t.Fatalf("at another host: the estimate = %+v; want the chapter and the names counted", estimate)
	}

	// The helper of the failure again: its consent acts, and so do the marks.
	connect(t, window, server.URL, advanced)
	marked("back at the helper of the failure")
}
