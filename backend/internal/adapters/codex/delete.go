package codex

import (
	"context"
	"errors"
	"sort"

	sdk "github.com/openai/codex/sdk/go"
	"github.com/openai/codex/sdk/go/protocol"
)

func nativeSubagentSourceKinds() []protocol.ThreadSourceKind {
	return []protocol.ThreadSourceKind{
		protocol.ThreadSourceKindSubAgent,
		protocol.ThreadSourceKindSubAgentReview,
		protocol.ThreadSourceKindSubAgentCompact,
		protocol.ThreadSourceKindSubAgentThreadSpawn,
		protocol.ThreadSourceKindSubAgentOther,
	}
}

func (client *Client) DeleteTree(ctx context.Context, selector RootSelector) (string, error) {
	runtime, err := client.runtime(ctx)
	if err != nil {
		return "", err
	}
	return runtime.deleteTree(ctx, selector)
}

func (client *Client) deleteTree(ctx context.Context, selector RootSelector) (string, error) {
	rootThreadID, err := client.resolveRoot(ctx, selector)
	if err != nil || rootThreadID == "" {
		if err == nil && selector.ThreadID != "" {
			client.dispatcher.detach(selector.ThreadID)
		}
		return rootThreadID, err
	}
	if _, err := client.cancelRoot(ctx, RootSelector{ThreadID: rootThreadID}); err != nil {
		return "", err
	}
	descendants, err := client.listDescendants(ctx, rootThreadID)
	if err != nil {
		return "", err
	}
	depths := descendantDepths(rootThreadID, descendants)
	sort.Slice(descendants, func(left, right int) bool {
		if depths[descendants[left].ID] != depths[descendants[right].ID] {
			return depths[descendants[left].ID] > depths[descendants[right].ID]
		}
		return descendants[left].ID < descendants[right].ID
	})
	for _, thread := range append(descendants, protocol.Thread{ID: rootThreadID}) {
		if _, err := client.sdk.Threads.Delete(ctx, protocol.ThreadDeleteParams{ThreadID: thread.ID}); err != nil && !isThreadNotFound(err, thread.ID) {
			return "", err
		}
	}
	client.dispatcher.detach(rootThreadID)
	return rootThreadID, nil
}

func (client *Client) listDescendants(ctx context.Context, rootThreadID string) ([]protocol.Thread, error) {
	var descendants []protocol.Thread
	cursor := ""
	for {
		params := protocol.ThreadListParams{
			AncestorThreadID: protocol.Some(rootThreadID), Limit: protocol.Some(uint32(100)),
			SourceKinds: protocol.Some(nativeSubagentSourceKinds()),
		}
		if cursor != "" {
			params.Cursor = protocol.Some(cursor)
		}
		page, err := client.sdk.Threads.List(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, thread := range page.Data {
			if thread.ID != rootThreadID {
				descendants = append(descendants, thread)
			}
		}
		next, ok := page.NextCursor.Value()
		if !ok || next == "" {
			return descendants, nil
		}
		cursor = next
	}
}

func descendantDepths(rootThreadID string, descendants []protocol.Thread) map[string]int {
	parents := map[string]string{}
	for _, thread := range descendants {
		parents[thread.ID], _ = thread.ParentThreadID.Value()
	}
	depths := map[string]int{}
	for _, thread := range descendants {
		depth := 1
		for parent := parents[thread.ID]; parent != "" && parent != rootThreadID; parent = parents[parent] {
			depth++
		}
		depths[thread.ID] = depth
	}
	return depths
}

func isThreadNotFound(err error, threadID string) bool {
	var rpcErr *sdk.RPCError
	if threadID == "" || !errors.As(err, &rpcErr) {
		return false
	}
	return rpcErr.Message == "thread not found: "+threadID ||
		rpcErr.Message == "thread not loaded: "+threadID ||
		rpcErr.Message == "no rollout found for thread id "+threadID
}
