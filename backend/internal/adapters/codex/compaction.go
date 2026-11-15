package codex

import (
	"context"

	"github.com/openai/codex/sdk/go/protocol"
)

// replayContextCompactions tells the observer of a run every compaction a thread already holds, before the run hands a
// call to its barrier handler.
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
