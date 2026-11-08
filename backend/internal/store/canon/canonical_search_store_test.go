package canonstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/search"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

func TestCanonicalSearchStoreUnavailableMarker(t *testing.T) {
	for _, test := range []struct {
		name   string
		view   governance.SourceView
		action search.Action
	}{
		{name: "draft", view: governance.SourceViewDraft, action: search.ActionCreateOrImportSource},
		{name: "published", view: governance.SourceViewPublished, action: search.ActionPublishSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanonicalIndexStoreFixture(t, 8100+int(test.view[0]), test.view)
			if _, err := fixture.db.Pool.Exec(context.Background(), `DELETE FROM project_source_views WHERE project_id=$1 AND source_view=$2`, fixture.projectID, fixture.view); err != nil {
				t.Fatal(err)
			}

			_, err := selectCanonicalTextForTest(fixture, CanonicalTextSelectionInput{
				ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID,
			})
			if !errors.Is(err, search.ErrSourceViewUnavailable) {
				t.Fatalf("error = %v, want source view unavailable", err)
			}
			if action := canonicalSearchActionForTest(t, err); action != test.action {
				t.Fatalf("action = %q, want %q", action, test.action)
			}
			for _, secret := range []string{"needle query", "paragraph body", "Indexing Book"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("safe error contains %q: %v", secret, err)
				}
			}
		})
	}
}

func TestCanonicalExactPhraseInputRejectsNegativeChapterOrder(t *testing.T) {
	_, _, err := validateExactPhraseInput(search.ExactPhraseSearchInput{
		ActorUserID: "00000000-0000-0000-0000-000000000001",
		ProjectID:   "00000000-0000-0000-0000-000000000002",
		SourceView:  governance.SourceViewDraft,
		BookID:      "00000000-0000-0000-0000-000000000003",
		Query:       "needle",
		Limit:       1,
		Cursor: &search.ExactPhraseCursor{
			ChapterID: "00000000-0000-0000-0000-000000000004", ChapterOrder: -1,
		},
	})
	if !errors.Is(err, search.ErrValidation) {
		t.Fatalf("error = %v, want validation", err)
	}
}

func TestCanonicalSearchStoreCurrentEmptyAndZeroChapterCoverage(t *testing.T) {
	t.Run("current empty marker", func(t *testing.T) {
		fixture := newCanonicalIndexStoreFixture(t, 8200, governance.SourceViewDraft)
		if _, err := fixture.db.Pool.Exec(context.Background(), `DELETE FROM book_source_members WHERE project_id=$1 AND source_view=$2`, fixture.projectID, fixture.view); err != nil {
			t.Fatal(err)
		}
		got := loadCanonicalTextSelectionForTest(t, fixture, "")
		assertCanonicalCoverageForTest(t, got.TextCoverage, jobs.TextCoverage{})
		if len(got.CurrentBookIDs) != 0 {
			t.Fatalf("current book IDs = %v, want none", got.CurrentBookIDs)
		}
	})

	t.Run("zero chapter draft is current", func(t *testing.T) {
		fixture := newCanonicalIndexStoreFixture(t, 8210, governance.SourceViewDraft)
		if _, err := fixture.db.Pool.Exec(context.Background(), `DELETE FROM chapter_source_members WHERE project_id=$1 AND source_view=$2`, fixture.projectID, fixture.view); err != nil {
			t.Fatal(err)
		}
		got := loadCanonicalTextSelectionForTest(t, fixture, "")
		assertCanonicalCoverageForTest(t, got.TextCoverage, jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1})
		if !reflect.DeepEqual(got.CurrentBookIDs, []string{fixture.bookID}) {
			t.Fatalf("current book IDs = %v", got.CurrentBookIDs)
		}
	})

	t.Run("zero chapter published is non-current", func(t *testing.T) {
		fixture := newCanonicalIndexStoreFixture(t, 8220, governance.SourceViewPublished)
		if _, err := fixture.db.Pool.Exec(context.Background(), `DELETE FROM chapter_source_members WHERE project_id=$1 AND source_view=$2`, fixture.projectID, fixture.view); err != nil {
			t.Fatal(err)
		}
		got := loadCanonicalTextSelectionForTest(t, fixture, "")
		assertCanonicalCoverageForTest(t, got.TextCoverage, jobs.TextCoverage{})
		if len(got.CurrentBookIDs) != 0 {
			t.Fatalf("current book IDs = %v, want none", got.CurrentBookIDs)
		}
	})

	t.Run("published chapter with matching state is current", func(t *testing.T) {
		fixture := newCanonicalIndexStoreFixture(t, 8230, governance.SourceViewPublished)
		indexCanonicalSearchFixtureForTest(t, fixture)
		got := loadCanonicalTextSelectionForTest(t, fixture, fixture.bookID)
		assertCanonicalCoverageForTest(t, got.TextCoverage, jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1})
	})
}

