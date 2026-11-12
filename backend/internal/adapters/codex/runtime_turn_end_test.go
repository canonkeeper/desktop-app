package codex

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// The turn ends where it ended (specs/2026-11-12-desk-codex.md CX2): the fork's features that let a thread go on after
// turn/completed are off in every profile, the model sees only the tools of its flow and Codex's own prefix of the root
// request keeps its size. The tests run the pinned binary against the stub model and skip without it.

// runtimeFlow is one Codex flow of the product as these tests run it: its profile, its skill and tools that carry
// names its bundle offers. Every flow reads the saga, so the stub's two saga.search calls fit each of them.
type runtimeFlow struct {
	home, skill string
	tools       []agent.ToolSpec
}

// runtimeTool is a tool of a flow's bundle as these tests declare it.
func runtimeTool(namespace, name string) agent.ToolSpec {
	return agent.ToolSpec{
		Namespace: namespace, Name: name, Description: "Test tool " + namespace + "." + name + ".",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"queries":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`),
	}
}

// chatFlow is the flow of the open core. The helpers of the workshops are closed code of the site, and so are their
// profiles: their runs are in runtime_turn_end_site_test.go.
func chatFlow() runtimeFlow {
	return runtimeFlow{home: "research-chat", skill: "canonkeeper-research-chat", tools: []agent.ToolSpec{
		runtimeTool("saga", "search"), runtimeTool("saga", "read"),
	}}
}

// codexTools are the only tools Codex itself adds to a flow: the goal tools of `goals = true`. No agents, workflows,
// worktrees, shell, clock or questions to the user.
var codexTools = []string{"create_goal", "get_goal", "update_goal"}

func (flow runtimeFlow) request(turnID string, calls *recordedCalls) RunRequest {
	request := runtimeChatRequest(turnID, calls)
	request.DeveloperInstructions, request.SkillName, request.Tools = "", flow.skill, flow.tools
	return request
}

func (flow runtimeFlow) wantTools() []string {
	tools := slices.Clone(codexTools)
	for _, spec := range flow.tools {
		tools = append(tools, spec.Namespace+"."+spec.Name)
	}
	slices.Sort(tools)
	return tools
}

// observedNotification is one notification of the app-server: its method, the thread and turn it names and the type
// and id of its item, if it carries one.
type observedNotification struct {
	method, threadID, turnID string
	itemType, itemID         string
}

// observeNotifications records every notification of the runtime until stop.
func observeNotifications(ctx context.Context, t *testing.T, client *Client) (stop func() []observedNotification) {
	t.Helper()
	runtime, err := client.runtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observeCtx, cancel := context.WithCancel(ctx)
	stream, err := runtime.sdk.Notifications(observeCtx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		seen []observedNotification
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		for {
			notification, ok := stream.Next(observeCtx)
			if !ok {
				return
			}
			var ids struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Turn     struct {
					ID string `json:"id"`
				} `json:"turn"`
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
				Item struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"item"`
			}
			_ = json.Unmarshal(notification.RawParams, &ids)
			observed := observedNotification{
				method: notification.Method, threadID: ids.ThreadID, turnID: ids.TurnID,
				itemType: ids.Item.Type, itemID: ids.Item.ID,
			}
			if observed.threadID == "" {
				observed.threadID = ids.Thread.ID
			}
			if observed.turnID == "" {
				observed.turnID = ids.Turn.ID
			}
			mu.Lock()
			seen = append(seen, observed)
			mu.Unlock()
		}
	}()
	return func() []observedNotification {
		cancel()
		_ = stream.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// afterTurnQuiet is how long a test listens after turn/completed: a woken thread starts its new turn at once.
const afterTurnQuiet = 3 * time.Second

// wentOn are the notifications of the root's turns and items after turn/completed of its turn: a thread that starts a
// turn by itself or takes an item after its turn ended shows here. completed is false without that turn/completed.
func wentOn(seen []observedNotification, root, turnID string) (after []observedNotification, completed bool) {
	end := slices.IndexFunc(seen, func(n observedNotification) bool {
		return n.method == "turn/completed" && n.threadID == root && n.turnID == turnID
	})
	if end < 0 {
		return nil, false
	}
	for _, notification := range seen[end+1:] {
		if notification.threadID == root &&
			(strings.HasPrefix(notification.method, "turn/") || strings.HasPrefix(notification.method, "item/")) {
			after = append(after, notification)
		}
	}
	return after, true
}

// testFlowEndsItsTurn runs one turn of the flow on the pinned binary: one turn is one root thread, after turn/completed
// the thread starts nothing by itself, and the model sees the tools of the flow and the goal tools only (CX2.2, CX2.3).
func testFlowEndsItsTurn(t *testing.T, binary string, flow runtimeFlow) {
	t.Helper()
	model := &stubModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client, err := NewClient(ctx, runtimeFlowConfig(t, flow.home, binary, t.TempDir(), server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	stop := observeNotifications(ctx, t, client)
	result, err := client.Run(ctx, flow.request("turn-1", &recordedCalls{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != agent.TurnCompleted || result.FinalResponse != stubAnswer || result.RootThreadID == "" {
		t.Fatalf("turn = %+v", result)
	}
	time.Sleep(afterTurnQuiet)
	seen := stop()

	root := result.RootThreadID
	after, completed := wentOn(seen, root, result.TurnID)
	if !completed {
		t.Fatalf("no turn/completed of turn %s among %v", result.TurnID, seen)
	}
	if len(after) > 0 {
		t.Fatalf("the thread went on after turn/completed: %+v", after)
	}
	for _, notification := range seen {
		if notification.method == "thread/started" && notification.threadID != root {
			t.Fatalf("a second thread started: %+v", notification)
		}
	}
	runtime, err := client.runtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := runtime.listTurns(ctx, root)
	if err != nil || len(turns) != 1 {
		t.Fatalf("root thread turns = %d, %v; want one", len(turns), err)
	}
	threads, err := runtime.sdk.Threads.List(ctx, protocol.ThreadListParams{})
	if err != nil || len(threads.Data) != 1 || threads.Data[0].ID != root {
		t.Fatalf("threads = %+v, %v; want the root only", threads.Data, err)
	}

	requests := model.snapshot()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want the call and the answer", len(requests))
	}
	for index, recorded := range requests {
		if want := flow.wantTools(); !slices.Equal(recorded.Tools, want) {
			t.Fatalf("model request %d tools = %v, want %v", index, recorded.Tools, want)
		}
	}
}

func TestRuntimeChatEndsItsTurnAtTurnCompleted(t *testing.T) {
	testFlowEndsItsTurn(t, runtimeTestBinary(t), chatFlow())
}

// The check of testFlowEndsItsTurn can fail. A thread woken after turn/completed — by a workflow's result, a background
// process or a subagent's answer — starts a new turn on its root, and that is what the check looks for. The profiles
// leave no feature that would wake a thread, so here the product itself asks for the second turn.
func TestRuntimeTurnEndCheckSeesATurnAfterTurnCompleted(t *testing.T) {
	binary := runtimeTestBinary(t)
	model := &stubModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	flow := chatFlow()
	client, err := NewClient(ctx, runtimeFlowConfig(t, flow.home, binary, t.TempDir(), server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	stop := observeNotifications(ctx, t, client)
	first, err := client.Run(ctx, flow.request("turn-1", &recordedCalls{}))
	if err != nil {
		t.Fatal(err)
	}
	request := flow.request("turn-2", &recordedCalls{})
	request.ExistingRootThreadID = first.RootThreadID
	second, err := client.Run(ctx, request)
	if err != nil || second.RootThreadID != first.RootThreadID || second.TurnID == first.TurnID {
		t.Fatalf("second turn = %+v, %v", second, err)
	}
	seen := stop()

	after, completed := wentOn(seen, first.RootThreadID, first.TurnID)
	if !completed || !slices.ContainsFunc(after, func(n observedNotification) bool {
		return n.method == "turn/started" && n.turnID == second.TurnID
	}) {
		t.Fatalf("the check missed turn %s after turn %s: completed %v, after %+v", second.TurnID, first.TurnID, completed, after)
	}
	if after, completed := wentOn(seen, first.RootThreadID, second.TurnID); !completed || len(after) > 0 {
		t.Fatalf("after the last turn: completed %v, after %+v", completed, after)
	}
}

// The profile names the new namespace of MultiAgentV2 (CX2.1). Codex 0.167.2 moves the agent tools to `agents` only
// while agent_worktree_isolation is on, and the profiles turn it off; were the agents ever turned on, their tools come
// under `agents` all the same, not under the reserved `collaboration`.
func TestRuntimeChatProfileNamesTheNewNamespaceOfAgents(t *testing.T) {
	binary := runtimeTestBinary(t)
	model := &stubModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	flow := chatFlow()
	config := runtimeFlowConfig(t, flow.home, binary, t.TempDir(), server.URL)
	config.ConfigOverrides = append(config.ConfigOverrides, "agents.enabled=true", "features.multi_agent_v2.enabled=true")
	client, err := NewClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Run(ctx, flow.request("turn-1", &recordedCalls{})); err != nil {
		t.Fatal(err)
	}
	tools := model.snapshot()[0].Tools
	if !slices.Contains(tools, "agents.spawn_agent") ||
		slices.ContainsFunc(tools, func(name string) bool { return strings.HasPrefix(name, "collaboration.") }) {
		t.Fatalf("tools with the agents on = %v, want the agent tools under agents", tools)
	}
}

// The first request of a chat turn as Codex 0.165.4 sent it (12.11, the stub of these tests): the characters Codex
// adds around the turn's own instructions and tools — its base instructions and its goal tools. A newer runtime may
// differ by 2 % at most (CX2.4); the turn's own skill and tools are not counted, so their edits do not move it, nor is
// the path of the profile, which names the skill roots and depends on where the tree lies.
const (
	codexRootInstructionChars = 21698
	codexRootToolChars        = 3213
	codexRootTolerance        = 0.02
)

func TestRuntimeChatRootRequestKeepsCodexPrefixSize(t *testing.T) {
	binary := runtimeTestBinary(t)
	model := &stubModel{}
	server := httptest.NewServer(model)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	flow := chatFlow()
	client, err := NewClient(ctx, runtimeFlowConfig(t, flow.home, binary, t.TempDir(), server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request := flow.request("turn-1", &recordedCalls{})
	if _, err := client.Run(ctx, request); err != nil {
		t.Fatal(err)
	}
	runtime, err := client.runtime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	own, err := runtime.developerInstructions(request)
	if err != nil {
		t.Fatal(err)
	}
	// Codex sends the turn's instructions as a developer message and appends its permissions to it.
	first := model.snapshot()[0]
	profile := runtimeTestProfile(t, flow.home)
	instructions, tools, ownSent := 0, 0, false
	for _, text := range first.Instructions {
		if rest, ok := strings.CutPrefix(text, own); ok && !ownSent {
			text, ownSent = rest, true
		}
		instructions += len([]rune(strings.ReplaceAll(text, profile, "")))
	}
	if !ownSent {
		t.Fatal("the turn's own instructions do not open a developer message of the first request")
	}
	for _, declaration := range first.Declarations {
		var named struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(declaration, &named)
		if !slices.ContainsFunc(flow.tools, func(spec agent.ToolSpec) bool { return spec.Namespace == named.Name }) {
			tools += len([]rune(string(declaration)))
		}
	}
	t.Logf("Codex prefix of the first chat request: instructions %d, tools %d characters", instructions, tools)
	for _, size := range []struct {
		name      string
		got, want int
	}{{"instructions", instructions, codexRootInstructionChars}, {"tools", tools, codexRootToolChars}} {
		if diff := float64(size.got-size.want) / float64(size.want); diff > codexRootTolerance || diff < -codexRootTolerance {
			t.Fatalf("Codex %s of the first chat request = %d characters, %d on 0.165.4 (%+.1f %%)", size.name, size.got, size.want, 100*diff)
		}
	}
}
