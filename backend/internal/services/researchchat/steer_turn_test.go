package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

type steerRepositoryStub struct {
	session  researchdomain.Session
	target   SteerTarget
	begin    BeginSteerCommand
	beginErr error
	reloads  []error
	finished []bool
}

func (stub *steerRepositoryStub) GetSession(context.Context, researchdomain.Actor, string) (researchdomain.Session, error) {
	return stub.session, nil
}

func (stub *steerRepositoryStub) BeginSteer(_ context.Context, command BeginSteerCommand) (SteerTarget, error) {
	stub.begin = command
	return stub.target, stub.beginErr
}

func (stub *steerRepositoryStub) ReloadSteerTarget(context.Context, SteerTarget) (SteerTarget, error) {
	if len(stub.reloads) == 0 {
		return stub.target, nil
	}
	err := stub.reloads[0]
	stub.reloads = stub.reloads[1:]
	return stub.target, err
}

func (stub *steerRepositoryStub) FinishSteer(_ context.Context, _ SteerTarget, accepted bool) error {
	stub.finished = append(stub.finished, accepted)
	return nil
}

type steerClock struct{}

func (steerClock) Now() time.Time { return time.Unix(1_800_000_000, 0) }

func steerFixture() *steerRepositoryStub {
	return &steerRepositoryStub{
		session: researchdomain.Session{
			ID: "chat-1", ProjectID: "project-1", SourceView: "published", Scope: researchdomain.ScopeProject,
			AgentGeneration: researchdomain.CurrentAgentGeneration,
		},
		target: SteerTarget{
			RecordID: "record-1", ProjectID: "project-1", JobID: "job-1", RootThreadID: "root-1", ProviderTurnID: "native-turn-1",
			ImageTitles: map[int]string{90: "Анна"},
		},
	}
}

func steerInput() SteerTurnInput {
	return SteerTurnInput{
		Text: "  Без дракона, лицо с фото 3  ", Images: []int{90}, FirstPhoto: 3, IdempotencyKey: "steer-key-1",
		RequestMethod: "POST", RequestPath: "/chats/chat-1/turns/turn-1/steer",
		SessionWriteProof: auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"},
	}
}

// specs/2026-10-30-chat-steering.md R1.3, R1.4: the correction goes into the bound native turn, in
// the format of the input of a turn, with the anchors going on from the first photo the web named.
func TestSteerTurnSendsTheInputOfATurnIntoTheBoundTurn(t *testing.T) {
	repository := steerFixture()
	var sent []TurnSteerRequest
	useCase := NewSteerTurn(repository, func(_ context.Context, request TurnSteerRequest) error {
		sent = append(sent, request)
		return nil
	}, steerClock{})

	steerID, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].RootThreadID != "root-1" || sent[0].TurnID != "native-turn-1" || sent[0].ClientMessageID != steerID || !strings.HasPrefix(steerID, "steer-") {
		t.Fatalf("sent = %+v, id = %q", sent, steerID)
	}
	var assignment researchdomain.Assignment
	if err := json.Unmarshal([]byte(sent[0].Input), &assignment); err != nil {
		t.Fatal(err)
	}
	if assignment.Question != "Без дракона, лицо с фото 3" || !assignment.Correction || assignment.FirstPhoto != 3 ||
		len(assignment.AttachedPhotos) != 1 || assignment.AttachedPhotos[0] != "фото 3 — изображение 90 (Анна)" ||
		assignment.SourceView != "published" || assignment.Scope != researchdomain.ScopeProject || assignment.ProjectRef != "project" {
		t.Fatalf("assignment = %+v", assignment)
	}
	question, ok := researchdomain.PublicAssignmentQuestion(sent[0].Input)
	if !ok || !question.Correction || question.Text != assignment.Question || question.FirstPhoto != 3 {
		t.Fatalf("the feed does not read the input: %+v, %v", question, ok)
	}
	if len(repository.finished) != 1 || !repository.finished[0] {
		t.Fatalf("finished = %v", repository.finished)
	}
}

// R1.5: a repeat of an accepted key answers the same id and sends nothing.
func TestSteerTurnReplayDoesNotSendAgain(t *testing.T) {
	repository := steerFixture()
	calls := 0
	useCase := NewSteerTurn(repository, func(context.Context, TurnSteerRequest) error {
		calls++
		return nil
	}, steerClock{})
	first, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput())
	if err != nil {
		t.Fatal(err)
	}
	repository.target.Replayed = true
	second, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput())
	if err != nil || second != first || calls != 1 {
		t.Fatalf("replay = %q, %v, calls %d, first %q", second, err, calls, first)
	}
}