func TestCanonicalSearchStoreCurrentRequiresExactIndexState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, canonicalIndexStoreFixture)
	}{
		{
			name: "missing state",
			mutate: func(t *testing.T, fixture canonicalIndexStoreFixture) {
				mustExecCanonicalSearchTest(t, fixture, `DELETE FROM chapter_text_index_state WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
			},
		},
		{
			name: "source revision",
			mutate: func(t *testing.T, fixture canonicalIndexStoreFixture) {
				mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET source_revision=source_revision+1 WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
			},
		},
		{
			name: "source generation",
			mutate: func(t *testing.T, fixture canonicalIndexStoreFixture) {
				mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET locator_source_generation=decode(repeat('01',32),'hex') WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanonicalIndexStoreFixture(t, 8300+index*20, governance.SourceViewDraft)
			indexCanonicalSearchFixtureForTest(t, fixture)
			test.mutate(t, fixture)

			got := loadCanonicalTextSelectionForTest(t, fixture, "")
			assertCanonicalCoverageForTest(t, got.TextCoverage, jobs.TextCoverage{
				TotalBooks: 1, NonCurrentBooks: 1, TotalNonCurrentBooks: 1,
			})
		})
	}
}

func TestCanonicalSearchStoreManifestUsesAtomicStateHash(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 8450, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	before := loadCanonicalTextSelectionForTest(t, fixture, fixture.bookID)
	wantManifest := before.currentBookManifests[fixture.bookID]
	if wantManifest == (governance.Digest{}) {
		t.Fatal("current book manifest missing")
	}

	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET chapter_content_hash=set_byte(chapter_content_hash,0,get_byte(chapter_content_hash,0) # 1) WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
	after := loadCanonicalTextSelectionForTest(t, fixture, fixture.bookID)
	assertCanonicalCoverageForTest(t, after.TextCoverage, jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1})
	if got := after.currentBookManifests[fixture.bookID]; got == (governance.Digest{}) || got == wantManifest {
		t.Fatalf("manifest did not follow exact state hash: %x -> %x", wantManifest, got)
	}
}

func TestCanonicalSearchStoreCoverageKeepsCurrentBooksAndIsolatesRows(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 8500, governance.SourceViewDraft)
	secondBookID, secondChapterID := addCanonicalSearchBookForTest(t, fixture, 20)
	other := createIndexingStoreFixtureForTest(t, fixture.db, 8600)
	indexCanonicalSearchFixtureForTest(t, fixture)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET source_revision=source_revision+1 WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
	mustExecCanonicalSearchTest(t, fixture, `INSERT INTO chapter_text_index_state (project_id,source_view,chapter_id,source_revision,locator_source_generation,chapter_content_hash,indexed_at) SELECT $1,'published',$2,source_revision,locator_source_generation,chapter_content_hash,indexed_at FROM chapter_text_index_state WHERE project_id=$1 AND source_view='draft' AND chapter_id=$2 ON CONFLICT DO NOTHING`, fixture.projectID, secondChapterID)

	got := loadCanonicalTextSelectionForTest(t, fixture, "")
	assertCanonicalCoverageForTest(t, got.TextCoverage, jobs.TextCoverage{
		CurrentBooks: 1, TotalBooks: 2, NonCurrentBooks: 1, TotalNonCurrentBooks: 1,
	})
	if !reflect.DeepEqual(got.CurrentBookIDs, []string{secondBookID}) {
		t.Fatalf("current book IDs = %v, want %s", got.CurrentBookIDs, secondBookID)
	}
	if strings.Contains(strings.Join(got.CurrentBookIDs, ","), other.bookID) {
		t.Fatalf("cross-project book leaked: %v", got.CurrentBookIDs)
	}

	_, err := selectCanonicalTextForTest(fixture, CanonicalTextSelectionInput{
		ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID,
	})
	if !errors.Is(err, search.ErrBookTextNotCurrent) {
		t.Fatalf("stale selected book error = %v", err)
	}
	if action := canonicalSearchActionForTest(t, err); action != search.ActionStartIndexing {
		t.Fatalf("stale selected book action = %q", action)
	}

	selected := loadCanonicalTextSelectionForTest(t, fixture, secondBookID)
	if !reflect.DeepEqual(selected.CurrentBookIDs, []string{secondBookID}) {
		t.Fatalf("selected current book IDs = %v", selected.CurrentBookIDs)
	}
	assertCanonicalCoverageForTest(t, selected.TextCoverage, jobs.TextCoverage{
		CurrentBooks: 1, TotalBooks: 2, NonCurrentBooks: 1, TotalNonCurrentBooks: 1,
	})
}

func TestCanonicalSearchStoreCurrentIgnoresProjectBookReorder(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 8700, governance.SourceViewDraft)
	secondBookID, _ := addCanonicalSearchBookForTest(t, fixture, 20)
	indexCanonicalSearchFixtureForTest(t, fixture)
	want := loadCanonicalTextSelectionForTest(t, fixture, "")
	wantManifest, err := currentBookManifestForTest(fixture)
	if err != nil {
		t.Fatal(err)
	}

	mustExecCanonicalSearchTest(t, fixture, `UPDATE books SET "order"=CASE id WHEN $1 THEN 99 ELSE 0 END WHERE project_id=$2`, fixture.bookID, fixture.projectID)
	got := loadCanonicalTextSelectionForTest(t, fixture, "")
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(got.CurrentBookIDs, []string{fixture.bookID, secondBookID}) {
		t.Fatalf("selection changed after reorder: %#v -> %#v", want, got)
	}
	gotManifest, err := currentBookManifestForTest(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if gotManifest != wantManifest {
		t.Fatalf("manifest changed after project book reorder")
	}
}

func TestCanonicalSearchStoreUnavailableSelectedStructuralBook(t *testing.T) {
	for _, test := range []struct {
		name   string
		base   int
		view   governance.SourceView
		mutate func(*testing.T, canonicalIndexStoreFixture)
	}{
		{
			name: "published without chapter", base: 8800, view: governance.SourceViewPublished,
			mutate: func(t *testing.T, fixture canonicalIndexStoreFixture) {
				mustExecCanonicalSearchTest(t, fixture, `DELETE FROM chapter_source_members WHERE project_id=$1 AND source_view=$2`, fixture.projectID, fixture.view)
			},
		},
		{
			name: "chapter lifecycle drift", base: 8810, view: governance.SourceViewDraft,
			mutate: func(t *testing.T, fixture canonicalIndexStoreFixture) {
				indexCanonicalSearchFixtureForTest(t, fixture)
				mustExecCanonicalSearchTest(t, fixture, `UPDATE chapters SET lifecycle_generation=lifecycle_generation+1 WHERE id=$1`, fixture.chapterID)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanonicalIndexStoreFixture(t, test.base, test.view)
			test.mutate(t, fixture)

			_, err := selectCanonicalTextForTest(fixture, CanonicalTextSelectionInput{
				ProjectID: fixture.projectID, SourceView: fixture.view, BookID: fixture.bookID,
			})
			if !errors.Is(err, search.ErrBookTextNotCurrent) {
				t.Fatalf("error = %v, want book text not current", err)
			}
			if action := canonicalSearchActionForTest(t, err); action != search.ActionFixSource {
				t.Fatalf("action = %q, want %q", action, search.ActionFixSource)
			}
		})
	}
}

func TestCanonicalLexicalRussianMorphologyAndExactLocator(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 8900, governance.SourceViewDraft)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_paragraphs SET markdown='Герой разбудил дракона', plain_text='Герой разбудил дракона', word_count=3 WHERE id=$1 AND variant='draft'`, fixture.paragraphID)
	indexCanonicalSearchFixtureForTest(t, fixture)

	got, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "дракон", Limit: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0].ChapterSource == nil {
		t.Fatalf("lexical rows = %#v", got.Rows)
	}
	if got.Rows[0].ID != fixture.chapterID || got.Rows[0].ChapterSource.SourceView != fixture.view ||
		got.Rows[0].ChapterSource.SourceRevision != 1 || got.Rows[0].Content != "Герой разбудил дракона" ||
		got.ManifestHash == ([32]byte{}) {
		t.Fatalf("lexical output = %#v", got)
	}
	if got.TextCoverage != (search.TextCoverage{CurrentBooks: 1, TotalBooks: 1}) {
		t.Fatalf("coverage = %#v", got.TextCoverage)
	}
	afterIndex := fixture.now.Add(time.Minute)
	filtered, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "дракон", UpdatedSince: &afterIndex, Limit: 11,
	})
	if err != nil || len(filtered.Rows) != 0 {
		t.Fatalf("updatedSince chapter rows = %#v, %v", filtered.Rows, err)
	}
}

