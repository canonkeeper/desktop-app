package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	agenttimeline "canonkeeper/backend/internal/domain/agenttimeline"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

func TestThreadStartUsesPermissionProfileWithoutConflictingSandbox(t *testing.T) {
	options := (&Client{cwd: "/safe/canonkeeper"}).startOptions("project", RunRequest{})
	value := reflect.ValueOf(options)
	permissions := value.FieldByName("Permissions")
	sandbox := value.FieldByName("Sandbox")
	if !permissions.IsValid() || fmt.Sprint(permissions.Interface()) != "canonkeeper-runtime" {
		t.Fatalf("permissions = %v", permissions)
	}
	if !sandbox.IsValid() || !sandbox.IsZero() {
		t.Fatalf("sandbox conflicts with permission profile: %v", sandbox.Interface())
	}
}

func TestGoalRunSetsNativeThreadGoal(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	transport.completeGoalOnContinuation = true
	client := newTestClient(t, transport)
	var boundTurn string
	request := testRunRequest("job", "job-turn", "", false, &boundTurn)
	request.GoalObjective = "complete the bound operation"

	if _, err := client.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if got := transport.methodCount("thread/goal/set"); got != 1 {
		t.Fatalf("thread/goal/set calls = %d, want 1", got)
	}
}

func TestGoalRunReportsCompletedContextCompactionItem(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	transport.completeGoalOnContinuation = true
	transport.completedItems = []protocol.ThreadItem{{TypeValue: "contextCompaction", ID: protocol.SomeNonNull("compact-1")}}
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.GoalObjective = "complete the bound operation"
	var got string
	request.OnContextCompaction = func(_ context.Context, threadID, turnID, itemID string) error {
		got = threadID + "/" + turnID + "/" + itemID
		return nil
	}

	if _, err := client.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if got != "root-1/turn-1/compact-1" {
		t.Fatalf("compaction = %q", got)
	}
}

func TestCompactionBarrierReplaysDurableItemBeforeFreezeHandler(t *testing.T) {
	transport := newFakeTransport()
	client := newTestClient(t, transport)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := false
	turnPrepared := make(chan struct{}, 1)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BindTurn = func(context.Context, string, string) error {
		turnPrepared <- struct{}{}
		return nil
	}
	request.OnContextCompaction = func(_ context.Context, _, _, _ string) error {
		observed = true
		return nil
	}
	request.ContextCompactionBarrier = func(call agent.ToolCall) bool {
		return call.Namespace == "indexing" && call.Tool == "finish_graph_self_review"
	}
	request.Handler = func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
		if !observed {
			return agent.ToolResult{}, errors.New("freeze handler ran before compaction replay")
		}
		return agent.ToolResult{Success: true}, nil
	}
	runDone := make(chan error, 1)
	go func() {
		_, err := client.Run(ctx, request)
		runDone <- err
	}()
	select {
	case <-turnPrepared:
	case <-time.After(time.Second):
		t.Fatal("turn was not prepared for dispatch")
	}
	transport.mu.Lock()
	rootThreadID := transport.turnStarts[0].ThreadID
	turnID := transport.turns[rootThreadID][0].ID
	transport.turns[rootThreadID][0].Items = append(transport.turns[rootThreadID][0].Items,
		protocol.ThreadItem{TypeValue: "contextCompaction", ID: protocol.SomeNonNull("compact-1")})
	transport.mu.Unlock()

	response, err := client.dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: rootThreadID, TurnID: turnID, CallID: "finish", Namespace: protocol.Some("indexing"),
		Tool: "finish_graph_self_review", Arguments: []byte(`{}`),
	})
	if err != nil || !response.Success {
		t.Fatalf("freeze response=%#v err=%v", response, err)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("run did not stop")
	}
}

func TestListTurnsHydratesItemsThroughPages(t *testing.T) {
	transport := newFakeTransport()
	items := make([]protocol.ThreadItem, 205)
	for index := range items {
		items[index] = protocol.ThreadItem{
			TypeValue: "agentMessage",
			ID:        protocol.SomeNonNull(fmt.Sprintf("item-%03d", index)),
			Text:      protocol.SomeNonNull("text"),
		}
	}
	transport.addThread(
		testProtocolThread("root-existing", "project-job", "", ""),
		protocol.Turn{ID: "turn-1", Status: protocol.TurnStatusCompleted, Items: items},
	)
	client := newTestClient(t, transport)

	turns, err := client.listTurns(t.Context(), "root-existing")
	if err != nil || len(turns) != 1 || len(turns[0].Items) != len(items) {
		t.Fatalf("turns=%#v err=%v", turns, err)
	}
	firstID, _ := turns[0].Items[0].ID.Value()
	lastID, _ := turns[0].Items[len(items)-1].ID.Value()
	if firstID != "item-000" || lastID != "item-204" ||
		transport.methodCount("thread/turns/list") != 1 || transport.methodCount("thread/items/list") != 3 {
		t.Fatalf("items=%s..%s turn_lists=%d item_lists=%d", firstID, lastID,
			transport.methodCount("thread/turns/list"), transport.methodCount("thread/items/list"))
	}
}

func TestRunPreservesRuntimeProcessExit(t *testing.T) {
	transport := newFakeTransport()
	transport.turnStarted = make(chan struct{}, 1)
	client := newTestClient(t, transport)
	runDone := make(chan error, 1)
	go func() {
		_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "", false, new(string)))
		runDone <- err
	}()
	select {
	case <-transport.turnStarted:
	case <-time.After(time.Second):
		t.Fatal("turn did not start")
	}
	transport.fail(agent.ErrRuntimeProcessExited)
	select {
	case err := <-runDone:
		if !errors.Is(err, agent.ErrRuntimeProcessExited) {
			t.Fatalf("run error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not observe process exit")
	}
}

func TestGoalRunFollowsNativeContinuationUntilGoalTerminal(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	transport.completeGoalOnContinuation = true
	client := newTestClient(t, transport)
	boundTurns := make([]string, 0, 2)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.GoalObjective = "complete the bound operation"
	request.BindTurn = func(_ context.Context, _ string, turnID string) error {
		boundTurns = append(boundTurns, turnID)
		return nil
	}

	result, err := client.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(boundTurns, []string{"turn-1", "turn-2"}) || result.TurnID != "turn-2" || result.FinalResponse != "goal done" {
		t.Fatalf("bound=%v result=%+v", boundTurns, result)
	}
}

