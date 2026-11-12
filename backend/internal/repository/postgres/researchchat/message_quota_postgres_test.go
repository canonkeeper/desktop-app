package researchchat

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	storedb "canonkeeper/backend/internal/store/db"
	quotastore "canonkeeper/backend/internal/store/quota"

	"github.com/jackc/pgx/v5"
)

// spent is the units of a daily limit the writer used, over all days.
func spent(t *testing.T, db *storedb.DB, column string) int64 {
	t.Helper()
	var used int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(`+column+`), 0) FROM writer_usage_days WHERE user_id = $1::uuid`, submitTurnActorID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

// specs/2026-11-09-quota-accounting.md Q2.1, Q2.2: a correction of a running turn is a question of the day, as the
// turn's own question is; a correction the turn did not take gives it back, a repeat of an accepted key takes none, a
// key let go takes one again; over the limit the correction is refused with the question's code and leaves no key.
func TestACorrectionSpendsAQuestionOfTheDayPostgres(t *testing.T) {
	fixture := newSteerFixture(t, "ck_rch_steer_quota")
	ctx := context.Background()
	if err := fixture.db.WithTx(ctx, func(tx pgx.Tx) error {
		value := int64(3)
		_, err := quotastore.SetOverridesInTx(ctx, tx, submitTurnActorID, []quota.OverrideChange{{Key: quota.KeyChatTurnsPerDay, Value: &value}}, fixture.now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fixture.run(t)
	questions := func() int64 { return spent(t, fixture.db, "chat_turns") }
	if used := questions(); used != 1 {
		t.Fatalf("questions after the turn = %d, want 1", used)
	}

	refused, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil {
		t.Fatal(err)
	}
	if used := questions(); used != 2 {
		t.Fatalf("questions while the correction goes = %d, want 2", used)
	}
	if err := fixture.repository.FinishSteer(ctx, refused, false); err != nil {
		t.Fatal(err)
	}
	if used := questions(); used != 1 {
		t.Fatalf("questions after a correction the turn did not take = %d, want 1", used)
	}

	accepted, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.FinishSteer(ctx, accepted, true); err != nil {
		t.Fatal(err)
	}
	if replay, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1")); err != nil || !replay.Replayed {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	if used := questions(); used != 2 {
		t.Fatalf("questions after an accepted correction and its replay = %d, want 2", used)
	}

	second, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.FinishSteer(ctx, second, true); err != nil {
		t.Fatal(err)
	}
	exceeded := wantQuota(t, func() error { _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-3")); return err }(), quota.KeyChatTurnsPerDay)
	if exceeded.Limit != 3 || exceeded.ResetsAt == nil {
		t.Fatalf("refusal = %+v", exceeded)
	}
	if used := questions(); used != 3 {
		t.Fatalf("questions after the refusal = %d, want 3", used)
	}
	var records int64
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_records WHERE idempotency_key_hash = 'key-3'`).Scan(&records); err != nil || records != 0 {
		t.Fatalf("records of the refused correction = %d, %v", records, err)
	}
}

// Q1.4, Q2.4: a turn that failed through no fault of the author — the model or the worker — gives its unit back to the
// day it was asked on; a turn the author stopped keeps it. A turn of a helper: TestAFailedTurnOfAHelperGivesBackItsTurnPostgres.
func TestATurnThatFailedGivesItsUnitBackPostgres(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_failed_turn_quota")
	ctx := context.Background()
	chats.setLimits(map[quota.Key]int64{quota.KeyActiveTurns: 5})
	session := chats.chat()
	questions := func() int64 { return spent(t, chats.db, "chat_turns") }

	model := chats.startTurnForTest(chats.askOrFail(session, "failed-model", chats.now), "root-failed", "turn-failed-model")
	if err := chats.repository.FinishFailure(ctx, researchdomain.FinishFailure{
		ProjectID: model.ProjectID, ChatID: model.ChatID, TurnID: model.TurnID, JobID: model.JobID,
		ClaimIncarnation: 1, Code: researchdomain.CodeServerOverloaded,
	}); err != nil {
		t.Fatal(err)
	}
	if used := questions(); used != 0 {
		t.Fatalf("questions after a failure of the model = %d, want 0", used)
	}

	worker := chats.askOrFail(session, "failed-worker", chats.now.Add(time.Minute))
	run := chats.startTurnForTest(worker, "root-failed", "turn-failed-worker")
	worker.Status = jobs.StatusRunning
	worker.WorkerClaim = jobs.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: 1}
	worker.Error = &jobs.Error{Code: "worker.retry_exhausted", Message: "worker retries exhausted"}
	if err := chats.repository.FailClaim(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if used := questions(); used != 0 {
		t.Fatalf("questions after a failure of the worker = %d, want 0", used)
	}

	stopped := chats.askOrFail(session, "stopped", chats.now.Add(2*time.Minute))
	if _, updated, err := chats.repository.BeginTargetCancel(ctx, jobs.TargetCancelInput{
		ActorUserID: submitTurnActorID, Job: stopped, TargetReadable: true, UpdatedAt: chats.now.Add(3 * time.Minute),
	}); err != nil || updated.Status != jobs.StatusCanceled {
		t.Fatalf("cancel = %+v, %v", updated, err)
	}
	if used := questions(); used != 1 {
		t.Fatalf("questions after a stop by the author = %d, want 1", used)
	}
}
