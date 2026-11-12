package researchchat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type runRepositoryStub struct {
	run       researchdomain.TurnRun
	finishErr error
	finished  []researchdomain.FinishResponse
	failed    []researchdomain.FinishFailure
	boundRoot string
	boundTurn string
}

func (stub *runRepositoryStub) LoadRun(context.Context, researchdomain.WorkerClaim) (researchdomain.TurnRun, error) {
	return stub.run, nil
}

func (stub *runRepositoryStub) BindRoot(_ context.Context, run researchdomain.TurnRun, rootThreadID string) error {
	stub.boundRoot = rootThreadID
	return nil
}

func (stub *runRepositoryStub) BindTurn(_ context.Context, run researchdomain.TurnRun, rootThreadID, turnID string) error {
	stub.boundTurn = turnID
	return nil
}

func (stub *runRepositoryStub) FinishResponse(_ context.Context, input researchdomain.FinishResponse) error {
	stub.finished = append(stub.finished, input)
	return stub.finishErr
}

func (stub *runRepositoryStub) FinishFailure(_ context.Context, input researchdomain.FinishFailure) error {
	stub.failed = append(stub.failed, input)
	return nil
}

type agentRunnerStub struct {
	request AgentRunRequest
	result  agent.RunOutcome
}

func (stub *agentRunnerStub) Run(ctx context.Context, request AgentRunRequest) (agent.RunOutcome, error) {
	stub.request = request
	if err := request.BindRoot(ctx, stub.result.RootThreadID); err != nil {
		return agent.RunOutcome{}, err
	}
	if err := request.BindTurn(ctx, stub.result.RootThreadID, stub.result.TurnID); err != nil {
		return agent.RunOutcome{}, err
	}
	return stub.result, nil
}

func TestRunTurnResumesExactSessionRootAndFinalizesCompletedResponse(t *testing.T) {
	run := researchdomain.TurnRun{
		Kind:      agent.SessionResearch,
		ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 3, SourceView: "draft", Scope: researchdomain.ScopeProject,
		Question: "Что известно?", RootThreadID: "root-1", ProviderTurnID: "provider-turn-1",
		SelectedContext: []researchdomain.ContextSelection{{BookTitle: "Том I", ChapterPosition: 3, ChapterTitle: "Развязка"}},
	}
	repository := &runRepositoryStub{run: run}
	runner := &agentRunnerStub{result: agent.RunOutcome{
		RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted,
		FinalResponse: "Точный ответ модели",
	}}
	bundle := Bundle{
		SkillName: "chat-skill",
		Tools:     []agent.ToolSpec{{Namespace: "source", Name: "read"}},
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Success: true}, nil
		},
	}

	result, err := NewRunTurn(repository, runner, chatBundles(bundle)).Execute(context.Background(), researchdomain.WorkerClaim{
		ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response != "Точный ответ модели" || len(repository.finished) != 1 {
		t.Fatalf("result/finalization = (%+v, %d)", result, len(repository.finished))
	}
	request := runner.request
	if request.CorrelationID != run.ChatID || request.TurnCorrelationID != run.TurnID ||
		request.ExistingRootThreadID != run.RootThreadID || request.ExistingTurnID != run.ProviderTurnID ||
		request.ContinueFailedTurn || request.SkillName != "chat-skill" {
		t.Fatalf("native request = %+v", request)
	}
	if !strings.Contains(request.InitialInput, "Развязка") || strings.Contains(request.InitialInput, run.ProjectID) {
		t.Fatalf("initial assignment = %s", request.InitialInput)
	}
	if question, ok := researchdomain.PublicAssignmentQuestion(request.InitialInput); !ok || question.Text != run.Question {
		t.Fatalf("public assignment question = %+v, valid = %t", question, ok)
	}
	finished := repository.finished[0]
	if finished.RootThreadID != "root-1" || finished.ProviderTurnID != "provider-turn-1" ||
		finished.Response != "Точный ответ модели" {
		t.Fatalf("finished response = %+v", finished)
	}
}

// specs/2026-11-02-image-workshop.md IW7.1: every turn of the chat gets the tools of its bundle, the same for every
// author and chat: there is no drawing list a turn adds.
func TestRunTurnOffersTheToolsOfTheBundle(t *testing.T) {
	bundle := Bundle{
		SkillName: "chat-skill",
		Tools:     []agent.ToolSpec{{Namespace: "saga", Name: "read"}, {Namespace: "saga", Name: "image"}},
		Handler:   successfulToolHandler,
	}
	run := researchdomain.TurnRun{
		Kind:      agent.SessionResearch,
		ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 1, SourceView: "published", Scope: researchdomain.ScopeProject,
		Question: "Нарисуй Мирона",
	}
	runner := &agentRunnerStub{result: agent.RunOutcome{
		RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "Готово",
	}}
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, runner, chatBundles(bundle)).Execute(context.Background(), researchdomain.WorkerClaim{
		ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation,
	}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range runner.request.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, " ") != "read image" {
		t.Fatalf("tools = %v", names)
	}
	runner.request.Tools[0].Name = "changed"
	if bundle.Tools[0].Name != "read" {
		t.Fatalf("a run changed the bundle: %v", bundle.Tools)
	}
}

