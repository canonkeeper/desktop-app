// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/coresteps"
)

// cards counts the calls of the card of a chapter and of its pass — the requests that read it, not the steps of the layer
// whose words name it too.
func (stub *readingStub) cards(chapter string) int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	count := 0
	for _, call := range stub.calls {
		if call.chapter == chapter && (call.step == string(coresteps.Card) || call.step == string(coresteps.CardPass)) {
			count++
		}
	}
	return count
}

// journalRows counts the rows of the journal of the reading of the saga: every request the gateway let go.
func (window readingWindow) journalRows() int {
	window.t.Helper()
	var count int
	if err := database(window.t, window.data).QueryRow(context.Background(), `
		SELECT count(*) FROM model_requests WHERE project_id = $1 AND purpose IN ('card', 'card_pass', 'names', 'layer_book',
		  'layer_reduce', 'layer_verify', 'layer_gap')`, window.project).Scan(&count); err != nil {
		window.t.Fatal(err)
	}
	return count
}

// settled waits until no request of the reading is at the stub and the count of the consent stands still.
func (window readingWindow) settled(timeout time.Duration) consentAnswer {
	window.t.Helper()
	deadline := time.Now().Add(timeout)
	last := window.consent()
	for {
		time.Sleep(time.Second)
		window.stub.mu.Lock()
		busy := window.stub.inFlight
		window.stub.mu.Unlock()
		consent := window.consent()
		if busy == 0 && consent.Spent == last.Spent {
			return consent
		}
		last = consent
		if time.Now().After(deadline) {
			window.t.Fatalf("the reading never settled: %+v%s", consent, window.journey())
		}
	}
}

// reserves are the reserves of the requests of the reading of the saga the gateway let go: the input by the size of each
// and the calibration — no check measured it, so the fallback — and the output by the limit of an answer.
func (window readingWindow) reserves(maxOutput int64) []int64 {
	window.t.Helper()
	rows, err := database(window.t, window.data).Query(context.Background(), `
		SELECT request_bytes FROM model_requests WHERE project_id = $1 AND purpose IN ('card', 'card_pass', 'names')`, window.project)
	if err != nil {
		window.t.Fatal(err)
	}
	defer rows.Close()
	var reserves []int64
	for rows.Next() {
		var size int64
		if err := rows.Scan(&size); err != nil {
			window.t.Fatal(err)
		}
		reserves = append(reserves, aisettings.Unseen(size, 0, 0).Input+maxOutput)
	}
	return reserves
}

// estimatedRows counts the rows of the journal of the saga by their outcome that are estimates — the provider sent no usage.
func (window readingWindow) estimatedRows(outcome string) int {
	window.t.Helper()
	var count int
	if err := database(window.t, window.data).QueryRow(context.Background(), `
		SELECT count(*) FROM model_requests WHERE project_id = $1 AND outcome = $2 AND estimated`, window.project, outcome).Scan(&count); err != nil {
		window.t.Fatal(err)
	}
	return count
}

