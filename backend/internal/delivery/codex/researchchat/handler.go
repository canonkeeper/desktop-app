package researchchat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/observability"
	service "canonkeeper/backend/internal/services/researchchat"
)

// Service is the saga memory tool set of the research agent.
type Service interface {
	Search(context.Context, service.ToolContext, service.SearchInput) (string, error)
	Read(context.Context, service.ToolContext, service.ReadInput) (string, error)
	Chapters(context.Context, service.ToolContext, service.ChaptersInput) (string, error)
	Glossary(context.Context, service.ToolContext, service.GlossaryInput) (string, error)
	Entity(context.Context, service.ToolContext, service.EntityInput) (string, error)
	Timeline(context.Context, service.ToolContext, service.TimelineInput) (string, error)
	Threads(context.Context, service.ToolContext, service.ThreadsInput) (string, error)
	Changes(context.Context, service.ToolContext, service.ChangesInput) (string, error)
	Scan(context.Context, service.ToolContext, service.ScanInput) (string, error)
	// Attachment reads the chapter attached to a question of the chat (specs/2026-11-07-chat-attach-chapter.md CA3.2).
	Attachment(context.Context, service.ToolContext, service.AttachmentInput) (string, error)
}

// Tool is a tool a build adds to the chat's set (specs/2026-11-07-desktop-app.md DA9.2): its spec in the saga
// namespace and its run on the raw arguments. Arguments that do not fit its schema are a *service.ArgumentError; its
// answer may carry an image the model sees.
type Tool struct {
	Spec agent.ToolSpec
	Run  func(context.Context, service.ToolContext, json.RawMessage) (service.ToolOutput, error)
}

// AuthorMemory is the memory of the author in the chat (specs/2026-11-01-writer-assistant-index.md, UC4).
type AuthorMemory interface {
	Remember(context.Context, service.ToolContext, service.RememberInput) (string, error)
	Memory(context.Context, service.ToolContext, service.MemoryInput) (string, error)
}

type Handler struct {
	service Service
	memory  AuthorMemory
	tools   map[string]Tool
	logger  *log.Logger
}

// NewHandler answers the chat's reading and memory tools and the tools of the build.
func NewHandler(service Service, memory AuthorMemory, tools ...Tool) *Handler {
	if service == nil || memory == nil {
		panic("research chat: Codex read and memory services are required")
	}
	handler := &Handler{service: service, memory: memory, tools: map[string]Tool{}, logger: log.Default()}
	for _, tool := range tools {
		if tool.Run == nil || tool.Spec.Namespace != toolNamespace || handler.tools[tool.Spec.Name].Run != nil {
			panic("research chat: a tool of the build needs a run and its own name in the saga namespace")
		}
		handler.tools[tool.Spec.Name] = tool
	}
	return handler
}

// skillName is the chat skill in codex-homes/research-chat/skills; the Codex adapter sends its
// SKILL.md as the developer instructions of the chat thread.
const skillName = "canonkeeper-research-chat"

// Bundle is the chat skill with the reading and memory tools every turn offers, the tools of the build (the site
// adds saga.image), the reading of the chapter attached to a question, and the handler of them all.
func Bundle(reads Service, memory AuthorMemory, extra ...Tool) service.Bundle {
	handler := NewHandler(reads, memory, extra...)
	tools := append(append([]agent.ToolSpec(nil), readToolSpecs...), memoryToolSpecs...)
	for _, tool := range extra {
		tools = append(tools, tool.Spec)
	}
	return service.Bundle{
		Flow: agent.FlowResearchChat, SkillName: skillName, Tools: append(tools, attachmentToolSpec), Handler: handler.Handle,
	}
}

const toolNamespace = researchdomain.ToolNamespace

// Handle decodes one tool call, runs it and returns the text the model reads. Correctable
// mistakes return a typed error the model can fix in the same run. A panic of the tool becomes
// a typed outcome and a tool_panic log line; the process and the turn go on.
func (handler *Handler) Handle(ctx context.Context, call agent.ToolCall) (result agent.ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			handler.logPanic(call, observability.RecoveredPanic(recovered))
			result, err = failureResult(toolFailure{
				Status: "error", Type: "tool_failed", Path: "$",
				Message:          "Инструмент дал сбой. Повторите вызов или продолжайте без него.",
				NextValidActions: []string{"retry_same_tool"},
			}), nil
		}
	}()
	return handler.handle(ctx, call)
}

type toolPanicLogEntry struct {
	Event        string `json:"event"`
	Tool         string `json:"tool"`
	RootThreadID string `json:"rootThreadId"`
	TurnID       string `json:"turnId"`
	CallID       string `json:"callId"`
	Panic        string `json:"panic"`
	Stack        string `json:"stack"`
}

// logPanic пишет имя инструмента и id вызова без аргументов: в них текст вопроса модели.
func (handler *Handler) logPanic(call agent.ToolCall, failure *observability.PanicError) {
	line, _ := json.Marshal(toolPanicLogEntry{
		Event: "tool_panic", Tool: call.Namespace + "." + call.Tool, RootThreadID: call.Caller.RootThreadID,
		TurnID: call.TurnID, CallID: call.CallID, Panic: failure.Value, Stack: failure.Stack,
	})
	handler.logger.Print(string(line))
}

