package codex

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

var ErrDenied = errors.New("codex caller denied")

type threadLookup func(context.Context, string) (protocol.Thread, error)

func resolveCaller(ctx context.Context, read threadLookup, threadID string) (agent.ToolCaller, error) {
	if ctx == nil || read == nil || threadID == "" {
		return agent.ToolCaller{}, ErrDenied
	}
	caller := agent.ToolCaller{ThreadID: threadID}
	seen := map[string]struct{}{}
	currentID := threadID
	for {
		if _, duplicate := seen[currentID]; duplicate {
			return agent.ToolCaller{}, ErrDenied
		}
		seen[currentID] = struct{}{}
		thread, err := read(ctx, currentID)
		if err != nil || thread.ID != currentID {
			return agent.ToolCaller{}, errors.Join(ErrDenied, err)
		}
		if currentID == threadID {
			caller.AgentRole, _ = thread.AgentRole.Value()
		}
		parentID, hasParent := thread.ParentThreadID.Value()
		if !hasParent || parentID == "" {
			caller.RootThreadID = currentID
			if currentID != threadID && caller.AgentRole == "" {
				return agent.ToolCaller{}, ErrDenied
			}
			return caller, nil
		}
		currentID = parentID
	}
}