// specs/2026-11-13-desk-consent.md CN3.3, CN3.5 with «Запросов одновременно» = 4 and a stub that holds its answers and sends
// no usage: at no moment do more requests stand at the stub than the sum lets go with the reserves of those that go; the
// count, an estimate, reaches the sum and does not pass it; «Пауза» in the middle of the stream counts the requests it cut by
// the estimate, and no request goes after it.
func TestTheReadingAtOnceStaysWithinItsSum(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава", "Глава 4. Дозор", "Глава 5. Рассвет",
		"Глава 6. Метель", "Глава 7. Обвал", "Глава 8. Ущелье"}
	stub := &readingStub{titles: titles, noUsage: true, delay: 1500 * time.Millisecond}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	const maxOutput = 1000
	connect(t, window, server.URL, map[string]any{"parallel": 4, "maxOutput": maxOutput})
	readingSettled(t, data, saga.project, 2*time.Minute)

	const stop = 10_000
	saga.setConsent(map[string]any{"state": "allowed", "stopAt": stop})
	saga.pausedAt(3 * time.Minute)
	consent := saga.settled(time.Minute)
	if consent.Pause == nil || *consent.Pause != "sum" || consent.Spent.Amount > stop || consent.Spent.Amount == 0 || !consent.Spent.Approximate {
		t.Fatalf("the reading at its sum: %+v%s", consent, saga.journey())
	}
	reserves := saga.reserves(maxOutput)
	least := reserves[0]
	for _, reserve := range reserves {
		least = min(least, reserve)
	}
	if stop >= 4*least {
		t.Fatalf("the sum %d lets four requests of %d go at once: the test proves nothing", stop, least)
	}
	stub.mu.Lock()
	most := stub.most
	stub.mu.Unlock()
	if most > int(stop/least) {
		t.Fatalf("%d requests stood at the stub at once; the sum %d with reserves of at least %d lets %d go%s", most, stop, least, stop/least,
			saga.journey())
	}
	t.Logf("at the sum: %d requests, at most %d at once, the least reserve %d, spent ≈ %d", len(reserves), most, least, consent.Spent.Amount)

	// «Пауза» in the middle of the stream: the requests it cut are counted by the estimate, none goes after it.
	stub.set(func(stub *readingStub) { stub.delay = 40 * time.Second })
	saga.setConsent(map[string]any{"state": "allowed", "continueBy": 1_000_000})
	deadline := time.Now().Add(time.Minute)
	for {
		stub.mu.Lock()
		busy := stub.inFlight
		stub.mu.Unlock()
		if busy > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reading did not go on%s", saga.journey())
		}
		time.Sleep(50 * time.Millisecond)
	}
	before := saga.consent()
	saga.setConsent(map[string]any{"state": "paused"})
	paused := saga.settled(2 * time.Minute)
	asked := stub.reading("")
	time.Sleep(3 * time.Second)
	if stub.reading("") != asked {
		t.Fatalf("%d requests went after the pause", stub.reading("")-asked)
	}
	if paused.Pause == nil || *paused.Pause != "author" || paused.Spent.Amount <= before.Spent.Amount || !paused.Spent.Approximate ||
		saga.estimatedRows("canceled") == 0 || saga.journalRows() != stub.reading("") {
		t.Fatalf("the pause in the stream: before %+v, after %+v, canceled rows %d, rows %d of %d requests%s", before, paused,
			saga.estimatedRows("canceled"), saga.journalRows(), stub.reading(""), saga.journey())
	}
	process.stop()
}

