package researchchat

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// startTurnForTest claims the turn's Job and binds it to the chat's root thread.
func (chats *quotaChats) startTurnForTest(job jobs.Job, rootThreadID, providerTurnID string) researchdomain.TurnRun {
	chats.t.Helper()
	ctx := context.Background()
	if err := chats.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE jobs SET status='running', progress_json='{"phase":"working"}',
			claimed_at=$2, started_at=$2, claim_incarnation=1, updated_at=$2 WHERE id=$1::uuid`, job.ID, time.Now().UTC().Add(-time.Minute))
		return err
	}); err != nil {
		chats.t.Fatal(err)
	}
	subject := job.Subject.(jobs.ResearchChatTurnSubject)
	run := researchdomain.TurnRun{
		ProjectID: submitTurnProjectID, ChatID: subject.ChatID, TurnID: subject.TurnID, JobID: job.ID,
		ActorUserID: submitTurnActorID, ClaimIncarnation: 1,
	}
	if err := chats.repository.BindRoot(ctx, run, rootThreadID); err != nil {
		chats.t.Fatal(err)
	}
	if err := chats.repository.BindTurn(ctx, run, rootThreadID, providerTurnID); err != nil {
		chats.t.Fatal(err)
	}
	return run
}

func (chats *quotaChats) chatUsage() (calls, input, cached, output, reasoning int64) {
	chats.t.Helper()
	if err := chats.db.Pool.QueryRow(context.Background(), `
		SELECT COALESCE(sum(calls), 0), COALESCE(sum(input_tokens), 0), COALESCE(sum(cached_input_tokens), 0),
		       COALESCE(sum(output_tokens), 0), COALESCE(sum(reasoning_output_tokens), 0)
		FROM model_usage_days
		WHERE user_id = $1::uuid AND project_id = $2::uuid AND source = 'chat' AND model = 'research-chat'
	`, submitTurnActorID, submitTurnProjectID).Scan(&calls, &input, &cached, &output, &reasoning); err != nil {
		chats.t.Fatal(err)
	}
	return
}

// R8.2: the first counted turn of a new thread spends its whole total, the next one the
// difference; the chat keeps the thread's total.
func TestFinishTurnCountsChatSpendByThreadTotals(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_chat_usage")
	session := chats.chat()
	const root = "root-usage"
	first := chats.startTurnForTest(func() jobs.Job { job, _ := chats.ask(session, "usage-1", chats.now); return job }(), root, "turn-usage-1")
	if err := chats.repository.FinishResponse(context.Background(), researchdomain.FinishResponse{
		ProjectID: first.ProjectID, ChatID: first.ChatID, TurnID: first.TurnID, JobID: first.JobID, ClaimIncarnation: 1,
		RootThreadID: root, ProviderTurnID: "turn-usage-1", Response: "Ответ.",
		Usage: agent.TurnUsage{Updates: 2, Total: agent.TokenCounts{Input: 1000, CachedInput: 800, Output: 100, ReasoningOutput: 20}},
	}); err != nil {
		t.Fatal(err)
	}
	if calls, input, cached, output, reasoning := chats.chatUsage(); calls != 2 || input != 1000 || cached != 800 || output != 100 || reasoning != 20 {
		t.Fatalf("after the first turn: %d calls, %d/%d/%d/%d tokens", calls, input, cached, output, reasoning)
	}
	second := chats.startTurnForTest(func() jobs.Job {
		job, err := chats.ask(session, "usage-2", chats.now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return job
	}(), root, "turn-usage-2")
	if err := chats.repository.FinishFailure(context.Background(), researchdomain.FinishFailure{
		ProjectID: second.ProjectID, ChatID: second.ChatID, TurnID: second.TurnID, JobID: second.JobID, ClaimIncarnation: 1,
		Code: "model.turn_failed",
		Usage: agent.TurnUsage{Updates: 1, Total: agent.TokenCounts{Input: 1600, CachedInput: 1300, Output: 150, ReasoningOutput: 30},
			StartTotal: agent.TokenCounts{Input: 999}},
	}); err != nil {
		t.Fatal(err)
	}
	if calls, input, cached, output, reasoning := chats.chatUsage(); calls != 3 || input != 1600 || cached != 1300 || output != 150 || reasoning != 30 {
		t.Fatalf("after the second turn: %d calls, %d/%d/%d/%d tokens", calls, input, cached, output, reasoning)
	}
	var counted int64
	if err := chats.db.Pool.QueryRow(context.Background(), `SELECT thread_input_tokens FROM chat_sessions WHERE id=$1::uuid`, session.ID).Scan(&counted); err != nil || counted != 1600 {
		t.Fatalf("chat thread total = %d, %v; want 1600", counted, err)
	}
}

// R7.2: a scan counts for the author of its chat turn, also in another writer's project.
func TestScanSpendCountsForTurnAuthor(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_scan_usage")
	proofB := chats.addWriter(fairWriterB)
	job := chats.askAs(fairWriterB, proofB, "scan-b", chats.now)
	if err := quotastore.RecordModelUsage(context.Background(), chats.db.Pool, quota.UsageRecord{
		Source: quota.UsageScan, ProjectID: submitTurnProjectID, JobID: job.ID, Model: "gpt-6-luna",
		Usage: quota.ModelUsage{Calls: 2, InputTokens: 5000},
	}, chats.now); err != nil {
		t.Fatal(err)
	}
	var writer string
	var calls int64
	if err := chats.db.Pool.QueryRow(context.Background(), `
		SELECT user_id::text, calls FROM model_usage_days WHERE source = 'scan' AND project_id = $1::uuid
	`, submitTurnProjectID).Scan(&writer, &calls); err != nil || writer != fairWriterB || calls != 2 {
		t.Fatalf("scan row = %s %d, %v; want writer B", writer, calls, err)
	}
}
