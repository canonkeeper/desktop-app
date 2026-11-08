package search

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

func TestCanonicalSearchRejectsInvalidRequestAndCursorBeforeStore(t *testing.T) {
	valid := SearchRequest{
		ProjectID: canonicalSearchProjectID, Query: "дракон", SourceView: governance.SourceViewDraft, Limit: 10,
	}
	other, err := normalizeSearchRequest(SearchRequest{
		ProjectID: canonicalSearchProjectID, Query: "другой", SourceView: governance.SourceViewDraft, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatchedCursor, err := encodeSearchCursor(searchCursor{
		RequestHash: searchRequestHash(other), ManifestHash: [32]byte{1}, RankBits: math.Float64bits(0.5),
		BookID: mustCanonicalCursorUUIDForTest(t, canonicalSearchBookID), ChapterID: mustCanonicalCursorUUIDForTest(t, canonicalSearchChapterID),
		ResultID: mustCanonicalCursorUUIDForTest(t, canonicalSearchResultID),
	})
	if err != nil {
		t.Fatal(err)
	}
	zeroTime := time.Time{}
	tests := map[string]func(*SearchRequest){
		"missing view":   func(request *SearchRequest) { request.SourceView = "" },
		"unknown view":   func(request *SearchRequest) { request.SourceView = "preview" },
		"empty query":    func(request *SearchRequest) { request.Query = "  " },
		"long query":     func(request *SearchRequest) { request.Query = strings.Repeat("я", maxQueryRunes+1) },
		"unknown scope":  func(request *SearchRequest) { request.Scope = "future" },
		"removed scope":  func(request *SearchRequest) { request.Scope = "annotations" },
		"negative limit": func(request *SearchRequest) { request.Limit = -1 },
		"large limit":    func(request *SearchRequest) { request.Limit = maxSearchLimit + 1 },
		"bad book":       func(request *SearchRequest) { request.Filters.BookID = "not-a-uuid" },
		"bad chapter":    func(request *SearchRequest) { request.Filters.ChapterID = "not-a-uuid" },
		"unknown kind":   func(request *SearchRequest) { request.Filters.ResultKinds = []SearchResultKind{"future"} },
		"removed kind":   func(request *SearchRequest) { request.Filters.ResultKinds = []SearchResultKind{"annotation"} },
		"duplicate kind": func(request *SearchRequest) {
			request.Filters.ResultKinds = []SearchResultKind{SearchResultKindChapter, SearchResultKindChapter}
		},
		"zero timestamp":   func(request *SearchRequest) { request.Filters.UpdatedSince = &zeroTime },
		"malformed cursor": func(request *SearchRequest) { request.Cursor = "not-base64" },
		"oversize cursor": func(request *SearchRequest) {
			request.Cursor = strings.Repeat("A", searchCursorEncodedLength+1)
		},
		"request mismatch": func(request *SearchRequest) { request.Cursor = mismatchedCursor },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			backend := &canonicalSearcherForTest{}
			_, err := NewService(backend).Search(context.Background(), Actor{UserID: canonicalSearchResultID}, request)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("Search() error = %v, want ErrValidation", err)
			}
			if len(backend.calls) != 0 {
				t.Fatalf("store called for invalid request: %#v", backend.calls)
			}
		})
	}
}

func TestCanonicalSearchRedactsUnknownStoreError(t *testing.T) {
	rawDatabaseError := errors.New("raw database sentinel: private canonical row detail")
	backend := &canonicalSearcherForTest{err: rawDatabaseError}

	_, err := NewService(backend).Search(context.Background(), Actor{UserID: canonicalSearchResultID}, SearchRequest{
		ProjectID: canonicalSearchProjectID, Query: "дракон", SourceView: governance.SourceViewDraft,
	})
	if err == nil || err.Error() != "search: failed" {
		t.Fatalf("Search() error = %v, want opaque canonical search error", err)
	}
	if strings.Contains(err.Error(), rawDatabaseError.Error()) {
		t.Fatalf("Search() leaked raw database error: %v", err)
	}
}

func TestCanonicalSearchPreservesSafeStoreErrors(t *testing.T) {
	actionError := &canonicalActionErrorForTest{kind: ErrBookTextNotCurrent}
	for _, want := range []error{ErrCursorDrift, ErrCorrupt, actionError} {
		backend := &canonicalSearcherForTest{err: want}
		_, err := NewService(backend).Search(context.Background(), Actor{UserID: canonicalSearchResultID}, SearchRequest{
			ProjectID: canonicalSearchProjectID, Query: "дракон", SourceView: governance.SourceViewDraft,
		})
		if err != want {
			t.Fatalf("Search() error = %v, want preserved %v", err, want)
		}
	}
}

