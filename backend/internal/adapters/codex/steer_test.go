package codex

import (
	"encoding/json"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// specs/2026-10-30-chat-steering.md R1.3: the correction names the turn it is for, so app-server
// refuses it rather than giving it to the next turn.
func TestSteerTurnSendsTheInputIntoTheExpectedTurn(t *testing.T) {
	transport := newFakeTransport()
	client := newTestClient(t, transport)

	err := client.SteerTurn(t.Context(), SteerRequest{
		ThreadID: "root", TurnID: "turn-1", ClientMessageID: "steer-1", Input: `{"question":"Без дракона","correction":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.steers) != 1 {
		t.Fatalf("steers = %d", len(transport.steers))
	}
	steer := transport.steers[0]
	clientID, _ := steer.ClientUserMessageID.Value()
	if steer.ThreadID != "root" || steer.ExpectedTurnID != "turn-1" || clientID != "steer-1" || len(steer.Input) != 1 {
		t.Fatalf("steer = %+v", steer)
	}
	text, _ := steer.Input[0].Text.Value()
	if steer.Input[0].TypeValue != "text" || text != `{"question":"Без дракона","correction":true}` {
		t.Fatalf("input = %+v", steer.Input[0])
	}
}

// R2.1, R2.2: the refusals of turn_steer_inner in their real shape.
func TestSteerTurnClassifiesTheRefusalsOfAppServer(t *testing.T) {
	notSteerable, err := json.Marshal(protocol.TurnError{Message: "cannot steer a compact turn"})
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(notSteerable, &data); err != nil {
		t.Fatal(err)
	}
	data["codexErrorInfo"] = map[string]any{"activeTurnNotSteerable": map[string]any{"turnKind": "compact"}}
	for _, test := range []struct {
		name  string
		error any
		want  error
	}{
		{"no active turn", map[string]any{"code": -32600, "message": "no active turn to steer"}, agent.ErrTurnNotActive},
		{"another turn", map[string]any{"code": -32600, "message": "expected active turn id `turn-1` but found `turn-2`"}, agent.ErrTurnNotActive},
		{"thread unloaded", map[string]any{"code": -32600, "message": "thread not found: root"}, agent.ErrTurnNotActive},
		{"compaction", map[string]any{"code": -32600, "message": "cannot steer a compact turn", "data": data}, agent.ErrTurnNotSteerable},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := newFakeTransport()
			encoded, err := json.Marshal(test.error)
			if err != nil {
				t.Fatal(err)
			}
			transport.steerRPCError = encoded
			client := newTestClient(t, transport)
			err = client.SteerTurn(t.Context(), SteerRequest{ThreadID: "root", TurnID: "turn-1", ClientMessageID: "steer-1", Input: "{}"})
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
		})
	}
}

// R2.3: any other refusal stays a failure.
func TestSteerTurnKeepsOtherFailuresAsFailures(t *testing.T) {
	transport := newFakeTransport()
	transport.steerRPCError = json.RawMessage(`{"code":-32603,"message":"failed to steer turn: boom"}`)
	client := newTestClient(t, transport)
	err := client.SteerTurn(t.Context(), SteerRequest{ThreadID: "root", TurnID: "turn-1", ClientMessageID: "steer-1", Input: "{}"})
	if err == nil || errors.Is(err, agent.ErrTurnNotActive) || errors.Is(err, agent.ErrTurnNotSteerable) {
		t.Fatalf("err = %v", err)
	}
}

func TestSteerTurnRefusesAnIncompleteRequest(t *testing.T) {
	client := newTestClient(t, newFakeTransport())
	if err := client.SteerTurn(t.Context(), SteerRequest{ThreadID: "root", ClientMessageID: "steer-1", Input: "{}"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
