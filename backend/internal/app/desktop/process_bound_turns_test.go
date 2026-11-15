// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// compactionMarks are words of the prompt of the compaction of Codex: a request with one of them is the compaction of the thread.
var compactionMarks = [][]byte{[]byte("CHECKPOINT COMPACTION"), []byte("handoff summary")}

// boundProvider is the provider of the test of the bound turns: a stub of Chat Completions that says the context is near
// the window of the author — so Codex compacts the thread —, holds an answer whose question asks it to, and counts.
type boundProvider struct {
	mu          sync.Mutex
	requests    int
	compactions int
	held        int
	// read: a question with «[читать]» is answered first by the calls of saga.read (the first paragraph of chapter 1) and
	// saga.memory, as Chat Completions names the tools of the namespace saga.
	read int
}

func (provider *boundProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	provider.mu.Lock()
	provider.requests++
	for _, mark := range compactionMarks {
		if bytes.Contains(raw, mark) {
			provider.compactions++
			break
		}
	}
	hold := strings.Contains(lastUser(raw), "[долго]")
	if hold {
		provider.held++
	}
	call := strings.Contains(lastUser(raw), "[читать]") && lastRole(raw) == "user"
	if call {
		provider.read++
	}
	provider.mu.Unlock()
	if hold {
		time.Sleep(4 * time.Second)
	}
	delta, finish := map[string]any{"role": "assistant", "content": chatAnswer}, "stop"
	if call {
		delta = map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"index": 0, "id": "call_read", "type": "function", "function": map[string]any{"name": "saga-read", "arguments": `{"chapter":"1","from":1,"to":1}`}},
			map[string]any{"index": 1, "id": "call_memory", "type": "function", "function": map[string]any{"name": "saga-memory", "arguments": `{}`}},
		}}
		finish = "tool_calls"
	}
	chunk, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 31_000, "completion_tokens": 40},
	})
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

// lastUser is the text of the last message of the author in a request of Chat Completions: the question of a new turn, or
// the correction that came in the middle of one.
func lastUser(raw []byte) string {
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	for index := len(body.Messages) - 1; index >= 0; index-- {
		message := body.Messages[index]
		if message.Role != "user" {
			continue
		}
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			return text
		}
		var parts []struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(message.Content, &parts)
		var joined []string
		for _, part := range parts {
			joined = append(joined, part.Text)
		}
		return strings.Join(joined, "\n")
	}
	return ""
}

// lastRole is the role of the last message of a request of Chat Completions: «user» before the tools of a turn answered.
func lastRole(raw []byte) string {
	var body struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Messages) == 0 {
		return ""
	}
	return body.Messages[len(body.Messages)-1].Role
}

// wentOf are the words «что ушло» of the sendings of the row of a Job, in their order, as «Настройки → Журнал» reads them.
func wentOf(t *testing.T, window *client, job string) []string {
	t.Helper()
	var page struct {
		Rows []struct {
			Key string `json:"key"`
		} `json:"rows"`
	}
	window.read("/api/v1/desktop/log?zone=UTC&kind=question", &page)
	var went []string
	for _, row := range page.Rows {
		if !strings.HasPrefix(row.Key, job+"|") {
			continue
		}
		var sendings struct {
			Sendings []struct {
				WhatWent string `json:"whatWent"`
			} `json:"sendings"`
		}
		window.read("/api/v1/desktop/log/sendings?zone=UTC&row="+url.QueryEscape(row.Key), &sendings)
		for _, sending := range sendings.Sendings {
			went = append(went, sending.WhatWent)
		}
	}
	return went
}

func (provider *boundProvider) counts() (requests, compactions, held int) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.requests, provider.compactions, provider.held
}

// turnOf is the turn of the answer of createChatTurn.
func turnOf(t *testing.T, body []byte) string {
	t.Helper()
	var started struct {
		Job struct {
			Subject struct {
				TurnID string `json:"turnId"`
			} `json:"subject"`
		} `json:"job"`
	}
	if json.Unmarshal(body, &started) != nil || started.Job.Subject.TurnID == "" {
		t.Fatalf("no turn in %s", body)
	}
	return started.Job.Subject.TurnID
}

