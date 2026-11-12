package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	agenttimeline "canonkeeper/backend/internal/domain/agenttimeline"
)

// A long chat compacts on the pinned binary (specs/2026-11-12-desk-codex.md CX4). The provider of the chat is not
// OpenAI by name, so Codex compacts it locally (codex-rs/model-provider/src/capabilities.rs, core/src/compact.rs): one
// more request to the same /responses route with the thread's history, the turn's tools and Codex's summarization
// prompt last. The reply becomes the summary of the new history, and the compaction is a contextCompaction item of the
// running turn. The stub reports more tokens than a small limit and answers that request; the test checks what the
// harness needs from it. It skips without the binary, as the other runtime tests do.

const (
	// compactTokenLimit is model_auto_compact_token_limit of the test: far below the 1M window of the chat, far above
	// the tokens the stub reports for an ordinary request.
	compactTokenLimit = 20_000
	// compactHeavyTokens are the input tokens of a request after which the thread is over the limit.
	compactHeavyTokens = 50_000
	// compactPrompt opens the summarization prompt of Codex (codex-rs/prompts/templates/compact/prompt.md).
	compactPrompt = "CONTEXT CHECKPOINT COMPACTION"
	// A turn's question asks the stub to report the heavy usage on the turn's answer or on its first call.
	overLimitAfterAnswer = "[over the limit after the answer]"
	overLimitAfterCall   = "[over the limit after the call]"
)

// The kinds of the stub's requests: the call of two saga searches, the answer after them and the summary of a
// compaction. A request to another route is unknown and refused.
const (
	stubCall       = "call"
	stubAnswerKind = "answer"
	stubCompaction = "compaction"
	stubUnknown    = "unknown"
)

// compactingModel is the stub chat model that also summarizes: it calls two saga searches, answers once they have
// answered, replies to the summarization prompt with a numbered summary and reports heavy usage where the turn's
// question asks for it.
type compactingModel struct {
	mu       sync.Mutex
	requests []compactingRequest
}

type compactingRequest struct {
	path, kind string
	heavy      bool
	body       string
}

// compactSummary is the summary the stub gives on the n-th compaction.
func compactSummary(n int) string {
	return fmt.Sprintf("Сводка %d: автор спрашивал, где меч; ответ — глава 3.", n)
}

func (model *compactingModel) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	raw, _ := io.ReadAll(request.Body)
	recorded := compactingRequest{path: request.URL.Path, kind: stubUnknown, body: string(raw)}
	if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/responses") {
		model.record(recorded)
		http.NotFound(writer, request)
		return
	}
	var body struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	question, lastType, lastText := "", "", ""
	for _, item := range body.Input {
		var typed struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(item, &typed)
		lastType, lastText = typed.Type, ""
		for _, part := range typed.Content {
			lastText += part.Text
		}
		if typed.Type == "message" && typed.Role == "user" && strings.Contains(lastText, `"question":`) {
			question = lastText
		}
	}
	switch {
	case strings.Contains(lastText, compactPrompt):
		recorded.kind = stubCompaction
	case lastType == "function_call_output":
		recorded.kind, recorded.heavy = stubAnswerKind, strings.Contains(question, overLimitAfterAnswer)
	default:
		recorded.kind = stubCall
		recorded.heavy = lastType == "message" && lastText == question && strings.Contains(question, overLimitAfterCall)
	}
	index, compactions := model.record(recorded)
	id := fmt.Sprintf("resp-%d", index)
	output := stubSearchEvents(id)
	switch recorded.kind {
	case stubAnswerKind:
		output = stubMessageEvents(id, stubAnswer)
	case stubCompaction:
		output = stubMessageEvents(id, compactSummary(compactions))
	}
	tokens := stubInputTokens
	if recorded.heavy {
		tokens = compactHeavyTokens
	}
	streamStubResponse(writer, id, output, tokens)
}

// record keeps the request and returns its number and the number of compactions so far, this one included.
func (model *compactingModel) record(recorded compactingRequest) (index, compactions int) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.requests = append(model.requests, recorded)
	for _, request := range model.requests {
		if request.kind == stubCompaction {
			compactions++
		}
	}
	return len(model.requests), compactions
}

