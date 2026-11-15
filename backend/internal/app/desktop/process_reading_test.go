// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/coresteps"
)

// readingStub is the provider of the author in the process tests of the reading of the books
// (specs/2026-11-13-desk-consent.md): Chat Completions that answers the steps of the core with a constant usage — 5 000 in
// and 500 out — and records the step and the chapter of every call; a test turns its answers into the refusals of a
// provider, cuts the cards of a chapter or holds them.
type readingStub struct {
	mu       sync.Mutex
	titles   []string
	calls    []readingCall
	mode     string
	retried  bool
	cut      string
	hold     string
	holding  bool
	noUsage  bool
	refusals int
	cuts     int
	// delay holds every answer of the reading for a while; inFlight and most count the requests of the reading at the stub
	// at once.
	delay          time.Duration
	inFlight, most int
	// names and book answer the register of names and the question of a book of the layer instead of JSON when set
	// (specs/2026-11-15-desk-reading-rest.md RR1).
	names, book string
	// silent is how the provider stops answering the reading — "503", "torn": a stream broken mid-answer, or "reset": the
	// connection reset mid-answer — once it answered quiet more requests of the reading
	// (specs/2026-11-15-desk-reading-no-answer.md NA1).
	silent string
	quiet  int
	// probe is how the provider answers the probe of «Определить» — /responses with an empty request: "503", "401", "drop" —
	// the connection closed without an answer —, or 404 with none: the provider is Chat Completions; probes counts them.
	probe  string
	probes int
}

type readingCall struct{ step, chapter string }

const (
	stubInput  = 5_000
	stubOutput = 500
)

func (stub *readingStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/responses" {
		stub.mu.Lock()
		stub.probes++
		probe := stub.probe
		stub.mu.Unlock()
		switch probe {
		case "503":
			http.Error(w, `{"error":{"message":"The server is overloaded"}}`, http.StatusServiceUnavailable)
		case "401":
			http.Error(w, `{"error":{"message":"Incorrect API key provided"}}`, http.StatusUnauthorized)
		case "drop":
			if connection, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = connection.Close()
			}
		default:
			http.NotFound(w, r)
		}
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Messages) == 0 {
		http.Error(w, "not Chat Completions", http.StatusBadRequest)
		return
	}
	step := stepOf(textOf(body.Messages[0].Content))
	stub.mu.Lock()
	chapter := ""
	for _, title := range stub.titles {
		if strings.Contains(string(raw), title) {
			chapter = title
			break
		}
	}
	stub.calls = append(stub.calls, readingCall{step: step, chapter: chapter})
	mode, cut, hold, noUsage, delay, names, book := stub.mode, stub.cut, stub.hold, stub.noUsage, stub.delay, stub.names, stub.book
	if delay > 0 && step != "chat" {
		stub.inFlight++
		stub.most = max(stub.most, stub.inFlight)
		defer func() {
			stub.mu.Lock()
			stub.inFlight--
			stub.mu.Unlock()
		}()
	}
	if mode == "retry" && !stub.retried {
		stub.retried = true
	} else if mode == "retry" {
		mode = ""
	}
	if hold != "" && chapter == hold && step == string(coresteps.Card) {
		stub.holding = true
	}
	silent := ""
	if stub.silent != "" && step != "chat" && step != string(coresteps.Scan) && step != string(coresteps.Materials) {
		if stub.quiet > 0 {
			stub.quiet--
		} else {
			silent = stub.silent
		}
	}
	stub.mu.Unlock()
	refuse := func(status int, body string) {
		stub.mu.Lock()
		stub.refusals++
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
	switch silent {
	case "503":
		refuse(http.StatusServiceUnavailable, `{"error":{"message":"The server is overloaded","type":"server_error"}}`)
		return
	case "torn":
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"{\"summary"}}]}`+"\n\n")
		return
	case "reset":
		// The answer begins, and the connection is reset mid-answer.
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"{\"summary"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		if connection, _, err := w.(http.Hijacker).Hijack(); err == nil {
			if tcp, ok := connection.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0)
			}
			_ = connection.Close()
		}
		return
	}
	switch mode {
	case "402":
		refuse(http.StatusPaymentRequired, `{"error":{"message":"Insufficient Balance","type":"payment_required"}}`)
		return
	case "quota":
		refuse(http.StatusTooManyRequests, `{"error":{"message":"You exceeded your current quota","code":"insufficient_quota"}}`)
		return
	case "401":
		refuse(http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided"}}`)
		return
	case "model":
		refuse(http.StatusNotFound, `{"error":{"message":"The model north-cards does not exist","code":"model_not_found"}}`)
		return
	case "retry":
		w.Header().Set("Retry-After", "1")
		refuse(http.StatusTooManyRequests, `{"error":{"message":"slow down","code":"rate_limit_exceeded"}}`)
		return
	}
	if hold != "" && chapter == hold && step == string(coresteps.Card) {
		// The card is held until the test lets it go — clears the hold — or for 90 seconds.
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
			stub.mu.Lock()
			held := stub.hold == chapter
			stub.mu.Unlock()
			if !held {
				break
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if delay > 0 && step != "chat" {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(delay):
		}
	}
	answer, finish := `{}`, "stop"
	switch coresteps.Name(step) {
	case coresteps.Card:
		answer = `{"summary": "Пересказ главы", "characters": [{"name": "Вера", "p": 1}], "events": [{"event": "Сигнал над перевалом", "p": 1}]}`
		if chapter != "" && chapter == cut {
			answer, finish = `{"summary": "Пересказ`, "length"
			stub.mu.Lock()
			stub.cuts++
			stub.mu.Unlock()
		}
	case coresteps.Names:
		answer = `{"persons":[]}`
		if names != "" {
			answer = names
		}
	case coresteps.LayerBook:
		answer = `{"arcs":[],"threads":[]}`
		if book != "" {
			answer = book
		}
	case coresteps.LayerReduce, coresteps.LayerGap:
		answer = `{"open":[]}`
	case coresteps.LayerVerify:
		answer = `{"v":"keep"}`
	case "chat":
		answer = "Застава стоит у перевала."
	}
	chunk := map[string]any{"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": answer}, "finish_reason": finish}}}
	if !noUsage {
		chunk["usage"] = map[string]any{"prompt_tokens": stubInput, "completion_tokens": stubOutput}
	}
	encoded, _ := json.Marshal(chunk)
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
}