func TestRunTurnRejectsDuplicateFinalization(t *testing.T) {
	run := researchdomain.TurnRun{
		Kind:      agent.SessionResearch,
		ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 3, SourceView: "draft", Scope: researchdomain.ScopeProject,
		Question: "Что известно?", RootThreadID: "root-1", ProviderTurnID: "provider-turn-1",
	}
	repository := &runRepositoryStub{run: run, finishErr: researchdomain.ErrConflict}
	runner := &agentRunnerStub{result: agent.RunOutcome{
		RootThreadID: run.RootThreadID, TurnID: run.ProviderTurnID,
		Status: agent.TurnCompleted, FinalResponse: "Ответ",
	}}

	_, err := NewRunTurn(repository, runner, chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})).
		Execute(context.Background(), researchdomain.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: 3})
	if !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("duplicate finalization error = %v", err)
	}
}

func TestRunTurnRequiresCompletedNativeResponse(t *testing.T) {
	run := researchdomain.TurnRun{
		Kind:      agent.SessionResearch,
		ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 3, SourceView: "draft", Scope: researchdomain.ScopeProject,
		Question: "Что известно?",
	}
	repository := &runRepositoryStub{run: run}
	runner := &agentRunnerStub{result: agent.RunOutcome{
		RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnFailed,
		Error: "codexErrorInfo=serverOverloaded: upstream overloaded",
	}}

	_, err := NewRunTurn(repository, runner, chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})).
		Execute(context.Background(), researchdomain.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: 3})
	if !errors.Is(err, researchdomain.ErrNativeIncomplete) || len(repository.finished) != 0 || len(repository.failed) != 1 ||
		repository.failed[0].Code != "model.turn_failed" {
		t.Fatalf("incomplete result = (%v, finalizations=%d, failures=%+v)", err, len(repository.finished), repository.failed)
	}
	if !strings.Contains(err.Error(), "status=failed") || !strings.Contains(err.Error(), "codexErrorInfo=serverOverloaded: upstream overloaded") {
		t.Fatalf("incomplete turn error = %q, want the native turn status and error for the diagnostic log", err)
	}
}

// R4.1.2: an interrupted turn keeps model.turn_interrupted, a failed one gets the code of its
// model failure class, and a failure without a class stays model.turn_failed.
func TestRunTurnNamesModelFailureClassInFailureCode(t *testing.T) {
	for _, test := range []struct {
		status  agent.TurnStatus
		failure agent.RunFailure
		want    string
	}{
		{agent.TurnFailed, agent.RunFailureUsageLimitExceeded, "model.usage_limit_exceeded"},
		{agent.TurnFailed, agent.RunFailureServerOverloaded, "model.server_overloaded"},
		{agent.TurnFailed, agent.RunFailureConnectionFailed, "model.http_connection_failed"},
		{agent.TurnFailed, agent.RunFailureContextWindowExceeded, "model.context_window_exceeded"},
		{agent.TurnCompleted, agent.RunFailureUsageLimitExceeded, "model.usage_limit_exceeded"},
		{agent.TurnInterrupted, agent.RunFailureUsageLimitExceeded, "model.turn_interrupted"},
		{agent.TurnInterrupted, "", "model.turn_interrupted"},
		{agent.TurnFailed, "", "model.turn_failed"},
	} {
		t.Run(string(test.status)+"/"+string(test.failure), func(t *testing.T) {
			run := researchdomain.TurnRun{
				Kind:      agent.SessionResearch,
				ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
				ActorUserID: "user-1", ClaimIncarnation: 3, SourceView: "draft", Scope: researchdomain.ScopeProject,
				Question: "Что известно?",
			}
			repository := &runRepositoryStub{run: run}
			runner := &agentRunnerStub{result: agent.RunOutcome{
				RootThreadID: "root-1", TurnID: "provider-turn-1", Status: test.status, Failure: test.failure,
			}}

			_, err := NewRunTurn(repository, runner, chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})).
				Execute(context.Background(), researchdomain.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: 3})
			if !errors.Is(err, researchdomain.ErrNativeIncomplete) || len(repository.failed) != 1 ||
				repository.failed[0].Code != test.want {
				t.Fatalf("failure = (%v, %+v), want code %q", err, repository.failed, test.want)
			}
		})
	}
}

