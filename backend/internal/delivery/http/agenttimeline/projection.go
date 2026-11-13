package agenttimeline

import (
	"bytes"
	"canonkeeper/backend/internal/domain/agent"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/domain/researchchat"
)

var (
	ErrInvalidCursor = errors.New("invalid agent timeline cursor")
	ErrUnavailable   = errors.New("agent timeline unavailable")
)

type Timeline interface {
	Snapshot(context.Context, string, string) (domain.Snapshot, error)
	Updates(context.Context, string, string) (<-chan domain.Update, error)
}

type Handlers struct {
	timeline                   Timeline
	places                     Places
	currentUser                CurrentUserService
	jobs                       JobAccessService
	allowedOrigins             []string
	authorizationLeaseDuration time.Duration
	bindingPollInterval        time.Duration
}

type ToolDTO struct {
	Namespace string          `json:"namespace,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Status    string          `json:"status,omitempty"`
	Success   *bool           `json:"success,omitempty"`
	Duration  *int64          `json:"durationMs,omitempty"`
	Summary   string          `json:"summary,omitempty"`
}

type EventDTO struct {
	AgentPath      string           `json:"agentPath,omitempty"`
	AgentThreadID  string           `json:"agentThreadId,omitempty"`
	Cursor         string           `json:"cursor"`
	ID             string           `json:"id"`
	JobID          string           `json:"jobId"`
	Kind           domain.EventKind `json:"kind"`
	LogicalSection int              `json:"logicalSection"`
	Outcome        domain.Outcome   `json:"outcome,omitempty"`
	Phase          string           `json:"phase,omitempty"`
	ProviderItemID string           `json:"providerItemId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	Role           string           `json:"role"`
	Text           string           `json:"text,omitempty"`
	Images         []int            `json:"images,omitempty"`
	FirstPhoto     int              `json:"firstPhoto,omitempty"`
	SteerID        string           `json:"steerId,omitempty"`
	Attachment     *AttachmentDTO   `json:"attachment,omitempty"`
	// Button: a button of the page sent the question (specs/2026-11-11-ux9-writing-b.md WB3.3).
	Button bool     `json:"button,omitempty"`
	Tool   *ToolDTO `json:"tool,omitempty"`
	// Places name the chapters of the keys the event shows — the arguments of its tool, the source links of its text —
	// as the author reads them (specs/2026-11-10-ux1-names.md N2.1).
	Places map[string]string `json:"places,omitempty"`
}

// AttachmentDTO is the card of the chapter attached to a question (specs/2026-11-07-chat-attach-chapter.md CA4.1).
type AttachmentDTO struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Words  int    `json:"words"`
}

type DeltaDTO struct {
	ID             string           `json:"id"`
	Kind           domain.EventKind `json:"kind"`
	JobID          string           `json:"jobId"`
	Role           string           `json:"role"`
	ProviderTurnID string           `json:"providerTurnId"`
	ProviderItemID string           `json:"providerItemId"`
	LogicalSection int              `json:"logicalSection"`
	Text           string           `json:"text"`
}

type ContextDTO struct {
	Role           string `json:"role"`
	ProviderTurnID string `json:"providerTurnId"`
	UsedTokens     *int64 `json:"usedTokens,omitempty"`
	ContextWindow  *int64 `json:"contextWindow,omitempty"`
}

type SnapshotDTO struct {
	Events         []EventDTO `json:"events"`
	EOF            bool       `json:"eof"`
	NextCursor     *string    `json:"nextCursor"`
	NeedsReconcile bool       `json:"needsReconcile"`
}

type EnvelopeDTO struct {
	Type     string       `json:"type"`
	Cursor   string       `json:"cursor,omitempty"`
	Snapshot *SnapshotDTO `json:"snapshot,omitempty"`
	Delta    *DeltaDTO    `json:"delta,omitempty"`
	Context  *ContextDTO  `json:"context,omitempty"`
}

func (handlers *Handlers) Snapshot(ctx context.Context, projectID, jobID string, cursor ...string) (SnapshotDTO, error) {
	if err := validateCursor(cursor); err != nil {
		return SnapshotDTO{}, err
	}
	return handlers.snapshot(ctx, projectID, jobID, placeMemo{})
}

func (handlers *Handlers) snapshot(ctx context.Context, projectID, jobID string, memo placeMemo) (SnapshotDTO, error) {
	if handlers == nil || handlers.timeline == nil {
		return SnapshotDTO{}, ErrUnavailable
	}
	snapshot, err := handlers.timeline.Snapshot(ctx, projectID, jobID)
	if err != nil {
		return SnapshotDTO{}, err
	}
	dto := snapshotDTO(snapshot)
	if err := handlers.namePlaces(ctx, projectID, dto.Events, memo); err != nil {
		return SnapshotDTO{}, err
	}
	return dto, nil
}

func (handlers *Handlers) Live(ctx context.Context, projectID, jobID string, cursor ...string) (EnvelopeDTO, error) {
	if err := validateCursor(cursor); err != nil {
		return EnvelopeDTO{}, err
	}
	return handlers.live(ctx, projectID, jobID, placeMemo{})
}