func TestCanonicalProjectSearchChapterCanBeReadWithoutBookScope(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9450, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)

	found, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11,
	})
	if err != nil || len(found.Rows) != 1 || found.Rows[0].ChapterID != fixture.chapterID {
		t.Fatalf("project search = %#v, %v", found, err)
	}

	var resolved ResolvedChapterSource
	err = fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		var resolveErr error
		resolved, resolveErr = fixture.db.ResolveCanonicalChapterSourceInTx(
			context.Background(), tx, fixture.projectID, fixture.view, "", fixture.chapterID,
		)
		return resolveErr
	})
	if err != nil {
		t.Fatalf("read project-search chapter without book scope: %v", err)
	}
	fields, err := resolved.ExactRef.Fields()
	if err != nil || fields.ProjectID != fixture.projectID || fields.BookID != fixture.bookID ||
		fields.ChapterID != fixture.chapterID || resolved.Content != found.Rows[0].Content {
		t.Fatalf("resolved source = %#v, %#v, %v", resolved, fields, err)
	}

	err = fixture.db.WithTx(context.Background(), func(tx pgx.Tx) error {
		for _, test := range []struct {
			name      string
			projectID string
			view      governance.SourceView
			bookID    string
		}{
			{name: "cross project", projectID: "00000000-0000-4000-8000-000000000099", view: fixture.view, bookID: ""},
			{name: "wrong view", projectID: fixture.projectID, view: governance.SourceViewPublished, bookID: ""},
			{name: "wrong book", projectID: fixture.projectID, view: fixture.view, bookID: "00000000-0000-4000-8000-000000000098"},
		} {
			if _, err := fixture.db.ResolveCanonicalChapterSourceInTx(
				context.Background(), tx, test.projectID, test.view, test.bookID, fixture.chapterID,
			); !errors.Is(err, ErrCanonicalSourceNotCurrent) {
				return fmt.Errorf("%s: %w", test.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalLexicalPartialCoverageAndSelectedTargets(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9000, governance.SourceViewDraft)
	secondBookID, secondChapterID := addCanonicalSearchBookForTest(t, fixture, 20)
	indexCanonicalSearchFixtureForTest(t, fixture)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET source_revision=source_revision+1 WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)

	project, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if project.TextCoverage != (search.TextCoverage{CurrentBooks: 1, TotalBooks: 2, NonCurrentBooks: 1, TotalNonCurrentBooks: 1}) {
		t.Fatalf("coverage = %#v", project.TextCoverage)
	}
	for _, row := range project.Rows {
		if row.BookID != secondBookID {
			t.Fatalf("stale book leaked: %#v", row)
		}
	}

	selected, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", BookID: secondBookID, Limit: 11,
	})
	if err != nil || len(selected.Rows) == 0 {
		t.Fatalf("current selected book search = %#v, %v", selected, err)
	}
	chapter, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", ChapterID: secondChapterID, Limit: 11,
	})
	if err != nil || len(chapter.Rows) == 0 || chapter.Rows[0].ChapterID != secondChapterID {
		t.Fatalf("current selected chapter search = %#v, %v", chapter, err)
	}
	_, err = fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", BookID: fixture.bookID, Limit: 11,
	})
	if !errors.Is(err, search.ErrBookTextNotCurrent) {
		t.Fatalf("stale selected book error = %v", err)
	}
}

