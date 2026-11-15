package sagamemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"canonkeeper/backend/internal/domain/quota"
	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/sagamemory"
	indexingstore "canonkeeper/backend/internal/store/indexing"
	"canonkeeper/backend/internal/testutil"
)

// noReading is the policy of a build whose helper is not connected: the model reads no book.
type noReading struct{}

func (noReading) ReadingAllowed(context.Context, string, bool) (bool, error) { return false, nil }

func (noReading) Unread(context.Context, string, int) (map[service.CardKey]bool, error) {
	return nil, nil
}

func (noReading) MarkUnread(context.Context, string, service.CardKey, int, string) error { return nil }

func (noReading) MarksChapters(context.Context) bool { return false }

func (noReading) ChaptersAtOnce(context.Context) int { return 0 }

func (noReading) NamesUnread(context.Context, string, string) (string, error) { return "", nil }

func (noReading) MarkNamesUnread(context.Context, string, string, string, string) error { return nil }

// refusingModel writes no card of the chapter whose text holds refuse.
type refusingModel struct {
	*namesCardModel
	refuse string
}

func (model refusingModel) Complete(ctx context.Context, instructions, input string) (string, quota.ModelUsage, error) {
	if strings.Contains(input, model.refuse) {
		return "", quota.ModelUsage{Calls: 1}, errors.New("no card of this chapter")
	}
	return model.namesCardModel.Complete(ctx, instructions, input)
}

// specs/2026-11-13-desk-gateway.md GW4.5: the memory there is works without the helper. A saga whose every chapter has its
// card and whose register of names is fresh gets its cards Job without the helper: it builds the map of the cards and
// calls no model. A saga with a part of the cards gets its Job too: the map of the cards there are, no model, and the
// chapters without cards wait for the helper. A saga without cards gets no Job, as before.
func TestTheMemoryThereIsWorksWithoutTheHelper(t *testing.T) {
	for _, part := range []struct {
		name, refuse string
		status       jobs.Status
	}{
		{"every chapter carded", "", jobs.StatusSucceeded},
		{"a part carded", "Ёлки и дорога.", jobs.StatusFailed},
	} {
		t.Run(part.name, func(t *testing.T) {
			db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_without_helper")
			seedMemoryProject(t, db)
			testutil.SeedSagaSearchRows(t, db, memoryProjectID)
			ctx := context.Background()
			withHelper := New(db).WithTextIndex(indexingstore.New(db, indexingstore.WithHelper(helperForCatchUpTest(true))))
			model := &namesCardModel{}
			var cardModel service.CardModel = model
			if part.refuse != "" {
				cardModel = refusingModel{namesCardModel: model, refuse: part.refuse}
			}
			// The helper read the books once, before the map existed.
			before := service.NewBuildCards(withHelper, withHelper, cardModel, nil, "test-model", 2, nil, nil)
			if _, err := before.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true); err != nil {
				t.Fatal(err)
			}
			cards, registry := model.count(), model.registryCalls
			if countCardsForTest(t, db) == 0 {
				t.Fatal("no card was built")
			}

			without := New(db).WithTextIndex(indexingstore.New(db, indexingstore.WithHelper(helperForCatchUpTest(false))))
			if queued, err := service.NewQueueMissingCards(without, without, without, without).Execute(ctx); err != nil || queued != 1 {
				t.Fatalf("the catch-up without the helper = %d, %v; want the Job of the map", queued, err)
			}
			build := mapCardsJob(t, without, model).WithPolicy(noReading{})
			status, err := build.RunForClaim(ctx, claimCardsJobForTest(t, db), cardsSubjectForTest(), true)
			if err != nil || status != part.status {
				t.Fatalf("the Job without the helper = %q, %v; want %q", status, err, part.status)
			}
			if graphs, nodes, _ := mapRowsForTest(t, db); graphs != 1 || nodes == 0 {
				t.Fatalf("the map without the helper: %d graphs, %d nodes", graphs, nodes)
			}
			if model.count() != cards || model.registryCalls != registry {
				t.Fatalf("the Job without the helper called the model: %d cards, %d registers", model.count()-cards, model.registryCalls-registry)
			}
		})
	}

	// A saga without a card gets no Job without the helper (specs/2026-11-12-desk-window.md WN3.4).
	db := testutil.OpenIsolatedPostgresTestDB(t, "ck_saga_memory_without_helper_cards")
	seedMemoryProject(t, db)
	testutil.SeedSagaSearchRows(t, db, memoryProjectID)
	without := New(db).WithTextIndex(indexingstore.New(db, indexingstore.WithHelper(helperForCatchUpTest(false))))
	if queued, err := service.NewQueueMissingCards(without, without, without, without).Execute(context.Background()); err != nil || queued != 0 || cardsJobsForTest(t, db) != 0 {
		t.Fatalf("a saga without cards without the helper = %d, %v", queued, err)
	}
}
