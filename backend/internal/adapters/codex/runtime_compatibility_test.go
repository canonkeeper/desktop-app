package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	agenttimeline "canonkeeper/backend/internal/domain/agenttimeline"

	sdk "github.com/openai/codex/sdk/go"
)

// The runtime tests start the pinned fork binary named by CK_CODEX_RUNTIME_TEST_BINARY and skip
// without it: the upgrade gate of specs/2026-10-28-codex-0165.md (UC5).
func runtimeTestBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("CK_CODEX_RUNTIME_TEST_BINARY")
	if binary == "" {
		t.Skip("set CK_CODEX_RUNTIME_TEST_BINARY to the pinned fork binary")
	}
	return binary
}

func runtimeTestProfile(t *testing.T) string {
	t.Helper()
	profile, err := filepath.Abs("../../../../codex-homes/research-chat")
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestRuntimeProfileInitializesWithPinnedSDK(t *testing.T) {
	binary := runtimeTestBinary(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	client, err := NewClient(ctx, ClientConfig{
		CodexPath: binary, CWD: root,
		RepositoryHome:  runtimeTestProfile(t),
		CodexHome:       filepath.Join(root, "codex-home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"),
		PrivateHome:     filepath.Join(root, "home"),
		TempDir:         filepath.Join(root, "tmp"),
	})
	if err != nil {
		t.Fatalf("native initialize: %v", err)
	}
	defer client.Close()
	runtime, err := client.runtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	metadata := runtime.sdk.Metadata()
	if metadata.Compatibility != sdk.CompatibilityStrict || metadata.CompatibilityOverrideActive || metadata.ProtocolMode != sdk.ProtocolModeExperimental {
		t.Fatal("runtime initialization did not preserve strict experimental protocol compatibility")
	}
}

// stubModel is a Responses API for the chat model on Codex fallback metadata: its first reply
// calls two saga searches at once, its second answers. It records what each request declared.
type stubModel struct {
	mu       sync.Mutex
	requests []stubRequest
	bodies   []string
}

// saw says whether any request carried the text.
func (model *stubModel) saw(text string) bool {
	model.mu.Lock()
	defer model.mu.Unlock()
	return slices.ContainsFunc(model.bodies, func(body string) bool { return strings.Contains(body, text) })
}

type stubRequest struct {
	Tools       []string // the tools field and every additional_tools item, namespace.name
	Parallel    bool     // parallel_tool_calls
	Lite        bool     // the Responses Lite header, which refuses parallel calls
	InputItems  int
	Model       string
	Effort      string
	CallOutputs []int // characters of each function_call_output the model reads back
}

const stubAnswer = "Меч упомянут в главе 3."

// stubSearchOutput is as long as a read of a long chapter: the model must get all of it.
var stubSearchOutput = strings.Repeat("меч ", 20_000)

func (model *stubModel) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/responses") {
		http.NotFound(writer, request)
		return
	}
	var body struct {
		Model     string            `json:"model"`
		Input     []json.RawMessage `json:"input"`
		Tools     []json.RawMessage `json:"tools"`
		Parallel  bool              `json:"parallel_tool_calls"`
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	raw, _ := io.ReadAll(request.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	recorded := stubRequest{
		InputItems: len(body.Input), Model: body.Model, Effort: body.Reasoning.Effort, Parallel: body.Parallel,
		Lite: request.Header.Get("X-Openai-Internal-Codex-Responses-Lite") != "",
	}
	declarations := append([]json.RawMessage(nil), body.Tools...)
	lastType := ""
	for _, item := range body.Input {
		var typed struct {
			Type   string            `json:"type"`
			Tools  []json.RawMessage `json:"tools"`
			Output json.RawMessage   `json:"output"`
		}
		_ = json.Unmarshal(item, &typed)
		lastType = typed.Type
		switch typed.Type {
		case "additional_tools":
			declarations = append(declarations, typed.Tools...)
		case "function_call_output":
			var text string
			_ = json.Unmarshal(typed.Output, &text)
			recorded.CallOutputs = append(recorded.CallOutputs, len([]rune(text)))
		case "message":
			recorded.CallOutputs = nil
		}
	}
	for _, declaration := range declarations {
		recorded.collect(declaration, "")
	}
	slices.Sort(recorded.Tools)
	recorded.Tools = slices.Compact(recorded.Tools)
	model.mu.Lock()
	model.requests = append(model.requests, recorded)
	model.bodies = append(model.bodies, string(raw))
	id := fmt.Sprintf("resp-%d", len(model.requests))
	model.mu.Unlock()

	events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": id}}}
	if lastType == "function_call_output" {
		message := map[string]any{
			"type": "message", "role": "assistant", "id": "msg-" + id, "phase": "final_answer",
			"content": []map[string]any{{"type": "output_text", "text": stubAnswer}},
		}
		events = append(events,
			map[string]any{"type": "response.output_item.added", "item": message},
			map[string]any{"type": "response.output_text.delta", "delta": stubAnswer},
			map[string]any{"type": "response.output_item.done", "item": message},
		)
	} else {
		for index, query := range []string{"меч", "щит"} {
			events = append(events, map[string]any{"type": "response.output_item.done", "item": map[string]any{
				"type": "function_call", "call_id": fmt.Sprintf("call-%s-%d", id, index), "name": "search",
				"namespace": "saga", "arguments": fmt.Sprintf(`{"queries":[%q]}`, query),
			}})
		}
	}
	events = append(events, map[string]any{"type": "response.completed", "response": map[string]any{
		"id": id, "usage": map[string]any{
			"input_tokens": 100, "input_tokens_details": nil, "output_tokens": 10,
			"output_tokens_details": nil, "total_tokens": 110,
		},
	}})
	writer.Header().Set("Content-Type", "text/event-stream")
	buffered := bufio.NewWriter(writer)
	for _, event := range events {
		encoded, _ := json.Marshal(event)
		fmt.Fprintf(buffered, "event: %s\ndata: %s\n\n", event["type"], encoded)
	}
	_ = buffered.Flush()
}

// collect names a declaration: `namespace.name` for namespaced tools, `name` for the default
// namespace.
func (recorded *stubRequest) collect(raw json.RawMessage, namespace string) {
	var tool struct {
		Type  string            `json:"type"`
		Name  string            `json:"name"`
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &tool) != nil {
		recorded.Tools = append(recorded.Tools, "<invalid>")
		return
	}
	if tool.Type == "namespace" {
		for _, nested := range tool.Tools {
			recorded.collect(nested, tool.Name)
		}
		return
	}
	name := tool.Name
	if name == "" {
		name = tool.Type
	}
	if namespace != "" && namespace != "functions" {
		name = namespace + "." + name
	}
	recorded.Tools = append(recorded.Tools, name)
}

func (model *stubModel) snapshot() []stubRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]stubRequest(nil), model.requests...)
}