func TestCanonicalLexicalManifestCursorDriftAndBookReorder(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9100, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	first, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11,
	})
	if err != nil {
		t.Fatal(err)
	}

	mustExecCanonicalSearchTest(t, fixture, `UPDATE books SET "order"=99 WHERE id=$1`, fixture.bookID)
	if _, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11, ExpectedManifestHash: &first.ManifestHash,
	}); err != nil {
		t.Fatalf("book reorder rejected cursor: %v", err)
	}

	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET chapter_content_hash=set_byte(chapter_content_hash,0,get_byte(chapter_content_hash,0) # 1) WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
	_, err = fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11, ExpectedManifestHash: &first.ManifestHash,
	})
	if !errors.Is(err, search.ErrCursorDrift) {
		t.Fatalf("manifest drift error = %v", err)
	}
	selection := loadCanonicalTextSelectionForTest(t, fixture, fixture.bookID)
	assertCanonicalCoverageForTest(t, selection.TextCoverage, jobs.TextCoverage{CurrentBooks: 1, TotalBooks: 1})
}

func TestCanonicalLexicalCrossViewAndActiveMembershipIsolation(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9700, governance.SourceViewDraft)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_paragraphs SET markdown='черновой дракон',plain_text='черновой дракон',word_count=2 WHERE id=$1 AND variant='draft'`, fixture.paragraphID)
	indexCanonicalSearchFixtureForTest(t, fixture)
	published := fixture
	published.view = governance.SourceViewPublished
	published.claim = addCanonicalIndexClaimForTest(t, published, 29)
	indexCanonicalSearchFixtureForTest(t, published)

	draft, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: governance.SourceViewDraft,
		Query: "черновой", Limit: 11,
	})
	if err != nil || len(draft.Rows) != 1 || draft.Rows[0].ChapterSource == nil ||
		draft.Rows[0].ChapterSource.SourceView != governance.SourceViewDraft {
		t.Fatalf("draft rows = %#v, %v", draft.Rows, err)
	}
	publishedResult, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: governance.SourceViewPublished,
		Query: "черновой", Limit: 11,
	})
	if err != nil || len(publishedResult.Rows) != 0 {
		t.Fatalf("cross-view rows = %#v, %v", publishedResult.Rows, err)
	}

	mustExecCanonicalSearchTest(t, fixture, `UPDATE project_memberships SET removed_at=$1 WHERE project_id=$2 AND user_id=$3`, fixture.now, fixture.projectID, fixture.actorID)
	_, err = fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: governance.SourceViewDraft,
		Query: "черновой", Limit: 11,
	})
	if !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("removed membership error = %v", err)
	}
}

func TestCanonicalLexicalEqualRankKeysetHasNoDuplicateOrSkip(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9300, governance.SourceViewDraft)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_paragraphs SET markdown='dragon',plain_text='dragon',word_count=1 WHERE id=$1 AND variant='draft'`, fixture.paragraphID)
	addCanonicalSearchBookForTest(t, fixture, 20)
	addCanonicalSearchBookForTest(t, fixture, 30)
	indexCanonicalSearchFixtureForTest(t, fixture)
	input := search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 2,
	}
	first, err := fixture.db.SearchLexical(context.Background(), input)
	if err != nil || len(first.Rows) != 2 || first.Rows[0].Rank != first.Rows[1].Rank {
		t.Fatalf("first page = %#v, %v", first.Rows, err)
	}
	input.ExpectedManifestHash = &first.ManifestHash
	input.Cursor = canonicalLexicalCursorFromRowForTest(first.Rows[1])
	second, err := fixture.db.SearchLexical(context.Background(), input)
	if err != nil || len(second.Rows) != 1 {
		t.Fatalf("second page = %#v, %v", second.Rows, err)
	}
	seen := map[string]bool{}
	for _, row := range append(append([]search.LexicalSearchRow(nil), first.Rows...), second.Rows...) {
		if seen[row.ID] {
			t.Fatalf("duplicate result %s", row.ID)
		}
		seen[row.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("results = %v, want three", seen)
	}
}