func (handler *Handler) handle(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
	toolContext := service.ToolContext{Caller: call.Caller, TurnID: call.TurnID}
	if call.Namespace != toolNamespace {
		return invalidArguments("$.tool", "неизвестный инструмент"), nil
	}
	var output string
	var image []byte
	var err error
	switch call.Tool {
	case "search":
		var input searchInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме search"), nil
		}
		output, err = handler.service.Search(ctx, toolContext, service.SearchInput{
			Queries: input.Queries, Match: input.Match, Order: input.Order, From: input.From, To: input.To,
			Books: input.Books, Limit: input.Limit, Offset: input.Offset,
		})
	case "read":
		var input readInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме read"), nil
		}
		output, err = handler.service.Read(ctx, toolContext, service.ReadInput{
			Chapter: input.Chapter, From: input.From, To: input.To, Around: input.Around, Radius: input.Radius,
		})
	case "chapters":
		var input chaptersInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме chapters"), nil
		}
		output, err = handler.service.Chapters(ctx, toolContext, service.ChaptersInput{
			From: input.From, To: input.To, Books: input.Books, Title: input.Title, Detail: service.ChaptersDetail(input.Detail),
		})
	case "glossary":
		var input glossaryInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме glossary"), nil
		}
		output, err = handler.service.Glossary(ctx, toolContext, service.GlossaryInput{Query: input.Query, Types: input.Types, Limit: input.Limit})
	case "entity":
		var input entityInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме entity"), nil
		}
		output, err = handler.service.Entity(ctx, toolContext, service.EntityInput{Name: input.Name, At: input.At, Attributes: input.Attributes})
	case "timeline":
		var input timelineInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме timeline"), nil
		}
		output, err = handler.service.Timeline(ctx, toolContext, service.TimelineInput{From: input.From, To: input.To, Query: input.Query})
	case "threads":
		var input threadsInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме threads"), nil
		}
		output, err = handler.service.Threads(ctx, toolContext, service.ThreadsInput{Query: input.Query, Who: input.Who, Kind: input.Kind, Level: input.Level, Part: input.Part})
	case "changes":
		var input changesInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме changes"), nil
		}
		output, err = handler.service.Changes(ctx, toolContext, service.ChangesInput{Subject: input.Subject, From: input.From, To: input.To})
	case "scan":
		var input scanInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме scan"), nil
		}
		output, err = handler.service.Scan(ctx, toolContext, service.ScanInput{Task: input.Task, From: input.From, To: input.To, Books: input.Books})
	case "attachment":
		var input attachmentInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме attachment"), nil
		}
		output, err = handler.service.Attachment(ctx, toolContext, service.AttachmentInput{Attachment: input.Attachment, From: input.From, To: input.To})
	case "remember":
		var input rememberInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме remember"), nil
		}
		output, err = handler.memory.Remember(ctx, toolContext, service.RememberInput{Kind: input.Kind, Text: input.Text, Chapter: input.Chapter, Name: input.Name})
	case "memory":
		var input memoryInput
		if decode(call.Arguments, &input) != nil {
			return invalidArguments("$", "аргументы не соответствуют схеме memory"), nil
		}
		output, err = handler.memory.Memory(ctx, toolContext, service.MemoryInput{Query: input.Query, Kind: input.Kind})
	default:
		tool, ok := handler.tools[call.Tool]
		if !ok {
			return invalidArguments("$.tool", "неизвестный инструмент"), nil
		}
		var shown service.ToolOutput
		shown, err = tool.Run(ctx, toolContext, call.Arguments)
		output, image = shown.Text, shown.Image
	}
	if err != nil {
		return handler.failedResult(call, err), nil
	}
	if strings.TrimSpace(output) == "" {
		return handler.failedResult(call, errors.New("empty read result")), nil
	}
	result := agent.ToolResult{Success: true, Payload: json.RawMessage(output)}
	if len(image) > 0 {
		// The copy of a transparent item is a PNG (R8.11), every other one a JPEG.
		mime := "image/jpeg"
		if bytes.HasPrefix(image, []byte("\x89PNG")) {
			mime = "image/png"
		}
		result.ImageURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image)
	}
	return result, nil
}

type attachmentInput struct {
	Attachment int `json:"attachment"`
	From       int `json:"from"`
	To         int `json:"to"`
}

type rememberInput struct {
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Chapter string `json:"chapter"`
	Name    string `json:"name"`
}

type memoryInput struct {
	Query string `json:"query"`
	Kind  string `json:"kind"`
}