func successfulToolHandler(context.Context, agent.ToolCall) (agent.ToolResult, error) {
	return agent.ToolResult{Success: true}, nil
}

// specs/2026-10-29-saga-illustrations.md R15.9: the model gets the images attached to the question
// as attachedImages of the turn's input, beside the question.
func TestRunTurnHandsTheAttachedImagesToTheModel(t *testing.T) {
	run := researchdomain.TurnRun{
		Kind:      agent.SessionResearch,
		ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 3, SourceView: "published", Scope: researchdomain.ScopeProject,
		Question: "Нарисуй Мирона с этим лицом", Images: []int{81, 82}, ImageTitles: map[int]string{81: "Лицо"}, RootThreadID: "root-1", ProviderTurnID: "provider-turn-1",
	}
	runner := &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "Ответ"}}
	bundle := Bundle{SkillName: "chat-skill", Tools: []agent.ToolSpec{{Namespace: "saga", Name: "read"}},
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Success: true}, nil
		}}
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, runner, chatBundles(bundle)).Execute(context.Background(), researchdomain.WorkerClaim{
		ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.request.InitialInput, `"attachedImages":[81,82]`) {
		t.Fatalf("initial input = %s", runner.request.InitialInput)
	}
	// R15.19: the anchors the author writes come with what they mean.
	if !strings.Contains(runner.request.InitialInput, `"attachedPhotos":["фото 1 — изображение 81 (Лицо)","фото 2 — изображение 82"]`) {
		t.Fatalf("initial input = %s", runner.request.InitialInput)
	}
	// specs/2026-11-02-image-workshop.md IW7.1: a turn of the chat draws nothing and names no chosen images.
	if strings.Contains(runner.request.InitialInput, "chosenReferences") {
		t.Fatalf("initial input = %s", runner.request.InitialInput)
	}
	question, ok := researchdomain.PublicAssignmentQuestion(runner.request.InitialInput)
	if !ok || question.Text != run.Question || len(question.Images) != 2 {
		t.Fatalf("public question = %+v, %t", question, ok)
	}
}

// chatBundles runs the turns of the chats with the bundle.
func chatBundles(bundle Bundle) map[string]Bundle {
	bundle.Flow = agent.FlowResearchChat
	return map[string]Bundle{agent.SessionResearch: bundle}
}

// specs/2026-11-02-image-workshop.md IW4.1: a turn of the helper of a drawing runs with the bundle of its session's
// kind in the flow of the workshop; a turn of a kind without a bundle starts nothing.
func TestRunTurnRunsTheHelperOfADrawingInItsFlow(t *testing.T) {
	chat := Bundle{SkillName: "chat-skill", Tools: []agent.ToolSpec{{Namespace: "saga", Name: "read"}}, Handler: successfulToolHandler}
	bundles := chatBundles(chat)
	bundles[agent.SessionDrawing] = Bundle{
		Flow: agent.FlowImageWorkshop, SkillName: "workshop-skill",
		Tools: []agent.ToolSpec{{Namespace: "drawing", Name: "edit"}}, Handler: successfulToolHandler,
	}
	run := researchdomain.TurnRun{
		Kind: agent.SessionDrawing, DrawingID: "drawing-1", ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 1, SourceView: "published", Scope: researchdomain.ScopeProject, Question: "Сделай рога длиннее",
		WorkshopLines: []string{"Состояние: текущая версия — рисунок 4; выбора не ждёт."},
	}
	runner := &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "Готово"}}
	claim := researchdomain.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation}
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, runner, bundles).Execute(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if request := runner.request; request.Flow != agent.FlowImageWorkshop || request.BundleID != "image-workshop" || request.SkillName != "workshop-skill" ||
		len(request.Tools) != 1 || request.Tools[0].Namespace != "drawing" {
		t.Fatalf("the run of the helper = %+v", request)
	}
	// specs/2026-11-03-workshop-helpers-steering.md S2.1: the model reads the lines of the state beside the words; the
	// feed shows the words alone.
	if !strings.Contains(runner.request.InitialInput, `"workshop":["Состояние: текущая версия — рисунок 4; выбора не ждёт."]`) {
		t.Fatalf("initial input = %s", runner.request.InitialInput)
	}
	if question, ok := researchdomain.PublicAssignmentQuestion(runner.request.InitialInput); !ok || question.Text != run.Question {
		t.Fatalf("public question = %+v, %t", question, ok)
	}
	run.Kind = agent.SessionDraft
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, &agentRunnerStub{}, bundles).Execute(context.Background(), claim); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("a turn of a kind without a bundle = %v", err)
	}
}

