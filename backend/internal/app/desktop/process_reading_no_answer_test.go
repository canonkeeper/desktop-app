// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/coresteps"
)

// readingJob is a Job of the cards or of the layer: its kind, how it ended and how many times the worker repeated it.
type readingJob struct {
	kind, status, code string
	retries            int
}

func (job readingJob) String() string {
	return fmt.Sprintf("%s %s %s retries %d", job.kind, job.status, job.code, job.retries)
}

// readingJobs are the Jobs of the cards and of the layer of the saga queued after a moment.
func (window readingWindow) readingJobs(after time.Time) []readingJob {
	window.t.Helper()
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	rows, err := connection.Query(context.Background(), `
		SELECT kind, status, coalesce(error_json->>'code', ''), retry_count FROM jobs
		WHERE project_id = $1 AND kind IN ('chapter_cards', 'saga_layer') AND created_at > $2 ORDER BY created_at`,
		window.project, after)
	if err != nil {
		window.t.Fatal(err)
	}
	defer rows.Close()
	var jobs []readingJob
	for rows.Next() {
		var job readingJob
		if err := rows.Scan(&job.kind, &job.status, &job.code, &job.retries); err != nil {
			window.t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		window.t.Fatal(err)
	}
	return jobs
}

// chapterMarks counts the chapters of the saga marked «не прочиталась».
func (window readingWindow) chapterMarks() int {
	window.t.Helper()
	connection := database(window.t, window.data)
	defer func() { _ = connection.Close(context.Background()) }()
	var count int
	if err := connection.QueryRow(context.Background(), `SELECT count(*) FROM reading_unread_chapters WHERE project_id = $1`, window.project).Scan(&count); err != nil {
		window.t.Fatal(err)
	}
	return count
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.1, NA1.2 on the built backend of the app, one request at a time: a provider
// that answers the first chapter and then fails every request with 503 pauses the reading at «провайдер не отвечает» once the
// repeats of one request are over — six attempts of the card of the second chapter, no request of the third; the Job ends
// reading.no_answer and no chapter is marked. Two restarts and the settings saved as they are send the provider nothing and
// queue no Job that reads: what the catch-up queues ends without a request (specs/2026-11-13-desk-consent.md CN3.7), and the
// pause stands. «Повторить» with the provider back reads the book through. A stream torn mid-answer at every attempt of the
// card of a new book does the same, and so does the connection reset mid-answer: the provider began its answer, so it is
// «провайдер не отвечает», not «нет связи». No Job of the whole test was repeated or ended by its chapters left.
func TestAProviderThatDoesNotAnswerPausesTheReading(t *testing.T) {
	_, resources := desktopBinary(t)
	binary := program(t, programVariant{name: "fast-repeats", retryPause: "20ms"})
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава"}
	stub := &readingStub{titles: append([]string(nil), titles...)}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	started := time.Now().Add(-time.Second)
	advanced := map[string]any{"parallel": 1, "maxOutput": 1000}
	connect(t, window, server.URL, advanced)
	readingSettled(t, data, saga.project, 2*time.Minute)

	// The provider answers the card and the pass of the first chapter and then nothing.
	stub.set(func(stub *readingStub) { stub.silent, stub.quiet = "503", 2 })
	consented := time.Now().Add(-time.Second)
	saga.setConsent(map[string]any{"state": "allowed"})
	paused := func(at string, after time.Time, chapter string, read int) {
		t.Helper()
		if consent := saga.pausedAt(2 * time.Minute); consent.Pause == nil || *consent.Pause != "no_answer" {
			t.Fatalf("%s: the reading paused %+v%s", at, consent, saga.journey())
		}
		if !saga.endedWith(after, "reading.no_answer", 2*time.Minute) {
			t.Fatalf("%s: no Job ended reading.no_answer%s", at, saga.journey())
		}
		if asked := stub.reading(chapter); asked != 6 {
			t.Fatalf("%s: %s was asked %d times; want the six attempts of one request%s", at, chapter, asked, saga.journey())
		}
		status := saga.status()
		if status.Reading == nil || status.Reading.State != "paused" || status.Reading.Pause == nil || *status.Reading.Pause != "no_answer" ||
			status.Reading.ChaptersRead != read || status.Reading.Unread != nil || saga.chapterMarks() != 0 {
			t.Fatalf("%s: the status = %+v, %d chapters marked%s", at, status.Reading, saga.chapterMarks(), saga.journey())
		}
	}
	paused("the first book", consented, titles[1], 1)
	if stub.reading(titles[0]) != 2 || stub.reading(titles[2]) != 0 || stub.steps(coresteps.Names) != 0 {
		t.Fatalf("asked the first chapter %d times, the third %d, the names %d", stub.reading(titles[0]), stub.reading(titles[2]), stub.steps(coresteps.Names))
	}

	// Nothing reads without the author: two restarts and the settings saved as they are.
	sent, quiet := stub.reading(""), time.Now()
	for range 2 {
		process.stop()
		process = startBackend(t, binary, resources, data, nil)
		window = openWindow(t, process)
		saga.client = window
		time.Sleep(3 * time.Second)
	}
	connect(t, window, server.URL, advanced)
	time.Sleep(3 * time.Second)
	if stub.reading("") != sent {
		t.Fatalf("without the author the provider was asked %d times more%s", stub.reading("")-sent, saga.journey())
	}
	if consent := saga.consent(); consent.State != "paused" || consent.Pause == nil || *consent.Pause != "no_answer" {
		t.Fatalf("the consent after the restarts = %+v", consent)
	}
	for _, job := range saga.readingJobs(quiet) {
		if job != (readingJob{kind: "chapter_cards", status: "failed", code: "cards.consent"}) {
			t.Fatalf("without the author a Job read: %s%s", job, saga.journey())
		}
	}

	// «Повторить» with the provider back reads the book through.
	stub.set(func(stub *readingStub) { stub.silent = "" })
	saga.setConsent(map[string]any{"state": "allowed"})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("«Повторить» did not read the book: %+v%s", status.Reading, saga.journey())
	}
	if stub.reading(titles[2]) != 2 {
		t.Fatalf("the third chapter was asked %d times; want its card and its pass", stub.reading(titles[2]))
	}

	// A stream torn mid-answer at every attempt pauses the reading the same.
	watch := "Глава 4. Дозор"
	stub.set(func(stub *readingStub) { stub.titles, stub.silent, stub.quiet = append(stub.titles, watch), "torn", 0 })
	imported := time.Now().Add(-time.Second)
	window.importFileAs(saga.project, "Дозор.md", bookOf(watch), "import-of-the-watch")
	paused("a torn stream", imported, watch, 3)
	stub.set(func(stub *readingStub) { stub.silent = "" })
	saga.setConsent(map[string]any{"state": "allowed"})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("«Повторить» did not read the new book: %+v%s", status.Reading, saga.journey())
	}

	// The connection reset mid-answer at every attempt is no answer too, not «нет связи»: the provider began its answer.
	border := "Глава 5. Рубеж"
	stub.set(func(stub *readingStub) {
		stub.titles, stub.silent, stub.quiet = append(stub.titles, border), "reset", 0
	})
	imported = time.Now().Add(-time.Second)
	window.importFileAs(saga.project, "Рубеж.md", bookOf(border), "import-of-the-border")
	paused("the connection reset mid-answer", imported, border, 4)
	stub.set(func(stub *readingStub) { stub.silent = "" })
	saga.setConsent(map[string]any{"state": "allowed"})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("«Повторить» did not read the book after the reset: %+v%s", status.Reading, saga.journey())
	}

	// No Job of the test went round its chapters: none repeated, none ended by the chapters it left.
	for _, job := range saga.readingJobs(started) {
		if job.retries != 0 || job.code == "cards.incomplete" || job.code == "worker.retry_exhausted" {
			t.Fatalf("a Job went round: %s%s", job, saga.journey())
		}
	}
	process.stop()
}

