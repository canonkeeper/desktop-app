package codex

import (
	"context"
	"encoding/json"
	"sync"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

type Handler func(context.Context, agent.ToolCall) (agent.ToolResult, error)

type Dispatcher struct {
	mu     sync.RWMutex
	roots  map[string]*rootDispatch
	caller func(context.Context, string) (agent.ToolCaller, error)
}

type rootDispatch struct {
	mu      sync.RWMutex
	handler Handler
	ready   chan struct{}
	turnID  string
	pending string
	seen    map[string]struct{}
	err     error
}

func newDispatcher(caller func(context.Context, string) (agent.ToolCaller, error)) *Dispatcher {
	return &Dispatcher{roots: map[string]*rootDispatch{}, caller: caller}
}

func (dispatcher *Dispatcher) attach(rootThreadID string, handler Handler) {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	dispatcher.roots[rootThreadID] = &rootDispatch{handler: handler, ready: make(chan struct{}), seen: map[string]struct{}{}}
}

func (dispatcher *Dispatcher) detach(rootThreadID string) {
	dispatcher.mu.Lock()
	root := dispatcher.roots[rootThreadID]
	delete(dispatcher.roots, rootThreadID)
	dispatcher.mu.Unlock()
	if root == nil {
		return
	}
	root.fail(ErrDenied)
}

func (dispatcher *Dispatcher) publishTurn(rootThreadID, turnID string) {
	dispatcher.mu.RLock()
	root := dispatcher.roots[rootThreadID]
	dispatcher.mu.RUnlock()
	if root != nil {
		root.publish(turnID)
	}
}

func (dispatcher *Dispatcher) prepareTurn(rootThreadID, turnID string) {
	dispatcher.mu.RLock()
	root := dispatcher.roots[rootThreadID]
	dispatcher.mu.RUnlock()
	if root == nil || turnID == "" {
		return
	}
	root.mu.Lock()
	if root.err == nil && root.turnID != turnID {
		if _, stale := root.seen[turnID]; !stale {
			root.pending = turnID
		}
	}
	root.mu.Unlock()
}

func (dispatcher *Dispatcher) denyTurn(rootThreadID string, err error) {
	dispatcher.mu.RLock()
	root := dispatcher.roots[rootThreadID]
	dispatcher.mu.RUnlock()
	if root != nil {
		root.fail(err)
	}
}

func (root *rootDispatch) publish(turnID string) {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.err != nil || turnID == "" {
		return
	}
	if root.turnID != "" {
		root.seen[root.turnID] = struct{}{}
	}
	root.turnID = turnID
	root.pending = ""
	ready := root.ready
	root.ready = make(chan struct{})
	close(ready)
}

func (root *rootDispatch) fail(err error) {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.err != nil {
		return
	}
	root.err = err
	close(root.ready)
}

func (dispatcher *Dispatcher) handle(ctx context.Context, params protocol.DynamicToolCallParams) (protocol.DynamicToolCallResponse, error) {
	if dispatcher == nil || dispatcher.caller == nil {
		return deniedToolResponse(), nil
	}
	caller, err := dispatcher.caller(ctx, params.ThreadID)
	if err != nil || caller.RootThreadID == "" {
		return deniedToolResponse(), nil
	}
	dispatcher.mu.RLock()
	root := dispatcher.roots[caller.RootThreadID]
	dispatcher.mu.RUnlock()
	if root == nil {
		return deniedToolResponse(), nil
	}
	waited := false
	var handler Handler
	for {
		root.mu.RLock()
		boundTurnID, gateErr := root.turnID, root.err
		handler = root.handler
		_, stale := root.seen[params.TurnID]
		pendingTurnID := root.pending
		ready := root.ready
		root.mu.RUnlock()
		if gateErr != nil || handler == nil || params.TurnID == "" || params.CallID == "" {
			return deniedToolResponse(), nil
		}
		if caller.ThreadID != caller.RootThreadID {
			if caller.AgentRole == "" || boundTurnID == "" {
				return deniedToolResponse(), nil
			}
			break
		}
		if params.TurnID == boundTurnID {
			break
		}
		if stale || params.TurnID != pendingTurnID || waited {
			return deniedToolResponse(), nil
		}
		waited = true
		select {
		case <-ready:
		case <-ctx.Done():
			return deniedToolResponse(), nil
		}
	}
	namespace, _ := params.Namespace.Value()
	result, err := handler(ctx, agent.ToolCall{
		Caller: caller, TurnID: params.TurnID, CallID: params.CallID,
		Namespace: namespace, Tool: params.Tool, Arguments: append(json.RawMessage(nil), params.Arguments...),
	})
	if err != nil {
		return protocol.DynamicToolCallResponse{}, err
	}
	payload := result.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	items := []protocol.DynamicToolCallOutputContentItem{{
		TypeValue: "inputText", Text: protocol.SomeNonNull(string(payload)),
	}}
	if result.ImageURL != "" {
		items = append(items, protocol.DynamicToolCallOutputContentItem{
			TypeValue: "inputImage", ImageURL: protocol.SomeNonNull(result.ImageURL),
		})
	}
	return protocol.DynamicToolCallResponse{Success: result.Success, ContentItems: items}, nil
}

func deniedToolResponse() protocol.DynamicToolCallResponse {
	return protocol.DynamicToolCallResponse{
		Success: false,
		ContentItems: []protocol.DynamicToolCallOutputContentItem{{
			TypeValue: "inputText", Text: protocol.SomeNonNull(`{"type":"tool_denied"}`),
		}},
	}
}