func TestGoalRunResumesCompletedInitialThroughNativeContinuation(t *testing.T) {
	transport := newFakeTransport()
	transport.completeGoalOnContinuation = true
	root := testProtocolThread("root-existing", "project-job", "", "")
	transport.addThread(root, testTurn("turn-existing", "job-turn", protocol.TurnStatusCompleted))
	transport.projects["job"] = "project-job"
	client := newTestClient(t, transport)
	boundTurns := make([]string, 0, 2)
	request := testRunRequest("job", "job-turn", "root-existing", false, new(string))
	request.GoalObjective = "complete the bound operation"
	request.BindTurn = func(_ context.Context, _ string, turnID string) error {
		boundTurns = append(boundTurns, turnID)
		return nil
	}

	result, err := client.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(boundTurns, []string{"turn-existing", "turn-1"}) || result.TurnID != "turn-1" || result.FinalResponse != "goal done" {
		t.Fatalf("bound=%v result=%+v", boundTurns, result)
	}
}

func TestGoalRunRestartsActiveGoalWithoutInProgressTurn(t *testing.T) {
	transport := newFakeTransport()
	transport.completeGoalOnContinuation = true
	root := testProtocolThread("root-existing", "project-job", "", "")
	transport.addThread(
		root,
		testTurn("turn-assigned", "job-turn", protocol.TurnStatusInterrupted),
		testTurn("turn-consultation", "operator-consultation", protocol.TurnStatusCompleted),
	)
	transport.projects["job"] = "project-job"
	transport.goals[root.ID] = protocol.ThreadGoal{
		ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusActive,
	}
	client := newTestClient(t, transport)
	boundTurns := make([]string, 0, 2)
	request := testRunRequest("job", "job-turn", "root-existing", true, new(string))
	request.GoalObjective = "complete the bound operation"
	request.ExistingTurnID = "turn-assigned"
	request.BindTurn = func(_ context.Context, _ string, turnID string) error {
		boundTurns = append(boundTurns, turnID)
		return nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	result, err := client.Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(boundTurns, []string{"turn-consultation", "turn-1"}) ||
		result.TurnID != "turn-1" || result.FinalResponse != "goal done" ||
		transport.methodCount("thread/goal/set") != 1 {
		t.Fatalf("bound=%v result=%+v goal sets=%d", boundTurns, result, transport.methodCount("thread/goal/set"))
	}
}

func TestGoalRunRecoversLatestDurableContinuation(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root-existing", "project-job", "", "")
	initial := testTurn("turn-initial", "job-turn", protocol.TurnStatusCompleted)
	continuation := testTurn("turn-goal", "", protocol.TurnStatusCompleted)
	continuation.Items = append(continuation.Items, protocol.ThreadItem{
		TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-goal"), Text: protocol.SomeNonNull("goal done"),
	})
	transport.addThread(root, initial, continuation)
	transport.projects["job"] = "project-job"
	transport.goals[root.ID] = protocol.ThreadGoal{
		ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusComplete,
	}
	client := newTestClient(t, transport)
	var boundTurn string
	request := testRunRequest("job", "job-turn", "root-existing", false, &boundTurn)
	request.GoalObjective = "complete the bound operation"

	result, err := client.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if boundTurn != "turn-goal" || result.TurnID != "turn-goal" || result.FinalResponse != "goal done" {
		t.Fatalf("bound=%q result=%+v", boundTurn, result)
	}
}

func TestGoalResumeReactivatesOnlyExplicitUsageLimitedGoal(t *testing.T) {
	limitedTransport := func() *fakeTransport {
		transport := newFakeTransport()
		root := testProtocolThread("root-existing", "project-job", "", "")
		transport.addThread(root, testTurn("turn-limited", "job-turn", protocol.TurnStatusFailed))
		transport.projects["job"] = "project-job"
		transport.goals[root.ID] = protocol.ThreadGoal{
			ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusUsageLimited,
		}
		return transport
	}

	t.Run("non-explicit recovery remains parked", func(t *testing.T) {
		transport := limitedTransport()
		client := newTestClient(t, transport)
		request := testRunRequest("job", "job-turn", "root-existing", true, new(string))
		request.GoalObjective = "complete the bound operation"
		request.ExistingTurnID = "turn-limited"

		result, err := client.Run(t.Context(), request)
		if err != nil || result.Failure != agent.RunFailureUsageLimitExceeded || transport.methodCount("thread/goal/set") != 0 {
			t.Fatalf("result=%+v goal sets=%d err=%v", result, transport.methodCount("thread/goal/set"), err)
		}
	})

	t.Run("explicit recovery reactivates the same goal", func(t *testing.T) {
		transport := limitedTransport()
		transport.completeGoalOnContinuation = true
		client := newTestClient(t, transport)
		request := testRunRequest("job", "job-turn", "root-existing", true, new(string))
		request.GoalObjective = "complete the bound operation"
		request.ExistingTurnID = "turn-limited"
		request.ResumeUsageLimitedGoal = true

		result, err := client.Run(t.Context(), request)
		if err != nil || result.RootThreadID != "root-existing" || result.Status != agent.TurnCompleted ||
			result.Failure != "" || transport.methodCount("thread/goal/set") != 1 {
			t.Fatalf("result=%+v goal sets=%d err=%v", result, transport.methodCount("thread/goal/set"), err)
		}
	})

	t.Run("stale explicit marker does not reactivate a completed goal", func(t *testing.T) {
		transport := limitedTransport()
		transport.goals["root-existing"] = protocol.ThreadGoal{
			ThreadID: "root-existing", Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusComplete,
		}
		client := newTestClient(t, transport)
		request := testRunRequest("job", "job-turn", "root-existing", true, new(string))
		request.GoalObjective = "complete the bound operation"
		request.ExistingTurnID = "turn-limited"
		request.ResumeUsageLimitedGoal = true

		if _, err := client.Run(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		if transport.methodCount("thread/goal/set") != 0 {
			t.Fatalf("stale resume goal sets = %d, want 0", transport.methodCount("thread/goal/set"))
		}
	})
}

func TestGoalRunRecoversTerminalGoalWhileLastTurnFinishes(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root-existing", "project-job", "", "")
	turn := testTurn("turn-active", "job-turn", protocol.TurnStatusInProgress)
	transport.addThread(root, turn)
	transport.completeOnList[root.ID] = turn.ID
	transport.projects["job"] = "project-job"
	transport.goals[root.ID] = protocol.ThreadGoal{
		ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusComplete,
	}
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "root-existing", false, new(string))
	request.GoalObjective = "complete the bound operation"
	request.ExistingTurnID = turn.ID
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	result, err := client.Run(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.TurnID != turn.ID || result.FinalResponse != "observed" {
		t.Fatalf("result=%+v", result)
	}
}

func TestGoalResumeReplaysPersistedContextCompactionBeforeTerminalReturn(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root-existing", "project-job", "", "")
	turn := testTurn("turn-existing", "job-turn", protocol.TurnStatusCompleted)
	turn.Items = append(turn.Items,
		protocol.ThreadItem{TypeValue: "contextCompaction", ID: protocol.SomeNonNull("compact-1")},
		protocol.ThreadItem{TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-existing"), Text: protocol.SomeNonNull("done")},
	)
	transport.addThread(root, turn)
	transport.projects["job"] = "project-job"
	transport.goals[root.ID] = protocol.ThreadGoal{ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusComplete}
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "root-existing", false, new(string))
	request.GoalObjective = "complete the bound operation"
	var got string
	request.OnContextCompaction = func(_ context.Context, threadID, turnID, itemID string) error {
		got = threadID + "/" + turnID + "/" + itemID
		return nil
	}

	if _, err := client.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if got != "root-existing/turn-existing/compact-1" {
		t.Fatalf("replayed compaction = %q", got)
	}
}

func TestResumeRootReconcilesInitialAndSingleContinuation(t *testing.T) {
	t.Run("terminal initial is rebound without replay", func(t *testing.T) {
		transport := newFakeTransport()
		root := testProtocolThread("root-existing", "project-job", "", "")
		turn := testTurn("turn-existing", "job-turn", protocol.TurnStatusCompleted)
		turn.Items = append(turn.Items, protocol.ThreadItem{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-existing"), Text: protocol.SomeNonNull("existing result"),
		})
		transport.addThread(root, turn)
		transport.projects["job"] = "project-job"
		client := newTestClient(t, transport)

		var boundTurn string
		result, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", false, &boundTurn))
		if err != nil || result.RootThreadID != "root-existing" || result.TurnID != "turn-existing" ||
			result.Status != agent.TurnCompleted || result.FinalResponse != "existing result" || boundTurn != "turn-existing" {
			t.Fatalf("resume = %#v bound=%q err=%v", result, boundTurn, err)
		}
		if got := transport.methodCount("turn/start"); got != 0 {
			t.Fatalf("resume replayed assigned input with %d turn/start calls", got)
		}
	})

	t.Run("failed initial starts one deterministic continuation", func(t *testing.T) {
		transport := newFakeTransport()
		transport.autoComplete = true
		root := testProtocolThread("root-existing", "project-job", "", "")
		transport.addThread(root, testTurn("failed-turn", "job-turn", protocol.TurnStatusFailed))
		transport.projects["job"] = "project-job"
		client := newTestClient(t, transport)

		var boundTurn string
		result, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", true, &boundTurn))
		if err != nil || result.Status != agent.TurnCompleted || boundTurn != result.TurnID {
			t.Fatalf("continuation = %#v bound=%q err=%v", result, boundTurn, err)
		}
		starts := transport.turnStartParams()
		if len(starts) != 1 {
			t.Fatalf("continuation starts = %d", len(starts))
		}
		clientID, _ := starts[0].ClientUserMessageID.Value()
		if clientID != "job-turn:failed-turn" {
			t.Fatalf("continuation client ID = %q", clientID)
		}

		boundTurn = ""
		second, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", true, &boundTurn))
		if err != nil || second.TurnID != result.TurnID || transport.methodCount("turn/start") != 1 {
			t.Fatalf("recovered continuation = %#v starts=%d err=%v", second, transport.methodCount("turn/start"), err)
		}
	})

	t.Run("ambiguous client ID is typed conflict", func(t *testing.T) {
		transport := newFakeTransport()
		root := testProtocolThread("root-existing", "project-job", "", "")
		transport.addThread(root,
			testTurn("turn-1", "job-turn", protocol.TurnStatusFailed),
			testTurn("turn-2", "job-turn", protocol.TurnStatusFailed),
		)
		transport.projects["job"] = "project-job"
		client := newTestClient(t, transport)
		_, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", true, new(string)))
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("ambiguity error = %v", err)
		}
	})
}

func TestBindTurnFailureInterruptsAndDeniesPendingCalls(t *testing.T) {
	transport := newFakeTransport()
	bindErr := errors.New("bind failed")
	interruptErr := errors.New("interrupt failed")
	transport.interruptErr = interruptErr
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BindTurn = func(context.Context, string, string) error { return bindErr }
	_, err := client.Run(t.Context(), request)
	if !errors.Is(err, bindErr) || !errors.Is(err, interruptErr) || transport.methodCount("turn/interrupt") != 1 {
		t.Fatalf("bind failure = err:%v interrupts:%d", err, transport.methodCount("turn/interrupt"))
	}
	if !transport.interruptWasBounded() || !dispatcherHasRoot(client.dispatcher, "root-1") {
		t.Fatalf("uncertain interrupt cleanup = bounded:%t retained:%t", transport.interruptWasBounded(), dispatcherHasRoot(client.dispatcher, "root-1"))
	}
}

func TestBindTurnFailureConfirmedInterruptDetachesRoot(t *testing.T) {
	transport := newFakeTransport()
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BindTurn = func(context.Context, string, string) error { return errors.New("bind failed") }
	_, err := client.Run(t.Context(), request)
	if err == nil {
		t.Fatal("bind failure succeeded")
	}
	if !transport.interruptWasBounded() || dispatcherHasRoot(client.dispatcher, "root-1") {
		t.Fatalf("confirmed interrupt cleanup = bounded:%t retained:%t", transport.interruptWasBounded(), dispatcherHasRoot(client.dispatcher, "root-1"))
	}
}

func TestResumeRootExistingFailedTurnHonorsContinuationPolicy(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	transport.projects["job"] = "project-job"
	transport.addThread(
		testProtocolThread("root-existing", "project-job", "", ""),
		testTurn("failed-turn", "job-turn", protocol.TurnStatusFailed),
	)
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "root-existing", true, new(string))
	request.ExistingTurnID = "failed-turn"
	result, err := client.Run(t.Context(), request)
	if err != nil || result.Status != agent.TurnCompleted || transport.methodCount("turn/start") != 1 {
		t.Fatalf("existing failed turn = %#v starts=%d err=%v", result, transport.methodCount("turn/start"), err)
	}
}

