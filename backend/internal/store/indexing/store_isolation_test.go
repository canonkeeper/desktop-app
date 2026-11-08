package indexingstore

import (
	"context"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

func TestStoreReplacePreservesOppositeView(t *testing.T) {
	for index, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		t.Run(string(view), func(t *testing.T) {
			fixture := newStoreFixture(t, 7800+index*30, view)
			otherView := governance.SourceViewPublished
			if view == governance.SourceViewPublished {
				otherView = governance.SourceViewDraft
			}
			other := fixture
			other.view = otherView
			other.claim = addCanonicalIndexClaimForTest(t, other, 20)
			otherSnapshot := other.snapshot(t)
			if _, err := other.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{Snapshot: otherSnapshot, IndexedAt: fixture.now}); err != nil {
				t.Fatal(err)
			}
			snapshot := fixture.snapshot(t)
			if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: fixture.now.Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			assertCanonicalIndexRows(t, fixture, view, fixture.chapterID, 1)
			assertCanonicalIndexRows(t, other, otherView, fixture.chapterID, 1)
		})
	}
}
