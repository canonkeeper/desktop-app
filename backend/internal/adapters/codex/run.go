package codex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"canonkeeper/backend/internal/domain/agent"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

var ErrConflict = errors.New("Codex native identity conflict")

const bindCleanupTimeout = 30 * time.Second

type runStageError struct {
	code string
	err  error
}

func (failure runStageError) Error() string         { return failure.err.Error() }
func (failure runStageError) Unwrap() error         { return failure.err }
func (failure runStageError) SafeErrorCode() string { return failure.code }

type RunRequest struct {
	CorrelationID            string
	TurnCorrelationID        string
	ExistingRootThreadID     string
	ExistingTurnID           string
	ContinueFailedTurn       bool
	ResumeUsageLimitedGoal   bool
	BundleID                 string
	GoalObjective            string
	SkillName                string
	DeveloperInstructions    string
	InitialInput             string
	Tools                    []agent.ToolSpec
	BindRoot                 func(context.Context, string) error
	BindTurn                 func(context.Context, string, string) error
	OnContextCompaction      func(context.Context, string, string, string) error
	ContextCompactionBarrier func(agent.ToolCall) bool
	Handler                  func(context.Context, agent.ToolCall) (agent.ToolResult, error)
}

type RunResult = agent.RunOutcome

type RootSelector struct {
	CorrelationID string
	ThreadID      string
}

func (client *Client) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	runtime, err := client.runtime(ctx)
	if err != nil {
		return RunResult{}, runStageError{code: "runtime.client_start_failed", err: err}
	}
	result, err := runtime.run(ctx, request)
	if err != nil {
		if exitErr := runtime.runtimeExitError(); exitErr != nil {
			err = errors.Join(err, exitErr)
		}
	}
	return result, err
}

func (client *Client) run(ctx context.Context, request RunRequest) (RunResult, error) {
	if client == nil || client.sdk == nil || ctx == nil || request.CorrelationID == "" ||
		request.TurnCorrelationID == "" || request.BundleID == "" || request.InitialInput == "" ||
		(request.SkillName != "" && (client.repositoryHome == "" || request.DeveloperInstructions != "")) ||
		request.BindRoot == nil || request.BindTurn == nil || request.Handler == nil {
		return RunResult{}, runStageError{code: "runtime.run_request_invalid", err: ErrInvalid}
	}
	instructions, err := client.developerInstructions(request)
	if err != nil {
		return RunResult{}, err
	}
	request.DeveloperInstructions = instructions
	projectID, err := client.ensureProject(ctx, request.CorrelationID)
	if err != nil {
		return RunResult{}, runStageError{code: "runtime.project_create_failed", err: err}
	}
	thread, created, err := client.openRoot(ctx, projectID, request)
	if err != nil {
		return RunResult{}, err
	}
	rootThreadID := thread.ID()
	if bindErr := request.BindRoot(ctx, rootThreadID); bindErr != nil {
		bindErr = runStageError{code: "runtime.root_bind_failed", err: bindErr}
		if request.ExistingRootThreadID != "" || !errors.Is(bindErr, ErrConflict) {
			return RunResult{}, bindErr
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindCleanupTimeout)
		_, deleteErr := client.sdk.Threads.Delete(cleanupCtx, protocol.ThreadDeleteParams{ThreadID: rootThreadID})
		cancel()
		if isThreadNotFound(deleteErr, rootThreadID) {
			deleteErr = nil
		}
		return RunResult{}, errors.Join(bindErr, deleteErr)
	}
	handler := Handler(request.Handler)
	if request.OnContextCompaction != nil && request.ContextCompactionBarrier != nil {
		handler = func(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
			if request.ContextCompactionBarrier(call) {
				turns, err := client.listTurns(ctx, rootThreadID)
				if err != nil {
					return agent.ToolResult{}, runStageError{code: "runtime.compaction_barrier_list_failed", err: err}
				}
				if err := replayContextCompactions(ctx, rootThreadID, turns, request.OnContextCompaction); err != nil {
					return agent.ToolResult{}, runStageError{code: "runtime.compaction_barrier_failed", err: err}
				}
			}
			return request.Handler(ctx, call)
		}
	}
	client.dispatcher.attach(rootThreadID, handler)
	var result RunResult
	if created {
		result, err = client.startTurn(ctx, thread, request, request.TurnCorrelationID, request.InitialInput)
	} else {
		result, err = client.runTurn(ctx, thread, request)
	}
	result.RootThreadID = rootThreadID
	if err == nil && result.Status != agent.TurnInProgress {
		client.dispatcher.detach(rootThreadID)
	}
	return result, err
}