// runtimeStubConfig is the chat configuration of scripts/codex-lb.mjs with the stub as provider.
func runtimeStubConfig(t *testing.T, binary, root, modelURL string) ClientConfig {
	t.Helper()
	t.Setenv("CODEX_LB_API_KEY", "stub-key")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	return ClientConfig{
		CodexPath: binary, CWD: workspace,
		RepositoryHome:  runtimeTestProfile(t),
		CodexHome:       filepath.Join(root, "codex-home"),
		CodexSQLiteHome: filepath.Join(root, "sqlite"),
		PrivateHome:     filepath.Join(root, "home"),
		TempDir:         filepath.Join(root, "tmp"),
		ConfigOverrides: []string{
			`model="gpt-6.1-sol"`,
			`model_provider="codex-lb"`,
			`model_context_window=1000000`,
			`model_auto_compact_token_limit=950000`,
			`model_reasoning_effort="high"`,
			`tool_output_token_limit=400000`,
			`model_providers.codex-lb.name="openai"`,
			fmt.Sprintf(`model_providers.codex-lb.base_url="%s/backend-api/codex"`, modelURL),
			`model_providers.codex-lb.wire_api="responses"`,
			`model_providers.codex-lb.env_key="CODEX_LB_API_KEY"`,
			`model_providers.codex-lb.supports_websockets=false`,
			`model_providers.codex-lb.requires_openai_auth=false`,
			`model_providers.codex-lb.stream_max_retries=0`,
		},
		PassEnv: []string{"CODEX_LB_API_KEY"},
	}
}

type recordedCalls struct {
	mu    sync.Mutex
	calls []agent.ToolCall
}

func (recorded *recordedCalls) handle(_ context.Context, call agent.ToolCall) (agent.ToolResult, error) {
	recorded.mu.Lock()
	recorded.calls = append(recorded.calls, call)
	recorded.mu.Unlock()
	payload, _ := json.Marshal(stubSearchOutput)
	return agent.ToolResult{Success: true, Payload: payload}, nil
}

func (recorded *recordedCalls) count() int {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	return len(recorded.calls)
}

func runtimeChatRequest(turnID string, calls *recordedCalls) RunRequest {
	return RunRequest{
		CorrelationID: "chat-runtime-test", TurnCorrelationID: turnID, BundleID: "research-chat",
		DeveloperInstructions: "Answer from the saga.",
		InitialInput:          `{"question":"Где упомянут меч?","projectRef":"project","sourceView":"published","scope":"project"}`,
		Tools: []agent.ToolSpec{{
			Namespace: "saga", Name: "search", Description: "Search the saga.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"queries":{"type":"array","items":{"type":"string"}}},"required":["queries"],"additionalProperties":false}`),
		}},
		BindRoot: func(context.Context, string) error { return nil },
		BindTurn: func(context.Context, string, string) error { return nil },
		Handler:  calls.handle,
	}
}

