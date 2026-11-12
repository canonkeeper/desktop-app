package researchchat

import (
	"context"
	"testing"
	"time"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/testutil"
)

// outcomes reads the writer's day row of specs/2026-10-28-owner-tools-and-counters.md UC1.
func (chats *quotaChats) outcomes(day string) (questions, answered, failed, interrupted int64) {
	chats.t.Helper()
	if err := chats.db.Pool.QueryRow(context.Background(), `
		SELECT chat_turns, turns_answered, turns_failed, turns_interrupted
		FROM writer_usage_days WHERE user_id = $1::uuid AND day = $2::date
	`, submitTurnActorID, day).Scan(&questions, &answered, &failed, &interrupted); err != nil {
		chats.t.Fatal(err)
	}
	return
}

func (chats *quotaChats) askOrFail(session researchdomain.Session, key string, at time.Time) jobs.Job {
	chats.t.Helper()
	job, err := chats.ask(session, key, at)
	if err != nil {
		chats.t.Fatal(err)
	}
	return job
}

// R1.1, R1.3: an answer, a refusal and a question stopped in the queue each count once in the
// writer's row of the day the question was asked; a terminal Job changed again does not count.
func TestTurnOutcomesCountInWritersDay(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_outcome_counters")
	ctx := context.Background()
	session := chats.chat()
	answered := chats.startTurnForTest(chats.askOrFail(session, "outcome-1", chats.now), "root-outcome", "turn-outcome-1")
	if err := chats.repository.FinishResponse(ctx, researchdomain.FinishResponse{
		ProjectID: answered.ProjectID, ChatID: answered.ChatID, TurnID: answered.TurnID, JobID: answered.JobID,
		ClaimIncarnation: 1, RootThreadID: "root-outcome", ProviderTurnID: "turn-outcome-1", Response: "Ответ.",
	}); err != nil {
		t.Fatal(err)
	}
	failed := chats.startTurnForTest(chats.askOrFail(session, "outcome-2", chats.now.Add(time.Minute)), "root-outcome", "turn-outcome-2")
	if err := chats.repository.FinishFailure(ctx, researchdomain.FinishFailure{
		ProjectID: failed.ProjectID, ChatID: failed.ChatID, TurnID: failed.TurnID, JobID: failed.JobID,
		ClaimIncarnation: 1, Code: researchdomain.CodeTurnFailed,
	}); err != nil {
		t.Fatal(err)
	}
	queued := chats.askOrFail(session, "outcome-3", chats.now.Add(2*time.Minute))
	if _, updated, err := chats.repository.BeginTargetCancel(ctx, jobs.TargetCancelInput{
		ActorUserID: submitTurnActorID, Job: queued, TargetReadable: true, UpdatedAt: chats.now.Add(3 * time.Minute),
	}); err != nil || updated.Status != jobs.StatusCanceled {
		t.Fatalf("cancel queued turn = %+v, %v", updated, err)
	}
	// The failed question was given back (specs/2026-11-09-quota-accounting.md Q2.4): the day spent two of three.
	if questions, answeredCount, failedCount, interrupted := chats.outcomes("2026-10-28"); questions != 2 ||
		answeredCount != 1 || failedCount != 1 || interrupted != 1 {
		t.Fatalf("day row = %d questions, %d answered, %d failed, %d interrupted", questions, answeredCount, failedCount, interrupted)
	}
	// Source deletion turns a finished Job into expired; that is not a second outcome.
	if _, err := chats.db.Pool.Exec(ctx, `UPDATE jobs SET status = 'expired' WHERE id = $1::uuid`, answered.JobID); err != nil {
		t.Fatal(err)
	}
	if _, answeredCount, _, interrupted := chats.outcomes("2026-10-28"); answeredCount != 1 || interrupted != 1 {
		t.Fatalf("after a terminal change: %d answered, %d interrupted", answeredCount, interrupted)
	}
}