func (client *Client) ensureProject(ctx context.Context, correlationID string) (string, error) {
	response, err := client.sdk.Raw().ProjectCreate(ctx, protocol.ProjectCreateParams{
		IDempotencyKey: correlationID,
		Name:           correlationID,
		Roots:          []protocol.ProjectRoot{{Path: protocol.AbsolutePathBuf(client.cwd)}},
	})
	if err != nil {
		return "", err
	}
	if response.Project.ID == "" {
		return "", ErrInvalid
	}
	return response.Project.ID, nil
}

func (client *Client) openRoot(ctx context.Context, projectID string, request RunRequest) (*sdk.Thread, bool, error) {
	threadID := request.ExistingRootThreadID
	if threadID == "" {
		roots, err := client.listProjectRoots(ctx, projectID)
		if err != nil {
			return nil, false, runStageError{code: "runtime.thread_list_failed", err: err}
		}
		switch len(roots) {
		case 0:
			thread, err := client.sdk.Threads.Start(ctx, client.startOptions(projectID, request))
			if err != nil {
				return nil, true, runStageError{code: "runtime.thread_start_failed", err: err}
			}
			return thread, true, nil
		case 1:
			threadID = roots[0].ID
		default:
			return nil, false, runStageError{
				code: "runtime.root_correlation_conflict",
				err:  fmt.Errorf("%w: correlation %q resolved %d roots", ErrConflict, request.CorrelationID, len(roots)),
			}
		}
	}
	provider, err := client.configuredModelProvider(ctx)
	if err != nil {
		return nil, false, runStageError{code: "runtime.config_read_failed", err: err}
	}
	thread, err := client.sdk.Threads.Resume(ctx, client.resumeOptions(threadID, provider, request))
	if err != nil {
		return nil, false, runStageError{code: "runtime.thread_resume_failed", err: err}
	}
	metadata, err := client.readThread(ctx, thread.ID(), false)
	if err != nil {
		return nil, false, runStageError{code: "runtime.thread_read_failed", err: err}
	}
	boundProjectID, _ := metadata.ProjectID.Value()
	parentID, hasParent := metadata.ParentThreadID.Value()
	if metadata.ID != threadID || boundProjectID != projectID || hasParent && parentID != "" {
		return nil, false, fmt.Errorf("%w: persisted thread is not the exact project root", ErrConflict)
	}
	return thread, false, nil
}

func (client *Client) startOptions(projectID string, request RunRequest) sdk.ThreadStartOptions {
	no := false
	return sdk.ThreadStartOptions{
		ProjectID: projectID, CWD: client.cwd,
		RuntimeWorkspaceRoots: []protocol.AbsolutePathBuf{protocol.AbsolutePathBuf(client.cwd)},
		ApprovalPolicy:        protocol.AskForApprovalNever,
		Permissions:           "canonkeeper-runtime", Ephemeral: &no, HistoryMode: protocol.ThreadHistoryModePaginated,
		DeveloperInstructions: request.DeveloperInstructions, DynamicTools: mapToolSpecs(request.Tools),
	}
}

// configuredModelProvider reads the provider the app-server runs with once per process. A thread
// keeps the provider it started on, so a chat started before production moved to codex-lb (13.10)
// still named the removed zai-relay and could not continue until resume named the current one.
func (client *Client) configuredModelProvider(ctx context.Context) (string, error) {
	client.providerMu.Lock()
	defer client.providerMu.Unlock()
	if client.provider != "" {
		return client.provider, nil
	}
	response, err := client.sdk.Config.Read(ctx, protocol.ConfigReadParams{Cwd: protocol.Some(client.cwd)})
	if err != nil {
		return "", err
	}
	provider, _ := response.Config.ModelProvider.Value()
	if provider == "" {
		return "", fmt.Errorf("%w: app-server reports no model provider", ErrInvalid)
	}
	client.provider = provider
	return provider, nil
}

