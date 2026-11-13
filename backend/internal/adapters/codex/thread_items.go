package codex

import (
	"context"
	"encoding/json"
	"errors"

	agenttimeline "canonkeeper/backend/internal/domain/agenttimeline"
	"canonkeeper/backend/internal/observability"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

// app-server answers thread/timeline/list of a paginated thread whose history is not in its
// store yet (from thread/start until the first turn is written) with this JSON-RPC error.
const (
	methodNotFoundCode      = -32601
	unwrittenHistoryMessage = "thread/timeline/list is not supported yet"
)

func (client *Client) ThreadItems(ctx context.Context, threadID string) ([]agenttimeline.RawItem, error) {
	runtime, err := client.runtime(ctx)
	if err != nil {
		return nil, runStageError{code: "runtime.unavailable", err: err}
	}
	return runtime.threadItems(ctx, threadID)
}

func (client *Client) threadItems(ctx context.Context, threadID string) ([]agenttimeline.RawItem, error) {
	if client == nil || client.sdk == nil || ctx == nil || threadID == "" {
		return nil, ErrInvalid
	}
	threads, err := client.nativeTimelineThreads(ctx, threadID)
	if err != nil {
		return nil, err
	}
	items := make([]agenttimeline.RawItem, 0)
	for _, thread := range threads {
		threadItems, err := client.nativeTimelineItems(ctx, thread.ID, "", thread.Role)
		if thread.historyUnwritten(err) {
			// A new chat's root is bound before its first turn is written: nothing to show yet.
			continue
		}
		if err != nil {
			return nil, runStageError{code: "timeline.history_list_failed", err: err}
		}
		items = append(items, threadItems...)
	}
	return items, nil
}

type nativeTimelineThread struct {
	ID   string
	Role agenttimeline.Role
	// Paginated is set for the root only: the history mode app-server reports for it.
	Paginated bool
}

// historyUnwritten tells an empty, not yet written history of a paginated thread from a native
// failure; a legacy thread answers the same way for good, and that stays a failure.
func (thread nativeTimelineThread) historyUnwritten(err error) bool {
	var rpcErr *sdk.RPCError
	return thread.Paginated && errors.As(err, &rpcErr) &&
		rpcErr.Code == methodNotFoundCode && rpcErr.Message == unwrittenHistoryMessage
}

func (client *Client) nativeTimelineThreads(ctx context.Context, rootThreadID string) ([]nativeTimelineThread, error) {
	root, err := client.readThread(ctx, rootThreadID, false)
	if err != nil || root.ID != rootThreadID {
		return nil, runStageError{code: "timeline.thread_read_failed", err: errors.Join(ErrInvalid, err)}
	}
	mode, _ := root.HistoryMode.Value()
	threads := []nativeTimelineThread{{
		ID: rootThreadID, Role: agenttimeline.RoleMain, Paginated: mode == protocol.ThreadHistoryModePaginated,
	}}
	seen := map[string]struct{}{rootThreadID: {}}
	var cursor protocol.Optional[string]
	for {
		page, err := client.sdk.Threads.List(ctx, protocol.ThreadListParams{
			AncestorThreadID: protocol.Some(rootThreadID), Cursor: cursor, Limit: protocol.Some(uint32(100)),
			SourceKinds: protocol.Some(nativeSubagentSourceKinds()),
		})
		if err != nil {
			return nil, runStageError{code: "timeline.thread_list_failed", err: err}
		}
		for _, thread := range page.Data {
			if thread.ID == "" {
				return nil, ErrInvalid
			}
			if _, duplicate := seen[thread.ID]; duplicate {
				return nil, ErrInvalid
			}
			seen[thread.ID] = struct{}{}
			if role, ok := nativeTimelineRole(thread); ok {
				threads = append(threads, nativeTimelineThread{ID: thread.ID, Role: role})
			}
		}
		next, ok := page.NextCursor.Value()
		if !ok || next == "" {
			return threads, nil
		}
		cursor = protocol.Some(next)
	}
}

func nativeTimelineRole(thread protocol.Thread) (agenttimeline.Role, bool) {
	role, _ := thread.AgentRole.Value()
	return nativeTimelineAgentRole(role)
}

func nativeTimelineAgentRole(role string) (agenttimeline.Role, bool) {
	switch role {
	case "book_enrichment_review_completeness":
		return agenttimeline.RoleCompleteness, true
	case "book_enrichment_review_source_citations":
		return agenttimeline.RoleSourceEvidence, true
	case "book_enrichment_review_saga_history":
		return agenttimeline.RoleSagaHistory, true
	default:
		return "", false
	}
}

func (client *Client) nativeTimelineItems(ctx context.Context, threadID, selectedTurn string, role agenttimeline.Role) ([]agenttimeline.RawItem, error) {
	pages := make([][]protocol.ThreadTimelineEntry, 0)
	seenCursors := make(map[string]struct{})
	var cursor protocol.Optional[string]
	for {
		page, err := client.sdk.Threads.ListTimeline(ctx, protocol.ThreadTimelineListParams{
			ThreadID: threadID, Cursor: cursor, Limit: protocol.Some(uint32(100)),
		})
		if err != nil {
			return nil, err
		}
		pages = append(pages, page.Data)
		next, ok := page.NextCursor.Value()
		if !ok || next == "" {
			break
		}
		if _, duplicate := seenCursors[next]; duplicate {
			return nil, ErrInvalid
		}
		seenCursors[next] = struct{}{}
		cursor = protocol.Some(next)
	}
	entries := make([]protocol.ThreadTimelineEntry, 0)
	for index := len(pages) - 1; index >= 0; index-- {
		entries = append(entries, pages[index]...)
	}
	return nativeTimelineEntries(entries, selectedTurn, role)
}

func nativeTimelineEntries(entries []protocol.ThreadTimelineEntry, selectedTurn string, role agenttimeline.Role) ([]agenttimeline.RawItem, error) {
	statuses := make(map[string]agenttimeline.TurnStatus)
	matched := selectedTurn == ""
	hasPosition := false
	lastPosition := uint64(0)
	for _, entry := range entries {
		position, ok := entry.Position.Value()
		if !ok || hasPosition && position < lastPosition {
			return nil, ErrInvalid
		}
		hasPosition = true
		lastPosition = position
		switch entry.TypeValue {
		case "turnStarted":
			turnID, ok := entry.TurnID.Value()
			if !ok || turnID == "" {
				return nil, ErrInvalid
			}
			statuses[turnID] = agenttimeline.TurnStatusInProgress
			matched = matched || turnID == selectedTurn
		case "item":
			turnID, ok := entry.TurnID.Value()
			if !ok || turnID == "" {
				return nil, ErrInvalid
			}
			if _, exists := statuses[turnID]; !exists {
				return nil, ErrInvalid
			}
			matched = matched || turnID == selectedTurn
		case "turnCompleted":
			turnID, ok := entry.TurnID.Value()
			status, hasStatus := entry.Status.Value()
			mapped, validStatus := nativeTurnStatus(status)
			if !ok || turnID == "" || !hasStatus || !validStatus {
				return nil, ErrInvalid
			}
			statuses[turnID] = mapped
			matched = matched || turnID == selectedTurn
		case "realtime":
			continue
		default:
			return nil, ErrInvalid
		}
	}
	if !matched {
		return nil, ErrInvalid
	}
	items := make([]agenttimeline.RawItem, 0)
	itemCounts := make(map[string]int)
	for _, entry := range entries {
		switch entry.TypeValue {
		case "item":
			turnID, _ := entry.TurnID.Value()
			if selectedTurn != "" && turnID != selectedTurn {
				continue
			}
			var item *protocol.ThreadItem
			if err := json.Unmarshal(entry.Item, &item); err != nil || item == nil {
				return nil, ErrInvalid
			}
			raw, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			itemCounts[turnID]++
			items = append(items, agenttimeline.RawItem{Role: role, TurnID: turnID, TurnStatus: statuses[turnID], Item: raw})
		case "turnCompleted":
			turnID, _ := entry.TurnID.Value()
			if (selectedTurn == "" || turnID == selectedTurn) && itemCounts[turnID] == 0 {
				items = append(items, agenttimeline.RawItem{Role: role, TurnID: turnID, TurnStatus: statuses[turnID]})
			}
		}
	}
	return items, nil
}

func (client *Client) ThreadUpdates(ctx context.Context, threadID string) (<-chan agenttimeline.Update, error) {
	runtime, err := client.runtime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.threadUpdates(ctx, threadID)
}

func (client *Client) threadUpdates(ctx context.Context, threadID string) (<-chan agenttimeline.Update, error) {
	if client == nil || client.sdk == nil || ctx == nil || threadID == "" {
		return nil, ErrInvalid
	}
	stream, err := client.sdk.Notifications(ctx)
	if err != nil {
		return nil, err
	}
	threads, err := client.nativeTimelineThreads(ctx, threadID)
	if err != nil {
		return nil, err
	}
	threadRoles := make(map[string]agenttimeline.Role, len(threads))
	for _, thread := range threads {
		threadRoles[thread.ID] = thread.Role
	}
	updates := make(chan agenttimeline.Update, 64)
	go client.publishThreadUpdates(ctx, stream, threadID, threadRoles, updates)
	return updates, nil
}

func (client *Client) publishThreadUpdates(
	ctx context.Context, stream *sdk.NotificationStream, rootThreadID string,
	threadRoles map[string]agenttimeline.Role,
	updates chan<- agenttimeline.Update,
) {
	defer close(updates)
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked := observability.RecoveredPanic(recovered)
			client.logTimelinePanic(panicked)
			select {
			case updates <- agenttimeline.Update{Err: panicked}:
			case <-ctx.Done():
			}
		}
	}()
	for {
		notification, ok := stream.Next(ctx)
		if !ok {
			if ctx.Err() != nil {
				return
			}
			err := stream.Err()
			var overflow *sdk.OverflowError
			if errors.As(err, &overflow) {
				next, subscribeErr := client.sdk.Notifications(ctx)
				if subscribeErr == nil {
					stream = next
					select {
					case updates <- agenttimeline.Update{}:
					case <-ctx.Done():
						return
					}
					continue
				}
				err = subscribeErr
			}
			if err == nil {
				err = ErrInvalid
			}
			select {
			case updates <- agenttimeline.Update{Err: err}:
			case <-ctx.Done():
			}
			return
		}
		if started, ok := notification.Payload.(protocol.ThreadStartedNotification); ok {
			parentID, hasParent := started.Thread.ParentThreadID.Value()
			if _, parentWatched := threadRoles[parentID]; hasParent && parentWatched {
				if role, knownRole := nativeTimelineRole(started.Thread); knownRole {
					threadRoles[started.Thread.ID] = role
				}
			}
		}
		threadID := timelineNotificationThreadID(notification.Payload)
		role, watched := threadRoles[threadID]
		if !watched && threadID != "" {
			caller, err := resolveCaller(ctx, func(ctx context.Context, id string) (protocol.Thread, error) {
				return client.readThread(ctx, id, false)
			}, threadID)
			if err == nil && caller.RootThreadID == rootThreadID {
				role, watched = nativeTimelineAgentRole(caller.AgentRole)
				if watched {
					threadRoles[threadID] = role
				}
			}
		}
		if update, publish := timelineUpdate(notification.Payload, role); watched && publish {
			select {
			case updates <- update:
			case <-ctx.Done():
				return
			}
		}
	}
}

