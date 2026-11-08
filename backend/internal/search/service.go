package search

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
)

var ErrSearchFailed = errors.New("search: failed")

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50
	maxQueryRunes      = 500
	maxExcerptRunes    = 240
)

type Service struct {
	store Store
}

func NewService(searchStore Store) *Service {
	if searchStore == nil {
		panic("search: store is required")
	}
	return &Service{store: searchStore}
}

func (service *Service) Search(ctx context.Context, actor Actor, request SearchRequest) (SearchResponse, error) {
	normalized, err := normalizeSearchRequest(request)
	if err != nil {
		return SearchResponse{}, err
	}
	actorID, _, err := canonicalUUID(actor.UserID)
	if err != nil {
		return SearchResponse{}, ErrNotFound
	}
	requestHash := searchRequestHash(normalized)
	var decoded *searchCursor
	if normalized.Cursor != "" {
		value, err := decodeSearchCursor(normalized.Cursor, requestHash)
		if err != nil {
			return SearchResponse{}, err
		}
		decoded = &value
	}
	input := LexicalSearchInput{
		ActorUserID: actorID, ProjectID: normalized.ProjectID, SourceView: normalized.SourceView,
		Query: normalized.Query, BookID: normalized.Filters.BookID, ChapterID: normalized.Filters.ChapterID,
		UpdatedSince: normalized.Filters.UpdatedSince, Limit: normalized.Limit + 1,
	}
	if decoded != nil {
		input.ExpectedManifestHash = &decoded.ManifestHash
		input.Cursor = &LexicalCursor{
			RankBits: decoded.RankBits, BookID: canonicalUUIDString(decoded.BookID),
			ChapterID: canonicalUUIDString(decoded.ChapterID), ResultID: canonicalUUIDString(decoded.ResultID),
		}
	}
	output, err := service.store.SearchLexical(ctx, input)
	if err != nil {
		return SearchResponse{}, mapSearchError(err)
	}
	hasMore := len(output.Rows) > normalized.Limit
	rows := output.Rows
	if hasMore {
		rows = rows[:normalized.Limit]
	}
	response := SearchResponse{
		Query: normalized.Query, Scope: normalized.Scope,
		Items: make([]SearchResult, len(rows)), TextCoverage: output.TextCoverage,
	}
	for index, row := range rows {
		item, err := searchResultFromRow(normalized, row)
		if err != nil {
			return SearchResponse{}, err
		}
		response.Items[index] = item
	}
	if hasMore {
		cursor, err := searchCursorFromRow(requestHash, output.ManifestHash, rows[len(rows)-1])
		if err != nil {
			return SearchResponse{}, err
		}
		response.NextCursor, err = encodeSearchCursor(cursor)
		if err != nil {
			return SearchResponse{}, err
		}
	}
	return response, nil
}

