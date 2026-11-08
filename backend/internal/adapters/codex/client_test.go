package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/agent"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

func TestSharedClientStartsRootsWithDistinctToolSets(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	client := newTestClient(t, transport)

	for _, tc := range []struct {
		correlation string
		namespace   string
		tool        string
	}{
		{correlation: "job-a", namespace: "source", tool: "read"},
		{correlation: "job-b", namespace: "canon", tool: "search"},
	} {
		_, err := client.Run(t.Context(), RunRequest{
			CorrelationID: tc.correlation, TurnCorrelationID: tc.correlation + "-turn",
			BundleID: tc.correlation, DeveloperInstructions: "test", InitialInput: "run",
			Tools:    []agent.ToolSpec{{Namespace: tc.namespace, Name: tc.tool, Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`)}},
			BindRoot: func(context.Context, string) error { return nil },
			BindTurn: func(context.Context, string, string) error { return nil },
			Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
				return agent.ToolResult{Success: true, Payload: json.RawMessage(`{"ok":true}`)}, nil
			},
		})
		if err != nil {
			t.Fatalf("run %s: %v", tc.correlation, err)
		}
	}

	starts := transport.threadStarts()
	if transport.methodCount("initialize") != 1 || len(starts) != 2 {
		t.Fatalf("shared client calls: initialize=%d thread/start=%d", transport.methodCount("initialize"), len(starts))
	}
	firstTools, _ := starts[0].DynamicTools.Value()
	if got := dynamicToolNames(firstTools); fmt.Sprint(got) != "[source.read]" {
		t.Fatalf("first root tools = %v", got)
	}
	secondTools, _ := starts[1].DynamicTools.Value()
	if got := dynamicToolNames(secondTools); fmt.Sprint(got) != "[canon.search]" {
		t.Fatalf("second root tools = %v", got)
	}
}

// A resumed chat gets the tools of the bundle, not the ones recorded when the thread started
// (specs/2026-10-28-codex-0165.md UC3).
func TestResumeHandsTheBundleToolsToTheThread(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	client := newTestClient(t, transport)
	var boundTurn string
	request := testRunRequest("chat", "turn-1", "", false, &boundTurn)
	request.Tools = []agent.ToolSpec{{Namespace: "saga", Name: "search", Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	first, err := client.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request = testRunRequest("chat", "turn-2", first.RootThreadID, false, &boundTurn)
	request.Tools = []agent.ToolSpec{
		{Namespace: "saga", Name: "search", Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Namespace: "saga", Name: "read", Description: "test", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	if _, err := client.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	resumes := append(transport.resumes[:0:0], transport.resumes...)
	transport.mu.Unlock()
	if len(resumes) != 1 {
		t.Fatalf("resumes = %d", len(resumes))
	}
	resumed, _ := resumes[0].DynamicTools.Value()
	if got := dynamicToolNames(resumed); fmt.Sprint(got) != "[saga.search saga.read]" {
		t.Fatalf("resumed tools = %v", got)
	}
}

// A chat started on a provider since removed from the config (zai-relay before 13.10) resumes
// on the provider the app-server is configured with; the provider is read once per process.
func TestResumeNamesTheConfiguredModelProvider(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	transport.configModelProvider = "codex-lb"
	client := newTestClient(t, transport)
	var boundTurn string
	first, err := client.Run(t.Context(), testRunRequest("chat", "turn-1", "", false, &boundTurn))
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range []string{"turn-2", "turn-3"} {
		if _, err := client.Run(t.Context(), testRunRequest("chat", turn, first.RootThreadID, false, &boundTurn)); err != nil {
			t.Fatal(err)
		}
	}
	transport.mu.Lock()
	resumes := append(transport.resumes[:0:0], transport.resumes...)
	transport.mu.Unlock()
	if len(resumes) != 2 {
		t.Fatalf("resumes = %d", len(resumes))
	}
	for _, resume := range resumes {
		if provider, _ := resume.ModelProvider.Value(); provider != "codex-lb" {
			t.Fatalf("resume model provider = %q, want the configured codex-lb", provider)
		}
	}
	if reads := transport.methodCount("config/read"); reads != 1 {
		t.Fatalf("config/read calls = %d, want one per process", reads)
	}
}

func TestClientReplacesExitedTransportOnNextResumeWithoutReplay(t *testing.T) {
	first := newFakeTransport()
	first.autoComplete = true
	second := newFakeTransport()
	second.autoComplete = true
	transports := []Transport{first, second}
	factoryCalls := 0
	client, err := NewClient(t.Context(), ClientConfig{
		CWD: "/tmp",
		transportFactory: func(context.Context) (Transport, error) {
			if factoryCalls >= len(transports) {
				return nil, errors.New("unexpected extra transport generation")
			}
			transport := transports[factoryCalls]
			factoryCalls++
			return transport, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var firstTurn string
	var rootThreadID string
	firstRequest := testRunRequest("job-a", "turn-a", "", false, &firstTurn)
	firstRequest.BindRoot = func(_ context.Context, observed string) error {
		rootThreadID = observed
		return nil
	}
	if _, err := client.Run(t.Context(), firstRequest); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first.mu.Lock()
	thread := first.threads[rootThreadID]
	turns := append([]protocol.Turn(nil), first.turns[rootThreadID]...)
	first.mu.Unlock()
	second.addThread(thread, turns...)
	first.fail(agent.ErrRuntimeProcessExited)
	if factoryCalls != 1 {
		t.Fatalf("process exit replayed work: factory calls = %d", factoryCalls)
	}
	var secondTurn string
	if _, err := client.Run(t.Context(), testRunRequest("job-a", "turn-a", rootThreadID, false, &secondTurn)); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if factoryCalls != 2 || len(first.threadStarts()) != 1 || len(second.threadStarts()) != 0 ||
		second.methodCount("thread/resume") != 1 {
		t.Fatalf("generations: factory=%d first_starts=%d second_starts=%d second_resumes=%d",
			factoryCalls, len(first.threadStarts()), len(second.threadStarts()), second.methodCount("thread/resume"))
	}
}

func TestRunLoadsSelectedSkillAsDeveloperInstructions(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	client := newTestClient(t, transport)
	client.repositoryHome = t.TempDir()
	skillBody := "# CanonKeeper indexing\nCall indexing.graph_status first.\n"
	skillPath := filepath.Join(client.repositoryHome, "skills", "canonkeeper-book-enrichment", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(skillBody), 0o444); err != nil {
		t.Fatal(err)
	}
	boundTurn := ""
	request := testRunRequest("job", "job-turn", "", false, &boundTurn)
	request.SkillName = "canonkeeper-book-enrichment"
	request.DeveloperInstructions = ""

	if _, err := client.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	threads := transport.threadStarts()
	developerInstructions, _ := threads[0].DeveloperInstructions.Value()
	turns := transport.turnStartParams()
	if len(threads) != 1 || developerInstructions != communicationInstructions+"\n\n"+skillBody || len(turns) != 1 || len(turns[0].Input) != 1 {
		t.Fatalf("thread/start = %#v, turn/start = %#v", threads, turns)
	}
	text, _ := turns[0].Input[0].Text.Value()
	if turns[0].Input[0].TypeValue != "text" || text != "assigned input" {
		t.Fatalf("turn/start input = %#v", turns[0].Input)
	}
}

func newTestClient(t *testing.T, transport Transport) *Client {
	return newTestClientWithLimits(t, transport, sdk.ClientLimits{})
}

func newTestClientWithLimits(t *testing.T, transport Transport, limits sdk.ClientLimits) *Client {
	t.Helper()
	client, err := NewClient(t.Context(), ClientConfig{Transport: transport, CWD: "/safe/canonkeeper", sdkLimits: limits})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func dynamicToolNames(specs []protocol.DynamicToolSpec) []string {
	var names []string
	for _, spec := range specs {
		if spec.TypeValue == "function" {
			name, _ := spec.Name.Value()
			names = append(names, name)
			continue
		}
		namespace, _ := spec.Name.Value()
		tools, _ := spec.Tools.Value()
		for _, tool := range tools {
			name, _ := tool.Name.Value()
			names = append(names, namespace+"."+name)
		}
	}
	return names
}

type fakeTransport struct {
	mu                         sync.Mutex
	recv                       chan json.RawMessage
	done                       chan struct{}
	closeOnce                  sync.Once
	methods                    []string
	projects                   map[string]string
	threads                    map[string]protocol.Thread
	turns                      map[string][]protocol.Turn
	goals                      map[string]protocol.ThreadGoal
	starts                     []protocol.ThreadStartParams
	resumes                    []protocol.ThreadResumeParams
	turnStarts                 []protocol.TurnStartParams
	turnLists                  []protocol.ThreadTurnsListParams
	deleted                    []string
	completeOnList             map[string]string
	interruptErr               error
	steerRPCError              json.RawMessage
	steers                     []protocol.TurnSteerParams
	interruptDeadline          bool
	projectCreateErr           error
	threadListErr              error
	threadStartErr             error
	threadResumeErr            error
	configModelProvider        string
	threadReadErr              error
	timelineListErr            error
	turnListErr                error
	turnStartErr               error
	autoComplete               bool
	completeGoalOnContinuation bool
	completedItems             []protocol.ThreadItem
	turnStarted                chan struct{}
	receiveErr                 error
	nextThread                 int
	nextTurn                   int
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		recv: make(chan json.RawMessage, 128), done: make(chan struct{}),
		projects: map[string]string{}, threads: map[string]protocol.Thread{}, turns: map[string][]protocol.Turn{},
		goals:          map[string]protocol.ThreadGoal{},
		completeOnList: map[string]string{},
		// A real app-server always reports the provider it runs with.
		configModelProvider: "test",
	}
}

func (transport *fakeTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case frame := <-transport.recv:
		return frame, nil
	case <-transport.done:
		transport.mu.Lock()
		err := transport.receiveErr
		transport.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (transport *fakeTransport) Send(ctx context.Context, frame json.RawMessage) error {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &request); err != nil {
		return err
	}
	if request.Method == "" || request.Method == "initialized" {
		return nil
	}
	transport.mu.Lock()
	transport.methods = append(transport.methods, request.Method)
	if request.Method == "turn/interrupt" {
		_, transport.interruptDeadline = ctx.Deadline()
	}
	if request.Method == "turn/steer" && transport.steerRPCError != nil {
		refusal := transport.steerRPCError
		transport.mu.Unlock()
		encoded, err := json.Marshal(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   json.RawMessage `json:"error"`
		}{JSONRPC: "2.0", ID: request.ID, Error: refusal})
		if err != nil {
			return err
		}
		transport.recv <- encoded
		return nil
	}
	result, notifications, err := transport.handleLocked(request.Method, request.Params)
	transport.mu.Unlock()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: request.ID, Result: result})
	if err != nil {
		return err
	}
	select {
	case transport.recv <- encoded:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, notification := range notifications {
		transport.recv <- notification
	}
	return nil
}

func (transport *fakeTransport) handleLocked(method string, raw json.RawMessage) (json.RawMessage, []json.RawMessage, error) {
	marshal := func(value any) (json.RawMessage, []json.RawMessage, error) {
		body, err := json.Marshal(value)
		return body, nil, err
	}
	switch method {
	case "initialize":
		return marshal(map[string]any{
			"userAgent": "fnd-test", "codexHome": "/tmp/codex", "platformFamily": "unix", "platformOs": "linux",
			"stableProtocolDigest": protocol.StableProtocolDigest, "experimentalProtocolDigest": protocol.ExperimentalProtocolDigest,
			"stableSchemaDigest": protocol.StableSchemaDigest, "experimentalSchemaDigest": protocol.ExperimentalSchemaDigest,
			"stableManifestDigest": protocol.StableManifestDigest, "experimentalManifestDigest": protocol.ExperimentalManifestDigest,
			"activeProtocolMode": "experimental",
		})
	case "project/create":
		if transport.projectCreateErr != nil {
			return nil, nil, transport.projectCreateErr
		}
		var params protocol.ProjectCreateParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		projectID := transport.projects[params.IDempotencyKey]
		if projectID == "" {
			projectID = "project-" + params.IDempotencyKey
			transport.projects[params.IDempotencyKey] = projectID
		}
		return marshal(protocol.ProjectCreateResponse{Project: protocol.Project{ID: projectID, Name: params.Name, Metadata: map[string]string{}, Roots: params.Roots}})
	case "thread/list":
		if transport.threadListErr != nil {
			return nil, nil, transport.threadListErr
		}
		var params protocol.ThreadListParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		projectID, _ := params.ProjectID.Value()
		ancestor, hasAncestor := params.AncestorThreadID.Value()
		if _, explicit := params.SourceKinds.Value(); hasAncestor && !explicit {
			return marshal(protocol.ThreadListResponse{Data: []protocol.Thread{}})
		}
		data := make([]protocol.Thread, 0)
		for _, thread := range transport.threads {
			threadProject, _ := thread.ProjectID.Value()
			if projectID != "" && threadProject != projectID {
				continue
			}
			if hasAncestor && !transport.isDescendantLocked(thread.ID, ancestor) {
				continue
			}
			data = append(data, thread)
		}
		return marshal(protocol.ThreadListResponse{Data: data})
	case "thread/start":
		var params protocol.ThreadStartParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		transport.nextThread++
		threadID := fmt.Sprintf("root-%d", transport.nextThread)
		projectID, _ := params.ProjectID.Value()
		thread := testProtocolThread(threadID, projectID, "", "")
		transport.threads[threadID] = thread
		transport.starts = append(transport.starts, params)
		if transport.threadStartErr != nil {
			return nil, nil, transport.threadStartErr
		}
		return marshal(protocol.ThreadStartResponse{
			ApprovalPolicy: protocol.AskForApprovalNever, Cwd: "/safe/canonkeeper", Model: "test", ModelProvider: "test",
			Sandbox: protocol.SandboxPolicy{TypeValue: "readOnly"}, Thread: thread,
		})
	case "config/read":
		return marshal(protocol.ConfigReadResponse{
			Config: protocol.Config{ModelProvider: protocol.Some(transport.configModelProvider)}, Origins: map[string]protocol.ConfigLayerMetadata{},
		})
	case "thread/resume":
		if transport.threadResumeErr != nil {
			return nil, nil, transport.threadResumeErr
		}
		var params protocol.ThreadResumeParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		transport.resumes = append(transport.resumes, params)
		if params.Sandbox.IsSet() && params.Permissions.IsSet() {
			return nil, nil, &sdk.RPCError{Message: "`permissions` cannot be combined with `sandbox`"}
		}
		excludeTurns, ok := params.ExcludeTurns.Value()
		if !ok || !excludeTurns {
			return nil, nil, &sdk.RPCError{Message: "thread/resume must exclude turns"}
		}
		thread, ok := transport.threads[params.ThreadID]
		if !ok {
			return nil, nil, &sdk.RPCError{Message: "thread not found: " + params.ThreadID}
		}
		return marshal(protocol.ThreadResumeResponse{
			ApprovalPolicy: protocol.AskForApprovalNever, Cwd: "/safe/canonkeeper", Model: "test", ModelProvider: "test",
			Sandbox: protocol.SandboxPolicy{TypeValue: "readOnly"}, Thread: thread,
		})
	case "thread/read":
		if transport.threadReadErr != nil {
			return nil, nil, transport.threadReadErr
		}
		var params protocol.ThreadReadParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		thread, ok := transport.threads[params.ThreadID]
		if !ok {
			return nil, nil, &sdk.RPCError{Message: "thread not found: " + params.ThreadID}
		}
		if include, _ := params.IncludeTurns.Value(); include {
			thread.Turns = append([]protocol.Turn(nil), transport.turns[thread.ID]...)
		}
		return marshal(protocol.ThreadReadResponse{Thread: thread})
	case "thread/goal/set":
		var params protocol.ThreadGoalSetParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		goal := transport.goals[params.ThreadID]
		goal.ThreadID = params.ThreadID
		goal.CreatedAt, goal.UpdatedAt = 1, 1
		if objective, ok := params.Objective.Value(); ok {
			goal.Objective = objective
		}
		if status, ok := params.Status.Value(); ok {
			goal.Status = status
		}
		transport.goals[params.ThreadID] = goal
		notification, err := fakeNotification("thread/goal/updated", protocol.ThreadGoalUpdatedNotification{
			ThreadID: params.ThreadID, Goal: goal,
		})
		body, _, marshalErr := marshal(protocol.ThreadGoalSetResponse{Goal: goal})
		if marshalErr != nil || err != nil {
			return nil, nil, errors.Join(marshalErr, err)
		}
		notifications := []json.RawMessage{notification}
		if transport.completeGoalOnContinuation && goal.Status == protocol.ThreadGoalStatusActive {
			transport.nextTurn++
			turnID := fmt.Sprintf("turn-%d", transport.nextTurn)
			turn := protocol.Turn{ID: turnID, Status: protocol.TurnStatusInProgress, Items: []protocol.ThreadItem{}}
			transport.turns[params.ThreadID] = append(transport.turns[params.ThreadID], turn)
			started, startErr := fakeNotification("turn/started", protocol.TurnStartedNotification{ThreadID: params.ThreadID, Turn: turn})
			finalItem := protocol.ThreadItem{TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-" + turnID), Text: protocol.SomeNonNull("goal done")}
			turn.Status, turn.Items = protocol.TurnStatusCompleted, []protocol.ThreadItem{finalItem}
			transport.replaceTurnLocked(params.ThreadID, turn)
			completedItem, itemErr := fakeNotification("item/completed", protocol.ItemCompletedNotification{ThreadID: params.ThreadID, TurnID: turnID, Item: finalItem})
			goal.Status = protocol.ThreadGoalStatusComplete
			transport.goals[params.ThreadID] = goal
			goalCompleted, goalErr := fakeNotification("thread/goal/updated", protocol.ThreadGoalUpdatedNotification{ThreadID: params.ThreadID, TurnID: protocol.Some(turnID), Goal: goal})
			turnCompleted, turnErr := fakeNotification("turn/completed", protocol.TurnCompletedNotification{ThreadID: params.ThreadID, Turn: turn})
			if joined := errors.Join(startErr, itemErr, goalErr, turnErr); joined != nil {
				return nil, nil, joined
			}
			notifications = append(notifications, started, completedItem, goalCompleted, turnCompleted)
		}
		return body, notifications, nil
	case "thread/goal/get":
		var params protocol.ThreadGoalGetParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		goal, ok := transport.goals[params.ThreadID]
		response := protocol.ThreadGoalGetResponse{}
		if ok {
			response.Goal = protocol.Some(goal)
		}
		return marshal(response)
	case "thread/goal/clear":
		var params protocol.ThreadGoalClearParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		_, cleared := transport.goals[params.ThreadID]
		delete(transport.goals, params.ThreadID)
		return marshal(protocol.ThreadGoalClearResponse{Cleared: cleared})
	case "thread/turns/list":
		if transport.turnListErr != nil {
			return nil, nil, transport.turnListErr
		}
		var params protocol.ThreadTurnsListParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		itemsView, ok := params.ItemsView.Value()
		if !ok || itemsView != protocol.TurnItemsViewNotLoaded {
			return nil, nil, &sdk.RPCError{Message: "thread/turns/list must not load items"}
		}
		transport.turnLists = append(transport.turnLists, params)
		data := append([]protocol.Turn{}, transport.turns[params.ThreadID]...)
		turnShells := func(turns []protocol.Turn) []protocol.Turn {
			shells := append([]protocol.Turn{}, turns...)
			for index := range shells {
				shells[index].Items = []protocol.ThreadItem{}
				shells[index].ItemsView = protocol.SomeNonNull(protocol.TurnItemsViewNotLoaded)
			}
			return shells
		}
		turnID := transport.completeOnList[params.ThreadID]
		if turnID == "" {
			return marshal(protocol.ThreadTurnsListResponse{Data: turnShells(data)})
		}
		turn, found := turnByID(data, turnID)
		if !found {
			return nil, nil, errors.New("completion fixture turn missing")
		}
		turn.Status = protocol.TurnStatusCompleted
		finalItem := protocol.ThreadItem{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-" + turnID), Text: protocol.SomeNonNull("observed"),
		}
		turn.Items = append(turn.Items, finalItem)
		transport.replaceTurnLocked(params.ThreadID, turn)
		delete(transport.completeOnList, params.ThreadID)
		body, _, err := marshal(protocol.ThreadTurnsListResponse{Data: turnShells(data)})
		if err != nil {
			return nil, nil, err
		}
		itemCompleted, err := fakeNotification("item/completed", protocol.ItemCompletedNotification{
			ThreadID: params.ThreadID, TurnID: turnID, Item: finalItem,
		})
		if err != nil {
			return nil, nil, err
		}
		turn.Items = []protocol.ThreadItem{}
		turnCompleted, err := fakeNotification("turn/completed", protocol.TurnCompletedNotification{ThreadID: params.ThreadID, Turn: turn})
		return body, []json.RawMessage{itemCompleted, turnCompleted}, err
	case "thread/items/list":
		var params protocol.ThreadItemsListParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		turnID, ok := params.TurnID.Value()
		if !ok || turnID == "" {
			return nil, nil, &sdk.RPCError{Message: "thread/items/list requires turnId"}
		}
		entries := make([]protocol.ThreadItemEntry, 0)
		for _, turn := range transport.turns[params.ThreadID] {
			if turn.ID != turnID {
				continue
			}
			for _, item := range turn.Items {
				entries = append(entries, protocol.ThreadItemEntry{TurnID: turn.ID, Item: item})
			}
		}
		limit := 100
		if value, ok := params.Limit.Value(); ok {
			limit = int(value)
		}
		start := 0
		if cursor, ok := params.Cursor.Value(); ok {
			value, opaque := cursor.StringValue()
			if !opaque {
				return nil, nil, errors.New("item anchors are not used")
			}
			if _, err := fmt.Sscanf(value, "%d", &start); err != nil || start < 0 || start > len(entries) {
				return nil, nil, errors.New("invalid item cursor")
			}
		}
		end := min(len(entries), start+limit)
		response := protocol.ThreadItemsListResponse{Data: append([]protocol.ThreadItemEntry(nil), entries[start:end]...)}
		if end < len(entries) {
			response.NextCursor = protocol.Some(fmt.Sprintf("%d", end))
		}
		return marshal(response)
	case "thread/timeline/list":
		if transport.timelineListErr != nil {
			return nil, nil, transport.timelineListErr
		}
		var params protocol.ThreadTimelineListParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		position := ^uint64(0)
		data := make([]protocol.ThreadTimelineEntry, 0)
		for _, turn := range transport.turns[params.ThreadID] {
			position++
			data = append(data, protocol.ThreadTimelineEntry{
				TypeValue: "turnStarted", Position: protocol.SomeNonNull(position),
				TurnID: protocol.SomeNonNull(turn.ID),
			})
			for _, item := range turn.Items {
				position++
				rawItem, err := json.Marshal(item)
				if err != nil {
					return nil, nil, err
				}
				data = append(data, protocol.ThreadTimelineEntry{
					TypeValue: "item", Position: protocol.SomeNonNull(position),
					TurnID: protocol.SomeNonNull(turn.ID), Item: rawItem,
				})
			}
			if turn.Status != protocol.TurnStatusInProgress {
				position++
				data = append(data, protocol.ThreadTimelineEntry{
					TypeValue: "turnCompleted", Position: protocol.SomeNonNull(position),
					TurnID: protocol.SomeNonNull(turn.ID), Status: protocol.SomeNonNull(turn.Status),
				})
			}
		}
		limit := 100
		if value, ok := params.Limit.Value(); ok {
			limit = int(value)
		}
		end := len(data)
		if value, ok := params.Cursor.Value(); ok {
			if _, err := fmt.Sscanf(value, "%d", &end); err != nil || end < 0 || end > len(data) {
				return nil, nil, errors.New("invalid timeline cursor")
			}
		}
		start := max(0, end-limit)
		response := protocol.ThreadTimelineListResponse{Data: append([]protocol.ThreadTimelineEntry(nil), data[start:end]...)}
		if start > 0 {
			response.NextCursor = protocol.Some(fmt.Sprintf("%d", start))
		}
		return marshal(response)
	case "turn/start":
		if transport.turnStartErr != nil {
			return nil, nil, transport.turnStartErr
		}
		var params protocol.TurnStartParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		transport.nextTurn++
		turnID := fmt.Sprintf("turn-%d", transport.nextTurn)
		clientID, _ := params.ClientUserMessageID.Value()
		turn := protocol.Turn{ID: turnID, Status: protocol.TurnStatusInProgress, Items: []protocol.ThreadItem{{
			TypeValue: "userMessage", ID: protocol.SomeNonNull("user-" + turnID), ClientID: protocol.Some(clientID),
			Content: json.RawMessage(`[{"type":"text","text":"test"}]`),
		}}}
		transport.turns[params.ThreadID] = append(transport.turns[params.ThreadID], turn)
		transport.turnStarts = append(transport.turnStarts, params)
		if transport.turnStarted != nil {
			transport.turnStarted <- struct{}{}
		}
		body, _, err := marshal(protocol.TurnStartResponse{Turn: turn})
		if err != nil || !transport.autoComplete {
			return body, nil, err
		}
		turn.Status = protocol.TurnStatusCompleted
		notifications := make([]json.RawMessage, 0, len(transport.completedItems)+1)
		for _, item := range transport.completedItems {
			turn.Items = append(turn.Items, item)
			completed, itemErr := fakeNotification("item/completed", protocol.ItemCompletedNotification{ThreadID: params.ThreadID, TurnID: turnID, Item: item})
			if itemErr != nil {
				return nil, nil, itemErr
			}
			notifications = append(notifications, completed)
		}
		turn.Items = append(turn.Items, protocol.ThreadItem{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-" + turnID), Text: protocol.SomeNonNull("done"),
		})
		transport.replaceTurnLocked(params.ThreadID, turn)
		notification, err := fakeNotification("turn/completed", protocol.TurnCompletedNotification{ThreadID: params.ThreadID, Turn: turn})
		notifications = append(notifications, notification)
		return body, notifications, err
	case "turn/interrupt":
		var params protocol.TurnInterruptParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		found := false
		for index, turn := range transport.turns[params.ThreadID] {
			if turn.Status != protocol.TurnStatusInProgress {
				continue
			}
			if turn.ID != params.TurnID {
				return nil, nil, fmt.Errorf("expected active turn id %s but found %s", params.TurnID, turn.ID)
			}
			turn.Status = protocol.TurnStatusInterrupted
			transport.turns[params.ThreadID][index] = turn
			found = true
		}
		if transport.interruptErr != nil {
			err := transport.interruptErr
			transport.interruptErr = nil
			return nil, nil, err
		}
		if !found {
			return nil, nil, errors.New("no active turn to interrupt")
		}
		return marshal(protocol.TurnInterruptResponse{})
	case "turn/steer":
		var params protocol.TurnSteerParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		transport.steers = append(transport.steers, params)
		return marshal(protocol.TurnSteerResponse{TurnID: params.ExpectedTurnID})
	case "thread/delete":
		var params protocol.ThreadDeleteParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, nil, err
		}
		delete(transport.threads, params.ThreadID)
		delete(transport.turns, params.ThreadID)
		transport.deleted = append(transport.deleted, params.ThreadID)
		return marshal(protocol.ThreadDeleteResponse{})
	default:
		return nil, nil, fmt.Errorf("unexpected SDK method %q", method)
	}
}

func (transport *fakeTransport) Close() error {
	transport.closeOnce.Do(func() { close(transport.done) })
	return nil
}

func (transport *fakeTransport) fail(err error) {
	transport.mu.Lock()
	transport.receiveErr = err
	transport.mu.Unlock()
	transport.closeOnce.Do(func() { close(transport.done) })
}

func (transport *fakeTransport) RuntimeDone() <-chan struct{} { return transport.done }

func (transport *fakeTransport) RuntimeExitError() error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.receiveErr
}

func (transport *fakeTransport) addThread(thread protocol.Thread, turns ...protocol.Turn) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.threads[thread.ID] = thread
	transport.turns[thread.ID] = append([]protocol.Turn(nil), turns...)
}

func (transport *fakeTransport) replaceTurnLocked(threadID string, replacement protocol.Turn) {
	for index, turn := range transport.turns[threadID] {
		if turn.ID == replacement.ID {
			transport.turns[threadID][index] = replacement
			return
		}
	}
}

func (transport *fakeTransport) isDescendantLocked(threadID, ancestor string) bool {
	for threadID != "" {
		thread, ok := transport.threads[threadID]
		if !ok {
			return false
		}
		parent, _ := thread.ParentThreadID.Value()
		if parent == ancestor {
			return true
		}
		threadID = parent
	}
	return false
}

func (transport *fakeTransport) threadStarts() []protocol.ThreadStartParams {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]protocol.ThreadStartParams(nil), transport.starts...)
}

func (transport *fakeTransport) turnStartParams() []protocol.TurnStartParams {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]protocol.TurnStartParams(nil), transport.turnStarts...)
}

func (transport *fakeTransport) turnListParams() []protocol.ThreadTurnsListParams {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]protocol.ThreadTurnsListParams(nil), transport.turnLists...)
}

func (transport *fakeTransport) methodCount(method string) int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	count := 0
	for _, got := range transport.methods {
		if got == method {
			count++
		}
	}
	return count
}

func (transport *fakeTransport) interruptWasBounded() bool {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.interruptDeadline
}

func testProtocolThread(id, projectID, parentID, role string) protocol.Thread {
	thread := protocol.Thread{
		CliVersion: "test", CreatedAt: 1, Cwd: "/safe/canonkeeper", ID: id, ModelProvider: "test",
		ProjectID: protocol.Some(projectID), SessionID: "session-" + id, Source: protocol.SessionSourceCli,
		Status: protocol.ThreadStatus{TypeValue: "idle"}, Turns: []protocol.Turn{}, UpdatedAt: 1,
	}
	if parentID != "" {
		thread.ParentThreadID = protocol.Some(parentID)
		thread.ThreadSource = protocol.Some(protocol.ThreadSource(protocol.ThreadSourceKindSubAgent))
	}
	if role != "" {
		thread.AgentRole = protocol.Some(role)
	}
	return thread
}

func testTurn(id, clientID string, status protocol.TurnStatus) protocol.Turn {
	return protocol.Turn{ID: id, Status: status, Items: []protocol.ThreadItem{{
		TypeValue: "userMessage", ID: protocol.SomeNonNull("user-" + id), ClientID: protocol.Some(clientID),
		Content: json.RawMessage(`[{"type":"text","text":"test"}]`),
	}}}
}

func fakeNotification(method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPC: "2.0", Method: method, Params: raw})
}
