package agent

import "encoding/json"

type ToolResult struct {
	Success bool
	Payload json.RawMessage
	// ImageURL is an image the model sees with the result, as a data URL: a drawing of the saga
	// gallery the chat compares with the text (specs/2026-10-29-saga-illustrations.md R1.5).
	ImageURL string
}

// RunFailure — класс отказа незавершённого хода модели; пустой — класс не известен.
type RunFailure string

const (
	RunFailureUsageLimitExceeded    RunFailure = "usageLimitExceeded"
	RunFailureServerOverloaded      RunFailure = "serverOverloaded"
	RunFailureConnectionFailed      RunFailure = "connectionFailed"
	RunFailureContextWindowExceeded RunFailure = "contextWindowExceeded"
)

type RunOutcome struct {
	RootThreadID string
	TurnID       string
	Status       TurnStatus
	Failure      RunFailure
	// Error — ошибка незавершённого хода от app-server (класс и сообщение) для журнала диагностики.
	Error         string
	FinalResponse string
	// Usage — расход токенов корневого треда за ход (specs/2026-10-28-writer-quotas-and-fair-queue.md
	// UC8); пуст, если app-server не прислал ни одного thread/tokenUsage/updated.
	Usage TurnUsage
}

// TokenCounts — токены треда по видам.
type TokenCounts struct {
	Input           int64
	CachedInput     int64
	Output          int64
	ReasoningOutput int64
}

// TurnUsage — расход корневого треда за ход: последний итог треда, итог до первого ответа модели
// в ходе (первое Total − Last) и число изменений итога (ответов модели).
type TurnUsage struct {
	Updates    int64
	Total      TokenCounts
	StartTotal TokenCounts
}
