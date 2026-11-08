package codex

import (
	"testing"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

func usageUpdate(threadID string, total, last int64) protocol.ThreadTokenUsageUpdatedNotification {
	return protocol.ThreadTokenUsageUpdatedNotification{ThreadID: threadID, TurnID: "turn", TokenUsage: protocol.ThreadTokenUsage{
		Total: protocol.TokenUsageBreakdown{InputTokens: total, CachedInputTokens: total / 2, OutputTokens: total / 10},
		Last:  protocol.TokenUsageBreakdown{InputTokens: last, CachedInputTokens: last / 2, OutputTokens: last / 10},
	}}
}

// R8.1: three growing totals and one repeat are three model responses; the usage keeps the last
// total and the total before the first response; other threads do not count.
func TestTurnUsageMeterCountsRootTotals(t *testing.T) {
	meter := &turnUsageMeter{threadID: "root"}
	for _, payload := range []any{
		usageUpdate("root", 1000, 200),
		usageUpdate("child", 99999, 99999),
		usageUpdate("root", 1500, 500),
		usageUpdate("root", 1500, 500),
		protocol.TurnStartedNotification{ThreadID: "root"},
		usageUpdate("root", 2100, 600),
	} {
		meter.observe(payload)
	}
	want := agent.TurnUsage{
		Updates:    3,
		Total:      agent.TokenCounts{Input: 2100, CachedInput: 1050, Output: 210},
		StartTotal: agent.TokenCounts{Input: 800, CachedInput: 400, Output: 80},
	}
	if meter.usage != want {
		t.Fatalf("usage = %+v, want %+v", meter.usage, want)
	}
}