type timelinePanicLogEntry struct {
	Event string `json:"event"`
	Panic string `json:"panic"`
	Stack string `json:"stack"`
}

// logTimelinePanic пишет панику рассылки ленты со стеком: поток этой ленты закрывается с ошибкой,
// процесс продолжает работать (R4.2).
func (client *Client) logTimelinePanic(panicked *observability.PanicError) {
	if client.logger == nil {
		return
	}
	line, _ := json.Marshal(timelinePanicLogEntry{Event: "timeline_updates_panic", Panic: panicked.Value, Stack: panicked.Stack})
	client.logger.Print(string(line))
}

func timelineUpdate(payload any, role agenttimeline.Role) (agenttimeline.Update, bool) {
	switch value := payload.(type) {
	case protocol.AgentMessageDeltaNotification:
		if value.TurnID == "" || value.ItemID == "" || value.Delta == "" {
			return agenttimeline.Update{}, false
		}
		return agenttimeline.Update{Delta: &agenttimeline.Delta{
			Kind: agenttimeline.EventAssistant, Role: role, TurnID: value.TurnID,
			ItemID: value.ItemID, Text: value.Delta,
		}}, true
	case protocol.ReasoningSummaryTextDeltaNotification:
		if value.TurnID == "" || value.ItemID == "" || value.Delta == "" || value.SummaryIndex < 0 {
			return agenttimeline.Update{}, false
		}
		return agenttimeline.Update{Delta: &agenttimeline.Delta{
			Kind: agenttimeline.EventReasoning, Role: role, TurnID: value.TurnID,
			ItemID: value.ItemID, LogicalSection: int(value.SummaryIndex), Text: value.Delta,
		}}, true
	case protocol.ReasoningTextDeltaNotification:
		if value.TurnID == "" || value.ItemID == "" || value.Delta == "" || value.ContentIndex < 0 {
			return agenttimeline.Update{}, false
		}
		return agenttimeline.Update{Delta: &agenttimeline.Delta{
			Kind: agenttimeline.EventReasoning, Role: role, TurnID: value.TurnID,
			ItemID: value.ItemID, LogicalSection: int(value.ContentIndex), Text: value.Delta,
			RawReasoning: true,
		}}, true
	case protocol.ThreadTokenUsageUpdatedNotification:
		if value.TurnID == "" || value.TokenUsage.Last.TotalTokens < 0 {
			return agenttimeline.Update{}, false
		}
		usedTokens := value.TokenUsage.Last.TotalTokens
		contextUpdate := &agenttimeline.ContextUpdate{
			Role: role, TurnID: value.TurnID, UsedTokens: &usedTokens,
		}
		if window, ok := value.TokenUsage.ModelContextWindow.Value(); ok && window > 0 {
			contextUpdate.ContextWindow = &window
		}
		return agenttimeline.Update{Context: contextUpdate}, true
	case protocol.ReasoningSummaryPartAddedNotification, protocol.PlanDeltaNotification,
		protocol.ItemStartedNotification, protocol.ItemCompletedNotification,
		protocol.TurnStartedNotification, protocol.TurnCompletedNotification,
		protocol.TurnPlanUpdatedNotification:
		return agenttimeline.Update{}, true
	default:
		return agenttimeline.Update{}, false
	}
}

