package codex

import (
	"encoding/json"
	"strings"

	"canonkeeper/backend/internal/domain/agent"

	"github.com/openai/codex/sdk/go/protocol"
)

// resultFromTurn переводит native turn в итог хода: статус, финальный ответ, класс отказа и
// ошибку app-server для журнала диагностики.
func resultFromTurn(turn protocol.Turn) RunResult {
	result := RunResult{
		TurnID: turn.ID, Status: agent.TurnStatus(turn.Status), FinalResponse: finalResponse(turn.Items),
	}
	if turnError, ok := turn.Error.Value(); ok {
		result.Error = turnError.Message
		if info, ok := turnError.CodexErrorInfo.Value(); ok {
			result.Failure = runFailure(info)
			if encoded, err := json.Marshal(info); err == nil {
				result.Error = "codexErrorInfo=" + strings.Trim(string(encoded), `"`) + ": " + turnError.Message
			}
		}
	}
	return result
}

// runFailure — шаг UC4.1 «класс отказа модели»: сопоставляет codexErrorInfo незавершённого хода
// классу agent.RunFailure по таблице Data (specs/2026-10-28-codex-0165.md UC4; классы Codex 0.167.2,
// новый в нём inferenceHookDenied — specs/2026-11-12-desk-codex.md); прочие классы app-server
// остаются без класса.
func runFailure(info protocol.CodexErrorInfo) agent.RunFailure {
	if info.HttpConnectionFailed.IsSet() || info.ResponseStreamConnectionFailed.IsSet() ||
		info.ResponseStreamDisconnected.IsSet() || info.ResponseTooManyFailedAttempts.IsSet() {
		return agent.RunFailureConnectionFailed
	}
	switch info.StringValue {
	case protocol.CodexErrorInfoUsageLimitExceeded.StringValue:
		return agent.RunFailureUsageLimitExceeded
	case protocol.CodexErrorInfoServerOverloaded.StringValue, protocol.CodexErrorInfoInternalServerError.StringValue,
		protocol.CodexErrorInfoRateLimitExceeded.StringValue, protocol.CodexErrorInfoPolicyCapacityUnavailable.StringValue,
		protocol.CodexErrorInfoFlexUnavailable.StringValue:
		return agent.RunFailureServerOverloaded
	case protocol.CodexErrorInfoContextWindowExceeded.StringValue, protocol.CodexErrorInfoSessionBudgetExceeded.StringValue:
		return agent.RunFailureContextWindowExceeded
	}
	return ""
}

func finalResponse(items []protocol.ThreadItem) string {
	fallback := ""
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		if item.TypeValue != "agentMessage" {
			continue
		}
		text, ok := item.Text.Value()
		if !ok {
			continue
		}
		phase, hasPhase := item.Phase.Value()
		if hasPhase {
			if phase == protocol.MessagePhaseFinalAnswer {
				return text
			}
			continue
		}
		if fallback == "" {
			fallback = text
		}
	}
	return fallback
}
