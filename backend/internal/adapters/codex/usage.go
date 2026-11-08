package codex

import (
	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// turnUsageMeter collects what the root thread spent during one turn from
// thread/tokenUsage/updated (specs/2026-10-28-writer-quotas-and-fair-queue.md R8.1): the last
// thread total, the total before the turn's first model response (its Total − Last) and the
// number of times the total changed. Totals are exact; summing Last overcounts, as the app-server
// repeats an update about once a turn.
type turnUsageMeter struct {
	threadID string
	usage    agent.TurnUsage
}

func (meter *turnUsageMeter) observe(payload any) {
	update, ok := payload.(protocol.ThreadTokenUsageUpdatedNotification)
	if !ok || meter == nil || update.ThreadID != meter.threadID {
		return
	}
	total := tokenCounts(update.TokenUsage.Total)
	if meter.usage.Updates > 0 && total == meter.usage.Total {
		return
	}
	if meter.usage.Updates == 0 {
		last := tokenCounts(update.TokenUsage.Last)
		meter.usage.StartTotal = agent.TokenCounts{
			Input: max(total.Input-last.Input, 0), CachedInput: max(total.CachedInput-last.CachedInput, 0),
			Output: max(total.Output-last.Output, 0), ReasoningOutput: max(total.ReasoningOutput-last.ReasoningOutput, 0),
		}
	}
	meter.usage.Updates++
	meter.usage.Total = total
}

func tokenCounts(breakdown protocol.TokenUsageBreakdown) agent.TokenCounts {
	return agent.TokenCounts{
		Input: breakdown.InputTokens, CachedInput: breakdown.CachedInputTokens,
		Output: breakdown.OutputTokens, ReasoningOutput: breakdown.ReasoningOutputTokens,
	}
}