// stepOf is the step of the core whose words begin the instructions of a call, or the chat.
func stepOf(system string) string {
	if found, ok := coresteps.Of(system); ok {
		return string(found.Name)
	}
	for _, step := range coresteps.All() {
		words := []rune(step.Instructions)
		if len(words) > 120 && strings.HasPrefix(system, string(words[:120])) {
			return string(step.Name)
		}
	}
	return "chat"
}

// textOf is the text of a message of Chat Completions: a string, or its parts of text one after another.
func textOf(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var text strings.Builder
		for _, part := range value {
			if object, ok := part.(map[string]any); ok {
				if words, ok := object["text"].(string); ok {
					text.WriteString(words)
				}
			}
		}
		return text.String()
	default:
		return ""
	}
}

func (stub *readingStub) set(change func(*readingStub)) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	change(stub)
}

// reading counts the calls of the reading of the books — every step of the core but scan and the materials — and of a
// chapter, when one is named.
func (stub *readingStub) reading(chapter string) int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	count := 0
	for _, call := range stub.calls {
		if call.step != "chat" && call.step != string(coresteps.Scan) && call.step != string(coresteps.Materials) && (chapter == "" || call.chapter == chapter) {
			count++
		}
	}
	return count
}

func (stub *readingStub) steps(step coresteps.Name) int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	count := 0
	for _, call := range stub.calls {
		if call.step == string(step) {
			count++
		}
	}
	return count
}

// bookOf is a book of chapters with their titles, each a few sentences long.
func bookOf(titles ...string) string {
	var book strings.Builder
	for _, title := range titles {
		fmt.Fprintf(&book, "# %s\n\nВера поднялась на стену заставы, когда над перевалом погасли сигнальные огни. %s начиналась тихо.\n\n", title, title)
	}
	return book.String()
}

// readingWindow is a window of the reading of the books: the saga, the stub and what the operations answer.
type readingWindow struct {
	*client
	t       *testing.T
	project string
	data    string
	stub    *readingStub
}

