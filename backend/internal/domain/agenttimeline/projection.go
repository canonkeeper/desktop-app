package agenttimeline

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"canonkeeper/backend/internal/domain/researchchat"
)

var ErrInvalidTimeline = errors.New("invalid native timeline item")

var (
	// ErrThreadPending — у хода ещё нет root thread: worker не захватил задачу или не создал thread.
	ErrThreadPending = errors.New("agent timeline: root thread is not bound yet")
	// ErrNotFound — у задачи нет читаемой привязки к ходу чата.
	ErrNotFound = errors.New("agent timeline: binding not found")
)

type RawItem struct {
	Role       Role
	TurnID     string
	TurnStatus TurnStatus
	Item       json.RawMessage
}

type Role string

const (
	RoleMain           Role = "main"
	RoleCompleteness   Role = "completeness"
	RoleSourceEvidence Role = "source_evidence"
	RoleSagaHistory    Role = "saga_history"
)

func (role Role) Valid() bool {
	switch role {
	case RoleMain, RoleCompleteness, RoleSourceEvidence, RoleSagaHistory:
		return true
	default:
		return false
	}
}

type TurnStatus string

const (
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusInterrupted TurnStatus = "interrupted"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInProgress  TurnStatus = "inProgress"
)

type EventKind string

const (
	EventUser        EventKind = "user"
	EventSteer       EventKind = "steer"
	EventReasoning   EventKind = "reasoning"
	EventAssistant   EventKind = "assistant"
	EventToolRequest EventKind = "tool_request"
	EventToolResult  EventKind = "tool_result"
	EventPlan        EventKind = "plan"
	EventSubagent    EventKind = "subagent"
	EventTurnOutcome EventKind = "turn_outcome"
)

type Outcome string

