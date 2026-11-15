package codex

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// specs/2026-11-14-desk-journal.md JR1.3: the question is named right before turn/start of the initial input — with the root
// thread — and never when a turn that runs is taken up or a failed one is continued; a naming that failed starts no turn.
func TestBeforeTurnComesOnlyBeforeANewTurn(t *testing.T) {
	transport := newFakeTransport()
	transport.autoComplete = true
	client := newTestClient(t, transport)
	var named []string
	starts := 0
	request := testRunRequest("job", "job-turn", "", false, new(string))
	request.BeforeTurn = func(_ context.Context, root string) error {
		named = append(named, root)
		starts = transport.methodCount("turn/start")
		return nil
	}
	result, err := client.Run(t.Context(), request)
	if err != nil || result.Status != agent.TurnCompleted {
		t.Fatalf("run = %#v, %v", result, err)
	}
	if len(named) != 1 || named[0] != result.RootThreadID || starts != 0 || transport.methodCount("turn/start") != 1 {
		t.Fatalf("named %v with %d starts before it", named, starts)
	}

	taken := newFakeTransport()
	turn := testTurn("turn-existing", "job-turn", protocol.TurnStatusCompleted)
	turn.Items = append(turn.Items, protocol.ThreadItem{TypeValue: "agentMessage", ID: protocol.SomeNonNull("agent-existing"), Text: protocol.SomeNonNull("existing result")})
	taken.addThread(testProtocolThread("root-existing", "project-job", "", ""), turn)
	taken.projects["job"] = "project-job"
	again := newTestClient(t, taken)
	request = testRunRequest("job", "job-turn", "root-existing", false, new(string))
	request.BeforeTurn = func(context.Context, string) error {
		t.Fatal("a turn taken up named its question")
		return nil
	}
	if _, err := again.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	// The continuation of a failed turn (ContinueFailedTurn) starts a turn of its own with words of the runner, not of the
	// author: it names no question a second time (the third check, C6).
	continued := newFakeTransport()
	continued.autoComplete = true
	continued.addThread(testProtocolThread("root-existing", "project-job", "", ""), testTurn("failed-turn", "job-turn", protocol.TurnStatusFailed))
	continued.projects["job"] = "project-job"
	continuing := newTestClient(t, continued)
	request = testRunRequest("job", "job-turn", "root-existing", true, new(string))
	request.BeforeTurn = func(context.Context, string) error {
		t.Fatal("the continuation of a failed turn named a question")
		return nil
	}
	if result, err := continuing.Run(t.Context(), request); err != nil || result.Status != agent.TurnCompleted || continued.methodCount("turn/start") != 1 {
		t.Fatalf("the continuation = %#v, %v, starts %d", result, err, continued.methodCount("turn/start"))
	}

	refused := newFakeTransport()
	refused.autoComplete = true
	failing := newTestClient(t, refused)
	request = testRunRequest("job", "job-turn", "", false, new(string))
	request.BeforeTurn = func(context.Context, string) error { return errors.New("the journal of the test refuses") }
	if _, err := failing.Run(t.Context(), request); err == nil || refused.methodCount("turn/start") != 0 {
		t.Fatalf("a naming that failed: %v, starts %d", err, refused.methodCount("turn/start"))
	}
}