func searchResultFromRow(request SearchRequest, row LexicalSearchRow) (SearchResult, error) {
	if _, _, err := canonicalUUID(row.ID); err != nil || row.ProjectID != request.ProjectID || row.Rank <= 0 || math.IsNaN(row.Rank) || math.IsInf(row.Rank, 0) {
		return SearchResult{}, ErrCorrupt
	}
	if row.ChapterSource == nil || row.ChapterSource.SourceView != request.SourceView ||
		(governance.CanonicalChapterSourceV1{
			ProjectID: row.ProjectID, BookID: row.BookID, ChapterID: row.ChapterID,
			SourceView: row.ChapterSource.SourceView, SourceRevision: row.ChapterSource.SourceRevision,
			SourceGeneration: row.ChapterSource.SourceGeneration, ContentHash: row.ChapterSource.ContentHash,
		}).Validate() != nil {
		return SearchResult{}, ErrCorrupt
	}
	result := SearchResult{
		ID: row.ID, Title: searchTitle(row), Excerpt: NavigationSnippet(row.Content, request.Query), Score: row.Rank,
	}
	paragraphID, paragraphPosition := "", 0
	if match := row.ParagraphMatch; match != nil {
		paragraphID, paragraphPosition = match.ID, match.Position
		result.Excerpt = NavigationSnippet(match.Content, request.Query)
	}
	anchor, err := library.NewSourceNavigationAnchor(governance.CanonicalChapterSourceLocatorV1{
		ProjectID: row.ProjectID, BookID: row.BookID, ChapterID: row.ChapterID, SourceRef: row.ChapterID,
		SourceView: row.ChapterSource.SourceView, SourceRevision: row.ChapterSource.SourceRevision,
		SourceGeneration: row.ChapterSource.SourceGeneration, ContentHash: row.ChapterSource.ContentHash,
	}, row.ChapterNumber, paragraphID, paragraphPosition)
	if err != nil {
		return SearchResult{}, ErrCorrupt
	}
	result.Locator = ChapterSearchLocator{
		ProjectID: row.ProjectID, BookID: row.BookID, ChapterID: row.ChapterID, SourceRef: row.ChapterID,
		SourceView: row.ChapterSource.SourceView, SourceRevision: row.ChapterSource.SourceRevision,
		SourceGeneration: row.ChapterSource.SourceGeneration, ContentHash: row.ChapterSource.ContentHash,
		SourceAnchor: anchor,
	}
	return result, nil
}

func searchCursorFromRow(requestHash, manifestHash [32]byte, row LexicalSearchRow) (searchCursor, error) {
	_, bookID, bookErr := canonicalUUID(row.BookID)
	_, chapterID, chapterErr := canonicalUUID(row.ChapterID)
	_, resultID, resultErr := canonicalUUID(row.ID)
	if bookErr != nil || chapterErr != nil || resultErr != nil {
		return searchCursor{}, ErrCorrupt
	}
	return searchCursor{
		RequestHash: requestHash, ManifestHash: manifestHash, RankBits: math.Float64bits(row.Rank),
		BookID: bookID, ChapterID: chapterID, ResultID: resultID,
	}, nil
}

func searchTitle(row LexicalSearchRow) string {
	bookTitle := strings.TrimSpace(row.BookTitle)
	chapterTitle := strings.TrimSpace(row.ChapterTitle)
	if bookTitle != "" && chapterTitle != "" {
		return bookTitle + " - " + chapterTitle
	}
	if chapterTitle != "" {
		return chapterTitle
	}
	if bookTitle != "" {
		return bookTitle
	}
	return row.ID
}

func mapSearchError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrForbidden):
		return ErrForbidden
	case errors.Is(err, ErrValidation):
		return ErrValidation
	}
	var actionError interface {
		error
		SearchAction() Action
	}
	if errors.As(err, &actionError) {
		return actionError
	}
	for _, safe := range []error{
		ErrSourceViewUnavailable,
		ErrBookTextNotCurrent,
		ErrCursorDrift,
		ErrCorrupt,
		ErrExactPhraseBoundedness,
	} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ErrSearchFailed
}

func NavigationSnippet(text, query string) string {
	normalized := strings.Join(strings.Fields(text), " ")
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= maxExcerptRunes {
		return normalized
	}
	matchRune := firstQueryMatchRune(normalized, query)
	start := 0
	if matchRune >= 0 {
		start = matchRune - maxExcerptRunes/3
	}
	if start < 0 {
		start = 0
	}
	if start+maxExcerptRunes > len(runes) {
		start = len(runes) - maxExcerptRunes
	}
	return string(runes[start : start+maxExcerptRunes])
}

func firstQueryMatchRune(text, query string) int {
	query = strings.TrimSpace(query)
	if query == "" {
		return -1
	}
	lowerText := strings.ToLower(text)
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if index := strings.Index(lowerText, term); index >= 0 {
			return utf8.RuneCountInString(lowerText[:index])
		}
	}
	return -1
}