// specs/2026-11-15-desk-reading-no-answer.md NA1.1 on the built backend of the app with the protocol «auto», which the gateway
// has not found yet — the first request of the reading after a start probes it: the check of the consent goes first, and a
// probe that fails pauses the reading by the table of the refusals — 503 at «провайдер не отвечает» after the six attempts of
// one request, 401 at «ключ» at once, a provider that drops the connection at «связь» after the six; each Job ends with the
// code of its pause, no chapter is asked and none marked, and the status is the pause, not «читает». With the protocol still
// unknown — no probe passed since the start — «Повторить» at the provider back probes it once and reads the book through.
func TestAProbeOfTheProtocolThatFailsPausesTheReading(t *testing.T) {
	_, resources := desktopBinary(t)
	binary := program(t, programVariant{name: "fast-repeats", retryPause: "20ms"})
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал"}
	stub := &readingStub{titles: append([]string(nil), titles...)}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	started := time.Now().Add(-time.Second)
	advanced := map[string]any{"parallel": 1, "maxOutput": 1000}
	connectBy(t, window, server.URL, "auto", advanced)
	readingSettled(t, data, saga.project, 2*time.Minute)
	for _, row := range []struct {
		probe, pause, code string
		probes             int
	}{
		{"503", "no_answer", "reading.no_answer", 6}, {"401", "key", "reading.key", 1}, {"drop", "connection", "reading.connection", 6},
	} {
		stub.set(func(stub *readingStub) { stub.probe, stub.probes = row.probe, 0 })
		before := time.Now().Add(-time.Second)
		saga.setConsent(map[string]any{"state": "allowed"})
		if consent := saga.pausedAt(2 * time.Minute); consent.Pause == nil || *consent.Pause != row.pause {
			t.Fatalf("a probe %s paused %+v%s", row.probe, consent, saga.journey())
		}
		if !saga.endedWith(before, row.code, 2*time.Minute) {
			t.Fatalf("a probe %s ended no Job with %s%s", row.probe, row.code, saga.journey())
		}
		stub.mu.Lock()
		probes := stub.probes
		stub.mu.Unlock()
		status := saga.status()
		if probes != row.probes || stub.reading("") != 0 || saga.chapterMarks() != 0 || status.Reading == nil || status.Reading.State != "paused" ||
			status.Reading.Pause == nil || *status.Reading.Pause != row.pause {
			t.Fatalf("a probe %s: probed %d times, asked %d, marked %d, the status %+v%s", row.probe, probes, stub.reading(""), saga.chapterMarks(), status.Reading, saga.journey())
		}
	}

	// The protocol is still unknown — no probe passed since the start: «Повторить» at the provider back probes it once and reads
	// the book.
	stub.set(func(stub *readingStub) { stub.probe, stub.probes = "", 0 })
	saga.setConsent(map[string]any{"state": "allowed"})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("«Повторить» did not read the book: %+v%s", status.Reading, saga.journey())
	}
	stub.mu.Lock()
	probes := stub.probes
	stub.mu.Unlock()
	if probes != 1 || stub.steps(coresteps.Card) != len(titles) || stub.steps(coresteps.CardPass) != len(titles) {
		t.Fatalf("probed %d times, asked %d cards and %d passes", probes, stub.steps(coresteps.Card), stub.steps(coresteps.CardPass))
	}
	for _, job := range saga.readingJobs(started) {
		if job.retries != 0 || job.code == "cards.incomplete" || job.code == "worker.retry_exhausted" {
			t.Fatalf("a Job went round: %s%s", job, saga.journey())
		}
	}
	process.stop()
}
