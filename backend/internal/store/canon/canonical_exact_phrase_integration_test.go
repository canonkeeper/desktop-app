package canonstore

import (
	"context"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"
)

func TestCanonicalExactPhraseReturnsWholeChapterRef(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 12500, governance.SourceViewDraft)
	secondParagraphID := indexingStoreUUIDForTest(12591)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_paragraphs SET markdown='Привет',plain_text='Привет',word_count=1 WHERE id=$1 AND variant='draft'`, fixture.paragraphID)
	mustExecCanonicalSearchTest(t, fixture, `
		INSERT INTO chapter_paragraphs (
			id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at
		) VALUES ($1,$2,1,'draft','paragraph',2,'мир','мир',1,$3,$3)
	`, secondParagraphID, fixture.chapterID, fixture.now)
	indexCanonicalSearchFixtureForTest(t, fixture)

	response, err := search.NewService(fixture.db).SearchExactPhrase(context.Background(), search.Actor{UserID: fixture.actorID}, search.ExactPhraseRequest{
		ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID,
		Query: "привет мир", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].ChapterID != fixture.chapterID ||
		response.Items[0].SourceRef != fixture.chapterID || response.Items[0].StartByte >= response.Items[0].EndByte {
		t.Fatalf("exact chapter result = %#v", response)
	}
}

func TestCanonicalExactPhraseCursorPagesByChapter(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 12600, governance.SourceViewDraft)
	siblingParagraphID := indexingStoreUUIDForTest(12691)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_paragraphs SET markdown='dragon',plain_text='dragon',word_count=1 WHERE id=$1 AND variant='draft'`, fixture.paragraphID)
	mustExecCanonicalSearchTest(t, fixture, `
		INSERT INTO chapter_paragraphs (
			id,chapter_id,revision,variant,kind,"order",markdown,plain_text,word_count,created_at,updated_at
		) VALUES ($1,$2,1,'draft','paragraph',1,'dragon','dragon',1,$3,$3)
	`, siblingParagraphID, fixture.siblingChapterID, fixture.now)
	indexCanonicalSearchFixtureForTest(t, fixture)

	service := search.NewService(fixture.db)
	request := search.ExactPhraseRequest{
		ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID,
		Query: "dragon", Limit: 1,
	}
	first, err := service.SearchExactPhrase(context.Background(), search.Actor{UserID: fixture.actorID}, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || !first.Truncated || first.NextCursor == "" {
		t.Fatalf("first exact page = %#v", first)
	}
	request.Cursor = first.NextCursor
	second, err := service.SearchExactPhrase(context.Background(), search.Actor{UserID: fixture.actorID}, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Truncated || second.NextCursor != "" ||
		second.Items[0].ChapterID == first.Items[0].ChapterID {
		t.Fatalf("second exact page = %#v", second)
	}
}

func TestCanonicalExactPhraseRejectsStaleManifestCursor(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 12700, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	input := search.ExactPhraseSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		BookID: fixture.bookID, Query: "dragon", Limit: 10,
		ExpectedManifestHash: &[32]byte{9},
	}
	if _, err := fixture.db.SearchExactPhrase(context.Background(), input); err != search.ErrCursorDrift {
		t.Fatalf("manifest drift error = %v", err)
	}
}