// specs/2026-11-02-writer-workshop.md W1.1, W1.2: a turn of the helper of a chapter draft runs with the bundle of the
// kind draft in the flow writer-workshop; the chat and the drawing helper keep theirs.
func TestRunTurnRunsTheHelperOfADraftInItsFlow(t *testing.T) {
	bundles := chatBundles(Bundle{SkillName: "chat-skill", Tools: []agent.ToolSpec{{Namespace: "saga", Name: "read"}}, Handler: successfulToolHandler})
	bundles[agent.SessionDraft] = Bundle{
		Flow: agent.FlowWriterWorkshop, SkillName: "writer-skill",
		Tools: []agent.ToolSpec{{Namespace: "draft", Name: "notes"}}, Handler: successfulToolHandler,
	}
	run := researchdomain.TurnRun{
		Kind: agent.SessionDraft, ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 1, SourceView: "published", Scope: researchdomain.ScopeProject, Question: "Сверь с каноном",
	}
	runner := &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "Готово"}}
	claim := researchdomain.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation}
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, runner, bundles).Execute(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	if request := runner.request; request.Flow != agent.FlowWriterWorkshop || request.BundleID != "writer-workshop" || request.SkillName != "writer-skill" ||
		len(request.Tools) != 1 || request.Tools[0].Namespace != "draft" {
		t.Fatalf("the run of the helper = %+v", request)
	}
	if flow, known := agent.SessionFlow(agent.SessionDraft); !known || flow != agent.FlowWriterWorkshop {
		t.Fatalf("SessionFlow(draft) = %q, %v", flow, known)
	}
}

// runnerFailing is a runner whose turn ends with an error of its own.
type runnerFailing struct{ err error }

func (runner runnerFailing) Run(context.Context, AgentRunRequest) (agent.RunOutcome, error) {
	return agent.RunOutcome{}, runner.err
}

// specs/2026-11-05-drawing-one-screen.md D3.7: the turn «Нарисовать» sent starts the drawing once the model answered,
// while the turn still holds its claim, before its answer is finished; a plain turn, a failed or stopped turn, an empty
// answer and an answer of another thread draw nothing; a bundle without a drawing just ends the turn.
func TestRunTurnDrawsAfterTheAnswerOfATurnThatAskedForIt(t *testing.T) {
	run := researchdomain.TurnRun{
		Kind: agent.SessionDrawing, DrawingID: "drawing-1", ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 1, SourceView: "published", Scope: researchdomain.ScopeProject, Question: "Нарисуй дракона",
		DrawAfter: true,
	}
	claim := researchdomain.WorkerClaim{ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation}
	completed := agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "Описание готово"}
	bundles := func(draw func(context.Context, researchdomain.TurnRun)) map[string]Bundle {
		all := chatBundles(Bundle{SkillName: "chat-skill", Handler: successfulToolHandler})
		all[agent.SessionDrawing] = Bundle{Flow: agent.FlowImageWorkshop, SkillName: "workshop-skill", Handler: successfulToolHandler, DrawAfter: draw}
		return all
	}

	repository := &runRepositoryStub{run: run}
	var drawn []researchdomain.TurnRun
	finishedAtDraw := -1
	result, err := NewRunTurn(repository, &agentRunnerStub{result: completed}, bundles(func(_ context.Context, turn researchdomain.TurnRun) {
		drawn, finishedAtDraw = append(drawn, turn), len(repository.finished)
	})).Execute(context.Background(), claim)
	if err != nil || result.Response != "Описание готово" || len(repository.finished) != 1 {
		t.Fatalf("the turn = %+v, %v, finished %d", result, err, len(repository.finished))
	}
	if len(drawn) != 1 || drawn[0].DrawingID != "drawing-1" || drawn[0].ClaimIncarnation != 1 || finishedAtDraw != 0 {
		t.Fatalf("the drawing started %+v with %d finished", drawn, finishedAtDraw)
	}

	plain := run
	plain.DrawAfter = false
	for name, test := range map[string]struct {
		run     researchdomain.TurnRun
		runner  AgentRunner
		wantErr bool
		failed  int
	}{
		"a plain turn":     {run: plain, runner: &agentRunnerStub{result: completed}},
		"a failed turn":    {run: run, runner: &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnFailed}}, wantErr: true, failed: 1},
		"a stopped turn":   {run: run, runner: &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnInterrupted, FinalResponse: "Начал"}}, wantErr: true, failed: 1},
		"an empty answer":  {run: run, runner: &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted}}, wantErr: true, failed: 1},
		"a runner error":   {run: run, runner: runnerFailing{err: errors.New("the claim went")}, wantErr: true},
		"no provider turn": {run: run, runner: &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", Status: agent.TurnCompleted, FinalResponse: "Ответ"}}, wantErr: true},
	} {
		repository := &runRepositoryStub{run: test.run}
		_, err := NewRunTurn(repository, test.runner, bundles(func(context.Context, researchdomain.TurnRun) {
			t.Errorf("%s: drew", name)
		})).Execute(context.Background(), claim)
		if (err != nil) != test.wantErr || len(repository.failed) != test.failed {
			t.Fatalf("%s = %v, failed %d", name, err, len(repository.failed))
		}
	}

	quiet := &runRepositoryStub{run: run}
	if _, err := NewRunTurn(quiet, &agentRunnerStub{result: completed}, bundles(nil)).Execute(context.Background(), claim); err != nil || len(quiet.finished) != 1 {
		t.Fatalf("a bundle without a drawing = %v, finished %d", err, len(quiet.finished))
	}
}

