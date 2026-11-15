// SPDX-License-Identifier: AGPL-3.0-only

package llmgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	codexadapter "canonkeeper/backend/internal/adapters/codex"
	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/modeljournal"
)

// The tests of this file run the pinned codex-app-server with the profile of the chat through the gateway to a stub of
// Chat Completions (specs/2026-11-13-desk-gateway.md GW2.1); they skip without the binary, as the runtime tests of the
// adapter of Codex do.

// compactPrompt opens the summarization prompt of Codex (codex-rs/prompts/templates/compact/prompt.md).
const compactPrompt = "CONTEXT CHECKPOINT COMPACTION"

const (
	turnInstructions = "Отвечай по тексту саги «Северный предел»."
	turnAnswer       = "Застава стоит у перевала, глава 3."
)

// chatMessage is a message of a request of Chat Completions as the stub reads it.
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (message chatMessage) text() string {
	var plain string
	if json.Unmarshal(message.Content, &plain) == nil {
		return plain
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(message.Content, &parts)
	var joined strings.Builder
	for _, part := range parts {
		joined.WriteString(part.Text)
	}
	return joined.String()
}

// chatModel is a stub provider of Chat Completions: it calls saga-search twice at once, answers once both answered,
// summarizes when Codex asks it to and reports heavy usage on its first call when heavy is set.
type chatModel struct {
	mu       sync.Mutex
	requests [][]chatMessage
	kinds    []string
	heavy    int
}

func (model *chatModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []chatMessage `json:"messages"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Messages) == 0 {
		http.Error(w, "not a request of Chat Completions", http.StatusBadRequest)
		return
	}
	last := body.Messages[len(body.Messages)-1]
	kind := "call"
	switch {
	case strings.Contains(last.text(), compactPrompt):
		kind = "compaction"
	case last.Role == "tool":
		kind = "answer"
	}
	model.mu.Lock()
	model.requests = append(model.requests, body.Messages)
	model.kinds = append(model.kinds, kind)
	calls := 0
	for _, seen := range model.kinds {
		if seen == "call" {
			calls++
		}
	}
	model.mu.Unlock()
	prompt := 1_000
	if kind == "call" && calls == 1 && model.heavy > 0 {
		prompt = model.heavy
	}
	usage := fmt.Sprintf(`"usage":{"prompt_tokens":%d,"completion_tokens":20}`, prompt)
	w.Header().Set("Content-Type", "text/event-stream")
	switch kind {
	case "call":
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[`+
			`{"index":0,"id":"call-a","type":"function","function":{"name":"saga-search","arguments":"{\"queries\":[\"застава\"]}"}},`+
			`{"index":1,"id":"call-b","type":"function","function":{"name":"saga-search","arguments":"{\"queries\":[\"перевал\"]}"}}]},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],`+usage+"}\n\n")
	case "compaction":
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Сводка: автор спрашивал о заставе."},"finish_reason":"stop"}],`+usage+"}\n\n")
	default:
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"`+turnAnswer+`"},"finish_reason":"stop"}],`+usage+"}\n\n")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (model *chatModel) seen() ([][]chatMessage, []string) {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([][]chatMessage(nil), model.requests...), append([]string(nil), model.kinds...)
}

// runtimeChat starts the app-server of the chat on the profile of the tree with the settings of the app over the gateway; a
// test may add overrides of its own after them — a shorter count of the repeats of Codex, never a pause by the clock.
func runtimeChat(t *testing.T, stand *stand, window int, overrides ...string) *codexadapter.Client {
	t.Helper()
	binary := os.Getenv("CK_CODEX_RUNTIME_TEST_BINARY")
	if binary == "" {
		t.Skip("set CK_CODEX_RUNTIME_TEST_BINARY to the pinned fork binary")
	}
	profile, err := filepath.Abs("../../../../codex-homes/research-chat")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := codexadapter.NewClient(ctx, codexadapter.ClientConfig{
		CodexPath: binary, CWD: workspace, RepositoryHome: profile, HomeCopies: true,
		CodexHome: filepath.Join(root, "codex-home"), CodexSQLiteHome: filepath.Join(root, "sqlite"),
		PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"),
		ConfigOverrides: append(CodexOverrides(stand.gateway.BaseURL(), window, window*9/10), overrides...),
		Environment:     []string{TokenVariable + "=" + stand.gateway.Token()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// turnRequest is a turn of the chat whose root thread the turn names to the gateway with its Job.
func turnRequest(stand *stand, calls *int) codexadapter.RunRequest {
	var mu sync.Mutex
	return codexadapter.RunRequest{
		CorrelationID: "chat-gateway-test", TurnCorrelationID: "turn-1", BundleID: "research-chat",
		DeveloperInstructions: turnInstructions,
		InitialInput:          `{"question":"Где застава?","projectRef":"project","sourceView":"published","scope":"project"}`,
		Tools: []agent.ToolSpec{{
			Namespace: "saga", Name: "search", Description: "Search the saga.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"queries":{"type":"array","items":{"type":"string"}}},"required":["queries"],"additionalProperties":false}`),
		}},
		BindRoot: func(_ context.Context, root string) error {
			stand.gateway.Threads().Bind(root, "job-of-the-turn")
			return nil
		},
		BindTurn: func(context.Context, string, string) error { return nil },
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			mu.Lock()
			*calls++
			mu.Unlock()
			return agent.ToolResult{Success: true, Payload: json.RawMessage(`{"found":"гл. 3: застава у перевала"}`)}, nil
		},
	}
}