// live is the snapshot a stream sends; memo is what the stream has named so far.
func (handlers *Handlers) live(ctx context.Context, projectID, jobID string, memo placeMemo) (EnvelopeDTO, error) {
	snapshot, err := handlers.snapshot(ctx, projectID, jobID, memo)
	if err != nil {
		return EnvelopeDTO{}, err
	}
	envelope := EnvelopeDTO{Type: "snapshot", Snapshot: &snapshot}
	if count := len(snapshot.Events); count > 0 {
		envelope.Cursor = snapshot.Events[count-1].Cursor
	}
	return envelope, nil
}

func snapshotDTO(snapshot domain.Snapshot) SnapshotDTO {
	events := make([]EventDTO, 0, len(snapshot.Events))
	for _, event := range snapshot.Events {
		mapped := EventDTO{
			Cursor: eventCursor(event), ID: event.ID, JobID: event.JobID, Kind: event.Kind,
			LogicalSection: event.LogicalSection, Outcome: event.Outcome, Phase: event.Phase,
			ProviderItemID: event.ItemID, ProviderTurnID: event.TurnID, Role: string(event.Role), Text: event.Text,
			Images: append([]int(nil), event.Images...), FirstPhoto: event.FirstPhoto, SteerID: event.SteerID, Button: event.Button,
		}
		if event.Attachment != nil {
			mapped.Attachment = &AttachmentDTO{Number: event.Attachment.Number, Title: event.Attachment.Title, Words: event.Attachment.Words}
		}
		if event.Tool != nil {
			mapped.Tool = &ToolDTO{
				Namespace: event.Tool.Namespace, Name: event.Tool.Name,
				Status: event.Tool.Status, Success: event.Tool.Success, Duration: event.Tool.Duration,
			}
			mapped.Tool.Arguments, mapped.Tool.Summary = sagaToolView(event.Tool)
		}
		if event.Subagent != nil {
			mapped.AgentPath = event.Subagent.Path
			mapped.AgentThreadID = event.Subagent.ThreadID
		}
		events = append(events, mapped)
	}
	return SnapshotDTO{Events: events, EOF: true, NextCursor: nil, NeedsReconcile: false}
}

const (
	maxToolArguments = 4096
	maxToolSummary   = 240
)

// sagaToolView shows the author what a saga tool or a tool of the drawing helper was asked and the first line of its
// answer — a summary such as «56 абзацев в 23 главах», «Рисунок 2: правка 1 готова» or a typed error; the rest of the
// result stays in the thread. Tools of the previous assistant used internal refs and get neither.
func sagaToolView(tool *domain.ToolCall) (json.RawMessage, string) {
	if tool.Namespace != researchchat.ToolNamespace && tool.Namespace != agent.ToolNamespaceDrawing {
		return nil, ""
	}
	var arguments json.RawMessage
	if trimmed := bytes.TrimSpace(tool.Arguments); len(trimmed) <= maxToolArguments &&
		bytes.HasPrefix(trimmed, []byte("{")) && json.Valid(trimmed) {
		arguments = append(json.RawMessage(nil), trimmed...)
	}
	return arguments, toolSummary(tool.Content)
}

func toolSummary(content []domain.ToolContent) string {
	for _, item := range content {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		var failure struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &failure) == nil && failure.Status == "error" {
			return truncateRunes(strings.TrimSpace(failure.Message), maxToolSummary)
		}
		line, _, _ := strings.Cut(text, "\n")
		return truncateRunes(strings.TrimSpace(strings.TrimLeft(line, "# ")), maxToolSummary)
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func eventCursor(event domain.Event) string {
	digest := sha256.Sum256([]byte(event.TurnID + "\x00" + event.ID))
	return "tl_" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func deltaEnvelope(jobID string, delta domain.Delta) EnvelopeDTO {
	event := delta.Event(jobID)
	return EnvelopeDTO{
		Type: "delta", Cursor: eventCursor(event),
		Delta: &DeltaDTO{
			ID: event.ID, Kind: event.Kind, JobID: event.JobID, Role: string(event.Role),
			ProviderTurnID: event.TurnID, ProviderItemID: event.ItemID,
			LogicalSection: event.LogicalSection, Text: event.Text,
		},
	}
}

func contextEnvelope(update domain.ContextUpdate) EnvelopeDTO {
	return EnvelopeDTO{Type: "context", Context: &ContextDTO{
		Role: string(update.Role), ProviderTurnID: update.TurnID,
		UsedTokens: update.UsedTokens, ContextWindow: update.ContextWindow,
	}}
}

func validateCursor(values []string) error {
	if len(values) == 0 {
		return nil
	}
	if len(values) != 1 {
		return ErrInvalidCursor
	}
	if values[0] == "" {
		return nil
	}
	if len(values[0]) > 128 || !strings.HasPrefix(values[0], "tl_") {
		return ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(values[0], "tl_"))
	if err != nil || len(raw) == 0 {
		return ErrInvalidCursor
	}
	return nil
}
