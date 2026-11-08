package codex

import (
	"context"
	"errors"
	"fmt"

	"canonkeeper/backend/internal/domain/agent"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

func (client *Client) startGoal(
	ctx context.Context,
	thread *sdk.Thread,
	request RunRequest,
	clientMessageID string,
	input string,
) (RunResult, error) {
	stream, err := client.sdk.Notifications(ctx)
	if err != nil {
		return RunResult{}, runStageError{code: "runtime.notifications_failed", err: err}
	}
	defer stream.Close()
	handle, err := client.beginTurn(ctx, thread, request, clientMessageID, input)
	if err != nil {
		return RunResult{}, err
	}
	if _, err := client.sdk.Threads.SetGoal(ctx, protocol.ThreadGoalSetParams{
		ThreadID: thread.ID(), Objective: protocol.Some(request.GoalObjective),
		Status: protocol.Some(protocol.ThreadGoalStatusActive),
	}); err != nil {
		return RunResult{}, runStageError{code: "runtime.goal_set_failed", err: err}
	}
	turn, goalStatus, err := client.collectGoal(
		ctx, stream, thread.ID(), handle.ID(), protocol.Turn{}, protocol.ThreadGoalStatusActive,
		request.BindTurn, request.OnContextCompaction,
	)
	if err != nil {
		return RunResult{}, err
	}
	return resultFromGoal(turn, goalStatus), nil
}

func (client *Client) collectGoal(
	ctx context.Context,
	stream *sdk.NotificationStream,
	threadID, initialTurnID string,
	lastCompleted protocol.Turn,
	goalStatus protocol.ThreadGoalStatus,
	bind func(context.Context, string, string) error,
	onContextCompaction func(context.Context, string, string, string) error,
) (protocol.Turn, protocol.ThreadGoalStatus, error) {
	currentTurnID := initialTurnID
	items := make(map[string][]protocol.ThreadItem)
	for {
		notification, ok := stream.Next(ctx)
		if !ok {
			streamErr := stream.Err()
			if streamErr == nil {
				streamErr = errors.New("goal notification stream closed")
			}
			return protocol.Turn{}, "", runStageError{code: "runtime.turn_stream_failed", err: streamErr}
		}
		switch payload := notification.Payload.(type) {
		case protocol.TurnStartedNotification:
			if payload.ThreadID != threadID || payload.Turn.ID == currentTurnID {
				continue
			}
			if err := client.bindReconciledTurn(ctx, threadID, payload.Turn, bind); err != nil {
				return protocol.Turn{}, "", err
			}
			client.dispatcher.publishTurn(threadID, payload.Turn.ID)
			currentTurnID = payload.Turn.ID
		case protocol.ItemCompletedNotification:
			if payload.ThreadID == threadID {
				if payload.Item.TypeValue == "contextCompaction" && onContextCompaction != nil {
					itemID, ok := payload.Item.ID.Value()
					if !ok || itemID == "" {
						return protocol.Turn{}, "", runStageError{code: "runtime.compaction_item_invalid", err: ErrInvalid}
					}
					if err := onContextCompaction(ctx, threadID, payload.TurnID, itemID); err != nil {
						return protocol.Turn{}, "", runStageError{code: "runtime.compaction_observation_failed", err: err}
					}
				}
				items[payload.TurnID] = append(items[payload.TurnID], payload.Item)
			}
		case protocol.ThreadGoalUpdatedNotification:
			if payload.ThreadID != threadID {
				continue
			}
			goalStatus = payload.Goal.Status
			if goalStatus != protocol.ThreadGoalStatusActive && currentTurnID == "" && lastCompleted.ID != "" {
				return lastCompleted, goalStatus, nil
			}
		case protocol.ThreadGoalClearedNotification:
			if payload.ThreadID == threadID {
				return protocol.Turn{}, "", runStageError{code: "runtime.goal_cleared", err: ErrConflict}
			}
		case protocol.TurnCompletedNotification:
			if payload.ThreadID != threadID {
				continue
			}
			turn := payload.Turn
			if len(turn.Items) == 0 {
				turn.Items = items[turn.ID]
			}
			delete(items, turn.ID)
			lastCompleted = turn
			if currentTurnID == turn.ID {
				currentTurnID = ""
			}
			if goalStatus != protocol.ThreadGoalStatusActive {
				return lastCompleted, goalStatus, nil
			}
		}
	}
}

func (client *Client) resumeGoal(ctx context.Context, thread *sdk.Thread, request RunRequest) (RunResult, error) {
	stream, err := client.sdk.Notifications(ctx)
	if err != nil {
		return RunResult{}, runStageError{code: "runtime.notifications_failed", err: err}
	}
	defer stream.Close()
	turns, err := client.listTurns(ctx, thread.ID())
	if err != nil {
		return RunResult{}, runStageError{code: "runtime.turn_list_failed", err: err}
	}
	if len(turns) == 0 {
		stream.Close()
		return client.startGoal(ctx, thread, request, request.TurnCorrelationID, request.InitialInput)
	}
	if err := replayContextCompactions(ctx, thread.ID(), turns, request.OnContextCompaction); err != nil {
		return RunResult{}, runStageError{code: "runtime.compaction_replay_failed", err: err}
	}
	if request.ExistingTurnID != "" {
		if _, found := turnByID(turns, request.ExistingTurnID); !found {
			return RunResult{}, fmt.Errorf("%w: persisted turn is absent", ErrConflict)
		}
	} else {
		_, matches := turnByClientID(turns, request.TurnCorrelationID)
		if matches > 1 {
			return RunResult{}, fmt.Errorf("%w: initial client message matched %d turns", ErrConflict, matches)
		}
		if matches == 0 {
			return RunResult{}, fmt.Errorf("%w: initial client message is absent", ErrConflict)
		}
	}
	active := activeTurns(turns)
	if len(active) > 1 {
		return RunResult{}, fmt.Errorf("%w: root has %d active turns", ErrConflict, len(active))
	}
	turn := turns[len(turns)-1]
	if len(active) == 1 {
		turn = active[0]
	}
	if err := client.bindReconciledTurn(ctx, thread.ID(), turn, request.BindTurn); err != nil {
		return RunResult{}, err
	}
	currentTurnID := ""
	lastCompleted := turn
	if turn.Status == protocol.TurnStatusInProgress {
		client.dispatcher.publishTurn(thread.ID(), turn.ID)
		currentTurnID = turn.ID
		lastCompleted = protocol.Turn{}
	}
	goalStatus, err := client.ensureGoal(
		ctx, thread.ID(), request.GoalObjective, request.ResumeUsageLimitedGoal, currentTurnID == "",
	)
	if err != nil {
		return RunResult{}, err
	}
	if goalStatus != protocol.ThreadGoalStatusActive && currentTurnID == "" {
		return resultFromGoal(lastCompleted, goalStatus), nil
	}
	observed, goalStatus, err := client.collectGoal(
		ctx, stream, thread.ID(), currentTurnID, lastCompleted, goalStatus,
		request.BindTurn, request.OnContextCompaction,
	)
	if err != nil {
		return RunResult{}, err
	}
	return resultFromGoal(observed, goalStatus), nil
}

func replayContextCompactions(
	ctx context.Context,
	threadID string,
	turns []protocol.Turn,
	observe func(context.Context, string, string, string) error,
) error {
	if observe == nil {
		return nil
	}
	for _, turn := range turns {
		for _, item := range turn.Items {
			if item.TypeValue != "contextCompaction" {
				continue
			}
			itemID, ok := item.ID.Value()
			if turn.ID == "" || !ok || itemID == "" {
				return ErrInvalid
			}
			if err := observe(ctx, threadID, turn.ID, itemID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (client *Client) ensureGoal(
	ctx context.Context,
	threadID, objective string,
	resumeUsageLimited, restartActive bool,
) (protocol.ThreadGoalStatus, error) {
	response, err := client.sdk.Threads.GetGoal(ctx, protocol.ThreadGoalGetParams{ThreadID: threadID})
	if err != nil {
		return "", runStageError{code: "runtime.goal_read_failed", err: err}
	}
	if goal, ok := response.Goal.Value(); ok {
		if goal.Objective != objective {
			return "", fmt.Errorf("%w: persisted goal objective differs", ErrConflict)
		}
		if (resumeUsageLimited && goal.Status == protocol.ThreadGoalStatusUsageLimited) ||
			(restartActive && goal.Status == protocol.ThreadGoalStatusActive) {
			updated, err := client.sdk.Threads.SetGoal(ctx, protocol.ThreadGoalSetParams{
				ThreadID: threadID, Status: protocol.Some(protocol.ThreadGoalStatusActive),
			})
			if err != nil {
				return "", runStageError{code: "runtime.goal_set_failed", err: err}
			}
			return updated.Goal.Status, nil
		}
		return goal.Status, nil
	}
	created, err := client.sdk.Threads.SetGoal(ctx, protocol.ThreadGoalSetParams{
		ThreadID: threadID, Objective: protocol.Some(objective), Status: protocol.Some(protocol.ThreadGoalStatusActive),
	})
	if err != nil {
		return "", runStageError{code: "runtime.goal_set_failed", err: err}
	}
	return created.Goal.Status, nil
}

func resultFromGoal(turn protocol.Turn, goalStatus protocol.ThreadGoalStatus) RunResult {
	result := resultFromTurn(turn)
	if goalStatus == protocol.ThreadGoalStatusUsageLimited {
		result.Failure = agent.RunFailureUsageLimitExceeded
	}
	return result
}
