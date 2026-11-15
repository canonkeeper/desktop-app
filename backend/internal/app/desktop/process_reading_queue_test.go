// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readingSettled waits until the text of a saga is indexed and its reading stands still: the published text has its index, and
// no import, text index or cards Job of the saga waits or runs. A connected helper used to be awaited by the cards Job its
// catch-up queued; a saga the model may not read gets none now (specs/2026-11-15-desk-reading-queue.md RQ2.1), so the tests
// wait for what they need — the text the questions, the sample and the consent read.
func readingSettled(t *testing.T, data, projectID string, timeout time.Duration) {
	t.Helper()
	connection := database(t, data)
	defer func() { _ = connection.Close(context.Background()) }()
	for deadline := time.Now().Add(timeout); ; time.Sleep(200 * time.Millisecond) {
		var settled bool
		if err := connection.QueryRow(context.Background(), `
			SELECT EXISTS (SELECT 1 FROM chapter_text_index_state WHERE project_id = $1 AND source_view = 'published')
			  AND NOT EXISTS (SELECT 1 FROM jobs WHERE project_id = $1 AND kind IN ('import', 'canon_text_index', 'chapter_cards')
			    AND status IN ('queued', 'running', 'canceling'))`, projectID).Scan(&settled); err != nil {
			t.Fatal(err)
		}
		if settled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the text of the saga was not indexed, or its reading did not stand still")
		}
	}
}

// liveCardsJobs counts the cards Jobs of the saga that wait or run.
func (window readingWindow) liveCardsJobs() int {
	window.t.Helper()
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	var count int
	if err := connection.QueryRow(context.Background(), `
		SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND status IN ('queued', 'running', 'canceling')`,
		window.project).Scan(&count); err != nil {
		window.t.Fatal(err)
	}
	return count
}