// resumeOptions loads a persisted root on the configured provider with the bundle's current tools:
// thread/resume.dynamicTools replaces the tools recorded at thread start for this load only
// (specs/2026-10-28-codex-0165.md UC3).
func (client *Client) resumeOptions(threadID, provider string, request RunRequest) sdk.ThreadResumeOptions {
	excludeTurns := true
	return sdk.ThreadResumeOptions{
		ThreadID: threadID, CWD: client.cwd, ModelProvider: provider,
		RuntimeWorkspaceRoots: []protocol.AbsolutePathBuf{protocol.AbsolutePathBuf(client.cwd)},
		ApprovalPolicy:        protocol.AskForApprovalNever,
		Permissions:           "canonkeeper-runtime", DeveloperInstructions: request.DeveloperInstructions,
		ExcludeTurns: &excludeTurns, DynamicTools: mapToolSpecs(request.Tools),
	}
}

func (client *Client) runTurn(ctx context.Context, thread *sdk.Thread, request RunRequest) (RunResult, error) {
	if request.GoalObjective != "" {
		return client.resumeGoal(ctx, thread, request)
	}
	notifications, err := client.sdk.Notifications(ctx)
	if err != nil {
		client.dispatcher.denyTurn(thread.ID(), err)
		return RunResult{}, runStageError{code: "runtime.notifications_failed", err: err}
	}
	defer notifications.Close()
	turns, err := client.listTurns(ctx, thread.ID())
	if err != nil {
		client.dispatcher.denyTurn(thread.ID(), err)
		return RunResult{}, runStageError{code: "runtime.turn_list_failed", err: err}
	}
	if request.ExistingTurnID != "" {
		turn, found := turnByID(turns, request.ExistingTurnID)
		if !found {
			client.dispatcher.denyTurn(thread.ID(), ErrConflict)
			return RunResult{}, fmt.Errorf("%w: persisted turn is absent", ErrConflict)
		}
		return client.reconcileTurn(ctx, thread, request, notifications, turn, turnHasClientID(turn, request.TurnCorrelationID))
	}
	initial, matches := turnByClientID(turns, request.TurnCorrelationID)
	if matches > 1 {
		client.dispatcher.denyTurn(thread.ID(), ErrConflict)
		return RunResult{}, fmt.Errorf("%w: initial client message matched %d turns", ErrConflict, matches)
	}
	if matches == 0 {
		return client.startTurn(ctx, thread, request, request.TurnCorrelationID, request.InitialInput)
	}
	return client.reconcileTurn(ctx, thread, request, notifications, initial, true)
}

func (client *Client) reconcileTurn(
	ctx context.Context,
	thread *sdk.Thread,
	request RunRequest,
	notifications *sdk.NotificationStream,
	turn protocol.Turn,
	allowContinuation bool,
) (RunResult, error) {
	if err := client.bindReconciledTurn(ctx, thread.ID(), turn, request.BindTurn); err != nil {
		return RunResult{}, err
	}
	if allowContinuation && request.ContinueFailedTurn &&
		(turn.Status == protocol.TurnStatusFailed || turn.Status == protocol.TurnStatusInterrupted) {
		continuationID := request.TurnCorrelationID + ":" + turn.ID
		turns, err := client.listTurns(ctx, thread.ID())
		if err != nil {
			client.dispatcher.denyTurn(thread.ID(), err)
			return RunResult{}, runStageError{code: "runtime.turn_list_failed", err: err}
		}
		continuation, matches := turnByClientID(turns, continuationID)
		if matches > 1 {
			client.dispatcher.denyTurn(thread.ID(), ErrConflict)
			return RunResult{}, fmt.Errorf("%w: continuation client message matched %d turns", ErrConflict, matches)
		}
		if matches == 0 {
			return client.startTurn(ctx, thread, request, continuationID, "Continue the assigned operation after the previous provider turn.")
		}
		if err := client.bindReconciledTurn(ctx, thread.ID(), continuation, request.BindTurn); err != nil {
			return RunResult{}, err
		}
		turn = continuation
	}
	client.dispatcher.publishTurn(thread.ID(), turn.ID)
	meter := &turnUsageMeter{threadID: thread.ID()}
	if turn.Status == protocol.TurnStatusInProgress {
		observed, err := waitForTurnWithUsage(ctx, notifications, thread.ID(), turn.ID, meter)
		if err != nil {
			return RunResult{}, runStageError{code: "runtime.turn_stream_failed", err: err}
		}
		turn = observed
	}
	result := resultFromTurn(turn)
	result.Usage = meter.usage
	return result, nil
}