func TestResumeRootObservesInProgressTurn(t *testing.T) {
	transport := newFakeTransport()
	transport.projects["job"] = "project-job"
	transport.completeOnList["root-existing"] = "active-turn"
	transport.addThread(
		testProtocolThread("root-existing", "project-job", "", ""),
		testTurn("active-turn", "job-turn", protocol.TurnStatusInProgress),
	)
	client := newTestClient(t, transport)
	result, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "root-existing", false, new(string)))
	if err != nil || result.Status != agent.TurnCompleted || result.FinalResponse != "observed" || transport.methodCount("turn/start") != 0 {
		t.Fatalf("in-progress turn = %#v starts=%d err=%v", result, transport.methodCount("turn/start"), err)
	}
}

func TestBindRootFailurePreservesUnboundRoot(t *testing.T) {
	transport := newFakeTransport()
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BindRoot = func(context.Context, string) error { return errors.New("lost bind acknowledgement") }
	if _, err := client.Run(t.Context(), request); err == nil {
		t.Fatal("bind root failure succeeded")
	}
	if transport.methodCount("thread/start") != 1 || transport.methodCount("turn/start") != 0 || transport.methodCount("thread/delete") != 0 {
		t.Fatalf("bind root failure calls: start=%d turn=%d delete=%d", transport.methodCount("thread/start"), transport.methodCount("turn/start"), transport.methodCount("thread/delete"))
	}
}