const (
	OutcomeInProgress  Outcome = "in_progress"
	OutcomeCompleted   Outcome = "completed"
	OutcomeFailed      Outcome = "failed"
	OutcomeInterrupted Outcome = "interrupted"
)

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolCall struct {
	Namespace string          `json:"namespace,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Status    string          `json:"status,omitempty"`
	Success   *bool           `json:"success,omitempty"`
	Duration  *int64          `json:"durationMs,omitempty"`
	Content   []ToolContent   `json:"content,omitempty"`
}

type Subagent struct {
	Path     string `json:"path"`
	ThreadID string `json:"threadId"`
	Status   string `json:"status"`
}

type Event struct {
	ID             string
	Kind           EventKind
	JobID          string
	Role           Role
	TurnID         string
	ItemID         string
	LogicalSection int
	Text           string
	// Images are the numbers of the gallery images attached to a question (EventUser,
	// specs/2026-10-29-saga-illustrations.md R15.9).
	Images []int
	// FirstPhoto and SteerID belong to a correction (EventSteer, specs/2026-10-30-chat-steering.md
	// R3.1): the anchor number of its first image and the id the web waits for.
	FirstPhoto int
	SteerID    string
	// Attachment is the card of the chapter of the author attached to a question (EventUser,
	// specs/2026-11-07-chat-attach-chapter.md CA4.1): read from the native question, never from PostgreSQL.
	Attachment *QuestionAttachment
	// Button: a button of the page sent the question (EventUser, specs/2026-11-11-ux9-writing-b.md WB3.3).
	Button   bool
	Phase    string
	Outcome  Outcome
	Tool     *ToolCall
	Subagent *Subagent
}

// QuestionAttachment is what the card of an attached chapter shows: its number in the chat, title and words.
type QuestionAttachment struct {
	Number int
	Title  string
	Words  int
}

type Delta struct {
	Kind           EventKind
	Role           Role
	TurnID         string
	ItemID         string
	LogicalSection int
	Text           string
	RawReasoning   bool
}

type ContextUpdate struct {
	Role          Role
	TurnID        string
	UsedTokens    *int64
	ContextWindow *int64
}

type Update struct {
	Delta   *Delta
	Context *ContextUpdate
	Err     error
}

func (delta Delta) Event(jobID string) Event {
	id := delta.ItemID + ":" + strconv.Itoa(delta.LogicalSection)
	if delta.RawReasoning {
		id = delta.ItemID + ":raw:" + strconv.Itoa(delta.LogicalSection)
	}
	return Event{
		ID: id, Kind: delta.Kind,
		JobID: jobID, Role: delta.Role, TurnID: delta.TurnID, ItemID: delta.ItemID,
		LogicalSection: delta.LogicalSection, Text: delta.Text,
	}
}

type Snapshot struct {
	Events []Event
}

type nativeItem struct {
	AgentPath     string                      `json:"agentPath"`
	AgentThreadID string                      `json:"agentThreadId"`
	Arguments     json.RawMessage             `json:"arguments"`
	ClientID      string                      `json:"clientId"`
	ContentItems  []ToolContent               `json:"contentItems"`
	Content       json.RawMessage             `json:"content"`
	Duration      *int64                      `json:"durationMs"`
	ID            string                      `json:"id"`
	Kind          string                      `json:"kind"`
	Namespace     string                      `json:"namespace"`
	Phase         string                      `json:"phase"`
	ReceiverIDs   []string                    `json:"receiverThreadIds"`
	SenderID      string                      `json:"senderThreadId"`
	Status        string                      `json:"status"`
	Success       *bool                       `json:"success"`
	Summary       []string                    `json:"summary"`
	Text          string                      `json:"text"`
	Tool          string                      `json:"tool"`
	Type          string                      `json:"type"`
	AgentStates   map[string]nativeAgentState `json:"agentsStates"`
}

type nativeAgentState struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Project turns the native items of a chat root thread into the public timeline; only the
// main thread's user messages become questions or corrections.
func Project(jobID string, items []RawItem) (Snapshot, error) {
	if strings.TrimSpace(jobID) == "" {
		return Snapshot{}, ErrInvalidTimeline
	}
	snapshot := Snapshot{Events: make([]Event, 0, len(items))}
	for index, raw := range items {
		if !raw.Role.Valid() || strings.TrimSpace(raw.TurnID) == "" || !validTurnStatus(raw.TurnStatus) {
			return Snapshot{}, ErrInvalidTimeline
		}
		if len(raw.Item) > 0 {
			if !json.Valid(raw.Item) {
				return Snapshot{}, ErrInvalidTimeline
			}
			var item nativeItem
			if err := json.Unmarshal(raw.Item, &item); err != nil {
				return Snapshot{}, ErrInvalidTimeline
			}
			events, err := projectItem(item, raw.Role == RoleMain)
			if err != nil {
				return Snapshot{}, err
			}
			for section, event := range events {
				if event.ID == "" {
					event.ID = item.ID + ":" + strconv.Itoa(section)
					event.LogicalSection = section
				}
				event.JobID = jobID
				event.Role = raw.Role
				event.TurnID = raw.TurnID
				event.ItemID = item.ID
				snapshot.Events = append(snapshot.Events, event)
			}
		}
		if index+1 < len(items) && items[index+1].TurnID == raw.TurnID {
			if items[index+1].Role != raw.Role || items[index+1].TurnStatus != raw.TurnStatus {
				return Snapshot{}, ErrInvalidTimeline
			}
			continue
		}
		if outcome, ok := terminalTurnOutcome(raw.TurnStatus); ok {
			snapshot.Events = append(snapshot.Events, Event{
				ID: raw.TurnID + ":turn_outcome", Kind: EventTurnOutcome,
				JobID: jobID, Role: raw.Role, TurnID: raw.TurnID, Outcome: outcome,
			})
		}
	}
	return snapshot, nil
}

func projectItem(item nativeItem, publicQuestion bool) ([]Event, error) {
	switch item.Type {
	case "userMessage":
		var content []ToolContent
		if json.Unmarshal(item.Content, &content) != nil {
			return nil, ErrInvalidTimeline
		}
		if !publicQuestion || len(content) != 1 || content[0].Type != "text" {
			return nil, nil
		}
		question, ok := researchchat.PublicAssignmentQuestion(content[0].Text)
		if !ok {
			return nil, nil
		}
		if strings.TrimSpace(item.ID) == "" {
			return nil, ErrInvalidTimeline
		}
		if question.Correction {
			return []Event{{
				Kind: EventSteer, Text: question.Text, Images: question.Images,
				FirstPhoto: question.FirstPhoto, SteerID: item.ClientID,
			}}, nil
		}
		event := Event{Kind: EventUser, Text: question.Text, Images: question.Images, Button: question.Button}
		if attachment := question.Attachment; attachment != nil {
			event.Attachment = &QuestionAttachment{Number: attachment.Number, Title: attachment.Title, Words: attachment.Words}
		}
		return []Event{event}, nil
	case "reasoning":
		if strings.TrimSpace(item.ID) == "" {
			return nil, ErrInvalidTimeline
		}
		// Summaries and raw reasoning keep the section ids of their live deltas.
		events := make([]Event, 0, len(item.Summary))
		for section, text := range item.Summary {
			if strings.TrimSpace(text) != "" {
				events = append(events, Event{
					ID: item.ID + ":" + strconv.Itoa(section), Kind: EventReasoning, LogicalSection: section, Text: text,
				})
			}
		}
		var content []string
		if len(item.Content) > 0 && json.Unmarshal(item.Content, &content) != nil {
			return nil, ErrInvalidTimeline
		}
		for section, text := range content {
			if strings.TrimSpace(text) != "" {
				events = append(events, Event{
					ID: item.ID + ":raw:" + strconv.Itoa(section), Kind: EventReasoning, LogicalSection: section, Text: text,
				})
			}
		}
		return events, nil
	case "agentMessage":
		if strings.TrimSpace(item.ID) == "" {
			return nil, ErrInvalidTimeline
		}
		if strings.TrimSpace(item.Text) == "" {
			return nil, nil
		}
		return []Event{{Kind: EventAssistant, Text: item.Text, Phase: item.Phase}}, nil
	case "dynamicToolCall":
		return projectTool(item)
	case "plan":
		if strings.TrimSpace(item.ID) == "" {
			return nil, ErrInvalidTimeline
		}
		if strings.TrimSpace(item.Text) == "" {
			return nil, nil
		}
		return []Event{{Kind: EventPlan, Text: item.Text}}, nil
	case "subAgentActivity":
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.AgentPath) == "" ||
			strings.TrimSpace(item.AgentThreadID) == "" || strings.TrimSpace(item.Kind) == "" {
			return nil, ErrInvalidTimeline
		}
		outcome, ok := subagentActivityOutcome(item.Kind)
		if !ok {
			return nil, ErrInvalidTimeline
		}
		return []Event{{
			Kind:     EventSubagent,
			Outcome:  outcome,
			Subagent: &Subagent{Path: item.AgentPath, ThreadID: item.AgentThreadID, Status: item.Kind},
		}}, nil
	case "collabAgentToolCall":
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.SenderID) == "" ||
			strings.TrimSpace(item.Tool) == "" || strings.TrimSpace(item.Status) == "" {
			return nil, ErrInvalidTimeline
		}
		events := make([]Event, 0, len(item.ReceiverIDs))
		for _, threadID := range item.ReceiverIDs {
			state, ok := item.AgentStates[threadID]
			if !ok || strings.TrimSpace(threadID) == "" || strings.TrimSpace(state.Status) == "" {
				return nil, ErrInvalidTimeline
			}
			outcome, ok := collabAgentOutcome(state.Status)
			if !ok {
				return nil, ErrInvalidTimeline
			}
			events = append(events, Event{
				Kind: EventSubagent, Text: state.Message, Outcome: outcome,
				Subagent: &Subagent{ThreadID: threadID, Status: state.Status},
			})
		}
		return events, nil
	default:
		return nil, nil
	}
}

func projectTool(item nativeItem) ([]Event, error) {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Tool) == "" ||
		len(item.Arguments) == 0 || !json.Valid(item.Arguments) || strings.TrimSpace(item.Status) == "" {
		return nil, ErrInvalidTimeline
	}
	request := &ToolCall{
		Namespace: item.Namespace,
		Name:      item.Tool,
		Arguments: append(json.RawMessage(nil), item.Arguments...),
	}
	events := []Event{{Kind: EventToolRequest, Tool: request}}
	outcome, terminal, ok := toolOutcome(item.Status)
	if !ok {
		return nil, ErrInvalidTimeline
	}
	if !terminal {
		return events, nil
	}
	result := &ToolCall{
		Namespace: item.Namespace,
		Name:      item.Tool,
		Arguments: append(json.RawMessage(nil), item.Arguments...),
		Status:    item.Status,
		Success:   item.Success,
		Duration:  item.Duration,
		Content:   append([]ToolContent(nil), item.ContentItems...),
	}
	return []Event{{Kind: EventToolResult, Outcome: outcome, Tool: result}}, nil
}

func toolOutcome(status string) (Outcome, bool, bool) {
	switch status {
	case "inProgress":
		return OutcomeInProgress, false, true
	case "completed":
		return OutcomeCompleted, true, true
	case "failed", "declined":
		return OutcomeFailed, true, true
	default:
		return "", false, false
	}
}

func subagentActivityOutcome(status string) (Outcome, bool) {
	switch status {
	case "started", "interacted":
		return OutcomeInProgress, true
	case "interrupted":
		return OutcomeInterrupted, true
	case "completed":
		return OutcomeCompleted, true
	default:
		return "", false
	}
}

func collabAgentOutcome(status string) (Outcome, bool) {
	switch status {
	case "pendingInit", "running":
		return OutcomeInProgress, true
	case "completed":
		return OutcomeCompleted, true
	case "errored", "notFound":
		return OutcomeFailed, true
	case "interrupted", "shutdown":
		return OutcomeInterrupted, true
	default:
		return "", false
	}
}

func validTurnStatus(status TurnStatus) bool {
	return status == TurnStatusCompleted || status == TurnStatusInterrupted || status == TurnStatusFailed || status == TurnStatusInProgress
}

func terminalTurnOutcome(status TurnStatus) (Outcome, bool) {
	switch status {
	case TurnStatusCompleted:
		return OutcomeCompleted, true
	case TurnStatusFailed:
		return OutcomeFailed, true
	case TurnStatusInterrupted:
		return OutcomeInterrupted, true
	default:
		return "", false
	}
}
