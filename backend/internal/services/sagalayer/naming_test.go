package sagalayer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/services/sagamemory/sagamemorytest"
)

// namedLayerModel keeps what each call of a step named in its context, the calls of the checks running in parallel.
type namedLayerModel struct {
	*layerModel
	mu    sync.Mutex
	named map[string][]disclosure.Named
}

func (model *namedLayerModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	named, _ := disclosure.Of(ctx)
	model.mu.Lock()
	if model.named == nil {
		model.named = map[string][]disclosure.Named{}
	}
	model.named[stepName(instructions)] = append(model.named[stepName(instructions)], named)
	model.mu.Unlock()
	return model.layerModel.Complete(ctx, instructions, input)
}

func precisions(named disclosure.Named) map[string]disclosure.Precision {
	byID := map[string]disclosure.Precision{}
	for _, chapter := range named.Chapters {
		byID[chapter.ID] = chapter.Precision
	}
	return byID
}

// specs/2026-11-14-desk-journal.md JR1.3: a book names what the helper learned of its chapters, the reduction the threads of
// the saga, a check of a thread its chapters whole — or their beginning — and what the helper learned of the later ones,
// «что упущено» the threads and what the helper learned of the chapters; each in the context of its own call, the checks in
// parallel.
func TestEveryStepOfTheLayerNamesWhatItGives(t *testing.T) {
	model := &namedLayerModel{layerModel: newLayerModel()}
	version := layerVersion("h1")
	long := strings.Repeat("Длинный абзац главы. ", 2000)
	version.Chapters[2].Paragraphs = append(version.Chapters[2].Paragraphs, sagamemory.Paragraph{Text: long})
	store := &layerStore{stamps: stampsOf(version)}
	build := NewBuildLayer(sagamemorytest.NewSaga(version, nil, layerCards()), store, model, &layerUsage{}, "test-model", 4, nil)
	if _, err := build.RunForClaim(context.Background(), layerClaim, layerSubject); err != nil {
		t.Fatal(err)
	}
	books := model.named["map"]
	if len(books) != 2 {
		t.Fatalf("the books named %+v", books)
	}
	for _, book := range books {
		for _, precision := range precisions(book) {
			if precision != disclosure.Cards || book.Threads {
				t.Fatalf("a book named %+v", book)
			}
		}
	}
	if reduce := model.named["reduce"]; len(reduce) != 1 || !reduce[0].Threads || len(reduce[0].Chapters) != 0 {
		t.Fatalf("the reduction named %+v", reduce)
	}
	if gap := model.named["gap"]; len(gap) != 1 || !gap[0].Threads || len(precisions(gap[0])) != 3 || precisions(gap[0])["c1"] != disclosure.Cards {
		t.Fatalf("«что упущено» named %+v", gap)
	}
	checks := model.named["verify"]
	if len(checks) == 0 {
		t.Fatal("no check named anything")
	}
	beginning := false
	for _, check := range checks {
		if len(check.Chapters) == 0 {
			t.Fatalf("a check named %+v", check)
		}
		for id, precision := range precisions(check) {
			if precision == disclosure.Beginning && id != "c3" || precision == disclosure.Excerpts || precision == disclosure.Titles {
				t.Fatalf("a check named %s as %s", id, precision)
			}
			beginning = beginning || precision == disclosure.Beginning
		}
	}
	if !beginning {
		t.Fatalf("the long chapter went whole: %+v", checks)
	}
}

// JR1.6: a check of a thread the journal of the app did not send writes no verdict, is not asked again and fails the run for
// the worker to repeat; it is no stop of the reading.
func TestACheckTheJournalDidNotSendWritesNoVerdict(t *testing.T) {
	inner := newLayerModel()
	inner.verify = func(string) (string, error) {
		return "", &disclosure.JournalError{Code: disclosure.CodeUnavailable}
	}
	model := &namedLayerModel{layerModel: inner}
	store, build := layerFixture(inner, &layerUsage{})
	build.model = model
	status, err := build.RunForClaim(context.Background(), layerClaim, layerSubject)
	if !errors.Is(err, disclosure.ErrJournal) || status != "" || store.outcome != nil {
		t.Fatalf("RunForClaim() = %q, %v, outcome %+v", status, err, store.outcome)
	}
	if _, stopped := sagamemory.StopOf(err); stopped {
		t.Fatal("a refusal of the journal stopped the reading")
	}
	if store.stored != nil && store.stored.Steps != nil && (len(store.stored.Steps.Verdicts) != 0 || len(store.stored.Steps.GapVerdicts) != 0) {
		t.Fatalf("verdicts were written: %+v", store.stored.Steps.Verdicts)
	}
	if inner.count("verify") > 4 {
		t.Fatalf("the checks were asked again: %d", inner.count("verify"))
	}
}