func TestDefiniteBindRootFailureDeletesLateNewRoot(t *testing.T) {
	transport := newFakeTransport()
	client := newTestClient(t, transport)
	if rootID, err := client.DeleteTree(t.Context(), RootSelector{CorrelationID: "job"}); err != nil || rootID != "" {
		t.Fatalf("deletion snapshot = (%q, %v)", rootID, err)
	}

	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BindRoot = func(context.Context, string) error {
		return fmt.Errorf("%w: root authority is gone", ErrConflict)
	}
	if _, err := client.Run(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("bind root failure = %v", err)
	}
	if transport.methodCount("thread/start") != 1 || transport.methodCount("turn/start") != 0 ||
		transport.methodCount("thread/delete") != 1 || fmt.Sprint(transport.deleted) != "[root-1]" {
		t.Fatalf("late root cleanup calls: start=%d turn=%d delete=%d roots=%v", transport.methodCount("thread/start"), transport.methodCount("turn/start"), transport.methodCount("thread/delete"), transport.deleted)
	}
}

func TestDefiniteBindRootFailureRetriesRecoveredCorrelationCleanup(t *testing.T) {
	base := newFakeTransport()
	deleteErr := errors.New("temporary thread delete failure")
	transport := &failFirstThreadDeleteTransport{Transport: base, err: deleteErr}
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "", false, new(string))
	bindErr := fmt.Errorf("%w: root authority is gone", ErrConflict)
	request.BindRoot = func(context.Context, string) error { return bindErr }

	if _, err := client.Run(t.Context(), request); !errors.Is(err, bindErr) || !errors.Is(err, deleteErr) {
		t.Fatalf("first bind cleanup error = %v", err)
	}
	if _, err := client.Run(t.Context(), request); !errors.Is(err, bindErr) {
		t.Fatalf("retry bind cleanup error = %v", err)
	}
	if transport.deleteAttempts != 2 || base.methodCount("thread/start") != 1 ||
		base.methodCount("thread/delete") != 1 || fmt.Sprint(base.deleted) != "[root-1]" {
		t.Fatalf(
			"retry cleanup = attempts:%d starts:%d deletes:%d roots:%v",
			transport.deleteAttempts, base.methodCount("thread/start"),
			base.methodCount("thread/delete"), base.deleted,
		)
	}
}

func TestDefiniteBindRootFailurePreservesPersistedRoot(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("root-persisted", "project-job", "", ""))
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "root-persisted", false, new(string))
	request.BindRoot = func(context.Context, string) error {
		return fmt.Errorf("%w: root authority is gone", ErrConflict)
	}
	if _, err := client.Run(t.Context(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("persisted bind root failure = %v", err)
	}
	if transport.methodCount("thread/delete") != 0 {
		t.Fatalf("persisted root delete calls = %d", transport.methodCount("thread/delete"))
	}
}

type failFirstThreadDeleteTransport struct {
	Transport
	err            error
	deleteAttempts int
}

func (transport *failFirstThreadDeleteTransport) Send(ctx context.Context, frame json.RawMessage) error {
	var request struct{ Method string }
	if err := json.Unmarshal(frame, &request); err != nil {
		return err
	}
	if request.Method == "thread/delete" {
		transport.deleteAttempts++
		if transport.deleteAttempts == 1 {
			return transport.err
		}
	}
	return transport.Transport.Send(ctx, frame)
}

func TestFinalResponsePrefersNativeFinalAnswer(t *testing.T) {
	items := []protocol.ThreadItem{
		{TypeValue: "agentMessage", ID: protocol.SomeNonNull("final"), Text: protocol.SomeNonNull("final"), Phase: protocol.Some(protocol.MessagePhaseFinalAnswer)},
		{TypeValue: "agentMessage", ID: protocol.SomeNonNull("commentary"), Text: protocol.SomeNonNull("later commentary"), Phase: protocol.Some(protocol.MessagePhase("commentary"))},
	}
	if got := finalResponse(items); got != "final" {
		t.Fatalf("final response = %q", got)
	}
}

func TestReconciledBindTurnFailureInterruptsActiveTurn(t *testing.T) {
	transport := newFakeTransport()
	transport.projects["job"] = "project-job"
	transport.addThread(
		testProtocolThread("root-existing", "project-job", "", ""),
		testTurn("active-turn", "job-turn", protocol.TurnStatusInProgress),
	)
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "root-existing", false, new(string))
	request.BindTurn = func(context.Context, string, string) error { return errors.New("bind denied") }
	if _, err := client.Run(t.Context(), request); err == nil {
		t.Fatal("reconciled bind failure succeeded")
	}
	if transport.methodCount("turn/interrupt") != 1 {
		t.Fatalf("reconciled bind failure interrupts = %d", transport.methodCount("turn/interrupt"))
	}
	if !transport.interruptWasBounded() || dispatcherHasRoot(client.dispatcher, "root-existing") {
		t.Fatalf("reconciled interrupt cleanup = bounded:%t retained:%t", transport.interruptWasBounded(), dispatcherHasRoot(client.dispatcher, "root-existing"))
	}
}

func TestTerminalReconcileBindFailureDetachesRoot(t *testing.T) {
	transport := newFakeTransport()
	transport.projects["job"] = "project-job"
	transport.addThread(
		testProtocolThread("root-existing", "project-job", "", ""),
		testTurn("terminal-turn", "job-turn", protocol.TurnStatusCompleted),
	)
	client := newTestClient(t, transport)
	request := testRunRequest("job", "job-turn", "root-existing", false, new(string))
	request.BindTurn = func(context.Context, string, string) error { return errors.New("bind failed") }
	if _, err := client.Run(t.Context(), request); err == nil {
		t.Fatal("terminal bind failure succeeded")
	}
	if transport.methodCount("turn/interrupt") != 0 || dispatcherHasRoot(client.dispatcher, "root-existing") {
		t.Fatalf("terminal cleanup = interrupts:%d retained:%t", transport.methodCount("turn/interrupt"), dispatcherHasRoot(client.dispatcher, "root-existing"))
	}
}