// R1.6, R2.2: a compaction passes: the retry goes through; one that does not pass is chat.steer_retry
// and lets go of the key.
func TestSteerTurnRetriesATurnThatTakesNoInputForAMoment(t *testing.T) {
	repository := steerFixture()
	calls := 0
	useCase := NewSteerTurn(repository, func(context.Context, TurnSteerRequest) error {
		calls++
		if calls < 3 {
			return agent.ErrTurnNotSteerable
		}
		return nil
	}, steerClock{})
	useCase.delays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	if _, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput()); err != nil || calls != 3 {
		t.Fatalf("retry = %v, calls %d", err, calls)
	}

	repository = steerFixture()
	calls = 0
	useCase = NewSteerTurn(repository, func(context.Context, TurnSteerRequest) error {
		calls++
		return agent.ErrTurnNotActive
	}, steerClock{})
	useCase.delays = []time.Duration{time.Millisecond, time.Millisecond}
	_, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput())
	if !errors.Is(err, researchdomain.ErrSteerRetry) || calls != 3 || len(repository.finished) != 1 || repository.finished[0] {
		t.Fatalf("exhausted = %v, calls %d, finished %v", err, calls, repository.finished)
	}
}

// R2.1: the Job ended while the correction waited.
func TestSteerTurnStopsWhenTheJobEnds(t *testing.T) {
	repository := steerFixture()
	repository.reloads = []error{researchdomain.ErrTurnEnded}
	calls := 0
	useCase := NewSteerTurn(repository, func(context.Context, TurnSteerRequest) error {
		calls++
		return agent.ErrTurnNotActive
	}, steerClock{})
	useCase.delays = []time.Duration{time.Millisecond, time.Millisecond}
	_, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput())
	if !errors.Is(err, researchdomain.ErrTurnEnded) || calls != 1 || len(repository.finished) != 1 || repository.finished[0] {
		t.Fatalf("ended = %v, calls %d, finished %v", err, calls, repository.finished)
	}
}

// R2.3: a failure is not retried and lets go of the key.
func TestSteerTurnDoesNotRetryAFailure(t *testing.T) {
	repository := steerFixture()
	calls := 0
	boom := errors.New("boom")
	useCase := NewSteerTurn(repository, func(context.Context, TurnSteerRequest) error {
		calls++
		return boom
	}, steerClock{})
	_, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput())
	if !errors.Is(err, boom) || calls != 1 || len(repository.finished) != 1 || repository.finished[0] {
		t.Fatalf("failure = %v, calls %d, finished %v", err, calls, repository.finished)
	}
}

// R1.1, R1.2: the limits of a question, and an outdated chat as for a question.
func TestSteerTurnValidatesAsAQuestion(t *testing.T) {
	refuse := func(name string, change func(*SteerTurnInput), want error) {
		t.Helper()
		input := steerInput()
		change(&input)
		useCase := NewSteerTurn(steerFixture(), func(context.Context, TurnSteerRequest) error { return nil }, steerClock{})
		if _, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", input); !errors.Is(err, want) {
			t.Fatalf("%s = %v, want %v", name, err, want)
		}
	}
	refuse("empty", func(input *SteerTurnInput) { input.Text = "  " }, researchdomain.ErrValidation)
	refuse("too long", func(input *SteerTurnInput) { input.Text = strings.Repeat("а", 8001) }, researchdomain.ErrValidation)
	refuse("nine images", func(input *SteerTurnInput) { input.Images = []int{1, 2, 3, 4, 5, 6, 7, 8, 9} }, researchdomain.ErrValidation)
	refuse("same image twice", func(input *SteerTurnInput) { input.Images = []int{1, 1} }, researchdomain.ErrValidation)
	refuse("images without anchor", func(input *SteerTurnInput) { input.FirstPhoto = 0 }, researchdomain.ErrValidation)
	refuse("anchor without images", func(input *SteerTurnInput) { input.Images = nil }, researchdomain.ErrValidation)
	refuse("no key", func(input *SteerTurnInput) { input.IdempotencyKey = "" }, researchdomain.ErrValidation)

	repository := steerFixture()
	repository.session.AgentGeneration = 1
	useCase := NewSteerTurn(repository, func(context.Context, TurnSteerRequest) error { return nil }, steerClock{})
	if _, err := useCase.Execute(t.Context(), researchdomain.Actor{UserID: "user-1"}, "chat-1", "turn-1", steerInput()); !errors.Is(err, researchdomain.ErrOutdatedAgent) {
		t.Fatalf("outdated = %v", err)
	}
}
