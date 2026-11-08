package search

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
)

var (
	ErrNotFound   = errors.New("search: not found")
	ErrForbidden  = errors.New("search: forbidden")
	ErrValidation = errors.New("search: validation failed")
)

type Actor struct {
	UserID string
}

type SearchScope string

const (
	SearchScopeAll      SearchScope = "all"
	SearchScopeChapters SearchScope = "chapters"
)

type SearchResultKind string

const SearchResultKindChapter SearchResultKind = "chapter"

type SearchProjectFilters struct {
	BookID       string             `json:"bookId"`
	ChapterID    string             `json:"chapterId"`
	ResultKinds  []SearchResultKind `json:"resultKinds"`
	UpdatedSince *time.Time         `json:"updatedSince"`
}

const SearchContractVersion = "ck-search-v2"

const searchRequestHashDomain = "CKCanonicalSearchRequestV1\x00"

type SearchRequest struct {
	ProjectID  string
	Query      string
	SourceView governance.SourceView
	Scope      SearchScope
	Filters    SearchProjectFilters
	Limit      int
	Cursor     string
}

type TextCoverage struct {
	CurrentBooks         int  `json:"currentBooks"`
	TotalBooks           int  `json:"totalBooks"`
	NonCurrentBooks      int  `json:"nonCurrentBooks"`
	TotalNonCurrentBooks int  `json:"totalNonCurrentBooks"`
	Truncated            bool `json:"truncated"`
}

type SearchResponse struct {
	Query        string
	Scope        SearchScope
	Items        []SearchResult
	NextCursor   string
	TextCoverage TextCoverage
}

type SearchResult struct {
	ID      string
	Title   string
	Excerpt string
	Score   float64
	Locator ChapterSearchLocator
}

type ChapterSearchLocator struct {
	ProjectID        string                         `json:"projectId"`
	BookID           string                         `json:"bookId"`
	ChapterID        string                         `json:"chapterId"`
	SourceRef        string                         `json:"sourceRef"`
	SourceView       governance.SourceView          `json:"sourceView"`
	SourceRevision   uint64                         `json:"sourceRevision"`
	SourceGeneration governance.Digest              `json:"sourceGeneration"`
	ContentHash      governance.Digest              `json:"contentHash"`
	SourceAnchor     library.SourceNavigationAnchor `json:"sourceAnchor"`
}

func normalizeSearchRequest(input SearchRequest) (SearchRequest, error) {
	projectID, _, err := canonicalUUID(input.ProjectID)
	if err != nil {
		return SearchRequest{}, ErrValidation
	}
	input.ProjectID = projectID
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || !utf8.ValidString(input.Query) || utf8.RuneCountInString(input.Query) > maxQueryRunes {
		return SearchRequest{}, ErrValidation
	}
	if input.SourceView != governance.SourceViewDraft && input.SourceView != governance.SourceViewPublished {
		return SearchRequest{}, ErrValidation
	}
	if input.Scope == "" {
		input.Scope = SearchScopeAll
	}
	if input.Scope != SearchScopeAll && input.Scope != SearchScopeChapters {
		return SearchRequest{}, ErrValidation
	}
	if input.Limit == 0 {
		input.Limit = defaultSearchLimit
	}
	if input.Limit < 1 || input.Limit > maxSearchLimit {
		return SearchRequest{}, ErrValidation
	}
	if input.Filters.BookID != "" {
		input.Filters.BookID, _, err = canonicalUUID(input.Filters.BookID)
		if err != nil {
			return SearchRequest{}, ErrValidation
		}
	}
	if input.Filters.ChapterID != "" {
		input.Filters.ChapterID, _, err = canonicalUUID(input.Filters.ChapterID)
		if err != nil {
			return SearchRequest{}, ErrValidation
		}
	}
	switch {
	case len(input.Filters.ResultKinds) == 0:
		input.Filters.ResultKinds = nil
	case len(input.Filters.ResultKinds) == 1 && input.Filters.ResultKinds[0] == SearchResultKindChapter:
		input.Filters.ResultKinds = []SearchResultKind{SearchResultKindChapter}
	default:
		return SearchRequest{}, ErrValidation
	}
	if input.Filters.UpdatedSince != nil {
		if input.Filters.UpdatedSince.IsZero() {
			return SearchRequest{}, ErrValidation
		}
		value := input.Filters.UpdatedSince.UTC()
		input.Filters.UpdatedSince = &value
	}
	if len(input.Cursor) > searchCursorEncodedLength {
		return SearchRequest{}, ErrValidation
	}
	return input, nil
}

func searchRequestHash(input SearchRequest) [32]byte {
	mask := byte(0)
	if len(input.Filters.ResultKinds) != 0 {
		mask = 1
	}
	updatedSince := ""
	if input.Filters.UpdatedSince != nil {
		updatedSince = input.Filters.UpdatedSince.UTC().Format(time.RFC3339Nano)
	}
	limit := make([]byte, 4)
	binary.BigEndian.PutUint32(limit, uint32(input.Limit))
	projection := []byte(searchRequestHashDomain)
	for _, field := range [][]byte{
		[]byte(input.ProjectID), []byte(input.Query), []byte(input.SourceView), []byte(input.Scope),
		[]byte(input.Filters.BookID), []byte(input.Filters.ChapterID), []byte{mask}, []byte(updatedSince), limit,
	} {
		projection = appendLengthPrefixed(projection, field)
	}
	return sha256.Sum256(projection)
}

func appendLengthPrefixed(dst, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func canonicalUUID(value string) (string, [16]byte, error) {
	value = strings.TrimSpace(value)
	var raw [16]byte
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", raw, ErrValidation
	}
	compact := value[:8] + value[9:13] + value[14:18] + value[19:23] + value[24:]
	if _, err := hex.Decode(raw[:], []byte(compact)); err != nil || raw == ([16]byte{}) {
		return "", [16]byte{}, ErrValidation
	}
	return canonicalUUIDString(raw), raw, nil
}

func canonicalUUIDString(raw [16]byte) string {
	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded[:])
}