func timelineNotificationThreadID(payload any) string {
	switch value := payload.(type) {
	case protocol.ThreadStartedNotification:
		return value.Thread.ID
	case protocol.AgentMessageDeltaNotification:
		return value.ThreadID
	case protocol.PlanDeltaNotification:
		return value.ThreadID
	case protocol.ReasoningSummaryTextDeltaNotification:
		return value.ThreadID
	case protocol.ReasoningSummaryPartAddedNotification:
		return value.ThreadID
	case protocol.ReasoningTextDeltaNotification:
		return value.ThreadID
	case protocol.ItemStartedNotification:
		return value.ThreadID
	case protocol.ItemCompletedNotification:
		return value.ThreadID
	case protocol.TurnStartedNotification:
		return value.ThreadID
	case protocol.TurnCompletedNotification:
		return value.ThreadID
	case protocol.TurnPlanUpdatedNotification:
		return value.ThreadID
	case protocol.ThreadTokenUsageUpdatedNotification:
		return value.ThreadID
	default:
		return ""
	}
}

func nativeTurnStatus(status protocol.TurnStatus) (agenttimeline.TurnStatus, bool) {
	switch status {
	case protocol.TurnStatusCompleted:
		return agenttimeline.TurnStatusCompleted, true
	case protocol.TurnStatusInterrupted:
		return agenttimeline.TurnStatusInterrupted, true
	case protocol.TurnStatusFailed:
		return agenttimeline.TurnStatusFailed, true
	case protocol.TurnStatusInProgress:
		return agenttimeline.TurnStatusInProgress, true
	default:
		return "", false
	}
}
