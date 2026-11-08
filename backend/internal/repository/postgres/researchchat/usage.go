package researchchat

import (
	"context"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/quota"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// chatUsageModel names the chat's model spend in model_usage_days.
const chatUsageModel = "research-chat"

// recordChatUsageInTx is the accounting step of UC8: in the transaction that finishes the turn
// the spend is the root thread's total minus the total the chat already counted (or, the first
// time, the total before the turn's first response), added for the author of the turn; the chat
// keeps the new total. A turn the adapter reported nothing for writes nothing (R8.3): its tokens
// fall into the next counted turn of the chat.
func recordChatUsageInTx(ctx context.Context, tx pgx.Tx, projectID, chatID, jobID string, usage agent.TurnUsage) error {
	if usage.Updates == 0 {
		return nil
	}
	var counted [4]*int64
	var author, kind string
	if err := tx.QueryRow(ctx, `
		SELECT chat.thread_input_tokens, chat.thread_cached_input_tokens, chat.thread_output_tokens,
		       chat.thread_reasoning_output_tokens, job.creator_user_id::text, chat.kind
		FROM chat_sessions AS chat
		JOIN jobs AS job ON job.project_id = chat.project_id AND job.id = $3::uuid
		WHERE chat.project_id = $1::uuid AND chat.id = $2::uuid AND job.creator_user_id IS NOT NULL
	`, projectID, chatID, jobID).Scan(&counted[0], &counted[1], &counted[2], &counted[3], &author, &kind); err != nil {
		return err
	}
	// The spend of each helper is a source of its own (specs/2026-11-02-image-workshop.md IW8.2,
	// specs/2026-11-02-writer-workshop.md W1.5).
	source, model := quota.UsageChat, chatUsageModel
	switch kind {
	case agent.SessionDrawing:
		source, model = quota.UsageDrawing, string(agent.FlowImageWorkshop)
	case agent.SessionDraft:
		source, model = quota.UsageDraftHelper, string(agent.FlowWriterWorkshop)
	}
	base := usage.StartTotal
	if counted[0] != nil && counted[1] != nil && counted[2] != nil && counted[3] != nil {
		base = agent.TokenCounts{Input: *counted[0], CachedInput: *counted[1], Output: *counted[2], ReasoningOutput: *counted[3]}
	}
	total := usage.Total
	if _, err := tx.Exec(ctx, `
		UPDATE chat_sessions
		SET thread_input_tokens = $3, thread_cached_input_tokens = $4,
		    thread_output_tokens = $5, thread_reasoning_output_tokens = $6
		WHERE project_id = $1::uuid AND id = $2::uuid
	`, projectID, chatID, total.Input, total.CachedInput, total.Output, total.ReasoningOutput); err != nil {
		return err
	}
	return quotastore.AddModelUsage(ctx, tx, author, quota.UsageRecord{
		Source: source, ProjectID: projectID, Model: model,
		Usage: quota.ModelUsage{
			Calls: usage.Updates, InputTokens: max(total.Input-base.Input, 0),
			CachedInputTokens:     max(total.CachedInput-base.CachedInput, 0),
			OutputTokens:          max(total.Output-base.Output, 0),
			ReasoningOutputTokens: max(total.ReasoningOutput-base.ReasoningOutput, 0),
		},
	}, time.Now())
}