func TestCanonicalLexicalStaleChapterRowFailsClosed(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9400, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_search_rows SET chapter_content_hash=set_byte(chapter_content_hash,0,get_byte(chapter_content_hash,0) # 1) WHERE project_id=$1`, fixture.projectID)
	got, err := fixture.db.SearchLexical(context.Background(), search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11,
	})
	if err != nil || len(got.Rows) != 0 {
		t.Fatalf("stale chapter rows = %#v, error = %v", got.Rows, err)
	}
}

func TestCanonicalLexicalRepeatableReadKeepsSelectionAndRowsOnOneSnapshot(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9500, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	ctx := context.Background()
	tx, err := fixture.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	selection, err := loadCanonicalTextSelectionInTx(ctx, tx, CanonicalTextSelectionInput{ProjectID: fixture.projectID, SourceView: fixture.view})
	if err != nil {
		t.Fatal(err)
	}
	mustExecCanonicalSearchTest(t, fixture, `UPDATE chapter_text_index_state SET chapter_content_hash=set_byte(chapter_content_hash,0,get_byte(chapter_content_hash,0) # 1) WHERE project_id=$1 AND source_view=$2 AND chapter_id=$3`, fixture.projectID, fixture.view, fixture.chapterID)
	got, err := searchLexicalInTx(ctx, tx, search.LexicalSearchInput{
		ActorUserID: fixture.actorID, ProjectID: fixture.projectID, SourceView: fixture.view,
		Query: "dragon", Limit: 11,
	}, selection)
	if err != nil || len(got.Rows) == 0 {
		t.Fatalf("same-snapshot search = %#v, %v", got, err)
	}
	outside := loadCanonicalTextSelectionForTest(t, fixture, fixture.bookID)
	if outside.currentBookManifests[fixture.bookID] == selection.currentBookManifests[fixture.bookID] {
		t.Fatal("external state mutation did not change a later snapshot")
	}
}