// chapterOf is the id of the chapter of the saga with the title.
func (window readingWindow) chapterOf(title string) string {
	window.t.Helper()
	var books struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	window.read("/api/v1/projects/"+window.project+"/books", &books)
	for _, book := range books.Data {
		var chapters struct {
			Data []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"data"`
		}
		window.read("/api/v1/books/"+book.ID+"/chapters", &chapters)
		for _, chapter := range chapters.Data {
			if chapter.Title == title {
				return chapter.ID
			}
		}
	}
	window.t.Fatalf("no chapter %q", title)
	return ""
}

// editChapter puts a new text into a chapter of the book the way the author does: the chapter opened for an edit, its text
// written, the chapter in the book replaced.
func (window readingWindow) editChapter(title, text string) {
	window.t.Helper()
	type draft struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	var opened, written draft
	chapter := window.chapterOf(title)
	response, raw := window.do(http.MethodPost, "/api/v1/projects/"+window.project+"/chapters/"+chapter+"/edit", window.csrf(), nil)
	if response.StatusCode != http.StatusOK || json.Unmarshal(raw, &opened) != nil {
		window.t.Fatalf("openChapterEdit = %d %s", response.StatusCode, raw)
	}
	response, raw = window.do(http.MethodPatch, "/api/v1/projects/"+window.project+"/chapter-drafts/"+opened.ID, window.csrf(),
		map[string]any{"expectedRevision": opened.Revision, "text": text})
	if response.StatusCode != http.StatusOK || json.Unmarshal(raw, &written) != nil {
		window.t.Fatalf("updateChapterDraft = %d %s", response.StatusCode, raw)
	}
	response, raw = window.do(http.MethodPost, "/api/v1/projects/"+window.project+"/chapter-drafts/"+opened.ID+"/replace-chapter", window.csrf(),
		map[string]any{"expectedRevision": written.Revision, "title": title})
	if response.StatusCode != http.StatusOK {
		window.t.Fatalf("replaceChapterFromDraft = %d %s", response.StatusCode, raw)
	}
}

// quiet waits a while and says whether the stub got no request of the reading meanwhile.
func (window readingWindow) quiet(asked int, wait time.Duration) bool {
	window.t.Helper()
	time.Sleep(wait)
	return window.stub.reading("") == asked
}

// CN3.1, CN3.2, CN3.4, CN3.7 with prices in rubles: a paused reading reads nothing after a restart and an edit of a chapter;
// «Продолжить» reads the edited chapter again under the same count; a new book reads under it too; the prices taken away
// under a sum in money pause the reading at «цены изменились» — no request goes, and «Продолжить» without a sum is refused.
func TestThePausedReadingWaitsAndGoesOnUnderItsCount(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава", "Глава 4. Дозор", "Глава 5. Рассвет"}
	stub := &readingStub{titles: titles}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles[:3]...)).watched(data, stub)
	priced := map[string]any{"parallel": 1, "maxOutput": 1000, "currency": "RUB", "priceInPerMillion": 30, "priceOutPerMillion": 120}
	connect(t, window, server.URL, priced)
	readingSettled(t, data, saga.project, 2*time.Minute)

	given := saga.setConsent(map[string]any{"state": "allowed", "stopAt": 1_000_000_000})
	if given.Unit != "RUB" || given.StopAt == nil || *given.StopAt != 1_000_000_000 {
		t.Fatalf("the consent = %+v", given)
	}
	saga.readThrough(5 * time.Minute)
	read := saga.consent()
	if read.Spent.Amount == 0 || read.Unit != "RUB" {
		t.Fatalf("the count after the reading = %+v", read)
	}

	// «Пауза», a restart and an edit of a chapter: nothing is read.
	saga.setConsent(map[string]any{"state": "paused"})
	process.stop()
	process = startBackend(t, binary, resources, data, nil)
	window = openWindow(t, process)
	saga.client = window
	asked, askedEdited, askedOthers := stub.reading(""), stub.cards(titles[1]), stub.cards(titles[0])+stub.cards(titles[2])
	editedAt := time.Now().Add(-time.Second)
	saga.editChapter(titles[1], "Вера спустилась со стены заставы. Глава 2. Перевал начиналась заново.")
	if status, code := cardsJobEnded(t, data, saga.project, editedAt, 2*time.Minute); status == "" {
		t.Fatalf("the cards Job of the edit never ended: %s%s", code, saga.journey())
	}
	if !saga.quiet(asked, 2*time.Second) {
		t.Fatalf("the paused reading read %d requests after a restart and an edit%s", stub.reading("")-asked, saga.journey())
	}
	if status := saga.status(); status.Reading == nil || status.Reading.State != "paused" || *status.Reading.Pause != "author" {
		t.Fatalf("the status of the paused reading = %+v", status.Reading)
	}

	// «Продолжить»: the edited chapter is read again under the same count.
	more := saga.setConsent(map[string]any{"state": "allowed"})
	saga.readThrough(5 * time.Minute)
	again := saga.consent()
	if stub.cards(titles[1]) != askedEdited+2 || stub.cards(titles[0])+stub.cards(titles[2]) != askedOthers ||
		again.Spent.Amount <= read.Spent.Amount || *again.StopAt != *given.StopAt || *more.StopAt != *given.StopAt {
		t.Fatalf("the edited chapter after «Продолжить»: %d calls of it (were %d), of the others %d (were %d), consent %+v%s",
			stub.cards(titles[1]), askedEdited, stub.cards(titles[0])+stub.cards(titles[2]), askedOthers, again, saga.journey())
	}

	// A new book reads under the same consent and count. Its cards Job follows its text index, so the test waits for the
	// calls of its chapter: the first cards Job that ends after the import may be one of the text there was, and the
	// status says «done» until the new chapters are counted.
	bookAt := time.Now().Add(-time.Second)
	window.importFileAs(saga.project, "Дозор.md", bookOf(titles[3]), "import-of-the-watch")
	for deadline := time.Now().Add(5 * time.Minute); stub.cards(titles[3]) < 2; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the new book was never read: %d calls of it%s", stub.cards(titles[3]), saga.journey())
		}
	}
	saga.readThrough(5 * time.Minute)
	if status, code := cardsJobEnded(t, data, saga.project, bookAt, 3*time.Minute); status != "succeeded" {
		t.Fatalf("the cards Job of the new book = %s %s%s", status, code, saga.journey())
	}
	withBook := saga.consent()
	if stub.cards(titles[3]) != 2 || withBook.Spent.Amount <= again.Spent.Amount || *withBook.StopAt != *given.StopAt {
		t.Fatalf("the new book: %d calls of it, consent %+v", stub.cards(titles[3]), withBook)
	}

	// The prices taken away under a sum in rubles: «цены изменились», nothing is read, «Продолжить» wants a sum.
	connect(t, window, server.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	if status := saga.status(); status.Reading == nil || status.Reading.State != "paused" || *status.Reading.Pause != "prices" ||
		status.Preparation != nil && status.Preparation.Waiting != "paused" {
		t.Fatalf("the status without the prices = %+v", status.Reading)
	}
	asked = stub.reading("")
	importedAt := time.Now().Add(-time.Second)
	window.importFileAs(saga.project, "Рассвет.md", bookOf(titles[4]), "import-of-the-dawn")
	// The check pauses the reading at «цены изменились» (reading.paused); a cards Job after it finds the consent paused.
	if status, code := cardsJobEnded(t, data, saga.project, importedAt, 2*time.Minute); status != "failed" || code != "reading.paused" && code != "cards.consent" {
		t.Fatalf("the cards Job of a new book without the prices of the sum = %s %s%s", status, code, saga.journey())
	}
	if !saga.quiet(asked, time.Second) || stub.cards(titles[4]) != 0 {
		t.Fatalf("a new book was read without the prices of the sum: %d requests%s", stub.reading("")-asked, saga.journey())
	}
	response, raw := window.do(http.MethodPut, "/api/v1/projects/"+saga.project+"/reading/consent", window.csrf(), map[string]any{"state": "allowed"})
	if response.StatusCode != http.StatusBadRequest || !bytes.Contains(raw, []byte("stopAt")) {
		t.Fatalf("«Продолжить» without a sum after the prices went = %d %s", response.StatusCode, raw)
	}
	tokens := saga.setConsent(map[string]any{"state": "allowed", "stopAt": 100_000_000})
	saga.readThrough(5 * time.Minute)
	if tokens.Unit != "tokens" || stub.cards(titles[4]) != 2 {
		t.Fatalf("a sum named again in tokens = %+v, %d calls of the new book", tokens, stub.cards(titles[4]))
	}
	process.stop()
}

// CN2.2–CN2.4: the sample of a saga of two chapters reads both through a restart in its middle — the catch-up of the start does
// not take it and reads nothing — and builds no names and no threads: the saga then waits for the consent with its names and
// threads; without the helper the sample is refused. The book imported during the sample of another saga is not taken by it.
func TestTheSampleOfTwoChaptersGoesThroughARestart(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	two := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал"}
	four := []string{"Глава 11. Утро", "Глава 12. Ветер", "Глава 13. Снег", "Глава 14. Лёд"}
	added := "Глава 21. Оттепель"
	stub := &readingStub{titles: append(append(append([]string(nil), two...), four...), added), hold: two[1]}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(two...)).watched(data, stub)
	if response, raw := saga.post("/api/v1/projects/"+saga.project+"/reading/sample", map[string]any{}, "sample-without-a-helper"); response.StatusCode != http.StatusConflict ||
		!containsCode(raw, "helper.not_connected") {
		t.Fatalf("a sample without the helper = %d %s", response.StatusCode, raw)
	}
	connect(t, window, server.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	readingSettled(t, data, saga.project, 2*time.Minute)

	type sampleAnswer struct {
		State   string `json:"state"`
		Done    int    `json:"done"`
		Total   int    `json:"total"`
		Seconds int    `json:"seconds"`
	}
	sampleOf := func(saga readingWindow) sampleAnswer {
		t.Helper()
		var sample sampleAnswer
		saga.read("/api/v1/projects/"+saga.project+"/reading/sample", &sample)
		return sample
	}
	held := func(saga readingWindow, done int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for {
			stub.mu.Lock()
			holding := stub.holding
			stub.mu.Unlock()
			if sample := sampleOf(saga); holding && sample.Done >= done {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the sample never reached its held chapter: %+v%s", sampleOf(saga), saga.journey())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	ended := func(saga readingWindow, timeout time.Duration) sampleAnswer {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			sample := sampleOf(saga)
			if sample.State == "succeeded" || sample.State == "failed" || sample.State == "canceled" {
				return sample
			}
			if time.Now().After(deadline) {
				t.Fatalf("the sample never ended: %+v%s", sample, saga.journey())
			}
			time.Sleep(300 * time.Millisecond)
		}
	}

	started := time.Now()
	if response, raw := saga.post("/api/v1/projects/"+saga.project+"/reading/sample", map[string]any{}, "sample-of-two"); response.StatusCode != http.StatusAccepted {
		t.Fatalf("startReadingSample = %d %s", response.StatusCode, raw)
	}
	held(saga, 1)
	// A restart in the middle of the sample: the catch-up of the start reads nothing, the sample reads on.
	process.stop()
	stopped := time.Now()
	stub.set(func(stub *readingStub) { stub.hold = "" })
	process = startBackend(t, binary, resources, data, nil)
	down := time.Since(stopped)
	window = openWindow(t, process)
	saga.client = window
	sample := ended(saga, 3*time.Minute)
	// The first chapter was read before the restart; the second — its card cut by the restart — once more, by the sample:
	// the catch-up of the start asked nothing.
	if sample.State != "succeeded" || sample.Done != 2 || sample.Total != 2 || stub.cards(two[0]) != 2 || stub.cards(two[1]) != 3 {
		t.Fatalf("the sample after a restart = %+v, the chapters asked %d and %d times%s", sample, stub.cards(two[0]), stub.cards(two[1]),
			saga.journey())
	}
	if took := time.Since(started) - down; sample.Seconds > int(took.Seconds())+1 {
		t.Fatalf("the time of the sample %d s counts the restart: %s without the %s of it", sample.Seconds, took, down)
	}
	if stub.steps(coresteps.Names) != 0 || stub.steps(coresteps.LayerBook) != 0 {
		t.Fatalf("the sample built the names (%d) or the threads (%d)", stub.steps(coresteps.Names), stub.steps(coresteps.LayerBook))
	}
	if status := saga.status(); status.Reading == nil || status.Reading.State != "waiting_consent" || !status.Reading.NamesWait ||
		status.Reading.ChaptersRead != 2 {
		t.Fatalf("the status after the sample of two = %+v", status.Reading)
	}
	if consent := saga.consent(); consent.State != "none" {
		t.Fatalf("the sample gave a consent: %+v", consent)
	}

	// The cards Job of a book imported during the sample of another saga does not take the sample and reads nothing.
	other := openSaga(t, window, "Ветреный берег", bookOf(four...)).watched(data, stub)
	_, _ = cardsJobEnded(t, data, other.project, time.Now().Add(-time.Minute), 2*time.Minute)
	stub.set(func(stub *readingStub) { stub.hold, stub.holding = four[1], false })
	if response, raw := other.post("/api/v1/projects/"+other.project+"/reading/sample", map[string]any{}, "sample-of-four"); response.StatusCode != http.StatusAccepted {
		t.Fatalf("the second sample = %d %s", response.StatusCode, raw)
	}
	held(other, 1)
	importedAt := time.Now().Add(-time.Second)
	window.importFileAs(other.project, "Оттепель.md", bookOf(added), "import-during-the-sample")
	// The cards Job after the index waits beside the sample — one cards Job of a saga is in work — and is not it.
	connection := database(t, data)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var waiting int
		if err := connection.QueryRow(context.Background(), `
			SELECT count(*) FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND created_at > $2 AND NOT (subject_json ? 'sample')`,
			other.project, importedAt).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no cards Job after the index of the book imported during the sample%s", other.journey())
		}
		time.Sleep(200 * time.Millisecond)
	}
	stub.set(func(stub *readingStub) { stub.hold = "" })
	if sample := ended(other, 3*time.Minute); sample.State != "succeeded" || sample.Total != 3 {
		t.Fatalf("the second sample = %+v%s", sample, other.journey())
	}
	if status, code := cardsJobEnded(t, data, other.project, importedAt, 2*time.Minute); status != "failed" || code != "cards.consent" {
		t.Fatalf("the cards Job after the index during the sample = %s %s%s", status, code, other.journey())
	}
	if stub.cards(four[3]) != 0 || stub.cards(added) != 0 {
		t.Fatalf("the fourth chapter was asked %d times, the new book %d%s", stub.cards(four[3]), stub.cards(added), other.journey())
	}
	process.stop()
}

// openSagaFile opens a saga file of one book of chapters «Застава N» in the window (specs/2026-11-13-desk-sagafile.md).
func openSagaFile(t *testing.T, window *client, chapters int) readingWindow {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "Северный предел.cksaga")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(sagaFile(t, 1, chapters, 3, "Ойвинградъ"))
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, window.origin+"/api/v1/saga-archives", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Origin", window.origin)
	request.Header.Set("X-CSRF-Token", window.csrf())
	request.Header.Set("Idempotency-Key", fmt.Sprintf("open-a-saga-of-%d", chapters))
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: window.cookie})
	response, err := window.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var opened struct {
		ProjectID string `json:"projectId"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(answer, &opened) != nil {
		t.Fatalf("openSagaArchive = %d %s", response.StatusCode, answer)
	}
	return readingWindow{client: window, t: t, project: opened.ProjectID}
}

// CN3.1, CN3.8: a saga opened from a file has no consent; while it reads without a pace its time is the estimate's; the sum
// that ends on the step of the layer pauses the reading with every chapter read and the names and threads waiting.
func TestTheSumThatEndsOnTheLayerSaysTheChaptersRead(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	stub := &readingStub{titles: []string{"Застава 1", "Застава 2"}, delay: time.Second}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	connect(t, window, server.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	saga := openSagaFile(t, window, 2).watched(data, stub)
	if consent := saga.consent(); consent.State != "none" {
		t.Fatalf("a saga from a file has a consent: %+v", consent)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if status := saga.status(); status.Reading != nil && status.Reading.State == "waiting_consent" && status.Reading.ChaptersTotal == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the saga from the file never waited for the consent: %+v", saga.status().Reading)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Two cards with their passes and the names: 5 × 5 500 tokens; the layer does not fit in what is left.
	const stop = 5*(stubInput+stubOutput) + 999
	saga.setConsent(map[string]any{"state": "allowed", "stopAt": stop})
	deadline = time.Now().Add(time.Minute)
	for {
		var status struct {
			Reading *struct {
				State            string `json:"state"`
				RemainingSeconds *int   `json:"remainingSeconds"`
			} `json:"reading"`
		}
		saga.read("/api/v1/projects/"+saga.project+"/indexing-status", &status)
		if status.Reading != nil && status.Reading.State == "reading" && status.Reading.RemainingSeconds != nil && *status.Reading.RemainingSeconds > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reading had no time by the estimate: %+v%s", status.Reading, saga.journey())
		}
		time.Sleep(100 * time.Millisecond)
	}
	paused := saga.pausedAt(3 * time.Minute)
	if paused.Pause == nil || *paused.Pause != "sum" || paused.Spent.Amount > stop {
		t.Fatalf("the pause = %+v%s", paused, saga.journey())
	}
	status := saga.status()
	if status.Reading == nil || status.Reading.State != "paused" || *status.Reading.Pause != "sum" || status.Reading.ChaptersRead != 2 ||
		status.Reading.ChaptersTotal != 2 || !status.Reading.ThreadsWait || stub.steps(coresteps.Names) != 1 ||
		stub.steps(coresteps.LayerBook) != 0 {
		t.Fatalf("the status at the sum on the layer = %+v; names %d, layer %d%s", status.Reading, stub.steps(coresteps.Names),
			stub.steps(coresteps.LayerBook), saga.journey())
	}
	process.stop()
}