type searchInput struct {
	Queries []string `json:"queries"`
	Match   string   `json:"match"`
	Order   string   `json:"order"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Books   []int    `json:"books"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

type readInput struct {
	Chapter string `json:"chapter"`
	From    int    `json:"from"`
	To      int    `json:"to"`
	Around  int    `json:"around"`
	Radius  *int   `json:"radius"`
}

type chaptersInput struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Books  []int  `json:"books"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type glossaryInput struct {
	Query string   `json:"query"`
	Types []string `json:"types"`
	Limit int      `json:"limit"`
}

type entityInput struct {
	Name       string   `json:"name"`
	At         string   `json:"at"`
	Attributes []string `json:"attributes"`
}

type timelineInput struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Query string `json:"query"`
}

type threadsInput struct {
	Query string `json:"query"`
	Who   string `json:"who"`
	Kind  string `json:"kind"`
	Level string `json:"level"`
	Part  string `json:"part"`
}

type changesInput struct {
	Subject string `json:"subject"`
	From    string `json:"from"`
	To      string `json:"to"`
}

type scanInput struct {
	Task  string `json:"task"`
	From  string `json:"from"`
	To    string `json:"to"`
	Books []int  `json:"books"`
}

func decode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

type toolFailure struct {
	Status           string   `json:"status"`
	Type             string   `json:"type"`
	Path             string   `json:"path"`
	Message          string   `json:"message"`
	NextValidActions []string `json:"next_valid_actions"`
}

func invalidArguments(path, message string) agent.ToolResult {
	return failureResult(toolFailure{
		Status: "error", Type: "invalid_arguments", Path: path, Message: message,
		NextValidActions: []string{"correct_arguments_and_retry"},
	})
}

type toolFailureLogEntry struct {
	Event        string `json:"event"`
	Tool         string `json:"tool"`
	RootThreadID string `json:"rootThreadId"`
	TurnID       string `json:"turnId"`
	CallID       string `json:"callId"`
	Error        string `json:"error"`
}

// failedResult maps a tool error to the outcome the model can act on. An unexpected error stays
// retryable for the model and is logged with the tool and call ids, never the arguments.
func (handler *Handler) failedResult(call agent.ToolCall, err error) agent.ToolResult {
	if argument, ok := service.IsArgumentError(err); ok {
		return invalidArguments(argument.Path, argument.Message)
	}
	if outcome, ok := service.AsToolOutcome(err); ok {
		return failureResult(toolFailure{
			Status: "error", Type: outcome.Type, Path: "$", Message: outcome.Message, NextValidActions: outcome.Next,
		})
	}
	switch {
	case errors.Is(err, researchdomain.ErrForbidden), errors.Is(err, researchdomain.ErrConflict):
		return failureResult(toolFailure{
			Status: "terminal", Type: "authority_lost", Path: "$",
			Message:          "Ход чата больше не может читать сагу. Остановитесь.",
			NextValidActions: []string{"stop"},
		})
	case errors.Is(err, researchdomain.ErrStaleSource), errors.Is(err, researchdomain.ErrNotFound), errors.Is(err, sagamemory.ErrTextChanged):
		return failureResult(toolFailure{
			Status: "error", Type: "source_changed", Path: "$",
			Message:          "Текст саги изменился во время чтения. Повторите тот же вызов.",
			NextValidActions: []string{"retry_same_tool"},
		})
	case errors.Is(err, researchdomain.ErrValidation):
		return invalidArguments("$", "аргументы не прошли проверку")
	case errors.Is(err, sagamemory.ErrSearchNotReady):
		// The search rows of the saga are not built yet (after a restore, until they are rebuilt):
		// no tool can read the saga, so the agent tells the author instead of guessing.
		return failureResult(toolFailure{
			Status: "terminal", Type: "saga_not_ready", Path: "$",
			Message:          "Текст саги сейчас недоступен для поиска и чтения: он ещё подготавливается. Скажите автору, что ответить по тексту можно будет позже, и остановитесь.",
			NextValidActions: []string{"stop"},
		})
	default:
		line, _ := json.Marshal(toolFailureLogEntry{
			Event: "tool_failed", Tool: call.Namespace + "." + call.Tool, RootThreadID: call.Caller.RootThreadID,
			TurnID: call.TurnID, CallID: call.CallID, Error: err.Error(),
		})
		handler.logger.Print(string(line))
		return failureResult(toolFailure{
			Status: "error", Type: "read_failed", Path: "$",
			Message:          "Чтение не удалось. Повторите вызов.",
			NextValidActions: []string{"retry_same_tool"},
		})
	}
}

func failureResult(failure toolFailure) agent.ToolResult {
	payload, _ := json.Marshal(failure)
	return agent.ToolResult{Success: false, Payload: payload}
}

// SagaToolSpecs are the specs of the saga tools named, for another bundle that reads the saga with the same tools
// and manifests (specs/2026-11-02-image-workshop.md IW4.3); the handler of the chat answers their calls.
func SagaToolSpecs(names ...string) []agent.ToolSpec {
	var specs []agent.ToolSpec
	for _, spec := range append(append([]agent.ToolSpec(nil), readToolSpecs...), memoryToolSpecs...) {
		for _, name := range names {
			if spec.Name == name {
				specs = append(specs, spec)
			}
		}
	}
	return specs
}
