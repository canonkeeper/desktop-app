package researchchat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type RunRepository interface {
	LoadRun(context.Context, researchdomain.WorkerClaim) (researchdomain.TurnRun, error)
	BindRoot(context.Context, researchdomain.TurnRun, string) error
	BindTurn(context.Context, researchdomain.TurnRun, string, string) error
	FinishResponse(context.Context, researchdomain.FinishResponse) error
	FinishFailure(context.Context, researchdomain.FinishFailure) error
}

type AgentRunRequest struct {
	// Flow is the app-server the run goes to; its bundle id is the flow's name.
	Flow                 agent.Flow
	CorrelationID        string
	TurnCorrelationID    string
	ExistingRootThreadID string
	ExistingTurnID       string
	ContinueFailedTurn   bool
	BundleID             string
	SkillName            string
	InitialInput         string
	Tools                []agent.ToolSpec
	BindRoot             func(context.Context, string) error
	BindTurn             func(context.Context, string, string) error
	Handler              func(context.Context, agent.ToolCall) (agent.ToolResult, error)
}

type AgentRunner interface {
	Run(context.Context, AgentRunRequest) (agent.RunOutcome, error)
}

type AgentRunnerFunc func(context.Context, AgentRunRequest) (agent.RunOutcome, error)

func (function AgentRunnerFunc) Run(ctx context.Context, request AgentRunRequest) (agent.RunOutcome, error) {
	return function(ctx, request)
}

// Bundle is what a turn of one session kind gives the model (specs/2026-11-02-image-workshop.md IW4.1): its flow,
// the repository skill whose SKILL.md becomes the developer instructions, the tools and the handler of them all.
type Bundle struct {
	Flow      agent.Flow
	SkillName string
	Tools     []agent.ToolSpec
	Handler   func(context.Context, agent.ToolCall) (agent.ToolResult, error)
	// DrawAfter starts the drawing of a turn «Нарисовать» sent, once the model answered and before the turn ends: the
	// page reads the turn over with the drawing already started (specs/2026-11-05-drawing-one-screen.md D3.7). What it
	// could not start stays a button for the author; the turn does not fail for it.
	DrawAfter func(context.Context, researchdomain.TurnRun)
}

type RunTurn struct {
	repository RunRepository
	runner     AgentRunner
	bundles    map[string]Bundle
}

// NewRunTurn runs the turns of the chat sessions with the bundle of their kind: research — the chat, drawing — the
// helper of the drawing workshop (IW4.2).
func NewRunTurn(repository RunRepository, runner AgentRunner, bundles map[string]Bundle) *RunTurn {
	if repository == nil || runner == nil || len(bundles) == 0 {
		panic("research chat: run dependencies are required")
	}
	for kind, bundle := range bundles {
		if flow, known := agent.SessionFlow(kind); !known || flow != bundle.Flow || strings.TrimSpace(bundle.SkillName) == "" || bundle.Handler == nil {
			panic("research chat: a bundle for every session kind is required")
		}
	}
	return &RunTurn{repository: repository, runner: runner, bundles: bundles}
}

func (useCase *RunTurn) Execute(ctx context.Context, claim researchdomain.WorkerClaim) (researchdomain.TurnResult, error) {
	if ctx == nil || strings.TrimSpace(claim.ProjectID) == "" || strings.TrimSpace(claim.JobID) == "" || claim.ClaimIncarnation < 1 {
		return researchdomain.TurnResult{}, researchdomain.ErrValidation
	}
	run, err := useCase.repository.LoadRun(ctx, claim)
	if err != nil {
		return researchdomain.TurnResult{}, err
	}
	bundle, known := useCase.bundles[run.Kind]
	if !validTurnRun(run, claim) || !known {
		return researchdomain.TurnResult{}, researchdomain.ErrConflict
	}
	input, err := initialAssignment(run)
	if err != nil {
		return researchdomain.TurnResult{}, err
	}
	tools := append([]agent.ToolSpec(nil), bundle.Tools...)
	request := AgentRunRequest{
		Flow: bundle.Flow, CorrelationID: run.ChatID, TurnCorrelationID: run.TurnID,
		ExistingRootThreadID: run.RootThreadID, ExistingTurnID: run.ProviderTurnID,
		ContinueFailedTurn: false, BundleID: string(bundle.Flow),
		SkillName: bundle.SkillName, InitialInput: input,
		Tools: tools, Handler: bundle.Handler,
		BindRoot: func(ctx context.Context, rootThreadID string) error {
			return useCase.repository.BindRoot(ctx, run, rootThreadID)
		},
		BindTurn: func(ctx context.Context, rootThreadID, turnID string) error {
			return useCase.repository.BindTurn(ctx, run, rootThreadID, turnID)
		},
	}
	outcome, err := useCase.runner.Run(ctx, request)
	if err != nil {
		return researchdomain.TurnResult{}, err
	}
	if outcome.Status != agent.TurnCompleted || outcome.FinalResponse == "" {
		if err := useCase.repository.FinishFailure(ctx, researchdomain.FinishFailure{
			ProjectID: run.ProjectID, ChatID: run.ChatID, TurnID: run.TurnID, JobID: run.JobID,
			ClaimIncarnation: run.ClaimIncarnation, Code: failureCode(outcome), Usage: outcome.Usage,
		}); err != nil {
			return researchdomain.TurnResult{}, err
		}
		// Статус и ошибка хода от app-server доходят до строки job_diagnostic задания.
		detail := "status=" + string(outcome.Status)
		if outcome.Error != "" {
			detail += " " + outcome.Error
		}
		return researchdomain.TurnResult{}, fmt.Errorf("%w: %s", researchdomain.ErrNativeIncomplete, detail)
	}
	if strings.TrimSpace(outcome.RootThreadID) == "" || strings.TrimSpace(outcome.TurnID) == "" ||
		(run.RootThreadID != "" && outcome.RootThreadID != run.RootThreadID) ||
		(run.ProviderTurnID != "" && outcome.TurnID != run.ProviderTurnID) {
		return researchdomain.TurnResult{}, researchdomain.ErrNativeIncomplete
	}
	if run.DrawAfter && bundle.DrawAfter != nil {
		bundle.DrawAfter(ctx, run)
	}
	finish := researchdomain.FinishResponse{
		ProjectID: run.ProjectID, ChatID: run.ChatID, TurnID: run.TurnID, JobID: run.JobID,
		ClaimIncarnation: run.ClaimIncarnation, RootThreadID: outcome.RootThreadID,
		ProviderTurnID: outcome.TurnID, Response: outcome.FinalResponse, Usage: outcome.Usage,
	}
	if err := useCase.repository.FinishResponse(ctx, finish); err != nil {
		return researchdomain.TurnResult{}, err
	}
	return researchdomain.TurnResult{
		RootThreadID: outcome.RootThreadID, ProviderTurnID: outcome.TurnID, Response: outcome.FinalResponse,
	}, nil
}