// journey tells what the reading of the saga did when a test of it fails: the Jobs of the reading and the calls of the stub.
func (window readingWindow) journey() string {
	var story strings.Builder
	if window.data != "" {
		rows, err := database(window.t, window.data).Query(context.Background(), `
			SELECT kind, status, coalesce(error_json->>'code', ''), retry_count FROM jobs
			WHERE project_id = $1 AND kind IN ('chapter_cards', 'saga_layer') ORDER BY created_at`, window.project)
		if err == nil {
			for rows.Next() {
				var kind, status, code string
				var retries int
				if rows.Scan(&kind, &status, &code, &retries) == nil {
					fmt.Fprintf(&story, "\n  %s %s %s retries %d", kind, status, code, retries)
				}
			}
			rows.Close()
		}
	}
	if window.stub != nil {
		window.stub.mu.Lock()
		steps := map[string]int{}
		for _, call := range window.stub.calls {
			steps[call.step]++
		}
		window.stub.mu.Unlock()
		fmt.Fprintf(&story, "\n  calls %v", steps)
	}
	return story.String()
}

type consentAnswer struct {
	State  string  `json:"state"`
	Reason *string `json:"reason"`
	Pause  *string `json:"pause"`
	Unit   string  `json:"unit"`
	StopAt *int64  `json:"stopAt"`
	Step   *int64  `json:"step"`
	Spent  struct {
		Amount      int64 `json:"amount"`
		Approximate bool  `json:"approximate"`
	} `json:"spent"`
}

type statusAnswer struct {
	Preparation *struct {
		Waiting string `json:"waiting"`
	} `json:"preparation"`
	Reading *struct {
		State         string  `json:"state"`
		Reason        *string `json:"reason"`
		Pause         *string `json:"pause"`
		ChaptersRead  int     `json:"chaptersRead"`
		ChaptersTotal int     `json:"chaptersTotal"`
		NamesWait     bool    `json:"namesWait"`
		ThreadsWait   bool    `json:"threadsWait"`
		UnreadRest    *struct {
			Names   *string `json:"names"`
			Threads *string `json:"threads"`
		} `json:"unreadRest"`
		Unread *struct {
			Chapters int    `json:"chapters"`
			Reason   string `json:"reason"`
		} `json:"unread"`
		Spent  int64  `json:"spent"`
		StopAt *int64 `json:"stopAt"`
	} `json:"reading"`
}

func (window readingWindow) consent() consentAnswer {
	window.t.Helper()
	var consent consentAnswer
	window.read("/api/v1/projects/"+window.project+"/reading/consent", &consent)
	return consent
}

func (window readingWindow) status() statusAnswer {
	window.t.Helper()
	var status statusAnswer
	window.read("/api/v1/projects/"+window.project+"/indexing-status", &status)
	return status
}

func (window readingWindow) setConsent(body map[string]any) consentAnswer {
	window.t.Helper()
	response, raw := window.do(http.MethodPut, "/api/v1/projects/"+window.project+"/reading/consent", window.csrf(), body)
	var consent consentAnswer
	if response.StatusCode != http.StatusOK || json.Unmarshal(raw, &consent) != nil {
		window.t.Fatalf("setReadingConsent %v = %d %s", body, response.StatusCode, raw)
	}
	return consent
}