func (client *Client) bindReconciledTurn(
	ctx context.Context,
	rootThreadID string,
	turn protocol.Turn,
	bind func(context.Context, string, string) error,
) error {
	client.dispatcher.prepareTurn(rootThreadID, turn.ID)
	err := bind(ctx, rootThreadID, turn.ID)
	if err == nil {
		return nil
	}
	client.dispatcher.denyTurn(rootThreadID, err)
	err = runStageError{code: "runtime.turn_bind_failed", err: err}
	if turn.Status != protocol.TurnStatusInProgress {
		client.dispatcher.detach(rootThreadID)
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindCleanupTimeout)
	defer cancel()
	_, interruptErr := client.sdk.Raw().TurnInterrupt(cleanupCtx, protocol.TurnInterruptParams{
		ThreadID: rootThreadID, TurnID: turn.ID,
	})
	if interruptErr == nil {
		client.dispatcher.detach(rootThreadID)
	}
	return errors.Join(err, interruptErr)
}

func (client *Client) startTurn(
	ctx context.Context,
	thread *sdk.Thread,
	request RunRequest,
	clientMessageID string,
	input string,
) (RunResult, error) {
	if request.GoalObjective != "" {
		return client.startGoal(ctx, thread, request, clientMessageID, input)
	}
	handle, err := client.beginTurn(ctx, thread, request, clientMessageID, input)
	if err != nil {
		return RunResult{}, err
	}
	stream, err := handle.Stream(ctx)
	if err != nil {
		client.dispatcher.denyTurn(thread.ID(), err)
		return RunResult{}, runStageError{code: "runtime.turn_stream_failed", err: err}
	}
	defer stream.Close()
	meter := &turnUsageMeter{threadID: thread.ID()}
	turn, err := collectTurn(ctx, stream, thread.ID(), handle.ID(), meter)
	if err != nil {
		return RunResult{}, runStageError{code: "runtime.turn_stream_failed", err: err}
	}
	result := resultFromTurn(turn)
	result.Usage = meter.usage
	return result, nil
}

func (client *Client) beginTurn(
	ctx context.Context,
	thread *sdk.Thread,
	request RunRequest,
	clientMessageID string,
	input string,
) (*sdk.TurnHandle, error) {
	handle, err := thread.Turn(ctx, sdk.Text(input), sdk.TurnOptions{ClientUserMessageID: clientMessageID})
	if err != nil {
		client.dispatcher.denyTurn(thread.ID(), err)
		return nil, runStageError{code: "runtime.turn_start_failed", err: err}
	}
	client.dispatcher.prepareTurn(thread.ID(), handle.ID())
	if bindErr := request.BindTurn(ctx, thread.ID(), handle.ID()); bindErr != nil {
		client.dispatcher.denyTurn(thread.ID(), bindErr)
		bindErr = runStageError{code: "runtime.turn_bind_failed", err: bindErr}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindCleanupTimeout)
		interruptErr := handle.Interrupt(cleanupCtx)
		cancel()
		if interruptErr == nil {
			client.dispatcher.detach(thread.ID())
		}
		return nil, errors.Join(bindErr, interruptErr)
	}
	client.dispatcher.publishTurn(thread.ID(), handle.ID())
	return handle, nil
}

func collectTurn(ctx context.Context, stream *sdk.NotificationStream, threadID, turnID string, meter *turnUsageMeter) (protocol.Turn, error) {
	items := make([]protocol.ThreadItem, 0)
	for {
		notification, ok := stream.Next(ctx)
		if !ok {
			return protocol.Turn{}, stream.Err()
		}
		meter.observe(notification.Payload)
		switch payload := notification.Payload.(type) {
		case protocol.ItemCompletedNotification:
			if payload.ThreadID == threadID && payload.TurnID == turnID {
				items = append(items, payload.Item)
			}
		case protocol.TurnCompletedNotification:
			if payload.ThreadID == threadID && payload.Turn.ID == turnID {
				if len(payload.Turn.Items) == 0 {
					payload.Turn.Items = items
				}
				return payload.Turn, nil
			}
		}
	}
}

