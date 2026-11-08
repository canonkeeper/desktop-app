package codex

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/codex/sdk/go/protocol"
)

const turnPageSize uint32 = 100

func (client *Client) CancelRoot(ctx context.Context, selector RootSelector) (string, error) {
	runtime, err := client.runtime(ctx)
	if err != nil {
		return "", err
	}
	return runtime.cancelRoot(ctx, selector)
}

func (client *Client) cancelRoot(ctx context.Context, selector RootSelector) (string, error) {
	rootThreadID, err := client.resolveRoot(ctx, selector)
	if err != nil || rootThreadID == "" {
		return rootThreadID, err
	}
	if err := client.clearGoal(ctx, rootThreadID); err != nil {
		return "", err
	}
	stream, err := client.sdk.Notifications(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	descendants, err := client.listDescendants(ctx, rootThreadID)
	if err != nil {
		return "", err
	}
	for _, thread := range append(descendants, protocol.Thread{ID: rootThreadID}) {
		turns, err := client.listTurnShells(ctx, thread.ID)
		if err != nil {
			return "", err
		}
		active := activeTurns(turns)
		if len(active) == 0 {
			continue
		}
		if len(active) > 1 {
			return "", fmt.Errorf("%w: thread %q has %d active turns", ErrConflict, thread.ID, len(active))
		}
		_, err = client.sdk.Raw().TurnInterrupt(ctx, protocol.TurnInterruptParams{
			ThreadID: thread.ID, TurnID: active[0].ID,
		})
		if err != nil {
			turns, readErr := client.listTurnShells(ctx, thread.ID)
			if readErr == nil && turnStopped(turns, active[0].ID) {
				continue
			}
			return "", errors.Join(err, readErr)
		}
		turns, err = client.listTurnShells(ctx, thread.ID)
		if err != nil {
			return "", err
		}
		turn, found := turnByID(turns, active[0].ID)
		if !found {
			return "", fmt.Errorf("%w: interrupted turn disappeared", ErrConflict)
		}
		if turn.Status == protocol.TurnStatusInProgress {
			if _, err := waitForTurn(ctx, stream, thread.ID, turn.ID); err != nil {
				return "", err
			}
		}
	}
	client.dispatcher.detach(rootThreadID)
	return rootThreadID, nil
}

func (client *Client) clearGoal(ctx context.Context, threadID string) error {
	response, err := client.sdk.Threads.GetGoal(ctx, protocol.ThreadGoalGetParams{ThreadID: threadID})
	if err != nil {
		return err
	}
	if _, ok := response.Goal.Value(); !ok {
		return nil
	}
	_, err = client.sdk.Threads.ClearGoal(ctx, protocol.ThreadGoalClearParams{ThreadID: threadID})
	return err
}

func (client *Client) listTurnShells(ctx context.Context, threadID string) ([]protocol.Turn, error) {
	var turns []protocol.Turn
	cursor := ""
	for {
		params := protocol.ThreadTurnsListParams{
			ThreadID: threadID, ItemsView: protocol.Some(protocol.TurnItemsViewNotLoaded),
			Limit: protocol.Some(turnPageSize), SortDirection: protocol.Some(protocol.SortDirectionAsc),
		}
		if cursor != "" {
			params.Cursor = protocol.Some(cursor)
		}
		page, err := client.sdk.Threads.ListTurns(ctx, params)
		if err != nil {
			return nil, err
		}
		turns = append(turns, page.Data...)
		next, ok := page.NextCursor.Value()
		if !ok || next == "" {
			return turns, nil
		}
		cursor = next
	}
}

func activeTurns(turns []protocol.Turn) []protocol.Turn {
	active := make([]protocol.Turn, 0, 1)
	for _, turn := range turns {
		if turn.Status == protocol.TurnStatusInProgress {
			active = append(active, turn)
		}
	}
	return active
}

func turnStopped(turns []protocol.Turn, turnID string) bool {
	turn, found := turnByID(turns, turnID)
	return found && turn.Status != protocol.TurnStatusInProgress && len(activeTurns(turns)) == 0
}

func (client *Client) resolveRoot(ctx context.Context, selector RootSelector) (string, error) {
	if client == nil || client.sdk == nil || ctx == nil {
		return "", ErrInvalid
	}
	if selector.ThreadID != "" {
		thread, err := client.readThread(ctx, selector.ThreadID, false)
		if isThreadNotFound(err, selector.ThreadID) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		parentID, hasParent := thread.ParentThreadID.Value()
		if thread.ID != selector.ThreadID || hasParent && parentID != "" {
			return "", fmt.Errorf("%w: selector is not a root", ErrConflict)
		}
		return thread.ID, nil
	}
	if selector.CorrelationID == "" {
		return "", ErrInvalid
	}
	projectID, err := client.ensureProject(ctx, selector.CorrelationID)
	if err != nil {
		return "", err
	}
	roots, err := client.listProjectRoots(ctx, projectID)
	if err != nil {
		return "", err
	}
	switch len(roots) {
	case 0:
		return "", nil
	case 1:
		return roots[0].ID, nil
	default:
		return "", fmt.Errorf("%w: correlation %q resolved %d roots", ErrConflict, selector.CorrelationID, len(roots))
	}
}