func dispatcherHasRoot(dispatcher *Dispatcher, rootThreadID string) bool {
	dispatcher.mu.RLock()
	defer dispatcher.mu.RUnlock()
	_, found := dispatcher.roots[rootThreadID]
	return found
}

func TestCancelRootTree(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root", "project-job", "", "")
	child := testProtocolThread("child", "project-job", "root", "reviewer")
	transport.addThread(root, testTurn("root-turn", "root-message", protocol.TurnStatusInProgress))
	transport.addThread(child, testTurn("child-turn", "child-message", protocol.TurnStatusInProgress))
	client := newTestClient(t, transport)

	rootID, err := client.CancelRoot(t.Context(), RootSelector{ThreadID: "root"})
	if err != nil || rootID != "root" || transport.methodCount("thread/list") != 1 || transport.methodCount("turn/interrupt") != 2 {
		t.Fatalf(
			"cancel = root:%q lists:%d interrupts:%d err:%v",
			rootID, transport.methodCount("thread/list"), transport.methodCount("turn/interrupt"), err,
		)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.turns["root"][0].Status != protocol.TurnStatusInterrupted ||
		transport.turns["child"][0].Status != protocol.TurnStatusInterrupted {
		t.Fatalf("tree turns = root:%q child:%q", transport.turns["root"][0].Status, transport.turns["child"][0].Status)
	}
}

func TestCancelRootClearsNativeGoalBeforeReturningIdle(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root-goal", "project-job", "", "")
	transport.addThread(root, testTurn("turn-complete", "job-turn", protocol.TurnStatusCompleted))
	transport.goals[root.ID] = protocol.ThreadGoal{
		ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusActive,
	}
	client := newTestClient(t, transport)

	if _, err := client.CancelRoot(t.Context(), RootSelector{ThreadID: root.ID}); err != nil {
		t.Fatal(err)
	}
	if got := transport.methodCount("thread/goal/clear"); got != 1 {
		t.Fatalf("thread/goal/clear calls = %d, want 1", got)
	}
}

func TestCancelRootTreatsConcurrentCompletionAsStopped(t *testing.T) {
	transport := newFakeTransport()
	transport.interruptErr = errors.New("no active turn to interrupt")
	transport.addThread(
		testProtocolThread("root", "project-job", "", ""),
		testTurn("root-turn", "root-message", protocol.TurnStatusInProgress),
	)
	client := newTestClient(t, transport)
	rootID, err := client.CancelRoot(t.Context(), RootSelector{ThreadID: "root"})
	if err != nil || rootID != "root" {
		t.Fatalf("concurrent completion = root:%q err:%v", rootID, err)
	}
}

func TestCancelRootUsesPaginatedTurnShells(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(
		testProtocolThread("root", "project-job", "", ""),
		testTurn("root-turn", "root-message", protocol.TurnStatusInProgress),
	)
	client := newTestClient(t, transport)
	if _, err := client.CancelRoot(t.Context(), RootSelector{ThreadID: "root"}); err != nil {
		t.Fatal(err)
	}
	if transport.methodCount("thread/read") != 1 {
		t.Fatalf("cancel full-history reads = %d, want metadata-only selector read", transport.methodCount("thread/read"))
	}
	lists := transport.turnListParams()
	if len(lists) < 2 {
		t.Fatalf("turn shell lists = %d", len(lists))
	}
	for _, params := range lists {
		itemsView, _ := params.ItemsView.Value()
		limit, _ := params.Limit.Value()
		if itemsView != protocol.TurnItemsViewNotLoaded || limit == 0 {
			t.Fatalf("turn list params = %#v", params)
		}
	}
}

func TestDeleteRootTree(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("root", "project-job", "", ""))
	transport.addThread(testProtocolThread("child", "project-job", "root", "reviewer"))
	transport.addThread(testProtocolThread("grandchild", "project-job", "child", "reviewer"))
	client := newTestClient(t, transport)

	rootID, err := client.DeleteTree(t.Context(), RootSelector{ThreadID: "root"})
	if err != nil || rootID != "root" || fmt.Sprint(transport.deleted) != "[grandchild child root]" {
		t.Fatalf("delete = root:%q order:%v err:%v", rootID, transport.deleted, err)
	}
}

func TestDeleteMissingUnloadedRootIsNoop(t *testing.T) {
	transport := newFakeTransport()
	transport.threadReadErr = &sdk.RPCError{Message: "thread not loaded: missing-root"}
	client := newTestClient(t, transport)

	rootID, err := client.DeleteTree(t.Context(), RootSelector{ThreadID: "missing-root"})
	if err != nil || rootID != "" {
		t.Fatalf("delete missing root = root:%q err:%v", rootID, err)
	}
}

