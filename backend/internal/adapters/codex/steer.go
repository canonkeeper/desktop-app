package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/domain/agent"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

// SteerRequest is one correction of the author for the turn working now
// (specs/2026-10-30-chat-steering.md R1.3).
type SteerRequest struct {
	ThreadID        string
	TurnID          string
	ClientMessageID string
	Input           string
}

// app-server refuses a steer with these invalid-request messages when the expected turn is not the
// active one (codex-rs/app-server/src/request_processors/turn_processor.rs#turn_steer_inner).
const (
	noActiveTurnMessage    = "no active turn to steer"
	turnMismatchPrefix     = "expected active turn id"
	steerThreadGonePrefix  = "thread not found"
	codexErrorNotSteerable = "activeTurnNotSteerable"
)

// SteerTurn sends the correction into exactly the expected turn: never into the next one.
func (client *Client) SteerTurn(ctx context.Context, request SteerRequest) error {
	runtime, err := client.runtime(ctx)
	if err != nil {
		return runStageError{code: "runtime.unavailable", err: err}
	}
	return runtime.steerTurn(ctx, request)
}

func (client *Client) steerTurn(ctx context.Context, request SteerRequest) error {
	if client == nil || client.sdk == nil || ctx == nil || request.ThreadID == "" || request.TurnID == "" ||
		request.ClientMessageID == "" || request.Input == "" {
		return ErrInvalid
	}
	_, err := client.sdk.Raw().TurnSteer(ctx, protocol.TurnSteerParams{
		ThreadID: request.ThreadID, ExpectedTurnID: request.TurnID,
		ClientUserMessageID: protocol.Some(request.ClientMessageID),
		Input: []protocol.UserInput{{
			TypeValue: "text", Text: protocol.SomeNonNull(request.Input), TextElements: protocol.SomeNonNull([]protocol.TextElement{}),
		}},
	})
	return classifySteerError(err)
}

// classifySteerError tells the refusals the service answers differently from a failure.
func classifySteerError(err error) error {
	var rpcErr *sdk.RPCError
	if err == nil || !errors.As(err, &rpcErr) {
		return err
	}
	if steerRefusedAsNotSteerable(rpcErr.Data) {
		return fmt.Errorf("%w: %s", agent.ErrTurnNotSteerable, rpcErr.Message)
	}
	if rpcErr.Message == noActiveTurnMessage || strings.HasPrefix(rpcErr.Message, turnMismatchPrefix) ||
		strings.HasPrefix(rpcErr.Message, steerThreadGonePrefix) {
		return fmt.Errorf("%w: %s", agent.ErrTurnNotActive, rpcErr.Message)
	}
	return err
}

func steerRefusedAsNotSteerable(data json.RawMessage) bool {
	var refusal struct {
		Info map[string]json.RawMessage `json:"codexErrorInfo"`
	}
	if len(data) == 0 || json.Unmarshal(data, &refusal) != nil {
		return false
	}
	_, refused := refusal.Info[codexErrorNotSteerable]
	return refused
}
