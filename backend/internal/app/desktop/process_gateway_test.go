// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/app/desktop/desktoptest"

	"canonkeeper/backend/internal/adapters/modelapi"
	"canonkeeper/backend/internal/domain/coresteps"
	readingdomain "canonkeeper/backend/internal/domain/readingconsent"
	readingrepo "canonkeeper/backend/internal/repository/postgres/readingconsent"
	storedb "canonkeeper/backend/internal/store/db"
)

// providerOfTheTest is the provider of the author in the process tests: a stub of Chat Completions that tells the calls
// of the core by their words and the turns of the chat by the model, cuts the first card it is asked for at its limit and
// holds an answer of the chat whose question asks it to.
type providerOfTheTest struct {
	mu       sync.Mutex
	requests []providerRequest
	cut      bool
	held     bool // the stub got a turn of the chat that asks to be held: the turn runs at the provider
}

// providerRequest is what the stub saw of one request.
type providerRequest struct {
	model, step, auth, job string
	headers                http.Header
}

const (
	longQuestion = "Расскажи подробно [долго]"
	chatAnswer   = "Застава стоит у перевала."
)

func (provider *providerOfTheTest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Messages) == 0 {
		http.Error(w, "not Chat Completions", http.StatusBadRequest)
		return
	}
	system, _ := body.Messages[0].Content.(string)
	step := ""
	if found, ok := coresteps.Of(system); ok {
		step = string(found.Name)
	}
	provider.mu.Lock()
	provider.requests = append(provider.requests, providerRequest{
		model: body.Model, step: step, auth: r.Header.Get("Authorization"), job: r.Header.Get(modelapi.JobHeader), headers: r.Header.Clone(),
	})
	cut := step == string(coresteps.Card) && !provider.cut
	if cut {
		provider.cut = true
	}
	provider.mu.Unlock()
	answer, finish := chatAnswer, "stop"
	switch coresteps.Name(step) {
	case coresteps.Card:
		answer = `{"summary": "Пересказ главы", "characters": [{"name": "Вера", "p": 1}]}`
		if cut {
			answer, finish = `{"summary": "Пересказ`, "length"
		}
	case coresteps.CardPass:
		answer = `{}`
	case coresteps.Names:
		answer = `{"persons":[]}`
	case "":
		if strings.Contains(string(raw), "[долго]") {
			provider.mu.Lock()
			provider.held = true
			provider.mu.Unlock()
			time.Sleep(4 * time.Second)
		}
	default:
		answer = `{}`
	}
	chunk, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": answer}, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 120, "completion_tokens": 12},
	})
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

func (provider *providerOfTheTest) seen() []providerRequest {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]providerRequest(nil), provider.requests...)
}

// holding waits until the stub holds the answer of a turn that asked it to, so the turn runs whatever the load of the machine.
func (provider *providerOfTheTest) holding(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := desktoptest.Deadline(timeout)
	for {
		provider.mu.Lock()
		held := provider.held
		provider.mu.Unlock()
		if held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the long turn never reached the provider")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (provider *providerOfTheTest) count(step coresteps.Name) int {
	count := 0
	for _, request := range provider.seen() {
		if request.step == string(step) {
			count++
		}
	}
	return count
}

// post sends a write of the window with a key of the request.
func (browser *client) post(path string, body any, key string) (*http.Response, []byte) {
	browser.t.Helper()
	encoded, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, browser.origin+path, bytes.NewReader(encoded))
	request.Header.Set("Origin", browser.origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", browser.csrf())
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: "ck_session", Value: browser.cookie})
	response, err := browser.http.Do(request)
	if err != nil {
		browser.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response, raw
}

// ask puts a question into the chat and answers the Job of its turn, or the refusal.
func (browser *client) ask(chatID, question, key string) (string, int, []byte) {
	browser.t.Helper()
	response, body := browser.post("/api/v1/chats/"+chatID+"/turns", map[string]string{"body": question}, key)
	var started struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	_ = json.Unmarshal(body, &started)
	return started.Job.ID, response.StatusCode, body
}