func TestRuntimeRunsResumesAndDeletesChatAgainstStubModel(t *testing.T) {
	binary := runtimeTestBinary(t)
	model := &stubModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	config := runtimeStubConfig(t, binary, t.TempDir(), server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	calls := &recordedCalls{}
	searchOnly := runtimeChatRequest("", calls).Tools
	withRead := append(slices.Clone(searchOnly), agent.ToolSpec{
		Namespace: "saga", Name: "read", Description: "Read a chapter.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"chapter":{"type":"string"}},"required":["chapter"],"additionalProperties":false}`),
	})
	// Each turn runs in a new app-server process, as after a backend restart or a release.
	run := func(turnID, rootThreadID string, tools []agent.ToolSpec) RunResult {
		t.Helper()
		client, err := NewClient(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		request := runtimeChatRequest(turnID, calls)
		request.ExistingRootThreadID, request.Tools = rootThreadID, tools
		result, err := client.Run(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != agent.TurnCompleted || result.FinalResponse != stubAnswer || result.RootThreadID == "" ||
			rootThreadID != "" && result.RootThreadID != rootThreadID {
			t.Fatalf("%s = %+v", turnID, result)
		}
		return result
	}

	root := run("turn-1", "", searchOnly).RootThreadID
	// The bundle has gained a tool since the chat started: the resumed thread sees it (UC3).
	run("turn-2", root, withRead)

	// Both searches of each answer reach the dynamic tool handler.
	if calls.count() != 4 {
		t.Fatalf("saga calls = %d, want 4", calls.count())
	}
	for _, call := range calls.calls {
		if call.Namespace != "saga" || call.Tool != "search" {
			t.Fatalf("saga call = %+v", call)
		}
	}

	requests := model.snapshot()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(requests))
	}
	// The model catalog of the profile does not name gpt-6.1-sol, so it runs on Codex fallback
	// metadata as on 0.153.4: direct saga tools called in parallel, no Responses Lite and no code
	// mode, no shell, images, web, agents, patches, sleep or questions to the user (UC2).
	for index, recorded := range requests {
		wantTools := []string{"create_goal", "get_goal", "saga.search", "update_goal"}
		if index >= 2 {
			wantTools = slices.Insert(wantTools, 2, "saga.read")
		}
		if !slices.Equal(recorded.Tools, wantTools) || !recorded.Parallel || recorded.Lite ||
			recorded.Model != "gpt-6.1-sol" || recorded.Effort != "high" {
			t.Fatalf("model request %d = %+v, want tools %v in parallel without Lite", index, recorded, wantTools)
		}
	}
	if requests[2].InputItems <= requests[1].InputItems {
		t.Fatalf("resumed turn lost history: %d input items after %d", requests[2].InputItems, requests[1].InputItems)
	}
	// tool_output_token_limit alone bounds a saga result: two long results arrive in full (R2.5).
	for _, index := range []int{1, 3} {
		outputs := requests[index].CallOutputs
		if len(outputs) != 2 || outputs[0] < len([]rune(stubSearchOutput)) || outputs[1] < len([]rune(stubSearchOutput)) {
			t.Fatalf("model request %d read back %v characters, want two of %d", index, outputs, len([]rune(stubSearchOutput)))
		}
	}

	// The feed shows each search as a saga action.
	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	items, err := client.ThreadItems(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := agenttimeline.Project("job-runtime-test", items)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, event := range snapshot.Events {
		kind := string(event.Kind)
		if event.Tool != nil {
			kind += ":" + event.Tool.Namespace + "." + event.Tool.Name
		}
		if event.Outcome != "" {
			kind += ":" + string(event.Outcome)
		}
		kinds = append(kinds, kind)
	}
	search := []string{"user", "tool_result:saga.search:completed", "tool_result:saga.search:completed", "assistant", "turn_outcome:completed"}
	if want := slices.Concat(search, search); !slices.Equal(kinds, want) {
		t.Fatalf("timeline = %v, want %v", kinds, want)
	}

	if _, err := client.DeleteTree(ctx, RootSelector{ThreadID: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ThreadItems(ctx, root); err == nil {
		t.Fatal("deleted chat thread is still readable")
	}
}

// A new chat binds its root right after thread/start; app-server writes the thread's history
// only after the first turn starts. A timeline read in between is an empty timeline, not a
// failure: the author's first seconds of a turn show no broken stream (feed spec, 30.10).
func TestRuntimeTimelineOfNewChatIsEmptyUntilHistoryIsWritten(t *testing.T) {
	binary := runtimeTestBinary(t)
	server := httptest.NewServer(&stubModel{})
	defer server.Close()
	config := runtimeStubConfig(t, binary, t.TempDir(), server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request := runtimeChatRequest("turn-1", &recordedCalls{})
	var early []string
	request.BindRoot = func(ctx context.Context, root string) error {
		items, err := client.ThreadItems(ctx, root)
		early = append(early, fmt.Sprintf("bind root: %d items, %v", len(items), err))
		return nil
	}
	result, err := client.Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bind root: 0 items, <nil>"}; !slices.Equal(early, want) {
		t.Fatalf("timeline before the first turn = %v, want %v", early, want)
	}
	if items, err := client.ThreadItems(ctx, result.RootThreadID); err != nil || len(items) == 0 {
		t.Fatalf("timeline after the turn = %d items, %v", len(items), err)
	}
}

// specs/2026-10-30-chat-steering.md: a correction on the pinned binary. It is sent while a tool of
// the turn waits, reaches the model in the same turn, and comes back as a steer event in its place
// in the timeline; a wrong or finished turn is refused.
func TestRuntimeSteersARunningTurnAgainstStubModel(t *testing.T) {
	binary := runtimeTestBinary(t)
	model := &stubModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	config := runtimeStubConfig(t, binary, t.TempDir(), server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	calls := &recordedCalls{}
	inTool := make(chan struct{}, 8)
	release := make(chan struct{})
	bound := make(chan [2]string, 1)
	request := runtimeChatRequest("turn-steer", calls)
	request.BindTurn = func(_ context.Context, rootThreadID, turnID string) error {
		bound <- [2]string{rootThreadID, turnID}
		return nil
	}
	request.Handler = func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
		inTool <- struct{}{}
		<-release
		return calls.handle(ctx, call)
	}
	type runResult struct {
		result RunResult
		err    error
	}
	finished := make(chan runResult, 1)
	go func() {
		result, err := client.Run(ctx, request)
		finished <- runResult{result, err}
	}()
	var identity [2]string
	select {
	case identity = <-bound:
	case done := <-finished:
		t.Fatalf("the turn ended before it was bound: %+v, %v", done.result, done.err)
	}
	select {
	case <-inTool:
	case done := <-finished:
		t.Fatalf("the turn ended before its tool: %+v, %v", done.result, done.err)
	}

	correction := `{"question":"Без дракона","projectRef":"project","sourceView":"published","scope":"project","correction":true}`
	wrongTurn := client.SteerTurn(ctx, SteerRequest{ThreadID: identity[0], TurnID: "turn-other", ClientMessageID: "steer-wrong", Input: correction})
	if !errors.Is(wrongTurn, agent.ErrTurnNotActive) {
		t.Fatalf("steer into another turn = %v", wrongTurn)
	}
	if err := client.SteerTurn(ctx, SteerRequest{ThreadID: identity[0], TurnID: identity[1], ClientMessageID: "steer-1", Input: correction}); err != nil {
		t.Fatalf("steer: %v", err)
	}
	close(release)
	done := <-finished
	if done.err != nil || done.result.Status != agent.TurnCompleted || done.result.FinalResponse != stubAnswer || done.result.TurnID != identity[1] {
		t.Fatalf("turn = %+v, %v", done.result, done.err)
	}
	if !model.saw("Без дракона") {
		t.Fatal("the model never read the correction")
	}

	items, err := client.ThreadItems(ctx, identity[0])
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := agenttimeline.Project("job-runtime-test", items)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var steer agenttimeline.Event
	for _, event := range snapshot.Events {
		kinds = append(kinds, string(event.Kind))
		if event.Kind == agenttimeline.EventSteer {
			steer = event
		}
	}
	turnIDs := map[string]struct{}{}
	for _, event := range snapshot.Events {
		turnIDs[event.TurnID] = struct{}{}
	}
	if len(turnIDs) != 1 || kinds[0] != "user" || slices.Index(kinds, "steer") < 1 || kinds[len(kinds)-1] != "turn_outcome" ||
		kinds[len(kinds)-2] != "assistant" || slices.Index(kinds, "steer") > len(kinds)-3 ||
		steer.Text != "Без дракона" || steer.SteerID != "steer-1" {
		t.Fatalf("timeline = %v, turns = %v, steer = %+v", kinds, turnIDs, steer)
	}

	late := client.SteerTurn(ctx, SteerRequest{ThreadID: identity[0], TurnID: identity[1], ClientMessageID: "steer-late", Input: correction})
	if !errors.Is(late, agent.ErrTurnNotActive) {
		t.Fatalf("steer into a finished turn = %v", late)
	}
}