// GW2.1: Codex of the app runs a turn through the gateway on a provider of Chat Completions — two saga searches at once
// and the answer; its base instructions and the instructions of the turn are the first system message, and the requests
// carry the Job of the turn into the journal.
func TestCodexOfTheAppAnswersThroughTheGateway(t *testing.T) {
	stand := openStand(t)
	model := &chatModel{}
	stand.settings.set(t, custom(startProvider(t, model), "chat"), "key-test-runtime-0001")
	client := runtimeChat(t, stand, 128_000)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := 0
	result, err := client.Run(ctx, turnRequest(stand, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != agent.TurnCompleted || result.FinalResponse != turnAnswer || calls != 2 {
		t.Fatalf("the turn = %+v, saga calls %d", result, calls)
	}
	requests, kinds := model.seen()
	if strings.Join(kinds, ",") != "call,answer" {
		t.Fatalf("the provider got %v", kinds)
	}
	first := requests[0]
	systems := 0
	for _, message := range first {
		if message.Role == "system" || message.Role == "developer" {
			systems++
		}
	}
	if first[0].Role != "system" || systems != 1 || !strings.Contains(first[0].text(), turnInstructions) || len(first[0].text()) < 10_000 {
		t.Fatalf("the first message is %s of %d characters, system messages %d", first[0].Role, len(first[0].text()), systems)
	}
	rows := stand.journal.all()
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	for _, row := range rows {
		if row.Purpose != modeljournal.PurposeChat || row.JobID != "job-of-the-turn" || row.Model != "north-chat" || row.Outcome != modeljournal.OutcomeCompleted {
			t.Fatalf("row %+v", row)
		}
	}
}

// GW2.1: with a window of 32 000 the compaction is at 90 % of it — a conversation over it is compacted by Codex through
// the gateway, by the same route, and the turn goes on to its answer.
func TestCodexOfTheAppCompactsThroughTheGateway(t *testing.T) {
	stand := openStand(t)
	model := &chatModel{heavy: 30_000}
	stand.settings.set(t, custom(startProvider(t, model), "chat"), "key-test-runtime-0002")
	client := runtimeChat(t, stand, 32_000)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := 0
	result, err := client.Run(ctx, turnRequest(stand, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != agent.TurnCompleted || result.FinalResponse != turnAnswer {
		t.Fatalf("the turn = %+v", result)
	}
	_, kinds := model.seen()
	if !strings.Contains(strings.Join(kinds, ","), "compaction") || kinds[len(kinds)-1] != "answer" {
		t.Fatalf("the provider got %v; want a compaction and then the answer", kinds)
	}
}

// GW1.6 on the binary of the set: a turn of Codex while «Свой ИИ» is not connected ends at once — the gateway answers a status
// Codex 0.167.2 does not repeat; a 403 it would repeat for about three minutes.
func TestCodexDoesNotRepeatAHelperThatIsNotConnected(t *testing.T) {
	stand := openStand(t)
	client := runtimeChat(t, stand, 128_000)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := 0
	began := time.Now()
	result, err := client.Run(ctx, turnRequest(stand, &calls))
	if err != nil || result.Status == agent.TurnCompleted || time.Since(began) > 30*time.Second {
		t.Fatalf("a turn without the helper = %v %v in %s", result.Status, err, time.Since(began))
	}
}
