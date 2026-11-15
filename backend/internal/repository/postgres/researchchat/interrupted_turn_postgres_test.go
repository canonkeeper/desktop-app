package researchchat

import (
	"context"
	"errors"
	"testing"
	"time"

	"canonkeeper/backend/internal/domain/agent"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/researchchat"
	authstore "canonkeeper/backend/internal/store/auth"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

// rootBindRefused is a binding the core refused, as the adapter of Codex names it (adapters/codex/run.go).
type rootBindRefused struct{ error }

func (rootBindRefused) SafeErrorCode() string { return "runtime.root_bind_failed" }

// specs/2026-11-15-desk-interrupted-turn.md IT1.1, IT1.3 (the site): the store reads the session of a turn by the rule its
// late effects lock it by — alive until «Выйти везде», ended after it; a Job without its binding is a conflict, not an
// interruption. A question whose session ended while it waited in the queue ends model.session_ended before Codex, and its
// unit comes back by the rule of a failure before the model: the same as for a turn whose binding the core refused while
// Codex started its thread — runtime.root_bind_failed, the way such a turn ended before.
func TestAQuestionWhoseSessionEndedInTheQueueEndsBeforeCodexPostgres(t *testing.T) {
	chats := newQuotaChats(t, "ck_rch_interrupted_turn")
	chats.setLimits(map[quota.Key]int64{quota.KeyActiveTurns: 5})
	ctx := context.Background()
	questions := func() int64 { return spent(t, chats.db, "chat_turns") }
	// A claim compares next_attempt_at with the clock of the database: the questions are asked just now.
	asked := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	refused := chats.askOrFail(chats.chat(), "interrupted-refused", asked)
	waiting := chats.askOrFail(chats.chat(), "interrupted-waiting", asked.Add(time.Second))
	if used := questions(); used != 2 {
		t.Fatalf("questions of the day after two questions = %d, want 2", used)
	}
	if _, err := chats.repository.SessionLive(ctx, researchdomain.TurnRun{ProjectID: submitTurnProjectID, JobID: "92000000-0000-4000-8000-00000000ffff", ClaimIncarnation: 1}); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("the session of a Job without its binding = %v, want a conflict", err)
	}
	bundles := map[string]service.Bundle{agent.SessionResearch: {
		Flow: agent.FlowResearchChat, SkillName: "chat-skill",
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Success: true}, nil
		},
	}}
	// fail ends the claim with the code of the error of the turn, as the worker does: the failure of the claim.
	fail := func(claim jobs.WorkerClaim, err error) jobs.Job {
		t.Helper()
		var classified interface{ SafeErrorCode() string }
		if !errors.As(err, &classified) {
			t.Fatalf("the turn ended without a code: %v", err)
		}
		job, loadErr := jobstore.New(chats.db).LoadJobForWorkerClaim(ctx, claim)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		job.Error = &jobs.Error{Code: classified.SafeErrorCode(), Message: "worker handler failed"}
		if err := service.NewFailTurn(chats.repository).FailClaim(ctx, job); err != nil {
			t.Fatal(err)
		}
		stored, err := jobstore.New(chats.db).GetJob(ctx, jobs.GetJobStoreInput{ActorUserID: submitTurnActorID, ProjectID: submitTurnProjectID, JobID: job.ID})
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}

	// The first question: its session lives when the worker takes it, so the turn goes on to Codex; the author leaves
	// everywhere while Codex starts the thread, and the binding refuses the turn — as before this slice.
	first := claimQueuedResearchTurnForTest(t, chats.db)
	if first.JobID != refused.ID {
		t.Fatalf("the worker took %s first, want %s", first.JobID, refused.ID)
	}
	if live, err := chats.repository.SessionLive(ctx, researchdomain.TurnRun{ProjectID: first.ProjectID, JobID: first.JobID, ClaimIncarnation: first.ClaimIncarnation}); err != nil || !live {
		t.Fatalf("the session of a question before «Выйти везде» = %v, %v", live, err)
	}
	started := 0
	leaving := service.AgentRunnerFunc(func(ctx context.Context, request service.AgentRunRequest) (agent.RunOutcome, error) {
		started++
		if err := authstore.New(chats.db).RevokeUserSessions(ctx, submitTurnActorID, time.Now().UTC()); err != nil {
			return agent.RunOutcome{}, err
		}
		if err := request.BindRoot(ctx, "root-interrupted-refused"); err != nil {
			return agent.RunOutcome{}, rootBindRefused{err}
		}
		return agent.RunOutcome{}, errors.New("the binding of a turn whose session ended went through")
	})
	_, err := service.NewRunTurn(chats.repository, leaving, bundles).Execute(ctx, researchdomain.WorkerClaim{
		ProjectID: first.ProjectID, JobID: first.JobID, ClaimIncarnation: first.ClaimIncarnation,
	})
	if stored := fail(first, err); started != 1 || stored.Status != jobs.StatusFailed || stored.Error == nil || stored.Error.Code != "runtime.root_bind_failed" {
		t.Fatalf("a turn the binding refused: Codex started %d times, the Job %s %+v", started, stored.Status, stored.Error)
	}
	if used := questions(); used != 1 {
		t.Fatalf("questions after a turn the binding refused = %d, want 1", used)
	}

	// The second question waited in the queue while the author left everywhere: the worker ends it at once.
	second := claimQueuedResearchTurnForTest(t, chats.db)
	if second.JobID != waiting.ID {
		t.Fatalf("the worker took %s second, want %s", second.JobID, waiting.ID)
	}
	if live, err := chats.repository.SessionLive(ctx, researchdomain.TurnRun{ProjectID: second.ProjectID, JobID: second.JobID, ClaimIncarnation: second.ClaimIncarnation}); err != nil || live {
		t.Fatalf("the session of a question after «Выйти везде» = %v, %v", live, err)
	}
	started = 0
	codex := service.AgentRunnerFunc(func(context.Context, service.AgentRunRequest) (agent.RunOutcome, error) {
		started++
		return agent.RunOutcome{}, errors.New("Codex was asked for a turn whose session ended")
	})
	_, err = service.NewRunTurn(chats.repository, codex, bundles).Execute(ctx, researchdomain.WorkerClaim{
		ProjectID: second.ProjectID, JobID: second.JobID, ClaimIncarnation: second.ClaimIncarnation,
	})
	if stored := fail(second, err); started != 0 || stored.Status != jobs.StatusFailed || stored.Error == nil || stored.Error.Code != researchdomain.CodeSessionEnded {
		t.Fatalf("a question whose session ended in the queue: Codex started %d times, the Job %s %+v", started, stored.Status, stored.Error)
	}
	if used := questions(); used != 0 {
		t.Fatalf("questions after a question whose session ended in the queue = %d, want 0 — the rule of a failure before the model", used)
	}
}