// pausedAt waits for the consent of the saga to stand paused and answers it.
func (window readingWindow) pausedAt(timeout time.Duration) consentAnswer {
	window.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		consent := window.consent()
		if consent.State == "paused" {
			return consent
		}
		if time.Now().After(deadline) {
			window.t.Fatalf("the reading never paused: %+v%s", consent, window.journey())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// read waits until the status says nothing of the reading waits: every chapter read, the names and the threads built.
func (window readingWindow) readThrough(timeout time.Duration) statusAnswer {
	window.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		status := window.status()
		if status.Reading == nil || status.Reading.State == "done" {
			return status
		}
		if time.Now().After(deadline) {
			window.t.Fatalf("the reading never ended: %+v%s", *status.Reading, window.journey())
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// openSaga starts a saga in the window and imports a book of its chapters.
func openSaga(t *testing.T, window *client, title string, book string) readingWindow {
	t.Helper()
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": title})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFileAs(project.ID, title+".md", book, "import-of-"+project.ID)
	return readingWindow{client: window, t: t, project: project.ID}
}

// endedWith waits for a cards Job of the saga queued after a moment to end with the code.
func (window readingWindow) endedWith(after time.Time, code string, timeout time.Duration) bool {
	window.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var found bool
		err := database(window.t, window.data).QueryRow(context.Background(), `
			SELECT EXISTS (SELECT 1 FROM jobs WHERE project_id = $1 AND kind = 'chapter_cards' AND created_at > $2
			  AND status = 'failed' AND error_json->>'code' = $3)`, window.project, after, code).Scan(&found)
		if err == nil && found {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// watched is the saga with the data directory and the stub its failures tell about.
func (window readingWindow) watched(data string, stub *readingStub) readingWindow {
	window.data, window.stub = data, stub
	return window
}

// connect sets «Свой ИИ» at the stub: one request at a time and a short answer, without prices unless named.
func connect(t *testing.T, window *client, stub string, advanced map[string]any) {
	t.Helper()
	connectBy(t, window, stub, "chat", advanced)
}

// connectBy is connect with the protocol named — "auto" has the gateway find it.
func connectBy(t *testing.T, window *client, stub, protocol string, advanced map[string]any) {
	t.Helper()
	settings := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub + "/v1", "protocol": protocol, "chatModel": "north-chat", "cardsModel": "north-cards",
		"advanced": advanced,
	}}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
}

func openWindow(t *testing.T, process *backend) *client {
	t.Helper()
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	return window
}

// specs/2026-11-13-desk-consent.md CN3.1, CN3.2, CN3.3, CN3.7, CN3.8, CN1.3 on the built backend of the app with a stub of
// the provider, one request at a time: before the consent nothing is read and the estimate counts the chapters; a sum lets
// the reading go only while what was spent and the reserve of the next request are within it — no request goes over, the
// consent stands at «сумма», and its count is the stub's usage; a restart reads nothing; «ещё на X» reads the rest under the
// same count; another provider is no consent — no request of the reading goes to it.
func TestTheReadingStopsWithinItsSum(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава", "Глава 4. Дозор"}
	stub := &readingStub{titles: titles}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	saved := process.catchUps()
	connect(t, window, server.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	process.caughtUp(saved, 1, 2*time.Minute)
	readingSettled(t, data, saga.project, 2*time.Minute)
	if stub.reading("") != 0 {
		t.Fatal("the books were read before the consent")
	}
	if status := saga.status(); status.Reading == nil || status.Reading.State != "waiting_consent" || status.Preparation == nil || status.Preparation.Waiting != "consent" {
		t.Fatalf("the status before the consent = %+v %+v", status.Preparation, status.Reading)
	}
	var estimate struct {
		Chapters       int    `json:"chapters"`
		Unit           string `json:"unit"`
		SampleChapters int    `json:"sampleChapters"`
		Local          bool   `json:"local"`
	}
	window.read("/api/v1/projects/"+saga.project+"/reading/estimate", &estimate)
	if estimate.Chapters != 4 || estimate.Unit != "tokens" || estimate.SampleChapters != 3 || !estimate.Local {
		t.Fatalf("the estimate = %+v", estimate)
	}

	const stop = 20_000
	saga.setConsent(map[string]any{"state": "allowed", "stopAt": stop})
	paused := saga.pausedAt(2 * time.Minute)
	sent := stub.reading("")
	if paused.Pause == nil || *paused.Pause != "sum" || sent == 0 || sent*(stubInput+stubOutput) > stop ||
		paused.Spent.Amount != int64(sent*(stubInput+stubOutput)) || paused.Spent.Amount > stop || paused.Spent.Approximate {
		t.Fatalf("the reading at its sum: %d requests, consent %+v", sent, paused)
	}
	// CN3.5: the pass of a chapter goes before a new card — the sum of a few requests reads a chapter whole.
	if status := saga.status(); status.Reading == nil || status.Reading.State != "paused" || *status.Reading.Pause != "sum" ||
		status.Reading.Spent != paused.Spent.Amount || status.Reading.ChaptersRead < 1 {
		t.Fatalf("the status at the sum = %+v%s", status.Reading, saga.journey())
	}

	// A restart reads nothing: the catch-up of the start queues the cards Job of a paused saga only for its map, which it
	// builds without the model (specs/2026-11-15-desk-reading-queue.md RQ2.1).
	process.stop()
	process = startBackend(t, binary, resources, data, nil)
	window = openWindow(t, process)
	saga.client = window
	time.Sleep(2 * time.Second)
	if stub.reading("") != sent {
		t.Fatalf("the restart read %d requests", stub.reading("")-sent)
	}
	if consent := saga.consent(); consent.State != "paused" || *consent.Pause != "sum" {
		t.Fatalf("the consent after the restart = %+v", consent)
	}

	// «ещё на X» reads the rest without a restart, under the same count.
	more := saga.setConsent(map[string]any{"state": "allowed", "continueBy": 10_000_000})
	if more.StopAt == nil || *more.StopAt != paused.Spent.Amount+10_000_000 || *more.Step != 10_000_000 {
		t.Fatalf("«ещё на X» = %+v", more)
	}
	saga.readThrough(5 * time.Minute)
	if stub.steps(coresteps.Card) < 4 {
		t.Fatalf("the rest was not read: %d cards", stub.steps(coresteps.Card))
	}
	after := saga.consent()
	if after.Spent.Amount <= paused.Spent.Amount || after.Spent.Amount != int64(stub.reading("")*(stubInput+stubOutput)) || *after.StopAt != *more.StopAt {
		t.Fatalf("the count after «ещё на X» = %+v, %d requests", after, stub.reading(""))
	}

	// Another provider is no consent: a new book reads nothing at the new host, and the status says why.
	other := &readingStub{titles: []string{"Глава 5. Рассвет"}}
	otherServer := httptest.NewServer(other)
	defer otherServer.Close()
	connect(t, window, otherServer.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	movedAt := time.Now().Add(-time.Second)
	window.importFileAs(saga.project, "Рассвет.md", bookOf("Глава 5. Рассвет"), "import-of-the-dawn")
	if status, code := cardsJobEnded(t, data, saga.project, movedAt, 2*time.Minute); status != "failed" || code != "cards.consent" {
		t.Fatalf("the cards Job at another provider = %s %s", status, code)
	}
	if other.reading("") != 0 {
		t.Fatalf("the new provider got %d requests of the reading", other.reading(""))
	}
	if status := saga.status(); status.Reading == nil || status.Reading.State != "waiting_consent" || status.Reading.Reason == nil || *status.Reading.Reason != "provider_changed" {
		t.Fatalf("the status at another provider = %+v", status.Reading)
	}
	if consent := saga.consent(); consent.State != "none" || consent.Reason == nil || *consent.Reason != "provider_changed" {
		t.Fatalf("the consent at another provider = %+v", consent)
	}
	process.stop()
}

// CN3.4, CN3.5, CN3.6: the refusals of the provider pause the reading with their reasons at once — no request is repeated —
// and the Job ends with them; «Продолжить» reads on; a wait the provider asks for is waited and the book read through; a
// chapter whose answers are always cut does not read and is not asked again after restarts, the status names it, and the
// repeat the author asks reads it; no connection pauses the reading once the repeats of a request are over.
func TestTheRefusalsOfTheProviderPauseTheReading(t *testing.T) {
	_, resources := desktopBinary(t)
	binary := program(t, programVariant{name: "fast-repeats", retryPause: "20ms"})
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава"}
	stub := &readingStub{titles: titles}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(stub)
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...)).watched(data, stub)
	connect(t, window, server.URL, map[string]any{"parallel": 1, "maxOutput": 1000})
	readingSettled(t, data, saga.project, 2*time.Minute)
	for _, refusal := range []struct{ mode, pause, code string }{
		{"402", "funds", "reading.funds"}, {"quota", "funds", "reading.funds"}, {"401", "key", "reading.key"}, {"model", "model", "reading.model"},
	} {
		stub.set(func(stub *readingStub) { stub.mode, stub.refusals = refusal.mode, 0 })
		before := time.Now().Add(-time.Second)
		saga.setConsent(map[string]any{"state": "allowed"})
		consent := saga.pausedAt(2 * time.Minute)
		if consent.Pause == nil || *consent.Pause != refusal.pause {
			t.Fatalf("%s paused %+v", refusal.mode, consent)
		}
		if !saga.endedWith(before, refusal.code, 2*time.Minute) {
			t.Fatalf("%s ended no Job with %s:%s", refusal.mode, refusal.code, saga.journey())
		}
		stub.mu.Lock()
		refusals := stub.refusals
		stub.mu.Unlock()
		if refusals != 1 {
			t.Fatalf("%s was asked %d times", refusal.mode, refusals)
		}
	}

	// A wait the provider asks for is waited; a chapter whose answers are always cut does not read.
	stub.set(func(stub *readingStub) { stub.mode, stub.cut = "retry", titles[1] })
	saga.setConsent(map[string]any{"state": "allowed"})
	status := saga.readThrough(5 * time.Minute)
	if status.Reading == nil || status.Reading.State != "done" || status.Reading.Unread == nil || status.Reading.Unread.Chapters != 1 ||
		status.Reading.Unread.Reason != "cut_answer" {
		t.Fatalf("the status with a chapter that did not read = %+v", status.Reading)
	}
	stub.mu.Lock()
	cut := stub.cuts
	stub.mu.Unlock()
	if cut == 0 || cut > 3 {
		t.Fatalf("the cut chapter was answered %d times", cut)
	}
	// specs/2026-11-15-desk-reading-rest.md RR1.8: the catch-up of the start knows the mark of the chapter — no cards Job is
	// queued for it at all. The status says «done» by the cards, while the Job that read may still build the register of the
	// names and the map: a stop in that moment leaves the next start a Job to queue for them, so the reading stands still first.
	readingSettled(t, data, saga.project, 2*time.Minute)
	for range 2 {
		process.stop()
		restartedAt := time.Now().Add(-time.Second)
		process = startBackend(t, binary, resources, data, nil)
		window = openWindow(t, process)
		saga.client = window
		time.Sleep(3 * time.Second)
		if jobs := saga.readingJobsSince(restartedAt); jobs != 0 {
			t.Fatalf("the restart queued %d Jobs of the reading for a marked chapter%s", jobs, saga.journey())
		}
	}
	stub.mu.Lock()
	again := stub.cuts
	stub.mu.Unlock()
	if again != cut {
		t.Fatalf("the restarts asked the cut chapter %d times more", again-cut)
	}
	stub.set(func(stub *readingStub) { stub.cut = "" })
	saga.setConsent(map[string]any{"state": "allowed", "retryUnread": true})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("the repeat did not read the chapter: %+v", status.Reading)
	}

	// No connection: the repeats of one request, then the pause «связь».
	address := listener.Addr().String()
	server.Close()
	newBook := []string{"Глава 4. Дозор"}
	stub.set(func(stub *readingStub) { stub.titles = append(stub.titles, newBook...) })
	window.importFileAs(saga.project, "Дозор.md", bookOf(newBook...), "import-of-the-watch")
	consent := saga.pausedAt(3 * time.Minute)
	if consent.Pause == nil || *consent.Pause != "connection" {
		t.Fatalf("no connection paused %+v", consent)
	}
	// The provider is back: «Повторить» reads the new chapter.
	back, err := net.Listen("tcp", address)
	if err != nil {
		t.Skipf("the port of the stub is gone: %v", err)
	}
	revived := httptest.NewUnstartedServer(stub)
	_ = revived.Listener.Close()
	revived.Listener = back
	revived.Start()
	defer revived.Close()
	saga.setConsent(map[string]any{"state": "allowed"})
	if status := saga.readThrough(5 * time.Minute); status.Reading != nil {
		t.Fatalf("«Повторить» did not read the new chapter: %+v", status.Reading)
	}
	process.stop()
}

// CN2.1–CN2.4, CN1.5: the sample reads exactly the three chapters chosen when it was pressed — the fourth gets no request —
// without a consent of the saga, without the names and the threads; a second press with the same key answers it; its
// answer carries the three cards, its time and what it spent; the estimate after it is «по пробе»; a sample under a consent
// that acts is refused; a sample stopped at its second chapter keeps one card.
func TestTheSampleReadsItsThreeChapters(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	titles := []string{"Глава 1. Сигнальные огни", "Глава 2. Перевал", "Глава 3. Застава", "Глава 4. Дозор"}
	held := []string{"Глава 11. Утро", "Глава 12. Ветер", "Глава 13. Снег"}
	stub := &readingStub{titles: append(append([]string(nil), titles...), held...), hold: held[1]}
	server := httptest.NewServer(stub)
	defer server.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := openWindow(t, process)
	saga := openSaga(t, window, "Северный предел", bookOf(titles...))
	connect(t, window, server.URL, map[string]any{"parallel": 2, "maxOutput": 1000})
	readingSettled(t, data, saga.project, 2*time.Minute)

	type sampleAnswer struct {
		State    string `json:"state"`
		Done     int    `json:"done"`
		Total    int    `json:"total"`
		Seconds  int    `json:"seconds"`
		Chapters []struct {
			Place   string   `json:"place"`
			Summary string   `json:"summary"`
			People  []string `json:"people"`
		} `json:"chapters"`
		Spent struct {
			InputTokens int64 `json:"inputTokens"`
		} `json:"spent"`
	}
	start := func(saga readingWindow, key string) (int, sampleAnswer, []byte) {
		t.Helper()
		response, raw := saga.post("/api/v1/projects/"+saga.project+"/reading/sample", map[string]any{}, key)
		var sample sampleAnswer
		_ = json.Unmarshal(raw, &sample)
		return response.StatusCode, sample, raw
	}
	if status, sample, raw := start(saga, "sample-of-the-north"); status != http.StatusAccepted || sample.Total != 3 {
		t.Fatalf("startReadingSample = %d %s", status, raw)
	}
	if status, sample, raw := start(saga, "sample-of-the-north"); status != http.StatusAccepted || sample.Total != 3 {
		t.Fatalf("the press again = %d %s", status, raw)
	}
	deadline := time.Now().Add(3 * time.Minute)
	var sample sampleAnswer
	for {
		window.read("/api/v1/projects/"+saga.project+"/reading/sample", &sample)
		if sample.State == "succeeded" || sample.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sample never ended: %+v", sample)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if sample.State != "succeeded" || sample.Done != 3 || len(sample.Chapters) != 3 || sample.Chapters[0].Place == "" ||
		sample.Chapters[0].Summary == "" || len(sample.Chapters[0].People) == 0 || sample.Spent.InputTokens != 6*stubInput {
		t.Fatalf("the sample = %+v", sample)
	}
	for _, title := range titles[:3] {
		if stub.reading(title) != 2 {
			t.Fatalf("%s was read %d times; want its card and its pass", title, stub.reading(title))
		}
	}
	if stub.reading(titles[3]) != 0 || stub.steps(coresteps.Names) != 0 || stub.steps(coresteps.LayerBook) != 0 {
		t.Fatalf("the sample read the fourth chapter (%d), the names (%d) or the threads (%d)", stub.reading(titles[3]),
			stub.steps(coresteps.Names), stub.steps(coresteps.LayerBook))
	}
	if consent := saga.consent(); consent.State != "none" {
		t.Fatalf("the sample gave a consent: %+v", consent)
	}
	var estimate struct {
		Kind     string `json:"kind"`
		Chapters int    `json:"chapters"`
	}
	window.read("/api/v1/projects/"+saga.project+"/reading/estimate", &estimate)
	if estimate.Kind != "sample" || estimate.Chapters != 1 {
		t.Fatalf("the estimate after the sample = %+v", estimate)
	}
	saga.setConsent(map[string]any{"state": "allowed", "stopAt": 1})
	saga.setConsent(map[string]any{"state": "paused"})
	if status, _, raw := start(saga, "sample-under-a-consent"); status != http.StatusConflict || !containsCode(raw, "reading.consent_given") {
		t.Fatalf("a sample under a consent = %d %s", status, raw)
	}

	// A sample stopped at its second chapter keeps the card of the first.
	other := openSaga(t, window, "Ветреный берег", bookOf(held...))
	_, _ = cardsJobEnded(t, data, other.project, time.Now().Add(-time.Minute), 2*time.Minute)
	if status, _, raw := start(other, "sample-of-the-shore"); status != http.StatusAccepted {
		t.Fatalf("the second sample = %d %s", status, raw)
	}
	deadline = time.Now().Add(2 * time.Minute)
	for {
		stub.mu.Lock()
		holding := stub.holding
		stub.mu.Unlock()
		window.read("/api/v1/projects/"+other.project+"/reading/sample", &sample)
		if holding && sample.Done >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second sample never reached its second chapter: %+v", sample)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if response, raw := window.do(http.MethodDelete, "/api/v1/projects/"+other.project+"/reading/sample", window.csrf(), nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancelReadingSample = %d %s", response.StatusCode, raw)
	}
	deadline = time.Now().Add(time.Minute)
	for {
		window.read("/api/v1/projects/"+other.project+"/reading/sample", &sample)
		if sample.State == "canceled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sample was not stopped: %+v", sample)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sample.Done < 1 || sample.Done >= 3 || len(sample.Chapters) != sample.Done {
		t.Fatalf("the stopped sample = %+v", sample)
	}
	process.stop()
}