// chatsBefore0059 is a database at migration 58 whose writer asked twice today: one answer and
// one question in the queue; the day counter holds only one of them, as when the first was asked
// before 0056 counted questions.
func chatsBefore0059(t *testing.T, name string) *quotaChats {
	t.Helper()
	ctx := context.Background()
	db := testutil.OpenIsolatedPostgresTestDB(t, name, 58)
	// The questions are asked with today's quota and chat code, which reads the limit column of
	// 0061, the platform defaults of 0068, the uploads limit of 0069, the writing limit of 0087
	// and the kind of a session and the helper's limit and counter of 0089 and 0091, and the limit of rewrites of 0108;
	// they exist only while the questions are asked, so migration 0059 meets the schema of 58.
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE writer_quota_overrides ADD COLUMN images_per_day integer, ADD COLUMN image_uploads_per_day integer, ADD COLUMN chapter_drafts_per_day integer,
		  ADD COLUMN drawing_turns_per_day integer, ADD COLUMN draft_turns_per_day integer, ADD COLUMN draft_rewrites_per_day integer;
		ALTER TABLE writer_usage_days ADD COLUMN drawing_turns integer NOT NULL DEFAULT 0, ADD COLUMN draft_turns integer NOT NULL DEFAULT 0;
		ALTER TABLE chat_sessions ADD COLUMN kind text NOT NULL DEFAULT 'research';
		CREATE TABLE platform_quota_defaults (
		  singleton boolean PRIMARY KEY DEFAULT true, chat_turns_per_day integer, active_turns integer,
		  imports_per_day integer, projects integer, project_books integer, project_chapters integer,
		  project_characters bigint, account_books integer, account_chapters integer, account_characters bigint,
		  images_per_day integer, image_uploads_per_day integer, chapter_drafts_per_day integer, drawing_turns_per_day integer, draft_turns_per_day integer, draft_rewrites_per_day integer,
		  updated_at timestamptz, updated_by uuid
		)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	chats := &quotaChats{t: t, db: db, repository: New(db), proof: seedSubmitTurnCanonicalBaseline(t, db, now), now: now}
	session := chats.chat()
	answered := chats.startTurnForTest(chats.askOrFail(session, "migration-1", now), "root-migration", "turn-migration-1")
	if err := chats.repository.FinishResponse(ctx, researchdomain.FinishResponse{
		ProjectID: answered.ProjectID, ChatID: answered.ChatID, TurnID: answered.TurnID, JobID: answered.JobID,
		ClaimIncarnation: 1, RootThreadID: "root-migration", ProviderTurnID: "turn-migration-1", Response: "Ответ.",
	}); err != nil {
		t.Fatal(err)
	}
	chats.askOrFail(session, "migration-2", now.Add(time.Minute))
	if _, err := db.Pool.Exec(ctx, `UPDATE writer_usage_days SET chat_turns = 1 WHERE user_id = $1`, submitTurnActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE writer_quota_overrides DROP COLUMN images_per_day, DROP COLUMN image_uploads_per_day, DROP COLUMN chapter_drafts_per_day,
		  DROP COLUMN drawing_turns_per_day, DROP COLUMN draft_turns_per_day, DROP COLUMN draft_rewrites_per_day;
		ALTER TABLE writer_usage_days DROP COLUMN drawing_turns, DROP COLUMN draft_turns;
		ALTER TABLE chat_sessions DROP COLUMN kind;
		DROP TABLE platform_quota_defaults`); err != nil {
		t.Fatal(err)
	}
	return chats
}

// R1.5, R2.3: migration 0059 carries the live Jobs over — a day keeps the larger of its counter
// and its Jobs, outcomes come from finished Jobs — and moves the counters of an account deleted
// before it to a pseudonym.
func TestMigration0059CarriesHistoryOfLiveJobs(t *testing.T) {
	ctx := context.Background()
	chats := chatsBefore0059(t, "ck_rch_migration_0059")
	const deleted = "92000000-0000-4000-8000-0000000000dd"
	for _, statement := range []string{
		`INSERT INTO users (id, email, display_name, password_hash, disabled_at, deleted_at)
		 VALUES ('` + deleted + `', 'deleted-` + deleted + `@deleted.invalid', 'Удалённый пользователь', 'hash', now(), now())`,
		`INSERT INTO writer_usage_days (user_id, day, chat_turns) VALUES ('` + deleted + `', '2026-10-20', 7)`,
	} {
		if _, err := chats.db.Pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := testutil.ApplyNextMigration(t, chats.db, 58); err != nil {
		t.Fatal(err)
	}
	if questions, answeredCount, failed, interrupted := chats.outcomes("2026-10-28"); questions != 2 ||
		answeredCount != 1 || failed != 0 || interrupted != 0 {
		t.Fatalf("carried day = %d questions, %d answered, %d failed, %d interrupted", questions, answeredCount, failed, interrupted)
	}
	if left := chats.count(`SELECT count(*) FROM writer_usage_days WHERE user_id = $1::uuid`, deleted); left != 0 {
		t.Fatalf("counters with the deleted id = %d", left)
	}
	if moved := chats.count(`
		SELECT count(*) FROM writer_usage_days AS usage
		WHERE usage.day = '2026-10-20' AND usage.chat_turns = 7
		  AND NOT EXISTS (SELECT 1 FROM users WHERE users.id = usage.user_id)`); moved != 1 {
		t.Fatalf("pseudonymous rows = %d", moved)
	}
}