func waitForTurn(ctx context.Context, stream *sdk.NotificationStream, threadID, turnID string) (protocol.Turn, error) {
	return collectTurn(ctx, stream, threadID, turnID, &turnUsageMeter{threadID: threadID})
}

func waitForTurnWithUsage(ctx context.Context, stream *sdk.NotificationStream, threadID, turnID string, meter *turnUsageMeter) (protocol.Turn, error) {
	return collectTurn(ctx, stream, threadID, turnID, meter)
}

func (client *Client) listProjectRoots(ctx context.Context, projectID string) ([]protocol.Thread, error) {
	var roots []protocol.Thread
	cursor := ""
	for {
		params := protocol.ThreadListParams{ProjectID: protocol.Some(projectID)}
		if cursor != "" {
			params.Cursor = protocol.Some(cursor)
		}
		page, err := client.sdk.Threads.List(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, thread := range page.Data {
			parentID, hasParent := thread.ParentThreadID.Value()
			if !hasParent || parentID == "" {
				roots = append(roots, thread)
			}
		}
		next, ok := page.NextCursor.Value()
		if !ok || next == "" {
			return roots, nil
		}
		cursor = next
	}
}

func (client *Client) listTurns(ctx context.Context, threadID string) ([]protocol.Turn, error) {
	turns, err := client.listTurnShells(ctx, threadID)
	if err != nil {
		return nil, err
	}
	for index := range turns {
		items, err := client.listTurnItems(ctx, threadID, turns[index].ID)
		if err != nil {
			return nil, err
		}
		turns[index].Items = items
		turns[index].ItemsView = protocol.SomeNonNull(protocol.TurnItemsViewFull)
	}
	return turns, nil
}

func (client *Client) listTurnItems(ctx context.Context, threadID, turnID string) ([]protocol.ThreadItem, error) {
	var items []protocol.ThreadItem
	cursor := ""
	for {
		params := protocol.ThreadItemsListParams{
			ThreadID: threadID, TurnID: protocol.Some(turnID),
			Limit: protocol.Some(turnPageSize), SortDirection: protocol.Some(protocol.SortDirectionAsc),
		}
		if cursor != "" {
			params.Cursor = protocol.Some(protocol.NewThreadItemsListCursorString(cursor))
		}
		page, err := client.sdk.Threads.ListItems(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, entry := range page.Data {
			if entry.TurnID != turnID {
				return nil, ErrInvalid
			}
			items = append(items, entry.Item)
		}
		next, ok := page.NextCursor.Value()
		if !ok || next == "" {
			return items, nil
		}
		cursor = next
	}
}

func (client *Client) readThread(ctx context.Context, threadID string, includeTurns bool) (protocol.Thread, error) {
	params := protocol.ThreadReadParams{ThreadID: threadID}
	if includeTurns {
		params.IncludeTurns = protocol.SomeNonNull(true)
	}
	response, err := client.sdk.Threads.Read(ctx, params)
	return response.Thread, err
}

func turnByID(turns []protocol.Turn, turnID string) (protocol.Turn, bool) {
	for _, turn := range turns {
		if turn.ID == turnID {
			return turn, true
		}
	}
	return protocol.Turn{}, false
}

func turnByClientID(turns []protocol.Turn, clientID string) (protocol.Turn, int) {
	var match protocol.Turn
	count := 0
	for _, turn := range turns {
		for _, item := range turn.Items {
			if item.TypeValue != "userMessage" {
				continue
			}
			value, ok := item.ClientID.Value()
			if ok && value == clientID {
				match = turn
				count++
			}
		}
	}
	return match, count
}

func turnHasClientID(turn protocol.Turn, clientID string) bool {
	_, count := turnByClientID([]protocol.Turn{turn}, clientID)
	return count == 1
}