func TestCanonicalSearchNormalizesBranchesAndUsesLastReturnedCursorItem(t *testing.T) {
	manifestHash := [32]byte{1, 3, 5, 7}
	rows := []LexicalSearchRow{
		canonicalChapterRowForTest(t, canonicalSearchResultID, 0.8, 0),
		canonicalChapterRowForTest(t, "00000000-0000-4000-8000-000000009006", 0.7, 1),
		canonicalChapterRowForTest(t, "00000000-0000-4000-8000-000000009007", 0.6, 2),
	}
	backend := &canonicalSearcherForTest{output: LexicalSearchOutput{
		Rows: rows, ManifestHash: manifestHash, TextCoverage: TextCoverage{CurrentBooks: 1, TotalBooks: 2, NonCurrentBooks: 1, TotalNonCurrentBooks: 1},
	}}
	service := NewService(backend)
	response, err := service.Search(context.Background(), Actor{UserID: canonicalSearchResultID}, SearchRequest{
		ProjectID: canonicalSearchProjectID, Query: "  дракона  ", SourceView: governance.SourceViewPublished,
		Scope: SearchScopeAll, Filters: SearchProjectFilters{ResultKinds: []SearchResultKind{SearchResultKindChapter}}, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.calls) != 1 {
		t.Fatalf("store calls = %d", len(backend.calls))
	}
	call := backend.calls[0]
	if call.Query != "дракона" || call.Limit != 3 || call.ExpectedManifestHash != nil || call.Cursor != nil {
		t.Fatalf("store input = %#v", call)
	}
	if len(response.Items) != 2 || response.NextCursor == "" || response.TextCoverage != backend.output.TextCoverage {
		t.Fatalf("response = %#v", response)
	}
	chapterLocator := response.Items[0].Locator
	if chapterLocator.SourceView != governance.SourceViewPublished ||
		chapterLocator.SourceGeneration != rows[0].ChapterSource.SourceGeneration ||
		chapterLocator.ContentHash != rows[0].ChapterSource.ContentHash {
		t.Fatalf("chapter locator = %#v", response.Items[0].Locator)
	}
	if got := []rune(response.Items[0].Excerpt); len(got) > maxExcerptRunes || !strings.Contains(response.Items[0].Excerpt, "дракон") {
		t.Fatalf("bounded excerpt = %q (%d runes)", response.Items[0].Excerpt, len(got))
	}
	normalized, err := normalizeSearchRequest(SearchRequest{
		ProjectID: canonicalSearchProjectID, Query: "дракона", SourceView: governance.SourceViewPublished,
		Scope: SearchScopeAll, Filters: SearchProjectFilters{ResultKinds: []SearchResultKind{SearchResultKindChapter}}, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := decodeSearchCursor(response.NextCursor, searchRequestHash(normalized))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ManifestHash != manifestHash || canonicalUUIDString(cursor.ResultID) != rows[1].ID || canonicalUUIDString(cursor.ResultID) == rows[2].ID {
		t.Fatalf("next cursor = %#v, want last returned row", cursor)
	}

	backend.output.Rows = nil
	_, err = service.Search(context.Background(), Actor{UserID: canonicalSearchResultID}, SearchRequest{
		ProjectID: canonicalSearchProjectID, Query: "дракона", SourceView: governance.SourceViewPublished,
		Scope: SearchScopeAll, Filters: SearchProjectFilters{ResultKinds: []SearchResultKind{SearchResultKindChapter}}, Limit: 2, Cursor: response.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	continued := backend.calls[1]
	if continued.ExpectedManifestHash == nil || *continued.ExpectedManifestHash != manifestHash || continued.Cursor == nil || continued.Cursor.ResultID != rows[1].ID {
		t.Fatalf("continuation input = %#v", continued)
	}
}

type canonicalSearcherForTest struct {
	output LexicalSearchOutput
	err    error
	calls  []LexicalSearchInput
}

type canonicalActionErrorForTest struct {
	kind error
}

func (err *canonicalActionErrorForTest) Error() string { return err.kind.Error() }
func (err *canonicalActionErrorForTest) Unwrap() error { return err.kind }
func (err *canonicalActionErrorForTest) SearchAction() Action {
	return ActionFixSource
}

func (searcher *canonicalSearcherForTest) SearchLexical(_ context.Context, input LexicalSearchInput) (LexicalSearchOutput, error) {
	searcher.calls = append(searcher.calls, input)
	return searcher.output, searcher.err
}

func (searcher *canonicalSearcherForTest) SearchExactPhrase(context.Context, ExactPhraseSearchInput) (ExactPhraseSearchOutput, error) {
	return ExactPhraseSearchOutput{}, errors.New("unexpected exact phrase search")
}

func canonicalChapterRowForTest(t *testing.T, id string, rank float64, _ uint32) LexicalSearchRow {
	t.Helper()
	return LexicalSearchRow{
		ID: id, ProjectID: canonicalSearchProjectID, BookID: canonicalSearchBookID,
		ChapterID: id,
		Rank:      rank, Content: strings.Repeat("начало ", 50) + "дракона " + strings.Repeat("конец ", 50),
		BookTitle: "Книга", ChapterTitle: "Глава", ChapterNumber: "1", ChapterSource: &ChapterSourceIdentity{
			SourceView: governance.SourceViewPublished, SourceRevision: 2,
			SourceGeneration: governance.Digest{1}, ContentHash: governance.Digest{2},
		},
	}
}
