package authormaterials

import (
	"context"
	"errors"
	"testing"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
)

// namedMapModel keeps the sections each call named; refuse makes the journal of the app send nothing.
type namedMapModel struct {
	named  []disclosure.Named
	refuse bool
}

func (model *namedMapModel) Complete(ctx context.Context, _, _ string) (string, quota.ModelUsage, error) {
	named, _ := disclosure.Of(ctx)
	model.named = append(model.named, named)
	if model.refuse {
		return "", quota.ModelUsage{}, &disclosure.JournalError{Code: disclosure.CodeUnavailable}
	}
	return `{"kind":"skill","header":true,"name":0,"description":null}`, quota.ModelUsage{}, nil
}

// specs/2026-11-14-desk-journal.md JR1.3, JR1.6: a call of the parsing names the section of the file or text of the author it
// gives by its number; one the journal did not send is not asked again and goes to the worker, with no outcome.
func TestTheParsingNamesItsSection(t *testing.T) {
	sections := []domain.Section{
		{Title: "Навыки", Rows: [][]domain.Cell{{{Text: "Навык"}}, {{Text: "омут снов"}}}},
		{Title: "Места", Rows: [][]domain.Cell{{{Text: "Место"}}, {{Text: "Застава"}}}},
	}
	model := &namedMapModel{}
	store := &fakeRunStore{parsed: domain.Parsed{Sections: sections}}
	if _, err := (RunImport{Store: store, Model: model}).RunForClaim(context.Background(), runClaim, runSubject); err != nil {
		t.Fatal(err)
	}
	if len(model.named) != 2 || len(model.named[0].Sections) != 1 || model.named[0].Sections[0] != 1 || model.named[1].Sections[0] != 2 {
		t.Fatalf("the calls named %+v", model.named)
	}
	refused := &namedMapModel{refuse: true}
	store = &fakeRunStore{parsed: domain.Parsed{Sections: sections}}
	_, err := (RunImport{Store: store, Model: refused}).RunForClaim(context.Background(), runClaim, runSubject)
	if !errors.Is(err, disclosure.ErrJournal) || store.outcome != nil || len(refused.named) != 1 {
		t.Fatalf("err = %v, outcome = %+v, calls %d", err, store.outcome, len(refused.named))
	}
}