func TestDeleteRootInterruptsActiveTreeBeforeDeleting(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(
		testProtocolThread("root-active", "project-job", "", ""),
		testTurn("root-turn", "root-message", protocol.TurnStatusInProgress),
	)
	transport.addThread(
		testProtocolThread("child-active", "project-job", "root-active", "reviewer"),
		testTurn("child-turn", "child-message", protocol.TurnStatusInProgress),
	)
	client := newTestClient(t, transport)

	rootID, err := client.DeleteTree(t.Context(), RootSelector{ThreadID: "root-active"})
	if err != nil || rootID != "root-active" || fmt.Sprint(transport.deleted) != "[child-active root-active]" {
		t.Fatalf("delete active tree = root:%q order:%v err:%v", rootID, transport.deleted, err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	interrupts, firstDelete := 0, len(transport.methods)
	for index, method := range transport.methods {
		if method == "turn/interrupt" {
			interrupts++
		}
		if method == "thread/delete" && firstDelete == len(transport.methods) {
			firstDelete = index
		}
	}
	if interrupts != 2 || firstDelete == len(transport.methods) {
		t.Fatalf("interrupts = %d, first delete = %d, methods = %v", interrupts, firstDelete, transport.methods)
	}
	for index := firstDelete; index < len(transport.methods); index++ {
		if transport.methods[index] == "turn/interrupt" {
			t.Fatalf("interrupt after delete: methods = %v", transport.methods)
		}
	}
}

func TestDeleteRootClearsNativeGoalBeforeDeletingTree(t *testing.T) {
	transport := newFakeTransport()
	root := testProtocolThread("root-goal", "project-job", "", "")
	transport.addThread(root)
	transport.goals[root.ID] = protocol.ThreadGoal{
		ThreadID: root.ID, Objective: "complete the bound operation", Status: protocol.ThreadGoalStatusActive,
	}
	client := newTestClient(t, transport)

	if _, err := client.DeleteTree(t.Context(), RootSelector{ThreadID: root.ID}); err != nil {
		t.Fatal(err)
	}
	if got := transport.methodCount("thread/goal/clear"); got != 1 {
		t.Fatalf("thread/goal/clear calls = %d, want 1", got)
	}
}

func TestRootSelectorZeroOneMany(t *testing.T) {
	transport := newFakeTransport()
	transport.projects["job"] = "project-job"
	client := newTestClient(t, transport)

	if rootID, err := client.DeleteTree(t.Context(), RootSelector{CorrelationID: "job"}); err != nil || rootID != "" {
		t.Fatalf("zero roots = %q, %v", rootID, err)
	}
	transport.addThread(testProtocolThread("root-1", "project-job", "", ""))
	if rootID, err := client.DeleteTree(t.Context(), RootSelector{CorrelationID: "job"}); err != nil || rootID != "root-1" {
		t.Fatalf("one root = %q, %v", rootID, err)
	}
	transport.addThread(testProtocolThread("root-2", "project-job", "", ""))
	transport.addThread(testProtocolThread("root-3", "project-job", "", ""))
	if _, err := client.DeleteTree(t.Context(), RootSelector{CorrelationID: "job"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("many roots error = %v", err)
	}
}

func TestThreadItemsReturnsRawNativeItems(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("root", "project-job", "", ""), protocol.Turn{
		ID: "turn-1", Status: protocol.TurnStatusFailed,
		Items: []protocol.ThreadItem{{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-1"), Text: protocol.SomeNonNull("native"),
		}},
	})
	client := newTestClient(t, transport)
	items, err := client.ThreadItems(t.Context(), "root")
	if err != nil || len(items) != 1 || items[0].TurnID != "turn-1" || items[0].TurnStatus != agenttimeline.TurnStatusFailed ||
		!strings.Contains(string(items[0].Item), `"native"`) || transport.methodCount("thread/items/list") != 0 {
		t.Fatalf("native items = %#v, %v", items, err)
	}
}

func TestNativeTimelineEntriesAcceptSharedPosition(t *testing.T) {
	entries := []protocol.ThreadTimelineEntry{
		{
			TypeValue: "turnStarted",
			Position:  protocol.SomeNonNull(uint64(20)),
			TurnID:    protocol.SomeNonNull("turn-1"),
		},
		{
			TypeValue: "item",
			Position:  protocol.SomeNonNull(uint64(20)),
			TurnID:    protocol.SomeNonNull("turn-1"),
			Item:      json.RawMessage(`{"type":"agentMessage","id":"item-1","text":"shared"}`),
		},
		{
			TypeValue: "turnCompleted",
			Position:  protocol.SomeNonNull(uint64(20)),
			TurnID:    protocol.SomeNonNull("turn-1"),
			Status:    protocol.SomeNonNull(protocol.TurnStatusCompleted),
		},
	}

	items, err := nativeTimelineEntries(entries, "", agenttimeline.RoleMain)
	if err != nil || len(items) != 1 || items[0].TurnID != "turn-1" ||
		items[0].TurnStatus != agenttimeline.TurnStatusCompleted ||
		!strings.Contains(string(items[0].Item), `"shared"`) {
		t.Fatalf("items = %#v, err = %v", items, err)
	}
}

func TestThreadItemsIncludesNativeReviewerThreads(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("root", "project-job", "", ""), protocol.Turn{
		ID: "turn-root", Status: protocol.TurnStatusInProgress,
		Items: []protocol.ThreadItem{
			{TypeValue: "agentMessage", ID: protocol.SomeNonNull("root-message"), Text: protocol.SomeNonNull("root")},
			{
				TypeValue: "subAgentActivity", ID: protocol.SomeNonNull("spawn-1"),
				AgentPath:     protocol.SomeNonNull("/root/reviewer_completeness"),
				AgentThreadID: protocol.SomeNonNull("child"), Kind: protocol.SomeNonNull(protocol.SubAgentActivityKindStarted),
			},
		},
	})
	transport.addThread(
		testProtocolThread("child", "project-job", "root", "book_enrichment_review_completeness"),
		protocol.Turn{ID: "turn-child", Status: protocol.TurnStatusInProgress, Items: []protocol.ThreadItem{{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("child-message"), Text: protocol.SomeNonNull("reviewing"),
		}}},
	)
	client := newTestClient(t, transport)

	items, err := client.ThreadItems(t.Context(), "root")
	if err != nil || len(items) != 3 || items[0].Role != agenttimeline.RoleMain ||
		items[1].Role != agenttimeline.RoleMain || items[2].Role != agenttimeline.RoleCompleteness ||
		items[2].TurnID != "turn-child" || !strings.Contains(string(items[2].Item), `"reviewing"`) ||
		transport.methodCount("thread/read") != 1 || transport.methodCount("thread/list") != 1 ||
		transport.methodCount("thread/timeline/list") != 2 {
		t.Fatalf("timeline items = %#v, %v", items, err)
	}
}

func TestThreadItemsReturnsTerminalMarkerForEmptyTurn(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("root", "project-job", "", ""), protocol.Turn{ID: "turn-empty", Status: protocol.TurnStatusInterrupted, Items: []protocol.ThreadItem{}})
	client := newTestClient(t, transport)

	items, err := client.ThreadItems(t.Context(), "root")
	if err != nil || len(items) != 1 || items[0].TurnID != "turn-empty" || items[0].TurnStatus != agenttimeline.TurnStatusInterrupted || len(items[0].Item) != 0 {
		t.Fatalf("terminal marker = %#v, %v", items, err)
	}
}

func TestThreadItemsPrependsOlderTimelinePages(t *testing.T) {
	transport := newFakeTransport()
	items := make([]protocol.ThreadItem, 101)
	for index := range items {
		items[index] = protocol.ThreadItem{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull(fmt.Sprintf("item-%03d", index)),
			Text: protocol.SomeNonNull(fmt.Sprintf("message-%03d", index)),
		}
	}
	transport.addThread(
		testProtocolThread("root", "project-job", "", ""),
		protocol.Turn{ID: "turn-1", Status: protocol.TurnStatusCompleted, Items: items},
	)
	client := newTestClient(t, transport)

	got, err := client.ThreadItems(t.Context(), "root")
	if err != nil || len(got) != 101 || !strings.Contains(string(got[0].Item), `"message-000"`) ||
		!strings.Contains(string(got[100].Item), `"message-100"`) || transport.methodCount("thread/timeline/list") != 2 {
		t.Fatalf("timeline = %#v, %v", got, err)
	}
}

