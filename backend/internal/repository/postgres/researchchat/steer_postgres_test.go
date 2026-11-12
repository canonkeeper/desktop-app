package researchchat

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/testutil"
)

type steerFixture struct {
	db         *storedb.DB
	repository *Repository
	proof      auth.SessionWriteProof
	session    researchdomain.Session
	job        jobs.Job
	turnID     string
	now        time.Time
}

func newSteerFixture(t *testing.T, name string) steerFixture {
	t.Helper()
	db := testutil.OpenIsolatedPostgresTestDB(t, name)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	proof := seedSubmitTurnCanonicalBaseline(t, db, now)
	repository := New(db)
	session, err := repository.CreateSession(ctx, service.CreateSessionCommand{
		ID: submitTurnChatID, ProjectID: submitTurnProjectID, ActorUserID: submitTurnActorID,
		SourceView: string(governance.SourceViewDraft), Scope: researchdomain.ScopeProject, Title: "Уточнения",
		SessionWriteProof: proof, ScopeHash: "create-scope", OperationID: "research_chat.session.create.v1",
		IdempotencyKeyHash: "create-key", RequestHash: "create-request", StatusCode: 201, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repository.SubmitTurn(ctx, service.SubmitTurnCommand{
		ActorUserID: submitTurnActorID, Session: session, Question: "Нарисуй обложку",
		SessionWriteProof: proof, ScopeHash: "turn-scope", OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: "turn-key", RequestHash: "turn-request", CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok {
		t.Fatalf("subject = %+v", job.Subject)
	}
	return steerFixture{db: db, repository: repository, proof: proof, session: session, job: job, turnID: subject.TurnID, now: now}
}

// run takes the queued turn in the worker and binds its native identity.
func (fixture steerFixture) run(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	claim := claimQueuedResearchTurnForTest(t, fixture.db)
	run := researchdomain.TurnRun{
		ProjectID: submitTurnProjectID, ChatID: fixture.session.ID, TurnID: fixture.turnID,
		JobID: fixture.job.ID, ActorUserID: submitTurnActorID, ClaimIncarnation: claim.ClaimIncarnation,
	}
	if err := fixture.repository.BindRoot(ctx, run, "root-steer"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.BindTurn(ctx, run, "root-steer", "native-steer"); err != nil {
		t.Fatal(err)
	}
}

func (fixture steerFixture) begin(key string) service.BeginSteerCommand {
	return service.BeginSteerCommand{
		ActorUserID: submitTurnActorID, ChatID: fixture.session.ID, TurnID: fixture.turnID, Session: fixture.session,
		SessionWriteProof: fixture.proof, ScopeHash: "steer-scope", OperationID: "research_chat.turn.steer.v1",
		IdempotencyKeyHash: key, RequestHash: "request-" + key, CreatedAt: fixture.now.Add(2 * time.Second),
	}
}

// specs/2026-10-30-chat-steering.md R1.3, R1.5, R1.6, R1.7: the target is the bound native turn;
// the key goes from sending to accepted or back; no Job and no turn is created (the question a correction spends —
// specs/2026-11-09-quota-accounting.md Q2.2 — is TestACorrectionSpendsAQuestionOfTheDayPostgres).
func TestBeginSteerTakesTheKeyOfABoundTurnPostgres(t *testing.T) {
	fixture := newSteerFixture(t, "ck_rch_steer_key")
	ctx := context.Background()
	fixture.run(t)
	counts := func() string {
		var jobCount, turnCount int
		if err := fixture.db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM jobs), (SELECT count(*) FROM chat_turns)`).Scan(&jobCount, &turnCount); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%d/%d", jobCount, turnCount)
	}
	before := counts()

	target, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil {
		t.Fatal(err)
	}
	if target.RootThreadID != "root-steer" || target.ProviderTurnID != "native-steer" || target.Replayed || target.RecordID == "" || target.JobID != fixture.job.ID {
		t.Fatalf("target = %+v", target)
	}
	// While the first request sends, a repeat of the key sends nothing.
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1")); !errors.Is(err, researchdomain.ErrSteerRetry) {
		t.Fatalf("repeat while sending = %v", err)
	}
	// A refusal lets go of the key: the same key tries again.
	if err := fixture.repository.FinishSteer(ctx, target, false); err != nil {
		t.Fatal(err)
	}
	target, err = fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil || target.Replayed {
		t.Fatalf("after a refusal = %+v, %v", target, err)
	}
	if err := fixture.repository.FinishSteer(ctx, target, true); err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-1"))
	if err != nil || !replay.Replayed {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	// Another body under an accepted key is a conflict.
	other := fixture.begin("key-1")
	other.RequestHash = "another-body"
	if _, err := fixture.repository.BeginSteer(ctx, other); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("another body = %v", err)
	}
	if after := counts(); after != before {
		t.Fatalf("jobs/turns = %s, want %s", after, before)
	}
}

// R1.2: a turn of another chat and a stranger are not found; a viewer is forbidden; an unverified
// address is refused as for a question.
func TestBeginSteerRefusesWhoMayNotWritePostgres(t *testing.T) {
	fixture := newSteerFixture(t, "ck_rch_steer_right")
	ctx := context.Background()
	fixture.run(t)

	foreignTurn := fixture.begin("key-foreign")
	foreignTurn.TurnID = "91000000-0000-4000-8000-0000000000ff"
	if _, err := fixture.repository.BeginSteer(ctx, foreignTurn); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("a turn of another chat = %v", err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_memberships SET role='viewer' WHERE project_id=$1`, submitTurnProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-viewer")); !errors.Is(err, researchdomain.ErrForbidden) {
		t.Fatalf("a viewer = %v", err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at=now() WHERE project_id=$1`, submitTurnProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-stranger")); !errors.Is(err, researchdomain.ErrNotFound) {
		t.Fatalf("a stranger = %v", err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE project_memberships SET removed_at=NULL, role='owner' WHERE project_id=$1`, submitTurnProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE users SET email_verified_at=NULL WHERE id=$1`, submitTurnActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-unverified")); !errors.Is(err, auth.ErrEmailNotVerified) {
		t.Fatalf("an unverified address = %v", err)
	}
}

// R2.1, R2.2: a queued turn has no native turn yet; a stopped or finished one has ended, but the
// accepted key still replays.
func TestBeginSteerReadsTheStateOfTheTurnPostgres(t *testing.T) {
	fixture := newSteerFixture(t, "ck_rch_steer_state")
	ctx := context.Background()
	queued, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-queued"))
	if err != nil || queued.ProviderTurnID != "" {
		t.Fatalf("queued = %+v, %v", queued, err)
	}
	if err := fixture.repository.FinishSteer(ctx, queued, false); err != nil {
		t.Fatal(err)
	}
	fixture.run(t)
	reloaded, err := fixture.repository.ReloadSteerTarget(ctx, queued)
	if err != nil || reloaded.ProviderTurnID != "native-steer" || reloaded.RecordID != queued.RecordID {
		t.Fatalf("reloaded = %+v, %v", reloaded, err)
	}
	accepted, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-accepted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.FinishSteer(ctx, accepted, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(ctx, `UPDATE jobs SET status='canceling', cancel_requested_at=now() WHERE id=$1::uuid`, fixture.job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-late")); !errors.Is(err, researchdomain.ErrTurnEnded) {
		t.Fatalf("a stopping turn = %v", err)
	}
	if _, err := fixture.repository.ReloadSteerTarget(ctx, queued); !errors.Is(err, researchdomain.ErrTurnEnded) {
		t.Fatalf("reload of a stopping turn = %v", err)
	}
	replay, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-accepted"))
	if err != nil || !replay.Replayed {
		t.Fatalf("replay after the end = %+v, %v", replay, err)
	}
	// A refused late correction left no key behind.
	var records int
	if err := fixture.db.Pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_records WHERE idempotency_key_hash='key-late'`).Scan(&records); err != nil || records != 0 {
		t.Fatalf("records of the refused correction = %d, %v", records, err)
	}
}

// R1.8: an author's turn takes MaxSteersPerTurn corrections; let-go keys do not count.
func TestBeginSteerLimitsTheCorrectionsOfATurnPostgres(t *testing.T) {
	fixture := newSteerFixture(t, "ck_rch_steer_limit")
	ctx := context.Background()
	fixture.run(t)
	refused, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-refused"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.FinishSteer(ctx, refused, false); err != nil {
		t.Fatal(err)
	}
	for index := range researchdomain.MaxSteersPerTurn {
		target, err := fixture.repository.BeginSteer(ctx, fixture.begin(fmt.Sprintf("key-%d", index)))
		if err != nil {
			t.Fatalf("correction %d: %v", index, err)
		}
		if err := fixture.repository.FinishSteer(ctx, target, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-over")); !errors.Is(err, researchdomain.ErrSteerLimit) {
		t.Fatalf("over the limit = %v", err)
	}
	// A repeat of an accepted key is not a new correction.
	replay, err := fixture.repository.BeginSteer(ctx, fixture.begin("key-0"))
	if err != nil || !replay.Replayed {
		t.Fatalf("replay at the limit = %+v, %v", replay, err)
	}
}
