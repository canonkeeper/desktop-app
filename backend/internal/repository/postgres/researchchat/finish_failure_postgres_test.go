package researchchat

import (
	"context"
	"errors"
	"testing"
	"time"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/testutil"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// R4.1.3: FinishFailure records only a code of the closed turn failure set; any other code is a
// validation error that leaves the running turn job untouched.
func TestFinishFailureAcceptsOnlyTurnFailureCodesPostgres(t *testing.T) {
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_rch_finish_failure_codes")
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	repository := New(db)
	session, err := repository.CreateSession(ctx, service.CreateSessionCommand{
		ID: submitTurnChatID, ProjectID: submitTurnProjectID, ActorUserID: submitTurnActorID,
		SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject,
		Title: "Research", SessionWriteProof: proof, ScopeHash: "failure-session-scope",
		OperationID: "research_chat.session.create.v1", IdempotencyKeyHash: "failure-session-key",
		RequestHash: "failure-session-request", StatusCode: 201, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repository.SubmitTurn(ctx, service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Что известно о событии?",
		SessionWriteProof: proof, ScopeHash: "failure-turn-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: "failure-turn-key", RequestHash: "failure-turn-request", CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := claimQueuedResearchTurnForTest(t, db)
	if claim.JobID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claim.JobID, job.ID)
	}
	run := researchdomain.TurnRun{
		ProjectID: submitTurnProjectID, ChatID: session.ID, TurnID: job.Subject.(jobs.ResearchChatTurnSubject).TurnID,
		JobID: job.ID, ActorUserID: submitTurnActorID, ClaimIncarnation: claim.ClaimIncarnation,
	}
	if err := repository.BindRoot(ctx, run, "root-finish-failure"); err != nil {
		t.Fatal(err)
	}
	if err := repository.BindTurn(ctx, run, "root-finish-failure", "turn-finish-failure"); err != nil {
		t.Fatal(err)
	}
	readJob := func() jobs.Job {
		t.Helper()
		stored, err := jobstore.New(db).GetJob(ctx, jobs.GetJobStoreInput{
			ActorUserID: submitTurnActorID, ProjectID: submitTurnProjectID, JobID: job.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	failure := researchdomain.FinishFailure{
		ProjectID: run.ProjectID, ChatID: run.ChatID, TurnID: run.TurnID, JobID: run.JobID,
		ClaimIncarnation: run.ClaimIncarnation,
	}

	failure.Code = "model.unknown"
	if err := repository.FinishFailure(ctx, failure); !errors.Is(err, researchdomain.ErrValidation) {
		t.Fatalf("unknown code error = %v, want ErrValidation", err)
	}
	if stored := readJob(); stored.Status != jobs.StatusRunning || stored.Error != nil {
		t.Fatalf("job after rejected code = status %q error %+v, want running without error", stored.Status, stored.Error)
	}

	failure.Code = "model.server_overloaded"
	if err := repository.FinishFailure(ctx, failure); err != nil {
		t.Fatalf("server overloaded failure: %v", err)
	}
	stored := readJob()
	if stored.Status != jobs.StatusFailed || stored.Error == nil || stored.Error.Code != "model.server_overloaded" ||
		stored.Error.Message != researchdomain.TurnFailureCodes["model.server_overloaded"] {
		t.Fatalf("failed job = status %q error %+v", stored.Status, stored.Error)
	}
}

// claimQueuedResearchTurnForTest takes the queued turn with the worker claim function of the
// isolated fixture schema, as the worker does.
func claimQueuedResearchTurnForTest(t *testing.T, db *storedb.DB) jobs.WorkerClaim {
	t.Helper()
	workerSchema := pgx.Identifier{testutil.IsolatedPostgresSchema(t, db) + "_worker"}.Sanitize()
	var projectID, jobID pgtype.UUID
	var kind, state string
	var incarnation int64
	var exhausted pgtype.Text
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT project_id, work_id, kind, state, claim_incarnation, exhausted_status
		FROM `+workerSchema+`.claim_next_queued_job_for_worker($1, NULL)
	`, []string{string(jobs.KindResearchChatTurn)}).Scan(&projectID, &jobID, &kind, &state, &incarnation, &exhausted); err != nil {
		t.Fatal(err)
	}
	if state != string(jobs.StatusRunning) || exhausted.Valid {
		t.Fatalf("claimed turn state = %q exhausted %v", state, exhausted)
	}
	return jobs.WorkerClaim{ProjectID: projectID.String(), JobID: jobID.String(), ClaimIncarnation: incarnation}
}