func TestThreadItemsReturnsEveryRootTurn(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(
		testProtocolThread("root", "project-chat", "", ""),
		protocol.Turn{ID: "turn-1", Status: protocol.TurnStatusCompleted, Items: []protocol.ThreadItem{{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-1"), Text: protocol.SomeNonNull("first"),
		}}},
		protocol.Turn{ID: "turn-2", Status: protocol.TurnStatusCompleted, Items: []protocol.ThreadItem{{
			TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-2"), Text: protocol.SomeNonNull("second"),
		}}},
	)
	client := newTestClient(t, transport)

	items, err := client.ThreadItems(t.Context(), "root")
	if err != nil || len(items) != 2 || items[0].TurnID != "turn-1" || items[1].TurnID != "turn-2" ||
		!strings.Contains(string(items[0].Item), `"first"`) || !strings.Contains(string(items[1].Item), `"second"`) {
		t.Fatalf("root items = %#v, %v", items, err)
	}
}

func TestThreadUpdatesFilterOtherThreads(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("thread-root", "project-job", "", ""))
	transport.addThread(testProtocolThread("thread-other-root", "project-other", "", ""))
	transport.addThread(testProtocolThread(
		"thread-other", "project-other", "thread-other-root", "book_enrichment_review_completeness",
	))
	client := newTestClient(t, transport)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	updates, err := client.ThreadUpdates(ctx, "thread-root")
	if err != nil {
		t.Fatal(err)
	}

	other, _ := fakeNotification("item/agentMessage/delta", protocol.AgentMessageDeltaNotification{ThreadID: "thread-other", TurnID: "turn-1", ItemID: "item-1", Delta: "other"})
	child := testProtocolThread("thread-child", "project-job", "thread-root", "book_enrichment_review_completeness")
	started, _ := fakeNotification("thread/started", protocol.ThreadStartedNotification{Thread: child})
	target, _ := fakeNotification("item/agentMessage/delta", protocol.AgentMessageDeltaNotification{ThreadID: "thread-child", TurnID: "turn-1", ItemID: "item-1", Delta: "target"})
	transport.recv <- other
	transport.recv <- started
	transport.recv <- target

	if update, ok := <-updates; !ok || update.Err != nil || update.Delta == nil ||
		update.Delta.Role != agenttimeline.RoleCompleteness || update.Delta.Text != "target" {
		t.Fatalf("target update = %#v, ok = %v", update, ok)
	}
	if transport.methodCount("thread/list") != 1 {
		t.Fatalf("thread/list calls = %d", transport.methodCount("thread/list"))
	}
}

func TestThreadUpdatesResolvesLateChildWithoutThreadStarted(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("thread-root", "project-job", "", ""))
	client := newTestClient(t, transport)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	updates, err := client.ThreadUpdates(ctx, "thread-root")
	if err != nil {
		t.Fatal(err)
	}

	transport.addThread(testProtocolThread(
		"thread-child", "project-job", "thread-root", "book_enrichment_review_completeness",
	))
	delta, _ := fakeNotification("item/reasoning/textDelta", protocol.ReasoningTextDeltaNotification{
		ThreadID: "thread-child", TurnID: "turn-child", ItemID: "reasoning-child", ContentIndex: 2, Delta: "live",
	})
	transport.recv <- delta

	update, ok := <-updates
	if !ok || update.Err != nil || update.Delta == nil ||
		update.Delta.Kind != agenttimeline.EventReasoning || update.Delta.Role != agenttimeline.RoleCompleteness ||
		update.Delta.TurnID != "turn-child" || update.Delta.ItemID != "reasoning-child" ||
		update.Delta.LogicalSection != 2 || update.Delta.Text != "live" || !update.Delta.RawReasoning {
		t.Fatalf("late child reasoning update = %#v, ok = %v", update, ok)
	}
}

func TestThreadUpdatesStreamsEveryRootTurnAndChildThread(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("thread-root", "project-job", "", ""))
	client := newTestClient(t, transport)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	updates, err := client.ThreadUpdates(ctx, "thread-root")
	if err != nil {
		t.Fatal(err)
	}

	continued, _ := fakeNotification("item/agentMessage/delta", protocol.AgentMessageDeltaNotification{
		ThreadID: "thread-root", TurnID: "turn-next", ItemID: "continued", Delta: "continued ",
	})
	bound, _ := fakeNotification("item/agentMessage/delta", protocol.AgentMessageDeltaNotification{
		ThreadID: "thread-root", TurnID: "turn-bound", ItemID: "root-item", Delta: "root ",
	})
	rawReasoning, _ := fakeNotification("item/reasoning/textDelta", protocol.ReasoningTextDeltaNotification{
		ThreadID: "thread-root", TurnID: "turn-bound", ItemID: "raw", ContentIndex: 0, Delta: "raw",
	})
	child := testProtocolThread("thread-child", "project-job", "thread-root", "book_enrichment_review_completeness")
	childStarted, _ := fakeNotification("thread/started", protocol.ThreadStartedNotification{Thread: child})
	childDelta, _ := fakeNotification("item/reasoning/summaryTextDelta", protocol.ReasoningSummaryTextDeltaNotification{
		ThreadID: "thread-child", TurnID: "turn-child", ItemID: "child-item", SummaryIndex: 1, Delta: "child",
	})
	rootUsage, _ := fakeNotification("thread/tokenUsage/updated", protocol.ThreadTokenUsageUpdatedNotification{
		ThreadID: "thread-root", TurnID: "turn-bound",
		TokenUsage: protocol.ThreadTokenUsage{
			Last: protocol.TokenUsageBreakdown{TotalTokens: 64_000}, ModelContextWindow: protocol.Some(int64(128_000)),
		},
	})
	childUsage, _ := fakeNotification("thread/tokenUsage/updated", protocol.ThreadTokenUsageUpdatedNotification{
		ThreadID: "thread-child", TurnID: "turn-child",
		TokenUsage: protocol.ThreadTokenUsage{
			Last: protocol.TokenUsageBreakdown{TotalTokens: 24_000}, ModelContextWindow: protocol.Some(int64(64_000)),
		},
	})
	transport.recv <- continued
	transport.recv <- bound
	transport.recv <- rawReasoning
	transport.recv <- childStarted
	transport.recv <- childDelta
	transport.recv <- rootUsage
	transport.recv <- childUsage

	first, ok := <-updates
	if !ok || first.Err != nil || first.Delta == nil || first.Delta.Kind != agenttimeline.EventAssistant ||
		first.Delta.Role != agenttimeline.RoleMain || first.Delta.TurnID != "turn-next" ||
		first.Delta.ItemID != "continued" || first.Delta.LogicalSection != 0 || first.Delta.Text != "continued " {
		t.Fatalf("continued root update = %#v, ok = %v", first, ok)
	}
	second, ok := <-updates
	if !ok || second.Err != nil || second.Delta == nil || second.Delta.Kind != agenttimeline.EventAssistant ||
		second.Delta.Role != agenttimeline.RoleMain || second.Delta.TurnID != "turn-bound" ||
		second.Delta.ItemID != "root-item" || second.Delta.LogicalSection != 0 || second.Delta.Text != "root " {
		t.Fatalf("bound root update = %#v, ok = %v", second, ok)
	}
	third, ok := <-updates
	if !ok || third.Err != nil || third.Delta == nil || third.Delta.Kind != agenttimeline.EventReasoning ||
		third.Delta.Role != agenttimeline.RoleMain || third.Delta.ItemID != "raw" ||
		third.Delta.LogicalSection != 0 || third.Delta.Text != "raw" || !third.Delta.RawReasoning {
		t.Fatalf("raw reasoning update = %#v, ok = %v", third, ok)
	}
	fourth, ok := <-updates
	if !ok || fourth.Err != nil || fourth.Delta == nil || fourth.Delta.Kind != agenttimeline.EventReasoning ||
		fourth.Delta.Role != agenttimeline.RoleCompleteness || fourth.Delta.ItemID != "child-item" ||
		fourth.Delta.LogicalSection != 1 || fourth.Delta.Text != "child" || fourth.Delta.RawReasoning {
		t.Fatalf("child summary update = %#v, ok = %v", fourth, ok)
	}
	fifth, ok := <-updates
	if !ok || fifth.Err != nil || fifth.Context == nil || fifth.Context.Role != agenttimeline.RoleMain ||
		fifth.Context.TurnID != "turn-bound" || fifth.Context.UsedTokens == nil || *fifth.Context.UsedTokens != 64_000 ||
		fifth.Context.ContextWindow == nil || *fifth.Context.ContextWindow != 128_000 {
		t.Fatalf("root context update = %#v, ok = %v", fifth, ok)
	}
	sixth, ok := <-updates
	if !ok || sixth.Err != nil || sixth.Context == nil || sixth.Context.Role != agenttimeline.RoleCompleteness ||
		sixth.Context.TurnID != "turn-child" || sixth.Context.UsedTokens == nil || *sixth.Context.UsedTokens != 24_000 ||
		sixth.Context.ContextWindow == nil || *sixth.Context.ContextWindow != 64_000 {
		t.Fatalf("reviewer context update = %#v, ok = %v", sixth, ok)
	}
}