func (model *compactingModel) snapshot() []compactingRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return slices.Clone(model.requests)
}

// withCompactLimit is the chat configuration with the limit of compaction in place of the one of codex-lb.
func withCompactLimit(t *testing.T, overrides []string, limit int) []string {
	t.Helper()
	index := slices.IndexFunc(overrides, func(override string) bool {
		return strings.HasPrefix(override, "model_auto_compact_token_limit=")
	})
	if index < 0 {
		t.Fatal("the chat configuration names no model_auto_compact_token_limit")
	}
	overrides = slices.Clone(overrides)
	overrides[index] = fmt.Sprintf("model_auto_compact_token_limit=%d", limit)
	return overrides
}

// withinTurn says whether the notification at index lies between turn/started and turn/completed of its turn.
func withinTurn(seen []observedNotification, index int) bool {
	at := func(method string) int {
		return slices.IndexFunc(seen, func(n observedNotification) bool {
			return n.method == method && n.threadID == seen[index].threadID && n.turnID == seen[index].turnID
		})
	}
	started, completed := at("turn/started"), at("turn/completed")
	return started >= 0 && started < index && index < completed
}

// The chat compacts twice in three turns and goes on (CX4.1, CX4.2): the answer of the first turn leaves the thread
// over the limit, so the second compacts before its question; the first call of the third carries it over again, so the
// third compacts after that call and calls again from the summary. A fourth turn, in a new app-server as after a restart
// of the backend, resumes the compacted thread. Without a compaction the test fails (CX4.3).
func TestRuntimeCompactsALongChatAndGoesOn(t *testing.T) {
	binary := runtimeTestBinary(t)
	model := &compactingModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	config := runtimeStubConfig(t, binary, t.TempDir(), server.URL)
	config.ConfigOverrides = withCompactLimit(t, config.ConfigOverrides, compactTokenLimit)

	var replayMu sync.Mutex
	replayed := map[[3]string]bool{}
	run := func(client *Client, turnID, mark, root string) RunResult {
		t.Helper()
		request := runtimeChatRequest(turnID, &recordedCalls{})
		request.ExistingRootThreadID = root
		request.InitialInput = fmt.Sprintf(`{"question":"Где меч? %s","projectRef":"project","sourceView":"published","scope":"project"}`, mark)
		// A short result: long ones would carry the thread over the limit by Codex's own count on every call.
		request.Handler = func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Success: true, Payload: json.RawMessage(`"Меч — в главе 3."`)}, nil
		}
		// Before every saga call the harness reads the root's compactions back from its items (run.go).
		request.ContextCompactionBarrier = func(agent.ToolCall) bool { return true }
		request.OnContextCompaction = func(_ context.Context, threadID, turnID, itemID string) error {
			replayMu.Lock()
			defer replayMu.Unlock()
			replayed[[3]string{threadID, turnID, itemID}] = true
			return nil
		}
		result, err := client.Run(ctx, request)
		if err != nil {
			t.Fatalf("%s: %v", turnID, err)
		}
		if result.Status != agent.TurnCompleted || result.FinalResponse != stubAnswer || result.RootThreadID == "" ||
			root != "" && result.RootThreadID != root {
			t.Fatalf("%s = %+v", turnID, result)
		}
		return result
	}

	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stop := observeNotifications(ctx, t, client)
	first := run(client, "turn-1", overLimitAfterAnswer, "")
	root := first.RootThreadID
	second := run(client, "turn-2", "", root)
	third := run(client, "turn-3", overLimitAfterCall, root)
	time.Sleep(afterTurnQuiet)
	seen := stop()
	_ = client.Close()

	// Each compaction is a completed contextCompaction item of the turn that ran it, with its id.
	type compaction struct{ turnID, itemID string }
	var live []compaction
	for index, notification := range seen {
		if notification.method == "item/completed" && notification.threadID == root && notification.itemType == "contextCompaction" {
			live = append(live, compaction{notification.turnID, notification.itemID})
			if !withinTurn(seen, index) {
				t.Fatalf("compaction %+v is outside its turn", notification)
			}
		}
	}
	if len(live) != 2 || live[0].turnID != second.TurnID || live[1].turnID != third.TurnID ||
		live[0].itemID == "" || live[1].itemID == "" || live[0].itemID == live[1].itemID {
		t.Fatalf("compactions = %+v, want one in turn %s and one in turn %s", live, second.TurnID, third.TurnID)
	}
	// A compaction is no turn of its own and starts none: the root runs the turns the product asked for, every item of
	// the root lies inside its turn, and after the last turn the thread is quiet.
	var started []string
	for index, notification := range seen {
		if notification.threadID != root {
			continue
		}
		if notification.method == "turn/started" {
			started = append(started, notification.turnID)
		}
		if strings.HasPrefix(notification.method, "item/") && !withinTurn(seen, index) {
			t.Fatalf("item %+v is outside its turn", notification)
		}
	}
	if want := []string{first.TurnID, second.TurnID, third.TurnID}; !slices.Equal(started, want) {
		t.Fatalf("turns started = %v, want %v", started, want)
	}
	if after, completed := wentOn(seen, root, third.TurnID); !completed || len(after) > 0 {
		t.Fatalf("after the compacting turn: completed %v, after %+v", completed, after)
	}

	// After a restart the thread resumes from its compacted history and answers; it does not compact again.
	client, err = NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stop = observeNotifications(ctx, t, client)
	fourth := run(client, "turn-4", "", root)
	if slices.ContainsFunc(stop(), func(n observedNotification) bool { return n.itemType == "contextCompaction" }) {
		t.Fatal("the resumed thread compacted again")
	}

	// The binary compacts through the route of every request, with the summarization prompt last: the second turn
	// before its question, the third after its first call. The request after a compaction carries its summary.
	requests := model.snapshot()
	var kinds []string
	for _, request := range requests {
		kinds = append(kinds, request.kind)
		if !strings.HasSuffix(request.path, "/backend-api/codex/responses") {
			t.Fatalf("Codex asked the stub at %s", request.path)
		}
	}
	want := []string{
		stubCall, stubAnswerKind,
		stubCompaction, stubCall, stubAnswerKind,
		stubCall, stubCompaction, stubCall, stubAnswerKind,
		stubCall, stubAnswerKind,
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("model requests = %v, want %v", kinds, want)
	}
	for index, summary := range map[int]string{3: compactSummary(1), 7: compactSummary(2), 9: compactSummary(2)} {
		if !strings.Contains(requests[index].body, summary) {
			t.Fatalf("model request %d does not carry the summary %q", index, summary)
		}
	}

	// Before every saga call, after the restart too, the harness reads the compactions back from the items of the thread
	// with their turns and ids: the same items the stream completed, not copies with new ids.
	replayMu.Lock()
	readBack := maps.Clone(replayed)
	replayMu.Unlock()
	wantReadBack := map[[3]string]bool{
		{root, second.TurnID, live[0].itemID}: true,
		{root, third.TurnID, live[1].itemID}:  true,
	}
	if !maps.Equal(readBack, wantReadBack) {
		t.Fatalf("compactions the harness read back = %v, want %v", readBack, wantReadBack)
	}

	// The feed reads the compacted thread: a compaction is no step of the feed, its summary is no answer.
	items, err := client.ThreadItems(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := agenttimeline.Project("job-runtime-test", items)
	if err != nil {
		t.Fatal(err)
	}
	search := []string{"user", "tool_result:saga.search:completed", "tool_result:saga.search:completed", "assistant", "turn_outcome:completed"}
	again := slices.Concat(search[:3], search[1:])
	if want := slices.Concat(search, search, again, search); !slices.Equal(timelineKinds(snapshot.Events), want) {
		t.Fatalf("timeline = %v, want %v", timelineKinds(snapshot.Events), want)
	}
	turns := map[string]bool{}
	for _, event := range snapshot.Events {
		turns[event.TurnID] = true
	}
	if len(turns) != 4 || !turns[first.TurnID] || !turns[fourth.TurnID] {
		t.Fatalf("timeline turns = %v, want the four turns", turns)
	}
}