// specs/2026-11-14-desk-journal.md JR1.2 with the real Codex of the kit through the gateway and a stub of the provider: a
// question, a correction in the middle of a turn, a cancellation, and the compaction of the thread under a small window —
// every turn lives, every request of Codex goes inside its turn (the gateway refused none as unbound) and every request the
// provider heard lies in the journal.
func TestEveryRequestOfCodexGoesInsideItsTurn(t *testing.T) {
	binary, resources := desktopBinary(t)
	data := filepath.Join(t.TempDir(), "data")
	provider := &boundProvider{}
	stub := httptest.NewServer(provider)
	defer stub.Close()
	process := startBackend(t, binary, resources, data, nil)
	window := &client{t: t, origin: process.origin(), http: &http.Client{Timeout: 60 * time.Second}}
	if response := window.open(process.ready.Code); response.StatusCode != http.StatusOK {
		t.Fatalf("open = %d", response.StatusCode)
	}
	response, body := window.do(http.MethodPost, "/api/v1/projects", window.csrf(), map[string]string{"title": "Северный предел"})
	var project struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &project) != nil {
		t.Fatalf("createProject = %d %s", response.StatusCode, body)
	}
	window.importFile(project.ID, "Застава у перевала.md", northernBook)
	indexed(t, window, project.ID)
	settings := map[string]any{"source": "own", "own": map[string]any{
		"preset": "custom", "baseUrl": stub.URL + "/v1", "protocol": "chat", "chatModel": "north-chat", "cardsModel": "north-cards",
		"advanced": map[string]any{"contextWindow": 32_000},
	}}
	if response, body := window.do(http.MethodPut, "/api/v1/desktop/ai", window.csrf(), settings); response.StatusCode != http.StatusOK {
		t.Fatalf("updateAiSettings = %d %s", response.StatusCode, body)
	}
	response, body = window.post("/api/v1/projects/"+project.ID+"/chats", map[string]string{"sourceView": "published", "scope": "project"}, "chat-of-the-bound-turns")
	var chat struct {
		ID string `json:"id"`
	}
	if response.StatusCode != http.StatusCreated || json.Unmarshal(body, &chat) != nil {
		t.Fatalf("createChatSession = %d %s", response.StatusCode, body)
	}

	// A question: the first record of its turn names the question of the author (specs/2026-11-14-desk-journal.md JR1.3).
	job, status, body := window.ask(chat.ID, "Где застава?", "bound-question")
	if status != http.StatusAccepted || window.jobEnded(job, 2*time.Minute) != "succeeded" {
		t.Fatalf("the question = %d %s", status, body)
	}
	if went := wentOf(t, window, job); len(went) == 0 || went[0] != "ваш вопрос" {
		t.Fatalf("the sendings of the question = %q", went)
	}
	// A question whose answer reads the first paragraph of chapter 1 and the memory of the author: the record of the next
	// request of the thread names the chapter with its paragraph and the record shown (JR1.3, JR1.4).
	if response, body := window.post("/api/v1/projects/"+project.ID+"/author-memory", map[string]string{"kind": "plan", "text": "Вера уйдёт за перевал"}, "bound-memory"); response.StatusCode != http.StatusCreated {
		t.Fatalf("createAuthorMemory = %d %s", response.StatusCode, body)
	}
	job, status, body = window.ask(chat.ID, "Что видела Вера? [читать]", "bound-read")
	if status != http.StatusAccepted || window.jobEnded(job, 2*time.Minute) != "succeeded" {
		t.Fatalf("the question that reads = %d %s", status, body)
	}
	// The thread is near its window, so Codex may compact it between the call and its answer: that request names nothing new.
	if went := wentOf(t, window, job); len(went) < 2 || went[0] != "ваш вопрос" ||
		!slices.Contains(went[1:], "Книга 1, гл. 1 „Сигнальные огни“, абзац 1; записи «Вы просили помнить»: 1") {
		t.Fatalf("the sendings of the question that reads = %q", went)
	}
	// A correction in the middle of a turn the provider holds: a record of the turn names it.
	job, status, body = window.ask(chat.ID, "Кто стоит на стене? [долго]", "bound-steered")
	if status != http.StatusAccepted {
		t.Fatalf("the long question = %d %s", status, body)
	}
	turn := turnOf(t, body)
	deadline := time.Now().Add(2 * time.Minute)
	for _, _, held := provider.counts(); held == 0; _, _, held = provider.counts() {
		if time.Now().After(deadline) {
			t.Fatal("the long turn never reached the provider")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if response, body := window.post("/api/v1/chats/"+chat.ID+"/turns/"+turn+"/steer", map[string]string{"body": "Короче"}, "bound-correction"); response.StatusCode != http.StatusAccepted {
		t.Fatalf("steerChatTurn = %d %s", response.StatusCode, body)
	}
	if ended := window.jobEnded(job, 2*time.Minute); ended != "succeeded" {
		t.Fatalf("the corrected turn = %s", ended)
	}
	if went := wentOf(t, window, job); len(went) < 2 || went[0] != "ваш вопрос" || !slices.Contains(went[1:], "ваше уточнение") {
		t.Fatalf("the sendings of the corrected turn = %q", went)
	}
	// A cancellation of a turn the provider holds.
	_, _, heldBefore := provider.counts()
	job, status, body = window.ask(chat.ID, "Что за перевалом? [долго]", "bound-canceled")
	if status != http.StatusAccepted {
		t.Fatalf("the canceled question = %d %s", status, body)
	}
	deadline = time.Now().Add(2 * time.Minute)
	for _, _, held := provider.counts(); held == heldBefore; _, _, held = provider.counts() {
		if time.Now().After(deadline) {
			t.Fatal("the turn to cancel never reached the provider")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if response, body := window.do(http.MethodPost, "/api/v1/jobs/"+job+"/cancel", window.csrf(), nil); response.StatusCode >= 300 {
		t.Fatalf("cancelJob = %d %s", response.StatusCode, body)
	}
	if ended := window.jobEnded(job, 2*time.Minute); ended != "canceled" {
		t.Fatalf("the canceled turn = %s", ended)
	}
	// The next questions go on a thread near its window: Codex compacts it inside a turn.
	for index, question := range []string{"Кто зажёг огни?", "Почему погасли огни?"} {
		if job, status, body := window.ask(chat.ID, question, fmt.Sprintf("bound-compacted-%d", index)); status != http.StatusAccepted || window.jobEnded(job, 2*time.Minute) != "succeeded" {
			t.Fatalf("the question after the compaction = %d %s", status, body)
		}
	}
	time.Sleep(2 * time.Second)

	requests, compactions, held := provider.counts()
	t.Logf("the provider heard %d requests, %d of them compactions, %d held", requests, compactions, held)
	if compactions == 0 {
		t.Fatalf("Codex never compacted the thread in %d requests", requests)
	}
	if strings.Contains(string(process.stderr.Bytes()), `"event":"gateway_unbound"`) {
		t.Fatal("the gateway refused a request of Codex outside its turn")
	}
	var recorded int
	if err := database(t, data).QueryRow(t.Context(), `SELECT count(*) FROM model_disclosures WHERE purpose = 'chat'`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != requests {
		t.Fatalf("the provider heard %d requests, the journal holds %d", requests, recorded)
	}
}
