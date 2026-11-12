package indexingstore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
)

type helperForStoreTest bool

func (connected helperForStoreTest) Connected(context.Context) bool { return bool(connected) }

// specs/2026-11-12-desk-window.md WN3.4: a build whose helper is not connected queues no cards Job — neither after the text
// index of a version nor when the start catches up the missing cards — so no Job ends with the refusal of a model nobody
// connected; the text is indexed and found all the same. Once the helper is there, the same catch-up queues it.
func TestNoCardsJobIsQueuedWithoutTheHelper(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 8100, governance.SourceViewPublished)
	fixture.store = New(fixture.db, WithHelper(helperForStoreTest(false)))
	indexCurrentCanonTextChaptersForTest(t, ctx, fixture)
	if err := fixture.store.CompleteCanonTextIndexForClaim(ctx, fixture.claim, canonTextSubjectForStoreTest(fixture)); err != nil {
		t.Fatal(err)
	}
	if queued, err := fixture.store.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewPublished); err != nil || queued {
		t.Fatalf("QueueChapterCards without the helper = %v, %v; want nothing queued", queued, err)
	}
	cards := "SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='chapter_cards'"
	if got := countStoreRowsForTest(t, fixture.db, cards, fixture.projectID); got != 0 {
		t.Fatalf("cards jobs without the helper = %d, want none", got)
	}
	if got := countStoreRowsForTest(t, fixture.db, "SELECT count(*) FROM chapter_text_index_state WHERE project_id=$1 AND source_view='published'", fixture.projectID); got == 0 {
		t.Fatal("the text of the version is not indexed")
	}

	connected := New(fixture.db, WithHelper(helperForStoreTest(true)))
	if queued, err := connected.QueueChapterCards(ctx, fixture.projectID, governance.SourceViewPublished); err != nil || !queued {
		t.Fatalf("QueueChapterCards with the helper = %v, %v; want the Job queued", queued, err)
	}
	if got := countStoreRowsForTest(t, fixture.db, cards, fixture.projectID); got != 1 {
		t.Fatalf("cards jobs once the helper is there = %d, want 1", got)
	}
}