func TestTimelineUpdatePublishesCurrentContextUsage(t *testing.T) {
	update, publish := timelineUpdate(protocol.ThreadTokenUsageUpdatedNotification{
		ThreadID: "thread-root",
		TurnID:   "turn-1",
		TokenUsage: protocol.ThreadTokenUsage{
			Last:               protocol.TokenUsageBreakdown{TotalTokens: 96_000},
			ModelContextWindow: protocol.Some(int64(128_000)),
		},
	}, agenttimeline.RoleMain)
	if !publish {
		t.Fatal("thread/tokenUsage/updated was dropped")
	}
	if update.Context == nil {
		t.Fatalf("context update = %#v", update)
	}
	if update.Context.Role != agenttimeline.RoleMain || update.Context.TurnID != "turn-1" ||
		update.Context.UsedTokens == nil || *update.Context.UsedTokens != 96_000 ||
		update.Context.ContextWindow == nil || *update.Context.ContextWindow != 128_000 {
		t.Fatalf("usage = %#v", update.Context)
	}

	compacted, publish := timelineUpdate(protocol.ContextCompactedNotification{
		ThreadID: "thread-root", TurnID: "turn-1",
	}, agenttimeline.RoleMain)
	if !publish || compacted.Context == nil || !compacted.Context.Compacted || compacted.Context.UsedTokens != nil {
		t.Fatalf("compaction = %#v, publish = %v", compacted, publish)
	}
}

func TestThreadUpdatesReconcilesInsteadOfClosingAfterGlobalNotificationOverflow(t *testing.T) {
	transport := newFakeTransport()
	transport.addThread(testProtocolThread("thread-root", "project-job", "", ""))
	client := newTestClientWithLimits(t, transport, sdk.ClientLimits{GlobalSubscriberQueue: 1})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	updates, err := client.ThreadUpdates(ctx, "thread-root")
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for index := 0; index < cap(transport.recv)+cap(updates)+4; index++ {
			notification, _ := fakeNotification("item/agentMessage/delta", protocol.AgentMessageDeltaNotification{
				ThreadID: "thread-root", TurnID: "turn-1", ItemID: fmt.Sprintf("item-%d", index), Delta: "x",
			})
			select {
			case transport.recv <- notification:
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-sent:
	case <-ctx.Done():
		t.Fatal("notification flood did not reach the SDK router")
	}

	reconciled := false
	for !reconciled {
		update, ok := <-updates
		if !ok || update.Err != nil {
			t.Fatalf("public stream closed on recoverable overflow: update=%#v ok=%v", update, ok)
		}
		reconciled = update.Delta == nil && update.Context == nil
	}
}

func TestTerminalRunDetachesDispatcher(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	client := newTestClient(t, transport)
	result, err := client.Run(t.Context(), testRunRequest("job", "job-turn", "", false, new(string)))
	if err != nil {
		t.Fatal(err)
	}
	lists := transport.turnListParams()
	if len(lists) != 0 {
		t.Fatalf("fresh root listed nonexistent turn history: %#v", lists)
	}
	response, err := client.dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: result.RootThreadID, TurnID: result.TurnID, CallID: "late", Tool: "late", Arguments: []byte(`{}`),
	})
	if err != nil || response.Success {
		t.Fatalf("terminal root remained live: response=%#v err=%v", response, err)
	}
}

func testRunRequest(correlation, turnCorrelation, existingRoot string, continueFailed bool, boundTurn *string) RunRequest {
	return RunRequest{
		CorrelationID: correlation, TurnCorrelationID: turnCorrelation,
		ExistingRootThreadID: existingRoot, ContinueFailedTurn: continueFailed,
		BundleID: "test", DeveloperInstructions: "test", InitialInput: "assigned input",
		BindRoot: func(context.Context, string) error { return nil },
		BindTurn: func(_ context.Context, _ string, turnID string) error {
			*boundTurn = turnID
			return nil
		},
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Success: true}, nil
		},
	}
}
