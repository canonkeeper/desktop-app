package sagamemory

import (
	"context"
	"testing"
)

// specs/2026-11-15-desk-reading-queue.md RQ2.1, RQ2.3: a saga the model may not read now — its reading paused, no consent that
// acts, no helper: the one rule of the builders, ReadingAllowed — gets a cards Job from the catch-up only for what the Job does
// without the model: the map of the cards there are, when it is behind. A chapter without a card, a register to build and
// names that wait queue nothing; a saga without a card has no map, so it gets no Job either. The reading allowed queues as
// before; the site — no policy — keeps its rule.
func TestTheCatchUpQueuesASagaTheModelMayNotReadOnlyForItsMap(t *testing.T) {
	text := cardsTextSource{chapters: []SelectedChapter{
		{ID: "c1", ContentHash: "h1", BookOrder: 1, Order: 1, Title: "Глава 1. Старт"},
		{ID: "c2", ContentHash: "h2", BookOrder: 1, Order: 2, Title: "Глава 2. Путь"},
	}}
	versions := indexedVersions{{ProjectID: "saga", View: "published"}}
	partly := versionCards{"saga": {{ChapterID: "c1", ContentHash: "h1"}: true}}
	bare := versionCards{"saga": {}}
	closed := &readingPolicyOfTheTest{closed: true}
	count := func(useCase *QueueMissingCards, one bool) int {
		t.Helper()
		var queued int
		var err error
		if one {
			queued, err = useCase.ExecuteFor(context.Background(), "saga")
		} else {
			queued, err = useCase.Execute(context.Background())
		}
		if err != nil {
			t.Fatal(err)
		}
		return queued
	}
	for _, one := range []bool{false, true} {
		fresh := namedVersions{versionCards: partly, digests: map[string]string{"saga": ""}, text: text}
		if got := count(NewQueueMissingCards(versions, text, fresh, &cardsQueue{}).WithPolicy(closed), one); got != 0 {
			t.Fatalf("one saga %v: a chapter without a card, no register and the map fresh queued %d; want nothing", one, got)
		}
		behind := namedVersions{versionCards: partly, digests: map[string]string{"saga": ""}, text: text, behind: map[string]bool{"saga": true}}
		if got := count(NewQueueMissingCards(versions, text, behind, &cardsQueue{}).WithPolicy(closed), one); got != 1 {
			t.Fatalf("one saga %v: the map behind queued %d; want the Job that builds it without the model", one, got)
		}
		cardless := namedVersions{versionCards: bare, digests: map[string]string{"saga": ""}, text: text, behind: map[string]bool{"saga": true}}
		if got := count(NewQueueMissingCards(versions, text, cardless, &cardsQueue{}).WithPolicy(closed), one); got != 0 {
			t.Fatalf("one saga %v: a saga without a card queued %d; it has no map to build", one, got)
		}
		if got := count(NewQueueMissingCards(versions, text, fresh, &cardsQueue{}).WithPolicy(&readingPolicyOfTheTest{}), one); got != 1 {
			t.Fatalf("one saga %v: the reading allowed queued %d; want the Job", one, got)
		}
		if got := count(NewQueueMissingCards(versions, text, fresh, &cardsQueue{}), one); got != 1 {
			t.Fatalf("one saga %v: the site queued %d; want its rule", one, got)
		}
	}
}
