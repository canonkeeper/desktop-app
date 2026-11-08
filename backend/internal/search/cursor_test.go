package search

import (
	"encoding/base64"
	"math"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
)

const (
	canonicalSearchProjectID   = "00000000-0000-4000-8000-000000009001"
	canonicalSearchBookID      = "00000000-0000-4000-8000-000000009002"
	canonicalSearchChapterID   = "00000000-0000-4000-8000-000000009003"
	canonicalSearchParagraphID = "00000000-0000-4000-8000-000000009004"
	canonicalSearchResultID    = "00000000-0000-4000-8000-000000009005"
)

func TestCanonicalCursorFixedRoundTripAndRequestBinding(t *testing.T) {
	updatedSince := time.Date(2026, 7, 24, 13, 15, 16, 123456000, time.FixedZone("offset", 3*60*60))
	normalized, err := normalizeSearchRequest(SearchRequest{
		ProjectID:  strings.ToUpper(canonicalSearchProjectID),
		Query:      "  поиск дракона  ",
		SourceView: governance.SourceViewDraft,
		Filters: SearchProjectFilters{
			BookID:       canonicalSearchBookID,
			ChapterID:    canonicalSearchChapterID,
			ResultKinds:  []SearchResultKind{SearchResultKindChapter},
			UpdatedSince: &updatedSince,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.ProjectID != canonicalSearchProjectID || normalized.Query != "поиск дракона" ||
		normalized.Scope != SearchScopeAll || normalized.Limit != defaultSearchLimit ||
		!normalized.Filters.UpdatedSince.Equal(updatedSince.UTC()) {
		t.Fatalf("normalized request = %#v", normalized)
	}
	if got := normalized.Filters.ResultKinds; len(got) != 1 || got[0] != SearchResultKindChapter {
		t.Fatalf("normalized result kinds = %v", got)
	}

	requestHash := searchRequestHash(normalized)
	cursor := searchCursor{
		RequestHash:  requestHash,
		ManifestHash: [32]byte{1, 2, 3},
		RankBits:     math.Float64bits(0.875),
		BookID:       mustCanonicalCursorUUIDForTest(t, canonicalSearchBookID),
		ChapterID:    mustCanonicalCursorUUIDForTest(t, canonicalSearchChapterID),
		ResultID:     mustCanonicalCursorUUIDForTest(t, canonicalSearchResultID),
	}
	encoded, err := encodeSearchCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != searchCursorEncodedLength {
		t.Fatalf("encoded length = %d, want %d", len(encoded), searchCursorEncodedLength)
	}
	decodedBytes, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decodedBytes) != searchCursorBinaryLength || strings.Contains(string(decodedBytes), normalized.Query) {
		t.Fatalf("cursor bytes length/content = %d/%q", len(decodedBytes), decodedBytes)
	}
	got, err := decodeSearchCursor(encoded, requestHash)
	if err != nil {
		t.Fatal(err)
	}
	if got != cursor {
		t.Fatalf("decoded cursor = %#v, want %#v", got, cursor)
	}

	changed := normalized
	changed.Limit++
	if _, err := decodeSearchCursor(encoded, searchRequestHash(changed)); err == nil {
		t.Fatal("request-mismatched cursor accepted")
	}
	changes := map[string]func(*SearchRequest){
		"query":   func(request *SearchRequest) { request.Query = "другой поиск" },
		"view":    func(request *SearchRequest) { request.SourceView = governance.SourceViewPublished },
		"scope":   func(request *SearchRequest) { request.Scope = SearchScopeChapters },
		"book":    func(request *SearchRequest) { request.Filters.BookID = canonicalSearchResultID },
		"chapter": func(request *SearchRequest) { request.Filters.ChapterID = canonicalSearchResultID },
		"result kinds": func(request *SearchRequest) {
			request.Filters.ResultKinds = nil
		},
		"updated": func(request *SearchRequest) {
			value := request.Filters.UpdatedSince.Add(time.Second)
			request.Filters.UpdatedSince = &value
		},
		"limit": func(request *SearchRequest) { request.Limit++ },
	}
	for name, mutate := range changes {
		t.Run(name, func(t *testing.T) {
			candidate := normalized
			candidate.Filters.ResultKinds = append([]SearchResultKind(nil), normalized.Filters.ResultKinds...)
			mutate(&candidate)
			if searchRequestHash(candidate) == requestHash {
				t.Fatal("request change did not change hash")
			}
		})
	}
}

func TestCanonicalCursorRejectsMalformedOrStructurallyInvalidPayload(t *testing.T) {
	hash := [32]byte{9}
	valid := searchCursor{
		RequestHash: hash, ManifestHash: [32]byte{8}, RankBits: math.Float64bits(0.5),
		BookID: mustCanonicalCursorUUIDForTest(t, canonicalSearchBookID), ChapterID: mustCanonicalCursorUUIDForTest(t, canonicalSearchChapterID),
		ResultID: mustCanonicalCursorUUIDForTest(t, canonicalSearchResultID),
	}
	encoded, err := encodeSearchCursor(valid)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]string{
		"padding":   encoded + "=",
		"short":     encoded[:len(encoded)-1],
		"oversize":  encoded + "A",
		"alphabet":  strings.Repeat("*", searchCursorEncodedLength),
		"bad magic": mutateCanonicalCursorForTest(t, raw, func(payload []byte) { payload[0] ^= 1 }),
		"nan rank": mutateCanonicalCursorForTest(t, raw, func(payload []byte) {
			for i := 76; i < 84; i++ {
				payload[i] = 0xff
			}
		}),
		"zero book": mutateCanonicalCursorForTest(t, raw, func(payload []byte) {
			for i := 84; i < 100; i++ {
				payload[i] = 0
			}
		}),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSearchCursor(candidate, hash); err == nil {
				t.Fatal("invalid cursor accepted")
			}
		})
	}
}

func TestCanonicalCursorCarriesOnlyChapterLevelIdentity(t *testing.T) {
	hash := [32]byte{9}
	encoded, err := encodeSearchCursor(searchCursor{
		RequestHash: hash, ManifestHash: [32]byte{8}, RankBits: math.Float64bits(0.5),
		BookID: mustCanonicalCursorUUIDForTest(t, canonicalSearchBookID), ChapterID: mustCanonicalCursorUUIDForTest(t, canonicalSearchChapterID),
		ResultID: mustCanonicalCursorUUIDForTest(t, canonicalSearchResultID),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 132 {
		t.Fatalf("cursor payload bytes = %d, want 132 chapter-level bytes", len(raw))
	}
}

func mustCanonicalCursorUUIDForTest(t *testing.T, value string) [16]byte {
	t.Helper()
	_, raw, err := canonicalUUID(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mutateCanonicalCursorForTest(t *testing.T, raw []byte, mutate func([]byte)) string {
	t.Helper()
	payload := append([]byte(nil), raw...)
	mutate(payload)
	return base64.RawURLEncoding.EncodeToString(payload)
}