func TestCanonicalLexicalSearchInTxUsesCallerSnapshotWithoutActor(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9550, governance.SourceViewDraft)
	secondBookID, secondChapterID := addCanonicalSearchBookForTest(t, fixture, 30)
	indexCanonicalSearchFixtureForTest(t, fixture)
	ctx := context.Background()
	input := search.LexicalSearchInput{
		ProjectID: fixture.projectID, SourceView: fixture.view,
		BookID: secondBookID, ChapterID: secondChapterID,
		Query: "dragon", Limit: 11,
	}

	if _, err := fixture.db.SearchLexical(ctx, input); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("public search without actor error = %v, want not found", err)
	}

	tx, err := fixture.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	got, err := fixture.db.SearchLexicalInTx(ctx, tx, input)
	if err != nil || len(got.Rows) == 0 {
		t.Fatalf("transactional search = %#v, %v", got, err)
	}
	for _, row := range got.Rows {
		if row.BookID != secondBookID || row.ChapterID != secondChapterID {
			t.Fatalf("row escaped backend scope: %#v", row)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE chapter_text_index_state
		SET source_revision = source_revision + 1
		WHERE project_id = $1 AND source_view = $2 AND chapter_id = $3
	`, fixture.projectID, fixture.view, secondChapterID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.SearchLexicalInTx(ctx, tx, input); !errors.Is(err, search.ErrBookTextNotCurrent) {
		t.Fatalf("search after uncommitted index drift error = %v, want not current", err)
	}
}

func TestCanonicalFTSIndexesArePlannerCapable(t *testing.T) {
	fixture := newCanonicalIndexStoreFixture(t, 9600, governance.SourceViewDraft)
	indexCanonicalSearchFixtureForTest(t, fixture)
	ctx := context.Background()
	tx, err := fixture.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL enable_indexscan=off`); err != nil {
		t.Fatal(err)
	}
	assertCanonicalExplainIndexForTest(t, tx, `EXPLAIN (COSTS OFF) SELECT chapter_id FROM chapter_text_search_rows WHERE search_vector @@ websearch_to_tsquery('russian','дракон')`, "chapter_text_search_rows_search_idx")
	if _, err := tx.Exec(ctx, `SET LOCAL enable_indexscan=on`); err != nil {
		t.Fatal(err)
	}
	assertCanonicalExplainIndexForTest(t, tx, `EXPLAIN (COSTS OFF) SELECT chapter_id FROM chapter_text_search_rows WHERE project_id=$1 AND source_view=$2 AND book_id=$3 ORDER BY chapter_id`, "chapter_text_search_rows_book_idx", fixture.projectID, fixture.view, fixture.bookID)
}

func canonicalLexicalCursorFromRowForTest(row search.LexicalSearchRow) *search.LexicalCursor {
	return &search.LexicalCursor{
		RankBits: math.Float64bits(row.Rank), BookID: row.BookID, ChapterID: row.ChapterID, ResultID: row.ID,
	}
}

