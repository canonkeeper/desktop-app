package canonstore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"
)

func TestCanonicalLexicalReturnsOneWholeChapterRow(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 8950, governance.SourceViewDraft)
	secondParagraphID := indexingStoreUUIDForTest(8959)
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		UPDATE chapter_paragraphs
		SET markdown='Дракон проснулся.', plain_text='Дракон проснулся.', word_count=2
		WHERE id=$1 AND variant='draft'
	`, fixture.paragraphID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.Pool.Exec(context.Background(), `
		INSERT INTO chapter_paragraphs (
			id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at
		) VALUES ($1,$2,1,'draft','paragraph',2,'Герой увидел дракона.','Герой увидел дракона.',3,$3,$3)
	`, secondParagraphID, fixture.chapterID, fixture.now); err != nil {
		t.Fatal(err)
	}
	indexCanonicalSearchFixtureForTest(t, fixture)

	got, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "дракон", Limit: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("chapter rows = %d, want one", len(got.Rows))
	}
	row := got.Rows[0]
	if row.ID != fixture.chapterID || row.ChapterSource == nil || row.ChapterSource.ContentHash == (governance.Digest{}) {
		t.Fatalf("chapter identity leaked chunk locator: %#v", row)
	}
	if row.Content != "Дракон проснулся.\n\nГерой увидел дракона." {
		t.Fatalf("chapter content = %q", row.Content)
	}

	if row.ParagraphMatch == nil || row.ParagraphMatch.ID != fixture.paragraphID || row.ParagraphMatch.Position != 1 {
		t.Fatalf("first persisted matching paragraph = %#v", row.ParagraphMatch)
	}
	for _, query := range []string{"герой дракон", "проснулся герой"} {
		matched, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
			ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
			Query: query, Limit: 11,
		})
		if err != nil || len(matched.Rows) != 1 {
			t.Fatalf("query %q: rows=%d, %v", query, len(matched.Rows), err)
		}
		paragraph := matched.Rows[0].ParagraphMatch
		if query == "герой дракон" {
			if paragraph == nil || paragraph.ID != secondParagraphID || paragraph.Position != 2 {
				t.Fatalf("full-query paragraph = %#v", paragraph)
			}
		} else if paragraph != nil {
			t.Fatalf("cross-paragraph match invented a paragraph: %#v", paragraph)
		}
	}
}

func TestCanonicalSearchSourceAnchorUsesPersistedView(t *testing.T) {
	for index, view := range []governance.SourceView{governance.SourceViewDraft, governance.SourceViewPublished} {
		t.Run(string(view), func(t *testing.T) {
			fixture := newCanonicalIndexStoreFixture(t, 28510+index*20, view)
			indexCanonicalSearchFixtureForTest(t, fixture)
			response, err := search.NewService(fixture.db).Search(context.Background(), search.Actor{UserID: fixture.actorID}, search.SearchRequest{
				ProjectID: fixture.projectID, SourceView: view, Query: "dragon", Scope: search.SearchScopeChapters,
			})
			if err != nil || len(response.Items) != 1 {
				t.Fatalf("search items=%d, error=%v", len(response.Items), err)
			}
			locator := response.Items[0].Locator
			anchor := locator.SourceAnchor
			if anchor.Kind != "paragraph" || anchor.Locator.ParagraphID == nil || *anchor.Locator.ParagraphID != fixture.paragraphID ||
				anchor.Locator.TargetView != string(view) || anchor.Locator.Revision != 1 || locator.SourceRef != fixture.chapterID {
				t.Fatalf("search source anchor = %#v", anchor)
			}
		})
	}
}