// mapped says the saga has its prepared map.
func (window readingWindow) mapped() bool {
	window.t.Helper()
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	var found bool
	if err := connection.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM saga_map_graphs WHERE project_id = $1)`, window.project).Scan(&found); err != nil {
		window.t.Fatal(err)
	}
	return found
}

// noLiveCardsJob waits until no cards Job of the saga waits or runs.
func (window readingWindow) noLiveCardsJob(timeout time.Duration) {
	window.t.Helper()
	for deadline := time.Now().Add(timeout); window.liveCardsJobs() != 0; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			window.t.Fatalf("a cards Job of the saga still waits or runs%s", window.journey())
		}
	}
}

// heldAt waits until the stub holds the card of its held chapter.
func (stub *readingStub) heldAt(t *testing.T, timeout time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(100 * time.Millisecond) {
		stub.mu.Lock()
		holding := stub.holding
		stub.mu.Unlock()
		if holding {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stub never held its chapter")
		}
	}
}

// journal is what the backends of the data directory wrote in their logs, in the order they wrote it: the backend logs into
// <data>/logs/backend-<day>.log, not into its stderr.
func journal(t *testing.T, data string) []byte {
	t.Helper()
	directory := DataDir{Root: data}.Logs()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, entry := range entries {
		if name := entry.Name(); strings.HasPrefix(name, "backend-") && strings.HasSuffix(name, ".log") {
			text, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, text...)
		}
	}
	return all
}

// catchUpMark is how many catch-ups of a change of the settings the journal says ended and failed.
type catchUpMark struct{ done, failed int }

// catchUps reads the mark of the journal now. A count of rows about a catch-up is read after its end, or a slow catch-up
// would pass it by.
func (process *backend) catchUps() catchUpMark {
	process.t.Helper()
	written := journal(process.t, process.data)
	return catchUpMark{
		done:   bytes.Count(written, []byte(`"event":"desktop_cards_catch_up_done"`)),
		failed: bytes.Count(written, []byte(`"event":"desktop_cards_catch_up_failed"`)),
	}
}

// caughtUp waits until n catch-ups of a change of the settings ended since the mark; one that failed fails the test.
func (process *backend) caughtUp(mark catchUpMark, n int, timeout time.Duration) {
	process.t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(100 * time.Millisecond) {
		now := process.catchUps()
		if now.failed > mark.failed {
			process.t.Fatal("a catch-up of a change of the settings failed")
		}
		if now.done >= mark.done+n {
			return
		}
		if time.Now().After(deadline) {
			process.t.Fatalf("%d of %d catch-ups of a change of the settings ended", now.done-mark.done, n)
		}
	}
}

// startCaughtUp starts the backend of the data directory and waits until its catch-ups of the start — the cards and the
// layer — went through every saga: the journal says the end of each, one that queued nothing too (app.startCatchUp); a
// failed one fails the test.
func startCaughtUp(t *testing.T, binary, resources, data string) *backend {
	t.Helper()
	from := len(journal(t, data))
	process := startBackend(t, binary, resources, data, nil)
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(100 * time.Millisecond) {
		written := journal(t, data)[from:]
		if bytes.Contains(written, []byte(`"event":"cards_catch_up_failed"`)) || bytes.Contains(written, []byte(`"event":"layer_catch_up_failed"`)) {
			t.Fatal("a catch-up of the start failed")
		}
		if bytes.Contains(written, []byte("chapter cards catch-up queued ")) && bytes.Contains(written, []byte("saga layer catch-up queued ")) {
			return process
		}
		if time.Now().After(deadline) {
			t.Fatal("the catch-ups of the start did not end")
		}
	}
}

// requestsOf counts the requests of the operation the journal of the data directory has, the answers 500 and the refusals
// 23505 of the base in it.
func requestsOf(t *testing.T, data, operation string) (requests, server, unique int) {
	t.Helper()
	for _, line := range bytes.Split(journal(t, data), []byte("\n")) {
		if bytes.Contains(line, []byte(`"operationId":"`+operation+`"`)) {
			requests++
		}
		if bytes.Contains(line, []byte(`"status":500`)) {
			server++
		}
		if bytes.Contains(line, []byte(`"errorCause":"23505"`)) {
			unique++
		}
	}
	return requests, server, unique
}

// specs/2026-11-15-desk-reading-queue.md RQ1.2 on the built backend of the app: the author paused the reading while its Job
// read, so the map is behind the cards it built, and at every start the catch-up queues the cards Job that builds the map —
// «Продолжить» pressed right after the start queues the reading beside it. Eight times the consent answers the consent and
// the reading goes on — one chapter more each time, to the next held one, so the map is behind at the next start again —, and
// no journal of the backends has an answer 500 or a refusal 23505 of the base; the hold let go, the book reads through.
func TestContinueRightAfterTheStartAnswersTheConsent(t *testing.T) {
	const rounds = 8
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	var titles []string
	for chapter := 1; chapter <= rounds+3; chapter++ {
		titles = append(titles, fmt.Sprintf("Глава %d. Застава %d", chapter, chapter))
	}
	stub := &readingStub{titles: append([]string(nil), titles...), hold: titles[2]}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	connect(t, window, server.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	saga.setConsent(map[string]any{"state": "allowed"})
	stub.heldAt(t, 2*time.Minute)
	saga.setConsent(map[string]any{"state": "paused"})
	saga.noLiveCardsJob(2 * time.Minute)

	for round := 1; round <= rounds; round++ {
		process.stop()
		process = startBackend(t, binary, resources, data, nil)
		window = openWindow(t, process)
		saga.client = window
		stub.set(func(stub *readingStub) { stub.hold, stub.holding = titles[round+2], false })
		response, body := window.do(http.MethodPut, "/api/v1/projects/"+saga.project+"/reading/consent", window.csrf(), map[string]any{"state": "allowed"})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("round %d: «Продолжить» right after the start = %d %s%s", round, response.StatusCode, body, saga.journey())
		}
		stub.heldAt(t, 2*time.Minute)
		saga.setConsent(map[string]any{"state": "paused"})
		saga.noLiveCardsJob(2 * time.Minute)
	}
	if consents, server, unique := requestsOf(t, data, "setReadingConsent"); consents < 2*rounds+2 || server != 0 || unique != 0 {
		t.Fatalf("the journal has %d requests of the consent, %d answers 500 and %d refusals 23505", consents, server, unique)
	}
	stub.set(func(stub *readingStub) { stub.hold = "" })
	saga.setConsent(map[string]any{"state": "allowed"})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("the reading did not go on: %+v%s", status.Reading, saga.journey())
	}
	process.stop()
}

// specs/2026-11-15-desk-reading-queue.md RQ2.1–RQ2.3 on the built backend of the app: a saga whose reading the author paused
// while its Job read has its map behind the cards — the first start queues the cards Job once, and it builds the map without
// a request of the model. Then, the map fresh, ten starts and ten settings saved as they are add no row of jobs to it, nor
// to a saga that waits for its consent and has no card, and send the provider nothing; the status says the pause and «готов
// прочитать книги» as before. «Продолжить» queues the Job at once, and the book reads through. A consent given to another
// host does not act — the same rule: the starts and the saves add no row either.
func TestAPauseMakesNoEmptyJobs(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава"}
	stub := &readingStub{titles: append([]string(nil), titles...), hold: titles[2]}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	waiting := openSaga(t, window, "Южный предел", bookOf("Глава 1. Рассвет")).watched(data, stub)
	advanced := map[string]any{"parallel": 1, "maxOutput": 1000}
	connect(t, window, server.URL, advanced)
	saga.setConsent(map[string]any{"state": "allowed"})
	stub.heldAt(t, 2*time.Minute)
	saga.setConsent(map[string]any{"state": "paused"})
	saga.noLiveCardsJob(2 * time.Minute)
	if saga.mapped() {
		t.Fatal("the map was built before the pause; the test wants it behind")
	}

	// The map behind: the first start queues the Job that builds it, without the model.
	sent := stub.reading("")
	behindAt := time.Now().Add(-time.Second)
	process.stop()
	process = startBackend(t, binary, resources, data, nil)
	window = openWindow(t, process)
	saga.client, waiting.client = window, window
	for deadline := time.Now().Add(2 * time.Minute); !saga.mapped() || saga.liveCardsJobs() != 0; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the map behind was not built after the start%s", saga.journey())
		}
	}
	if jobs := saga.readingJobsSince(behindAt); jobs == 0 {
		t.Fatalf("the map was built with no Job%s", saga.journey())
	}
	if stub.reading("") != sent {
		t.Fatalf("the Job of the map asked the provider %d times", stub.reading("")-sent)
	}

	// The map fresh: ten starts and ten saves add nothing. The rows are counted after the catch-ups they are about: the
	// journal says the end of the catch-ups of each start and of each save.
	freshAt := time.Now()
	for range 10 {
		process.stop()
		process = startCaughtUp(t, binary, resources, data)
	}
	window = openWindow(t, process)
	saga.client, waiting.client = window, window
	saved := process.catchUps()
	for range 10 {
		connect(t, window, server.URL, advanced)
	}
	process.caughtUp(saved, 10, 2*time.Minute)
	if status := saga.status(); status.Reading == nil || status.Reading.State != "paused" || status.Reading.Pause == nil || *status.Reading.Pause != "author" {
		t.Fatalf("the status of the paused saga = %+v", status.Reading)
	}
	if status := waiting.status(); status.Reading == nil || status.Reading.State != "waiting_consent" || status.Preparation == nil || status.Preparation.Waiting != "consent" {
		t.Fatalf("the status of the saga that waits for its consent = %+v %+v", status.Preparation, status.Reading)
	}
	pressedAt := time.Now()
	saga.setConsent(map[string]any{"state": "allowed"})
	if saga.liveCardsJobs() == 0 {
		t.Fatalf("«Продолжить» queued no Job at once%s", saga.journey())
	}
	if jobs := saga.readingJobsSince(freshAt) - saga.readingJobsSince(pressedAt); jobs != 0 {
		t.Fatalf("on the pause the starts and the saves added %d Jobs%s", jobs, saga.journey())
	}
	if jobs := waiting.readingJobsSince(freshAt); jobs != 0 {
		t.Fatalf("the saga that waits for its consent got %d Jobs%s", jobs, waiting.journey())
	}
	if stub.reading("") != sent {
		t.Fatalf("on the pause the provider was asked %d times", stub.reading("")-sent)
	}
	stub.set(func(stub *readingStub) { stub.hold = "" })
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("«Продолжить» did not read the book: %+v%s", status.Reading, saga.journey())
	}

	// A consent of another host does not act: the starts and the saves at it add no row either.
	other := httptest.NewServer(&readingStub{})
	defer other.Close()
	moved := process.catchUps()
	connect(t, window, other.URL, advanced)
	process.caughtUp(moved, 1, 2*time.Minute)
	saga.noLiveCardsJob(2 * time.Minute)
	movedAt := time.Now()
	for range 3 {
		process.stop()
		process = startCaughtUp(t, binary, resources, data)
		window = openWindow(t, process)
		saga.client, waiting.client = window, window
		saved := process.catchUps()
		connect(t, window, other.URL, advanced)
		process.caughtUp(saved, 1, 2*time.Minute)
	}
	if consent := saga.consent(); consent.State != "none" || consent.Reason == nil || *consent.Reason != "provider_changed" {
		t.Fatalf("the consent at another host = %+v", consent)
	}
	if jobs := saga.readingJobsSince(movedAt) + waiting.readingJobsSince(movedAt); jobs != 0 {
		t.Fatalf("at another host the starts and the saves added %d Jobs%s", jobs, saga.journey())
	}
	process.stop()
}