// jobEnded waits for a Job to end and answers its status.
func (browser *client) jobEnded(jobID string, timeout time.Duration) string {
	browser.t.Helper()
	deadline := desktoptest.Deadline(timeout)
	for {
		var job struct {
			Status string `json:"status"`
		}
		browser.read("/api/v1/jobs/"+jobID, &job)
		switch job.Status {
		case "succeeded", "failed", "canceled", "expired":
			return job.Status
		}
		if time.Now().After(deadline) {
			browser.t.Fatalf("the Job %s is still %s", jobID, job.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// cardsJobEnded waits for a cards Job of the saga newer than after to end and answers its status and code.
func cardsJobEnded(t *testing.T, data, projectID string, after time.Time, timeout time.Duration) (string, string) {
	t.Helper()
	connection := database(t, data)
	defer func() { _ = connection.Close(context.Background()) }()
	deadline := desktoptest.Deadline(timeout)
	for {
		var status, code string
		err := connection.QueryRow(context.Background(), `
			SELECT status, coalesce(error_json->>'code', '') FROM jobs
			WHERE project_id = $1 AND kind = 'chapter_cards' AND created_at > $2
			ORDER BY created_at DESC LIMIT 1`, projectID, after).Scan(&status, &code)
		if err == nil && status != "queued" && status != "running" {
			return status, code
		}
		if time.Now().After(deadline) {
			t.Fatalf("no cards Job ended: %q %v", status, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waiting is what the reading of the saga waits for, as its status says.
func (browser *client) waiting(projectID string) (string, bool) {
	browser.t.Helper()
	var status struct {
		Preparation *struct {
			Waiting string `json:"waiting"`
		} `json:"preparation"`
	}
	browser.read("/api/v1/projects/"+projectID+"/indexing-status", &status)
	if status.Preparation == nil {
		return "", false
	}
	return status.Preparation.Waiting, true
}

// appServer is the codex-app-server of a run.
func appServer(t *testing.T, process *backend) int {
	t.Helper()
	for _, pid := range family(t, process.command.Process.Pid, process.data) {
		if cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline")); err == nil && bytes.Contains(cmdline, []byte("codex-app-server")) {
			return pid
		}
	}
	return 0
}

// environ is the environment of a process, as NAME=value.
func environ(t *testing.T, pid int) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
}

// specs/2026-11-13-desk-gateway.md GW1–GW4 on the built backend of the app, the test playing its shell and the provider of
// the author a stub of Chat Completions. Before «Свой ИИ» the reading waits for the helper and a question is refused, and
// nothing goes to the provider; the token of the gateway is in the environment of the app-server alone. With the settings
// the helper is connected: the cards Job is queued at once and waits for the consent of the author without the model,
// the status says so, the chat answers through the gateway with the Job of its turn in the journal. With the consent the
// cards are read through the gateway by the model of the reading — a cut card is the builder's retry, not six of the
// client — and the journal has their steps and Job. A new window waits for the answer of a running turn and restarts the
// app-server after it, the thread goes on; a new model holds from the next turn without a restart. Without the helper
// again a new book gets the cards Job of the map, which asks no model, and the reading waits for the helper.
func TestTheOwnAIOfTheAppGoesThroughTheGateway(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	provider := &providerOfTheTest{}
	stub := httptest.NewServer(provider)
	defer stub.Close()
	process := startBackend(t, binary, resources, data, nil)
	if process.ready.GatewayPort == 0 || process.ready.GatewayPort == process.ready.Port {
		t.Fatalf("ready names the gateway at %d", process.ready.GatewayPort)
	}
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: desktoptest.Waited(60 * time.Second)}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}

	// GW2.2: the token is in the environment of the app-server alone.
	server := appServer(t, process)
	token := ""
	for _, pair := range environ(t, server) {
		if value, ok := strings.CutPrefix(pair, "CANONKEEPER_GATEWAY_TOKEN="); ok {
			token = value
		}
	}
	if server == 0 || len(token) != 64 {
		t.Fatalf("the app-server %d has no token of the gateway", server)
	}
	for _, pid := range family(t, process.command.Process.Pid, data) {
		if pid == server {
			continue
		}
		for _, pair := range environ(t, pid) {
			if strings.Contains(pair, token) || strings.HasPrefix(pair, "CANONKEEPER_GATEWAY_TOKEN") {
				t.Fatalf("the process %d holds the token in its environment", pid)
			}
		}
	}

	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFile(project.ID, "Застава у перевала.md", northernBook)
	deadline := desktoptest.Deadline(90 * time.Second)
	for {
		if waits, ok := window.waiting(project.ID); ok && waits == "helper" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reading never waited for the helper")
		}
		time.Sleep(200 * time.Millisecond)
	}
	response, body = window.post("/api/v1/projects/"+project.ID+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, "chat-of-the-gateway")
	var chat struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
		t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
	}
	// GW4.4: a question without the helper is refused by the server and asks nobody.
	if _, status, body := window.ask(chat.ID, "Где застава?", "question-without-the-helper"); status != http.StatusConflict || !containsCode(body, "helper.not_connected") {
		t.Fatalf("a question without the helper = %d %s", status, body)
	}
	if len(provider.seen()) != 0 {
		t.Fatal("the provider was asked without the helper")
	}

	// GW4.1, GW4.2, GW4.3: «Свой ИИ» at the stub — a custom address needs no key — connects the helper: the reading waits for
	// the consent and calls no model; the catch-up queues no cards Job for a saga without a card, which has nothing to build
	// without the model (specs/2026-11-15-desk-reading-queue.md RQ2.1).
	settings := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
		"advanced": map[string]any{"parallel": 2},
	}}
	saved := process.catchUps()
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	process.caughtUp(saved, 1, 2*time.Minute)
	readingSettled(t, data, project.ID, 2*time.Minute)
	if waits, _ := window.waiting(project.ID); waits != "consent" {
		t.Fatalf("the reading with the helper and without the consent waits for %q", waits)
	}
	if provider.count(coresteps.Card) != 0 {
		t.Fatal("a card was read without the consent")
	}

	// GW4.4, GW2.4, GW1.7: the chat answers through the gateway, the Job of its turn in the journal.
	turn, status, body := window.ask(chat.ID, "Где застава?", "question-with-the-helper")
	if status != http.StatusAccepted || window.jobEnded(turn, 2*time.Minute) != "succeeded" {
		t.Fatalf("a question with the helper = %d %s", status, body)
	}
	chats := 0
	for _, request := range provider.seen() {
		if request.model == "north-chat" {
			chats++
			if request.auth != "" || request.job != "" || request.headers.Get("Thread-Id") != "" || request.headers.Get("Session-Id") != "" {
				t.Fatalf("the provider of the chat got %v", request.headers)
			}
		}
	}
	if chats == 0 {
		t.Fatal("the turn did not go through the gateway")
	}

	// GW3.1, GW3.2, GW3.3: with the consent the cards are read by the model of the reading through the gateway.
	db, err := storedb.Open(context.Background(), runningCluster(t, data).ProductURL())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := readingrepo.New(db).ChangeConsent(context.Background(), project.ID, now, func(current *readingdomain.Consent) (readingdomain.Consent, error) {
		return readingdomain.Set(current, readingdomain.Change{State: readingdomain.StateAllowed}, now, readingdomain.SourceOwn,
			strings.TrimPrefix(stub.URL, "http://"), readingdomain.UnitTokens)
	}, false); err != nil {
		t.Fatal(err)
	}
	db.Close()
	consentedAt := time.Now().Add(-time.Second)
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	if status, code := cardsJobEnded(t, data, project.ID, consentedAt, 2*time.Minute); status != "succeeded" {
		t.Fatalf("the cards Job with the consent = %s %s", status, code)
	}
	if cards := provider.count(coresteps.Card); cards != 3 {
		t.Fatalf("card calls = %d; want two chapters and one retry of the builder after the cut answer", cards)
	}
	for _, request := range provider.seen() {
		if request.step != "" && request.model != "north-cards" {
			t.Fatalf("the step %s went to %s", request.step, request.model)
		}
	}
	if waits, read := window.waiting(project.ID); read && waits != "" {
		t.Fatalf("the read saga waits for %q", waits)
	}
	connection := database(t, data)
	rows, err := connection.Query(context.Background(), `
		SELECT r.purpose, coalesce(j.kind, ''), coalesce(r.project_id::text, ''), r.model, r.outcome, r.input_tokens IS NOT NULL, r.request_bytes
		FROM model_requests AS r LEFT JOIN jobs AS j ON j.id = r.job_id ORDER BY r.at`)
	if err != nil {
		t.Fatal(err)
	}
	purposes := map[string]string{}
	for rows.Next() {
		var purpose, kind, saga, model, outcome string
		var tokens bool
		var size int64
		if err := rows.Scan(&purpose, &kind, &saga, &model, &outcome, &tokens, &size); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"chat": "research_chat_turn", "card": "chapter_cards", "card_pass": "chapter_cards", "names": "chapter_cards"}[purpose]
		if want != "" && (kind != want || saga != project.ID || !tokens || size <= 0) {
			t.Fatalf("a row of %s: Job %q, saga %q, tokens %v, size %d", purpose, kind, saga, tokens, size)
		}
		purposes[purpose] = outcome
	}
	rows.Close()
	t.Logf("the journal: %v", purposes)
	if purposes["chat"] != "completed" || purposes["card_pass"] != "completed" || purposes["names"] != "completed" || purposes["card"] == "" {
		t.Fatalf("the journal = %v", purposes)
	}

	// GW2.3: a new window waits for the answer of the turn that runs, then restarts the app-server; the thread goes on.
	long, status, body := window.ask(chat.ID, longQuestion, "question-that-takes-long")
	if status != http.StatusAccepted {
		t.Fatalf("a long question = %d %s", status, body)
	}
	provider.holding(t, 2*time.Minute)
	wider := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
		"advanced": map[string]any{"parallel": 2, "contextWindow": 64000},
	}}
	var state struct {
		AppliesAfterAnswer bool `json:"appliesAfterAnswer"`
	}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), wider); response.StatusCode != http.StatusOK ||
		json.Unmarshal(body, &state) != nil || !state.AppliesAfterAnswer {
		t.Fatalf("a new window while a turn runs = %d %s", response.StatusCode, body)
	}
	if window.jobEnded(long, 2*time.Minute) != "succeeded" {
		t.Fatal("the long turn did not end")
	}
	window.read("/api/v1/desktop/ai", &state)
	restarted := appServer(t, process)
	if state.AppliesAfterAnswer || restarted == 0 || restarted == server {
		t.Fatalf("after the answer: waits %v, app-server %d (was %d)", state.AppliesAfterAnswer, restarted, server)
	}
	if args, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(restarted), "cmdline")); !bytes.Contains(args, []byte("model_context_window=64000")) {
		t.Fatal("the new app-server does not run with the new window")
	}
	renamed := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat-2", "cardsModel": "north-cards",
		"advanced": map[string]any{"parallel": 2, "contextWindow": 64000},
	}}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), renamed); response.StatusCode != http.StatusOK {
		t.Fatalf("a new model = %d %s", response.StatusCode, body)
	}
	before := len(provider.seen())
	next, status, body := window.ask(chat.ID, "А где дозорные?", "question-after-the-restart")
	if status != http.StatusAccepted || window.jobEnded(next, 2*time.Minute) != "succeeded" {
		t.Fatalf("the thread after the restart = %d %s", status, body)
	}
	if appServer(t, process) != restarted {
		t.Fatal("a new model restarted the app-server")
	}
	var models []string
	for _, request := range provider.seen()[before:] {
		if strings.HasPrefix(request.model, "north-chat") {
			models = append(models, request.model)
		}
	}
	if len(models) == 0 || models[0] != "north-chat-2" {
		t.Fatalf("the next turn went to %v", models)
	}

	// GW4.5: without the helper a new book gets the cards Job of the map, which asks no model; the reading waits for it.
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), map[string]any{"source": "none"}); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings none = %d %s", response.StatusCode, body)
	}
	asked := len(provider.seen())
	disconnectedAt := time.Now().Add(-time.Second)
	window.importFileAs(project.ID, "Дозор на рассвете.md", "# Глава 3. Дозор\n\nДозорные вернулись на заставу к вечеру.\n", "import-of-the-second-book")
	if status, code := cardsJobEnded(t, data, project.ID, disconnectedAt, 2*time.Minute); status != "failed" || code != "cards.consent" {
		t.Fatalf("the cards Job without the helper = %s %s", status, code)
	}
	if len(provider.seen()) != asked {
		t.Fatal("the cards Job without the helper asked the provider")
	}
	if waits, _ := window.waiting(project.ID); waits != "helper" {
		t.Fatalf("the reading of a new book without the helper waits for %q", waits)
	}
	var graphs int
	if err := database(t, data).QueryRow(context.Background(), `SELECT count(*) FROM saga_map_graphs WHERE project_id = $1`, project.ID).Scan(&graphs); err != nil || graphs == 0 {
		t.Fatalf("the map of the cards there are = %d, %v", graphs, err)
	}
	process.stop()

	// GW1.1: the token is in no output of the backend and in no file of its settings or logs.
	if bytes.Contains(process.stderr.Bytes(), []byte(token)) {
		t.Fatal("the output of the backend holds the token")
	}
	for _, place := range []string{filepath.Join(data, "settings.json"), DataDir{Root: data}.Logs()} {
		holdsTheKey(t, place, []byte(token))
	}
}