func assertCanonicalExplainIndexForTest(t *testing.T, tx pgx.Tx, query, index string, args ...any) {
	t.Helper()
	rows, err := tx.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan, "\n"), index) {
		t.Fatalf("plan does not use %s:\n%s", index, strings.Join(plan, "\n"))
	}
}

func loadCanonicalTextSelectionForTest(t *testing.T, fixture canonicalIndexStoreFixture, bookID string) CanonicalTextSelection {
	t.Helper()
	got, err := selectCanonicalTextForTest(fixture, CanonicalTextSelectionInput{
		ProjectID: fixture.projectID, SourceView: fixture.view, BookID: bookID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// selectCanonicalTextForTest вызывает тот же выбор текста, что поиск и проверка
// цели, в собственной read-only транзакции.
func selectCanonicalTextForTest(fixture canonicalIndexStoreFixture, input CanonicalTextSelectionInput) (CanonicalTextSelection, error) {
	var selection CanonicalTextSelection
	err := pgx.BeginTxFunc(context.Background(), fixture.db.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		selection, err = loadCanonicalTextSelectionInTx(context.Background(), tx, input)
		return err
	})
	return selection, err
}

// currentBookManifestForTest считает манифест книги той же загрузкой снимка,
// что и выбор базового текста чата.
func currentBookManifestForTest(fixture canonicalIndexStoreFixture) (governance.Digest, error) {
	snapshot, err := loadCurrentBookSourceSnapshot(context.Background(), sqlc.New(fixture.db.Pool), fixture.projectID, fixture.view, fixture.bookID)
	if err != nil {
		return governance.Digest{}, err
	}
	return bookSourceSnapshotManifest(snapshot)
}

func indexCanonicalSearchFixtureForTest(t *testing.T, fixture canonicalIndexStoreFixture) {
	t.Helper()
	descriptors, err := fixture.store.ListCanonTextChapterDescriptorsForClaim(context.Background(), fixture.claim)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		snapshot, err := fixture.store.LoadCanonTextChapterSnapshotForClaim(context.Background(), fixture.claim, descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.ReplaceCanonTextChapterForClaim(context.Background(), indexing.ReplaceCanonTextChapterInput{
			Snapshot: snapshot, IndexedAt: fixture.now,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func addCanonicalSearchBookForTest(t *testing.T, fixture canonicalIndexStoreFixture, offset int) (string, string) {
	t.Helper()
	bookID := indexingStoreUUIDForTest(9000 + offset)
	chapterID := indexingStoreUUIDForTest(9100 + offset)
	paragraphID := indexingStoreUUIDForTest(9200 + offset)
	insertIndexingStoreBookForTest(t, fixture.db, bookID, fixture.projectID, fixture.now)
	insertIndexingStoreChapterForTest(t, fixture.db, fixture.projectID, chapterID, bookID, fixture.now)
	insertIndexingStoreParagraphForTest(t, fixture.db, paragraphID, chapterID, fixture.now)
	mustExecCanonicalSearchTest(t, fixture, `UPDATE books SET "order"=2 WHERE id=$1`, bookID)
	mustExecCanonicalSearchTest(t, fixture, `INSERT INTO book_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,title) VALUES ($1,$2,1,$3,1,'Second Book')`, fixture.projectID, fixture.view, bookID)
	mustExecCanonicalSearchTest(t, fixture, `INSERT INTO chapter_source_members (project_id,source_view,project_lifecycle_generation,book_id,book_lifecycle_generation,chapter_id,chapter_lifecycle_generation,source_revision,title,chapter_order) VALUES ($1,$2,1,$3,1,$4,1,1,'Second Chapter',1)`, fixture.projectID, fixture.view, bookID, chapterID)
	return bookID, chapterID
}

func assertCanonicalCoverageForTest(t *testing.T, got, want jobs.TextCoverage) {
	t.Helper()
	if got != want {
		t.Fatalf("coverage = %#v, want %#v", got, want)
	}
}

func canonicalSearchActionForTest(t *testing.T, err error) search.Action {
	t.Helper()
	var outcome interface {
		SearchAction() search.Action
	}
	if !errors.As(err, &outcome) {
		t.Fatalf("error has no canonical action: %v", err)
	}
	return outcome.SearchAction()
}

func mustExecCanonicalSearchTest(t *testing.T, fixture canonicalIndexStoreFixture, query string, args ...any) {
	t.Helper()
	if _, err := fixture.db.Pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}
