package authormaterials

import (
	"context"
	"errors"
	"strings"
	"testing"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
)

type fakeRunStore struct {
	parsed  domain.Parsed
	outcome *ImportOutcome
}

func (store *fakeRunStore) ImportParsed(context.Context, jobs.WorkerClaim, string) (domain.Parsed, error) {
	return store.parsed, nil
}

func (store *fakeRunStore) FinishImport(_ context.Context, _ jobs.WorkerClaim, outcome ImportOutcome) error {
	store.outcome = &outcome
	return nil
}

type fakeModel struct {
	answers []string
	inputs  []string
	err     error
}

func (model *fakeModel) Complete(_ context.Context, _, input string) (string, quota.ModelUsage, error) {
	model.inputs = append(model.inputs, input)
	if model.err != nil {
		return "", quota.ModelUsage{}, model.err
	}
	answer := model.answers[0]
	if len(model.answers) > 1 {
		model.answers = model.answers[1:]
	}
	return answer, quota.ModelUsage{}, nil
}

var runClaim = jobs.WorkerClaim{ProjectID: "10000000-0000-4000-8000-000000000001", JobID: "10000000-0000-4000-8000-000000000002", ClaimIncarnation: 1}

var runSubject = jobs.AuthorMaterialsImportSubject{
	Type: jobs.KindAuthorMaterialsImport, ProjectID: runClaim.ProjectID, ImportID: "10000000-0000-4000-8000-000000000003",
}

func TestRunMapsSectionsAndKeepsTheNotesFromTheModel(t *testing.T) {
	store := &fakeRunStore{parsed: domain.Parsed{Sections: []domain.Section{{
		Title: "Навыки",
		Rows: [][]domain.Cell{
			{{Text: "Навык"}, {Text: "Редкость"}},
			{{Text: "омут снов", Note: "Секретное описание.\nРанг: Уникальный"}, {Text: "эпический", Look: "оранжевая заливка"}},
		},
	}}}}
	model := &fakeModel{answers: []string{"```json\n{\"kind\":\"skill\",\"header\":true,\"name\":0,\"description\":null}\n```"}}
	status, err := RunImport{Store: store, Model: model}.RunForClaim(context.Background(), runClaim, runSubject)
	if err != nil || status != jobs.StatusSucceeded {
		t.Fatalf("status = %s, err = %v", status, err)
	}
	if strings.Contains(model.inputs[0], "Секретное") || strings.Contains(model.inputs[0], "Уникальный") {
		t.Fatalf("a note reached the model: %s", model.inputs[0])
	}
	drafts := store.outcome.Candidates
	if store.outcome.Status != jobs.StatusSucceeded || len(drafts) != 1 || drafts[0].Name != "омут снов" || drafts[0].Kind != domain.KindSkill {
		t.Fatalf("outcome = %+v", store.outcome)
	}
	if drafts[0].Description != "Секретное описание." {
		t.Fatalf("description = %q", drafts[0].Description)
	}
}

func TestRunAsksAgainAndFailsOnAnAnswerThatDoesNotRead(t *testing.T) {
	store := &fakeRunStore{parsed: domain.Parsed{Sections: []domain.Section{{Rows: [][]domain.Cell{{{Text: "Имя"}}}}}}}
	model := &fakeModel{answers: []string{"не знаю"}}
	status, err := RunImport{Store: store, Model: model}.RunForClaim(context.Background(), runClaim, runSubject)
	if err != nil || status != jobs.StatusFailed || store.outcome.Error == nil || store.outcome.Error.Code != answerFailed {
		t.Fatalf("status = %s, err = %v, outcome = %+v", status, err, store.outcome)
	}
	if len(model.inputs) != callAttempts {
		t.Fatalf("calls = %d", len(model.inputs))
	}
}

func TestRunLeavesAFailingModelToTheWorker(t *testing.T) {
	store := &fakeRunStore{parsed: domain.Parsed{Sections: []domain.Section{{Rows: [][]domain.Cell{{{Text: "Имя"}}}}}}}
	failure := errors.New("gateway down")
	model := &fakeModel{err: failure}
	_, err := RunImport{Store: store, Model: model}.RunForClaim(context.Background(), runClaim, runSubject)
	if !errors.Is(err, failure) || store.outcome != nil {
		t.Fatalf("err = %v, outcome = %+v", err, store.outcome)
	}
}

// endsOfTheTest hears the ends of the Jobs.
type endsOfTheTest struct{ ends []jobs.Status }

func (ends *endsOfTheTest) Ended(_ context.Context, _ string, outcome jobs.Status) {
	ends.ends = append(ends.ends, outcome)
}

// specs/2026-11-13-desk-account-ai.md AI3.8: the end of an upload the run committed closes its permit — succeeded, failed,
// canceled —; a failure the worker repeats is no end.
func TestTheEndOfAnUploadIsTold(t *testing.T) {
	ends := &endsOfTheTest{}
	store := &fakeRunStore{parsed: domain.Parsed{Sections: []domain.Section{{Rows: [][]domain.Cell{{{Text: "Имя"}}}}}}}
	if _, err := (RunImport{Store: store, Model: &fakeModel{err: errors.New("gateway down")}, Ended: ends}).RunForClaim(context.Background(), runClaim, runSubject); err == nil || len(ends.ends) != 0 {
		t.Fatalf("a failure the worker repeats ended the Job: %v", ends.ends)
	}
	run := RunImport{Store: store, Model: &fakeModel{answers: []string{"не знаю"}}, Ended: ends}
	if status, _ := run.RunForClaim(context.Background(), runClaim, runSubject); status != jobs.StatusFailed {
		t.Fatal(status)
	}
	_ = run.CancelForClaim(context.Background(), runClaim)
	_, _ = run.FailForClaim(context.Background(), runClaim, jobs.Error{Code: "x"})
	if len(ends.ends) != 3 || ends.ends[0] != jobs.StatusFailed || ends.ends[1] != jobs.StatusCanceled || ends.ends[2] != jobs.StatusFailed {
		t.Fatalf("ends = %v", ends.ends)
	}
}