// failureCode — шаг UC4.1 «код отказа хода»: прерванный ход — model.turn_interrupted, иначе код
// по классу отказа модели, без класса — model.turn_failed.
func failureCode(outcome agent.RunOutcome) string {
	if outcome.Status == agent.TurnInterrupted {
		return researchdomain.CodeTurnInterrupted
	}
	switch outcome.Failure {
	case agent.RunFailureUsageLimitExceeded:
		return researchdomain.CodeUsageLimitExceeded
	case agent.RunFailureServerOverloaded:
		return researchdomain.CodeServerOverloaded
	case agent.RunFailureConnectionFailed:
		return researchdomain.CodeConnectionFailed
	case agent.RunFailureContextWindowExceeded:
		return researchdomain.CodeContextWindowExceeded
	}
	return researchdomain.CodeTurnFailed
}

func validTurnRun(run researchdomain.TurnRun, claim researchdomain.WorkerClaim) bool {
	return run.ProjectID == claim.ProjectID && run.JobID == claim.JobID && run.ClaimIncarnation == claim.ClaimIncarnation &&
		strings.TrimSpace(run.ChatID) != "" && strings.TrimSpace(run.TurnID) != "" &&
		strings.TrimSpace(run.ActorUserID) != "" && strings.TrimSpace(run.Question) != "" &&
		(run.SourceView == "draft" || run.SourceView == "published") &&
		(run.Scope == researchdomain.ScopeProject && run.BookID == "" || run.Scope == researchdomain.ScopeBook && run.BookID != "")
}

func initialAssignment(run researchdomain.TurnRun) (string, error) {
	return encodeAssignment(assignmentInput{
		Text: run.Question, WorkshopLines: run.WorkshopLines, SourceView: run.SourceView, Scope: run.Scope, BookID: run.BookID,
		SelectedContext: run.SelectedContext, Images: run.Images, ImageTitles: run.ImageTitles, FirstPhoto: 1,
		Attachment: run.Attachment,
	})
}

// assignmentInput is what a native user input of a chat turn says: its question or, in a turn already
// running, a correction (specs/2026-10-30-chat-steering.md R1.4).
type assignmentInput struct {
	Text            string
	WorkshopLines   []string
	SourceView      string
	Scope           researchdomain.Scope
	BookID          string
	SelectedContext []researchdomain.ContextSelection
	Images          []int
	ImageTitles     map[int]string
	FirstPhoto      int
	Correction      bool
	// Attachment is the header of the chapter attached to a question (specs/2026-11-07-chat-attach-chapter.md CA3.1); a
	// correction carries none.
	Attachment *researchdomain.AssignmentAttachment
}

func encodeAssignment(input assignmentInput) (string, error) {
	bookRef := ""
	if input.Scope == researchdomain.ScopeBook {
		bookRef = "book"
	}
	assignment := researchdomain.Assignment{
		Question: input.Text, ProjectRef: "project", BookRef: bookRef,
		SourceView: input.SourceView, Scope: input.Scope,
		SelectedContext: append([]researchdomain.ContextSelection(nil), input.SelectedContext...),
		AttachedImages:  append([]int(nil), input.Images...),
		AttachedPhotos:  attachedPhotos(input.Images, input.ImageTitles, input.FirstPhoto),
		Correction:      input.Correction,
		WorkshopLines:   append([]string(nil), input.WorkshopLines...),
	}
	if input.Attachment != nil && !input.Correction {
		header := *input.Attachment
		assignment.Attachment = &header
	}
	if input.Correction && len(input.Images) > 0 {
		assignment.FirstPhoto = input.FirstPhoto
	}
	encoded, err := json.Marshal(assignment)
	return string(encoded), err
}

// attachedPhotos say what the anchors of the question mean (R15.19): «фото 1 — изображение 81
// (подпись)», one per attached image in order, counted from the anchor of the first.
func attachedPhotos(numbers []int, titles map[int]string, first int) []string {
	var photos []string
	for index, number := range numbers {
		line := fmt.Sprintf("%s — изображение %d", researchdomain.PhotoName(first+index), number)
		if title := strings.TrimSpace(titles[number]); title != "" {
			line += fmt.Sprintf(" (%s)", title)
		}
		photos = append(photos, line)
	}
	return photos
}
