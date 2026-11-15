package researchchat

import (
	"context"
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// codexOfTheTest is the runner as the adapter of Codex meets a turn (adapters/codex/run.go): it starts the thread of the turn
// first and binds it after, and a binding the core refused ends the run runtime.root_bind_failed.
type codexOfTheTest struct {
	agentRunnerStub
	started int
}

type rootBindFailed struct{ error }

func (rootBindFailed) SafeErrorCode() string { return "runtime.root_bind_failed" }

func (codex *codexOfTheTest) Run(ctx context.Context, request AgentRunRequest) (agent.RunOutcome, error) {
	codex.started++
	codex.request = request
	if err := request.BindRoot(ctx, codex.result.RootThreadID); err != nil {
		return agent.RunOutcome{}, rootBindFailed{err}
	}
	if err := request.BindTurn(ctx, codex.result.RootThreadID, codex.result.TurnID); err != nil {
		return agent.RunOutcome{}, err
	}
	return codex.result, nil
}

// specs/2026-11-15-desk-interrupted-turn.md IT1.1, IT1.2: a turn whose session ended before it began — the desktop app
// revokes the sessions of its author at every start, the site at «Выйти везде», a change of the password, a block — ends
// model.session_ended at once, the end of a turn before the model: no work of a site is taken, no thread of Codex is
// started, nothing is bound; the worker fails its claim with the code, and that failure closes the work. A turn whose session
// lives goes the way it went, with one read of its session more.
func TestATurnWhoseSessionEndedEndsBeforeAnything(t *testing.T) {
	claim := researchdomain.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 2}
	answer := agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "У перевала."}
	for kind, flow := range map[string]agent.Flow{
		agent.SessionResearch: agent.FlowResearchChat, agent.SessionDrawing: agent.FlowImageWorkshop, agent.SessionDraft: agent.FlowWriterWorkshop,
	} {
		t.Run(kind, func(t *testing.T) {
			bundles := map[string]Bundle{kind: {Flow: flow, SkillName: "skill-of-" + kind, Handler: successfulToolHandler}}
			run := runOfTheWork()
			run.Kind, run.ClaimIncarnation = kind, 2
			repository := &runRepositoryStub{run: run, sessionGone: true}
			codex := &codexOfTheTest{agentRunnerStub: agentRunnerStub{result: answer}}
			work := &workOfASite{repository: repository}
			_, err := NewRunTurn(repository, codex, bundles).WithWork(work).Execute(context.Background(), claim)
			if code := codeOf(err); code != researchdomain.CodeSessionEnded {
				t.Fatalf("a turn whose session ended = %q (%v)", code, err)
			}
			if len(work.begun) != 0 || codex.started != 0 || repository.boundRoot != "" || repository.boundTurn != "" {
				t.Fatalf("a turn whose session ended: the work begun %v, Codex started %d times, bound %q %q",
					work.begun, codex.started, repository.boundRoot, repository.boundTurn)
			}
			if len(repository.failed) != 0 || len(repository.finished) != 0 || len(work.ended) != 0 || repository.sessionReads != 1 {
				t.Fatalf("a turn whose session ended is finished as a turn of the model: %+v %+v, ended %v, %d reads",
					repository.failed, repository.finished, work.ended, repository.sessionReads)
			}

			repository = &runRepositoryStub{run: run}
			codex = &codexOfTheTest{agentRunnerStub: agentRunnerStub{result: answer}}
			work = &workOfASite{repository: repository}
			if _, err := NewRunTurn(repository, codex, bundles).WithWork(work).Execute(context.Background(), claim); err != nil {
				t.Fatal(err)
			}
			if repository.sessionReads != 1 || len(work.begun) != 1 || codex.started != 1 || repository.boundRoot != "root-1" ||
				len(repository.finished) != 1 || len(work.ended) != 1 || work.ended[0] != researchdomain.TurnAnswered {
				t.Fatalf("a turn whose session lives: %d reads, began %v, Codex %d, bound %q, finished %d, ended %v",
					repository.sessionReads, work.begun, codex.started, repository.boundRoot, len(repository.finished), work.ended)
			}
		})
	}
}

// IT1.3: the site counts no work of a turn — the same check ends the turn with the same code before the model.
func TestATurnOfTheSiteWhoseSessionEndedEndsWithItsCode(t *testing.T) {
	repository := &runRepositoryStub{run: runOfTheWork(), sessionGone: true}
	codex := &codexOfTheTest{agentRunnerStub: agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "x"}}}
	_, err := NewRunTurn(repository, codex, chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})).
		Execute(context.Background(), researchdomain.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 1})
	if code := codeOf(err); code != researchdomain.CodeSessionEnded || codex.started != 0 {
		t.Fatalf("a turn of the site whose session ended = %q (%v), Codex started %d times", code, err, codex.started)
	}
	// A read of the session that fails is the failure of the attempt, not an ended session.
	failing := &failingSessionRead{runRepositoryStub: runRepositoryStub{run: runOfTheWork()}}
	if _, err := NewRunTurn(failing, codex, chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})).
		Execute(context.Background(), researchdomain.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 1}); !errors.Is(err, errSessionRead) || codex.started != 0 {
		t.Fatalf("a failed read of the session = %v, Codex started %d times", err, codex.started)
	}
}

// codeOf is the code the worker fails the claim of a turn with: the safe code of its error.
func codeOf(err error) string {
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) {
		return ""
	}
	return classified.SafeErrorCode()
}

var errSessionRead = errors.New("the session could not be read")

type failingSessionRead struct{ runRepositoryStub }

func (*failingSessionRead) SessionLive(context.Context, researchdomain.TurnRun) (bool, error) {
	return false, errSessionRead
}