// specs/2026-11-07-chat-attach-chapter.md CA3.1: the input of a turn with a chapter carries its header, signed, and never
// its text; a turn without one carries none.
func TestRunTurnHandsTheHeaderOfTheAttachedChapterToTheModel(t *testing.T) {
	header := researchdomain.Attachment{Number: 1, Title: "Глава 634", Paragraphs: []string{"а", "б", "в"}, Words: 3120}.Header()
	run := researchdomain.TurnRun{
		Kind:      agent.SessionResearch,
		ProjectID: "project-1", ChatID: "chat-1", TurnID: "chat-turn-1", JobID: "job-1",
		ActorUserID: "user-1", ClaimIncarnation: 3, SourceView: "draft", Scope: researchdomain.ScopeProject,
		Question: "Ложится ли эта глава на канон?", Attachment: header, RootThreadID: "root-1", ProviderTurnID: "provider-turn-1",
	}
	runner := &agentRunnerStub{result: agent.RunOutcome{RootThreadID: "root-1", TurnID: "provider-turn-1", Status: agent.TurnCompleted, FinalResponse: "Ответ"}}
	bundle := Bundle{SkillName: "chat-skill", Tools: []agent.ToolSpec{{Namespace: "saga", Name: "attachment"}},
		Handler: func(context.Context, agent.ToolCall) (agent.ToolResult, error) {
			return agent.ToolResult{Success: true}, nil
		}}
	if _, err := NewRunTurn(&runRepositoryStub{run: run}, runner, chatBundles(bundle)).Execute(context.Background(), researchdomain.WorkerClaim{
		ProjectID: run.ProjectID, JobID: run.JobID, ClaimIncarnation: run.ClaimIncarnation,
	}); err != nil {
		t.Fatal(err)
	}
	input := runner.request.InitialInput
	if !strings.Contains(input, `"attachment":{"number":1,"title":"Глава 634","paragraphs":3,"words":3120,"note":"`+researchdomain.AttachmentNote+`"}`) {
		t.Fatalf("initial input = %s", input)
	}
	question, ok := researchdomain.PublicAssignmentQuestion(input)
	if !ok || question.Attachment == nil || question.Attachment.Number != 1 {
		t.Fatalf("public question = %+v, %t", question, ok)
	}
}

// specs/2026-11-11-ux9-writing-b.md WB3.3: the native question of a turn a button of the page sent says so, and the
// timeline reads it back; a correction is never a press.
func TestTheInputOfATurnAButtonSentSaysSo(t *testing.T) {
	run := researchdomain.TurnRun{Question: "Написать главу", SourceView: "draft", Scope: researchdomain.ScopeProject, Button: true}
	input, err := initialAssignment(run)
	if err != nil {
		t.Fatal(err)
	}
	if question, ok := researchdomain.PublicAssignmentQuestion(input); !ok || !question.Button || question.Text != "Написать главу" {
		t.Fatalf("the question = %+v, %v (input %s)", question, ok, input)
	}
	steer, err := encodeAssignment(assignmentInput{Text: "ещё", SourceView: "draft", Scope: researchdomain.ScopeProject, Correction: true, Button: true})
	if err != nil {
		t.Fatal(err)
	}
	if question, ok := researchdomain.PublicAssignmentQuestion(steer); !ok || question.Button {
		t.Fatalf("a correction = %+v, %v", question, ok)
	}
}
