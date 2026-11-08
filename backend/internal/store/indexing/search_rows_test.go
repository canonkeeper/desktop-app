package indexingstore

import (
	"context"
	"sync"
	"testing"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
)

type storedSearchText struct {
	texts, words, wrongStems int
	hash                     []byte
	orders, tokens           []int32
}

func readStoredSearchText(t *testing.T, f storeFixture) storedSearchText {
	t.Helper()
	var stored storedSearchText
	if err := f.db.Pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM chapter_search_texts WHERE project_id=$1 AND chapter_id=$2),
		       (SELECT count(*) FROM chapter_search_words w JOIN chapter_search_texts t ON t.id=w.text_id WHERE t.project_id=$1 AND t.chapter_id=$2),
		       (SELECT count(*) FROM chapter_search_words w JOIN chapter_search_texts t ON t.id=w.text_id WHERE t.project_id=$1 AND t.chapter_id=$2
		          AND w.stem <> coalesce(array_to_string(ts_lexize('russian_stem', w.word), ' '), w.word))
	`, f.projectID, f.chapterID).Scan(&stored.texts, &stored.words, &stored.wrongStems); err != nil {
		t.Fatal(err)
	}
	if stored.texts == 1 {
		if err := f.db.Pool.QueryRow(context.Background(), `
			SELECT content_hash, paragraph_orders, paragraph_tokens FROM chapter_search_texts WHERE project_id=$1 AND chapter_id=$2
		`, f.projectID, f.chapterID).Scan(&stored.hash, &stored.orders, &stored.tokens); err != nil {
			t.Fatal(err)
		}
	}
	return stored
}

// The text index writes the saga search rows of a chapter text in the transaction of its state
// (specs/2026-10-28-saga-search-in-postgres.md, UC1, UC5).
func TestChapterTextIndexWritesSagaSearchRows(t *testing.T) {
	ctx := context.Background()
	fixture := newStoreFixture(t, 7400, governance.SourceViewDraft)
	text := "Тихо-тихо, кот Кутузова ждал у двери."
	setDraftChapterTextForTest(t, fixture, text)
	replace := func() CanonTextChapterSnapshot {
		t.Helper()
		snapshot := fixture.snapshot(t)
		if _, err := fixture.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: snapshot, IndexedAt: fixture.now}); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}

	snapshot := replace()
	stored := readStoredSearchText(t, fixture)
	words := domain.ChapterSearchWords([]string{text})
	if stored.texts != 1 || stored.words != len(words) || stored.wrongStems != 0 || string(stored.hash) != string(snapshot.ChapterContentHash[:]) {
		t.Fatalf("search rows after indexing: %+v, want one text of %d words with russian_stem stems and the state hash", stored, len(words))
	}
	if len(stored.orders) != 1 || stored.orders[0] != int32(snapshot.Paragraphs[0].Order) || stored.tokens[0] != int32(len(domain.Tokenize(domain.Normalize(text)))) {
		t.Fatalf("paragraph orders %v and tokens %v, want the snapshot order and the token count", stored.orders, stored.tokens)
	}

	replace()
	if again := readStoredSearchText(t, fixture); again.texts != 1 || again.words != stored.words {
		t.Fatalf("indexing the same text again: %+v, want the same single text", again)
	}

	// Rows lost (as after a restore without them) can only be modeled by deleting them: no
	// product path drops the rows of a text its state still names.
	if _, err := fixture.db.Pool.Exec(ctx, `DELETE FROM chapter_search_texts WHERE project_id=$1`, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	replace()
	if healed := readStoredSearchText(t, fixture); healed.texts != 1 || healed.words != stored.words {
		t.Fatalf("an unchanged text without search rows: %+v, want its rows written", healed)
	}

	setDraftChapterTextForTest(t, fixture, "Совсем другой текст.")
	changed := replace()
	if after := readStoredSearchText(t, fixture); after.texts != 1 || string(after.hash) != string(changed.ChapterContentHash[:]) {
		t.Fatalf("after a text change: %+v, want only the rows of the new text", after)
	}
}

// R1.3: the draft and the published text index of one chapter run at once. The draft changes
// the text to H2 and drops the rows of H1, which no state names yet; the published text index
// of H1 must not commit its state without rows (the scenario of the review: published checked
// for H1 rows before the draft dropped them).
func TestConcurrentVersionsKeepSearchRowsOfEveryState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	draft := newStoreFixture(t, 7420, governance.SourceViewDraft)
	published := draft
	published.view = governance.SourceViewPublished
	published.claim = addCanonicalIndexClaimForTest(t, published, 21)
	// Draft and published hold the same text H1 with search rows, as after a publication.
	if _, err := draft.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: draft.snapshot(t), IndexedAt: draft.now}); err != nil {
		t.Fatal(err)
	}
	publishedSnapshot := published.snapshot(t)
	setDraftChapterTextForTest(t, draft, "Совсем другой текст главы.")
	draftSnapshot := draft.snapshot(t)

	reached, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	releaseDraft := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseDraft()
	paused := New(draft.db, withFailpoint(func(point string) error {
		if point == "after_state_upsert" {
			pauseOnce.Do(func() { close(reached); <-release })
		}
		return nil
	}))
	draftResult := make(chan error, 1)
	go func() {
		_, err := paused.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: draftSnapshot, IndexedAt: draft.now})
		draftResult <- err
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("the draft text index did not reach its pause")
	}
	publishedResult := make(chan error, 1)
	go func() {
		_, err := published.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{Snapshot: publishedSnapshot, IndexedAt: published.now})
		publishedResult <- err
	}()
	// The published index either waits for the draft or finishes; give it the time to finish.
	select {
	case err := <-publishedResult:
		publishedResult <- err
	case <-time.After(500 * time.Millisecond):
	}
	releaseDraft()
	if err := <-draftResult; err != nil {
		t.Fatal(err)
	}
	if err := <-publishedResult; err != nil {
		t.Fatal(err)
	}
	var missing int
	if err := draft.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM chapter_text_index_state AS state
		WHERE state.project_id = $1 AND state.chapter_id = $2
		  AND NOT EXISTS (SELECT 1 FROM chapter_search_texts AS text
		    WHERE text.project_id = state.project_id AND text.chapter_id = state.chapter_id AND text.content_hash = state.chapter_content_hash)
	`, draft.projectID, draft.chapterID).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("%d text index states of the chapter have no search rows", missing)
	}
}

// setDraftChapterTextForTest edits the draft text of the fixture chapter. The store fixture has
// no editing path of its own (the library owns it); like the other tests of this package, the
// edit is made on the paragraph rows of the indexed revision.
func setDraftChapterTextForTest(t *testing.T, f storeFixture, text string) {
	t.Helper()
	if _, err := f.db.Pool.Exec(context.Background(), `
		UPDATE chapter_paragraphs SET plain_text=$2, markdown=$2 WHERE chapter_id=$1 AND variant='draft'
	`, f.chapterID, text); err != nil {
		t.Fatal(err)
	}
}
