package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

func TestNativeChildCallerAndPendingTurnGate(t *testing.T) {
	callers := map[string]agent.ToolCaller{
		"root-a":  {ThreadID: "root-a", RootThreadID: "root-a"},
		"root-b":  {ThreadID: "root-b", RootThreadID: "root-b"},
		"child-a": {ThreadID: "child-a", RootThreadID: "root-a", AgentRole: "reviewer-completeness"},
	}
	dispatcher := newDispatcher(func(_ context.Context, threadID string) (agent.ToolCaller, error) {
		return callers[threadID], nil
	})
	attach := func(root, marker string) {
		dispatcher.attach(root, func(_ context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			payload, _ := json.Marshal(map[string]string{"root": marker, "caller": call.Caller.ThreadID, "turn": call.TurnID})
			return agent.ToolResult{Success: true, Payload: payload}, nil
		})
	}
	attach("root-a", "a")
	attach("root-b", "b")
	dispatcher.publishTurn("root-b", "turn-b")
	dispatcher.prepareTurn("root-a", "turn-a")

	pending := make(chan protocol.DynamicToolCallResponse, 1)
	go func() {
		response, _ := dispatcher.handle(context.Background(), protocol.DynamicToolCallParams{
			ThreadID: "root-a", TurnID: "turn-a", CallID: "call-a", Tool: "read", Arguments: json.RawMessage(`{}`),
		})
		pending <- response
	}()
	select {
	case <-pending:
		t.Fatal("root call passed before durable BindTurn")
	case <-time.After(20 * time.Millisecond):
	}
	dispatcher.publishTurn("root-a", "turn-a")
	if response := <-pending; !response.Success || responseText(response) != `{"caller":"root-a","root":"a","turn":"turn-a"}` {
		t.Fatalf("root dispatch = success:%v payload:%s", response.Success, responseText(response))
	}

	nextPending := make(chan protocol.DynamicToolCallResponse, 1)
	dispatcher.prepareTurn("root-a", "turn-a-2")
	go func() {
		response, _ := dispatcher.handle(context.Background(), protocol.DynamicToolCallParams{
			ThreadID: "root-a", TurnID: "turn-a-2", CallID: "call-a-2", Tool: "read", Arguments: json.RawMessage(`{}`),
		})
		nextPending <- response
	}()
	select {
	case <-nextPending:
		t.Fatal("continuation call passed before durable BindTurn")
	case <-time.After(20 * time.Millisecond):
	}
	dispatcher.publishTurn("root-a", "turn-a-2")
	if response := <-nextPending; !response.Success || responseText(response) != `{"caller":"root-a","root":"a","turn":"turn-a-2"}` {
		t.Fatalf("continuation dispatch = success:%v payload:%s", response.Success, responseText(response))
	}
	stale, _ := dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: "root-a", TurnID: "turn-a", CallID: "stale", Tool: "read", Arguments: json.RawMessage(`{}`),
	})
	if stale.Success {
		t.Fatal("superseded root turn remained authorized")
	}

	child, err := dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: "child-a", TurnID: "child-turn", CallID: "child-call", Tool: "review", Arguments: json.RawMessage(`{}`),
	})
	if err != nil || !child.Success || responseText(child) != `{"caller":"child-a","root":"a","turn":"child-turn"}` {
		t.Fatalf("child dispatch = response:%#v err:%v", child, err)
	}

	wrongRootTurn, err := dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: "root-a", TurnID: "child-turn", CallID: "wrong", Tool: "read", Arguments: json.RawMessage(`{}`),
	})
	if err != nil || wrongRootTurn.Success || responseText(wrongRootTurn) != `{"type":"tool_denied"}` {
		t.Fatalf("wrong root turn = response:%#v err:%v", wrongRootTurn, err)
	}

	otherRoot, err := dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: "root-b", TurnID: "turn-b", CallID: "call-b", Tool: "read", Arguments: json.RawMessage(`{}`),
	})
	if err != nil || !otherRoot.Success || responseText(otherRoot) != `{"caller":"root-b","root":"b","turn":"turn-b"}` {
		t.Fatalf("second root dispatch = response:%#v err:%v", otherRoot, err)
	}

	missingCallID, err := dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: "root-a", TurnID: "turn-a", Tool: "read", Arguments: json.RawMessage(`{}`),
	})
	if err != nil || missingCallID.Success || responseText(missingCallID) != `{"type":"tool_denied"}` {
		t.Fatalf("missing call ID = response:%#v err:%v", missingCallID, err)
	}
}

func responseText(response protocol.DynamicToolCallResponse) string {
	if len(response.ContentItems) != 1 {
		return ""
	}
	text, _ := response.ContentItems[0].Text.Value()
	return text
}

// specs/2026-10-29-saga-illustrations.md R1.5: a result with an image reaches the model as the
// text and then an inputImage item with the data URL.
func TestDispatcherSendsTheImageOfAResult(t *testing.T) {
	dispatcher := newDispatcher(func(_ context.Context, threadID string) (agent.ToolCaller, error) {
		return agent.ToolCaller{ThreadID: threadID, RootThreadID: threadID}, nil
	})
	dispatcher.attach("root", func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
		return agent.ToolResult{Success: true, Payload: []byte("Рисунок 3 готов."), ImageURL: "data:image/jpeg;base64,AAAA"}, nil
	})
	dispatcher.publishTurn("root", "turn")
	response, err := dispatcher.handle(t.Context(), protocol.DynamicToolCallParams{
		ThreadID: "root", TurnID: "turn", CallID: "call", Tool: "draw", Arguments: json.RawMessage(`{}`),
	})
	if err != nil || !response.Success || len(response.ContentItems) != 2 {
		t.Fatalf("response = %#v, %v", response, err)
	}
	text, _ := response.ContentItems[0].Text.Value()
	image, _ := response.ContentItems[1].ImageURL.Value()
	if response.ContentItems[0].TypeValue != "inputText" || text != "Рисунок 3 готов." ||
		response.ContentItems[1].TypeValue != "inputImage" || image != "data:image/jpeg;base64,AAAA" {
		t.Fatalf("content items = %#v", response.ContentItems)
	}
}
