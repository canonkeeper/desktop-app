package authormaterials

import (
	"context"
	"slices"
	"testing"

	domain "canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/modelthread"
	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
)

// threadedMapModel keeps the thread of each call.
type threadedMapModel struct {
	threads [][]string
}

func (model *threadedMapModel) Complete(ctx context.Context, _, _ string) (string, quota.ModelUsage, error) {
	parts, _ := modelthread.Of(ctx)
	model.threads = append(model.threads, parts)
	return `{"kind":"skill","header":true,"name":0,"description":null}`, quota.ModelUsage{}, nil
}

// specs/2026-11-17-model-cache.md MC1.3: every call of the parsing of the author's materials goes under the thread of the
// materials of its Job.
func TestTheParsingGoesUnderTheThreadOfItsJob(t *testing.T) {
	sections := []domain.Section{
		{Title: "Навыки", Rows: [][]domain.Cell{{{Text: "Навык"}}, {{Text: "омут снов"}}}},
		{Title: "Места", Rows: [][]domain.Cell{{{Text: "Место"}}, {{Text: "Застава"}}}},
	}
	model := &threadedMapModel{}
	store := &fakeRunStore{parsed: domain.Parsed{Sections: sections}}
	if _, err := (RunImport{Store: store, Model: model}).RunForClaim(jobs.WithJobID(context.Background(), runClaim.JobID), runClaim, runSubject); err != nil {
		t.Fatal(err)
	}
	if len(model.threads) != 2 {
		t.Fatalf("%d calls", len(model.threads))
	}
	for _, parts := range model.threads {
		if !slices.Equal(parts, []string{"materials", runClaim.JobID}) {
			t.Fatalf("a call went under %q", parts)
		}
	}
}
