package researchchat

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/jobs"
)

// workOfASite is the work of a turn a site counts, as a test sees it: what it began and ended, in what order with the
// terminal transaction, and what it answers.
type workOfASite struct {
	repository *runRepositoryStub
	begun      []string
	ended      []researchdomain.TurnEnd
	// endedAfter is how many terminal writes the repository had when each End came.
	endedAfter []int
	refuse     string
	failure    string
}

func (work *workOfASite) Begin(_ context.Context, run researchdomain.TurnRun) (string, error) {
	work.begun = append(work.begun, run.JobID)
	return work.refuse, nil
}

func (work *workOfASite) Failure(string) string { return work.failure }

func (work *workOfASite) End(_ context.Context, jobID string, end researchdomain.TurnEnd) {
	work.ended = append(work.ended, end)
	if work.repository != nil {
		work.endedAfter = append(work.endedAfter, len(work.repository.finished)+len(work.repository.failed))
	}
}

func runOfTheWork() researchdomain.TurnRun {
	return researchdomain.TurnRun{
		Kind: agent.SessionResearch, ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 1, SourceView: "draft", Scope: researchdomain.ScopeProject, Question: "Где застава?",
	}
}

// specs/2026-11-13-desk-account-ai.md AI2.3, AI2.5, AI2.7: a turn whose work a site counts takes it before the model and a
// refusal ends the turn with its code and no model; a failed turn names the reason the gateway heard; the permit is closed
// after the terminal transaction — answered, failed — and never before it.
func TestATurnTakesAndClosesTheWorkASiteCounts(t *testing.T) {
	claim := researchdomain.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 1}
	bundles := chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})

	repository := &runRepositoryStub{run: runOfTheWork()}
	runner := &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "У перевала."}}
	work := &workOfASite{repository: repository}
	if _, err := NewRunTurn(repository, runner, bundles).WithWork(work).Execute(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if len(work.begun) != 1 || len(work.ended) != 1 || work.ended[0] != researchdomain.TurnAnswered || work.endedAfter[0] != 1 {
		t.Fatalf("an answer: began %v, ended %v after %v terminal writes", work.begun, work.ended, work.endedAfter)
	}

	repository = &runRepositoryStub{run: runOfTheWork()}
	runner = &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnFailed, Failure: agent.RunFailureServerOverloaded}}
	work = &workOfASite{repository: repository, failure: researchdomain.CodeDayFence}
	_, _ = NewRunTurn(repository, runner, bundles).WithWork(work).Execute(context.Background(), claim)
	if len(repository.failed) != 1 || repository.failed[0].Code != researchdomain.CodeDayFence || len(work.ended) != 1 ||
		work.ended[0] != researchdomain.TurnFailed || work.endedAfter[0] != 1 {
		t.Fatalf("a failure the gateway named: %+v, ended %v after %v", repository.failed, work.ended, work.endedAfter)
	}

	repository = &runRepositoryStub{run: runOfTheWork()}
	runner = &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnFailed, Failure: agent.RunFailureServerOverloaded}}
	work = &workOfASite{repository: repository}
	_, _ = NewRunTurn(repository, runner, bundles).WithWork(work).Execute(context.Background(), claim)
	if repository.failed[0].Code != researchdomain.CodeServerOverloaded {
		t.Fatalf("without a reason of the gateway the code is the class of the failure: %+v", repository.failed)
	}

	// A refusal before the model: the turn has no thread or turn of Codex yet, so it is not finished as a turn of the model —
	// the worker fails its claim with the code, and the failure of the claim closes the work.
	for _, code := range []string{researchdomain.CodePermitEnded, researchdomain.CodeQuestionsOver, researchdomain.CodeAccountUnlinked} {
		repository = &runRepositoryStub{run: runOfTheWork()}
		runner = &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "x"}}
		work = &workOfASite{repository: repository, refuse: code}
		_, err := NewRunTurn(repository, runner, bundles).WithWork(work).Execute(context.Background(), claim)
		var classified interface{ SafeErrorCode() string }
		if err == nil || !errors.As(err, &classified) || classified.SafeErrorCode() != code {
			t.Fatalf("a turn the site refused with %s ended with %v", code, err)
		}
		if runner.request.InitialInput != "" || len(repository.failed) != 0 || len(work.ended) != 0 {
			t.Fatalf("a refused work: the model asked %v, finished as a turn %+v, ended %v", runner.request.InitialInput != "", repository.failed, work.ended)
		}
	}
	for _, code := range []string{
		researchdomain.CodeDayFence, researchdomain.CodeAccountUnlinked, researchdomain.CodePermitEnded,
		researchdomain.CodeSiteUnavailable, researchdomain.CodeAppUnfit, researchdomain.CodeThreadTooLarge,
		researchdomain.CodeSiteUnreachable,
	} {
		if researchdomain.TurnFailureCodes[code] == "" {
			t.Errorf("the code %s is not in the closed set", code)
		}
	}
}

// AI2.7: a turn the author stopped is closed as canceled once the cancel is written; a turn the worker failed, once its
// failure is written.
func TestAStoppedOrFailedTurnClosesTheWorkASiteCounts(t *testing.T) {
	work := &workOfASite{}
	cancel := NewCancelTurn(&cancellationStub{target: researchdomain.CancelTarget{
		ProjectID: "project-1", ChatID: "chat-1", JobID: "job-1", NeedsInterrupt: true, Flow: agent.FlowResearchChat,
	}}, func(context.Context, RootSelector) error { return nil }).WithWork(work)
	if _, err := cancel.CancelJobTarget(context.Background(), jobs.TargetCancelInput{
		Job: jobs.Job{ID: "job-1", ProjectID: "project-1"}, TargetReadable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(work.ended) != 1 || work.ended[0] != researchdomain.TurnCanceled {
		t.Fatalf("a stopped turn ended %v", work.ended)
	}
}

// cancellationStub cancels one running turn.
type cancellationStub struct {
	target researchdomain.CancelTarget
}

func (stub *cancellationStub) BeginTargetCancel(_ context.Context, input jobs.TargetCancelInput) (researchdomain.CancelTarget, jobs.Job, error) {
	return stub.target, input.Job, nil
}

func (stub *cancellationStub) CancelTargetForJob(context.Context, jobs.Job) (researchdomain.CancelTarget, error) {
	return stub.target, nil
}

func (stub *cancellationStub) CompleteCancel(_ context.Context, target researchdomain.CancelTarget) (jobs.Job, error) {
	return jobs.Job{ID: target.JobID, ProjectID: target.ProjectID, Status: jobs.StatusCanceled}, nil
}

func (stub *cancellationStub) CanReadJobTarget(context.Context, jobs.Actor, jobs.Subject) (bool, error) {
	return true, nil
}
