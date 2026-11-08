package agent

import "encoding/json"

type ToolSpec struct {
	Namespace   string
	Name        string
	Description string
	InputSchema json.RawMessage
}

type TurnStatus string

const (
	TurnCompleted   TurnStatus = "completed"
	TurnInterrupted TurnStatus = "interrupted"
	TurnFailed      TurnStatus = "failed"
	TurnInProgress  TurnStatus = "inProgress"
)

type ToolCaller struct {
	ThreadID     string
	RootThreadID string
	AgentRole    string
}

type ToolCall struct {
	Caller    ToolCaller
	TurnID    string
	CallID    string
	Namespace string
	Tool      string
	Arguments json.RawMessage
}
