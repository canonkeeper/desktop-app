package codex

import (
	"context"
	"errors"
	"testing"

	"github.com/openai/codex/sdk/go/protocol"
)

func TestNativeChildResolvesExactRootAndRole(t *testing.T) {
	threads := map[string]protocol.Thread{
		"root":   testProtocolThread("root", "project", "", ""),
		"child":  testProtocolThread("child", "project", "root", "reviewer-sources"),
		"nested": testProtocolThread("nested", "project", "child", "reviewer-saga"),
	}
	caller, err := resolveCaller(t.Context(), func(_ context.Context, threadID string) (protocol.Thread, error) {
		thread, ok := threads[threadID]
		if !ok {
			return protocol.Thread{}, errors.New("missing")
		}
		return thread, nil
	}, "nested")
	if err != nil || caller.ThreadID != "nested" || caller.RootThreadID != "root" || caller.AgentRole != "reviewer-saga" {
		t.Fatalf("caller = %#v, %v", caller, err)
	}
}
