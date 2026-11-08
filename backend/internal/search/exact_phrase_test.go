package search

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/governance"
)

func TestCanonicalExactPhrasePrimitiveExists(t *testing.T) {
	serviceType := reflect.TypeOf((*Service)(nil))
	if _, ok := serviceType.MethodByName("SearchExactPhrase"); !ok {
		t.Fatal("Service.SearchExactPhrase is missing")
	}
}

func TestCanonicalExactPhraseNormalizesUnicodeAndBindsEffectiveRequest(t *testing.T) {
	request, err := normalizeExactPhraseRequest(ExactPhraseRequest{
		ProjectID:  canonicalSearchProjectID,
		SourceView: governance.SourceViewDraft,
		BookID:     canonicalSearchBookID,
		ChapterID:  canonicalSearchChapterID,
		Query:      "  A\u030A\tẞ\u00a0ПРИВЕТ\nМИР  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Query != "å ss привет мир" || request.Limit != defaultSearchLimit {
		t.Fatalf("normalized request = %#v", request)
	}

	baseHash := exactPhraseRequestHash(request)
	for name, mutate := range map[string]func(*ExactPhraseRequest){
		"query":   func(value *ExactPhraseRequest) { value.Query = "другое" },
		"view":    func(value *ExactPhraseRequest) { value.SourceView = governance.SourceViewPublished },
		"book":    func(value *ExactPhraseRequest) { value.BookID = canonicalSearchResultID },
		"chapter": func(value *ExactPhraseRequest) { value.ChapterID = "" },
		"limit":   func(value *ExactPhraseRequest) { value.Limit++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := request
			mutate(&changed)
			if exactPhraseRequestHash(changed) == baseHash {
				t.Fatalf("request hash did not bind %s", name)
			}
		})
	}

	for name, query := range map[string]string{
		"empty": "\u2003\t\n", "too long": strings.Repeat("я", maxQueryRunes+1), "invalid UTF-8": string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeExactPhraseRequest(ExactPhraseRequest{
				ProjectID: canonicalSearchProjectID, SourceView: governance.SourceViewDraft, BookID: canonicalSearchBookID, Query: query,
			})
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestCanonicalExactPhraseCursorIsFixedStrictAndKindBound(t *testing.T) {
	request, err := normalizeExactPhraseRequest(ExactPhraseRequest{
		ProjectID: canonicalSearchProjectID, SourceView: governance.SourceViewDraft,
		BookID: canonicalSearchBookID, Query: "точная фраза", Limit: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestHash := exactPhraseRequestHash(request)
	cursor := exactPhraseCursor{
		RequestHash: requestHash, ManifestHash: [32]byte{1}, ChapterOrder: 2,
		ChapterID: mustCanonicalCursorUUIDForTest(t, canonicalSearchChapterID),
	}
	negativeChapter := cursor
	negativeChapter.ChapterOrder = -1
	if _, err := encodeExactPhraseCursor(negativeChapter); !errors.Is(err, ErrValidation) {
		t.Fatalf("negative chapter order error = %v", err)
	}
	encoded, err := encodeExactPhraseCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != exactPhraseCursorEncodedLength {
		t.Fatalf("encoded length = %d", len(encoded))
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) != exactPhraseCursorBinaryLength || string(payload[:4]) != "CKE3" {
		t.Fatalf("payload = %x, err = %v", payload, err)
	}
	decoded, err := decodeExactPhraseCursor(encoded, requestHash)
	if err != nil || decoded != cursor {
		t.Fatalf("decoded = %#v, err = %v", decoded, err)
	}

	other := request
	other.Query = "другая фраза"
	if _, err := decodeExactPhraseCursor(encoded, exactPhraseRequestHash(other)); !errors.Is(err, ErrValidation) {
		t.Fatalf("request mismatch error = %v", err)
	}
	lexical, err := encodeSearchCursor(searchCursor{
		RequestHash: [32]byte{1}, ManifestHash: [32]byte{2}, RankBits: 0x3fe0000000000000,
		BookID: mustCanonicalCursorUUIDForTest(t, canonicalSearchBookID), ChapterID: cursor.ChapterID,
		ResultID: mustCanonicalCursorUUIDForTest(t, canonicalSearchResultID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeExactPhraseCursor(lexical, requestHash); !errors.Is(err, ErrValidation) {
		t.Fatalf("lexical cursor error = %v", err)
	}

	mutated := []byte(encoded)
	mutated[len(mutated)-1] = '/'
	if _, err := decodeExactPhraseCursor(string(mutated), requestHash); !errors.Is(err, ErrValidation) {
		t.Fatalf("non URL-safe cursor error = %v", err)
	}
}

func TestCanonicalExactPhraseServiceNeverFallsBackAndBindsManifest(t *testing.T) {
	backend := &exactPhraseStoreForTest{output: ExactPhraseSearchOutput{
		ManifestHash: governance.Digest{9}, Truncated: true,
		Matches: []ExactPhraseMatch{{
			ChapterOrder: 1, ChapterID: canonicalSearchChapterID,
			StartByte: 3, EndByte: 9, SourceRef: canonicalSearchChapterID,
		}},
		NextCursor: &ExactPhraseCursor{
			ChapterOrder: 1, ChapterID: canonicalSearchChapterID,
		},
	}}
	request := ExactPhraseRequest{
		ProjectID: canonicalSearchProjectID, SourceView: governance.SourceViewDraft,
		BookID: canonicalSearchBookID, Query: "  ПРИВЕТ\tМИР ", Limit: 1,
	}
	response, err := NewService(backend).SearchExactPhrase(context.Background(), Actor{UserID: canonicalSearchResultID}, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || !response.Truncated || response.NextCursor == "" || len(backend.exactCalls) != 1 || len(backend.lexicalCalls) != 0 {
		t.Fatalf("response/calls = %#v / %#v / %#v", response, backend.exactCalls, backend.lexicalCalls)
	}
	if response.Items[0].SourceRef != canonicalSearchChapterID || response.Items[0].ChapterID != canonicalSearchChapterID {
		t.Fatalf("chapter result = %#v", response.Items[0])
	}
	if backend.exactCalls[0].Query != "привет мир" || backend.exactCalls[0].ExpectedManifestHash != nil {
		t.Fatalf("first exact call = %#v", backend.exactCalls[0])
	}

	request.Cursor = response.NextCursor
	backend.output = ExactPhraseSearchOutput{ManifestHash: governance.Digest{9}, Matches: []ExactPhraseMatch{}}
	second, err := NewService(backend).SearchExactPhrase(context.Background(), Actor{UserID: canonicalSearchResultID}, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Items == nil || second.Truncated || second.NextCursor != "" || len(backend.lexicalCalls) != 0 {
		t.Fatalf("second response = %#v, lexical calls = %#v", second, backend.lexicalCalls)
	}
	if got := backend.exactCalls[1].ExpectedManifestHash; got == nil || *got != (governance.Digest{9}) {
		t.Fatalf("expected manifest = %#v", got)
	}
	if got := backend.exactCalls[1].Cursor; got == nil || got.ChapterID != canonicalSearchChapterID {
		t.Fatalf("cursor = %#v", got)
	}
}

type exactPhraseStoreForTest struct {
	output       ExactPhraseSearchOutput
	err          error
	exactCalls   []ExactPhraseSearchInput
	lexicalCalls []LexicalSearchInput
}

func (searcher *exactPhraseStoreForTest) SearchLexical(_ context.Context, input LexicalSearchInput) (LexicalSearchOutput, error) {
	searcher.lexicalCalls = append(searcher.lexicalCalls, input)
	return LexicalSearchOutput{}, errors.New("lexical fallback invoked")
}

func (searcher *exactPhraseStoreForTest) SearchExactPhrase(_ context.Context, input ExactPhraseSearchInput) (ExactPhraseSearchOutput, error) {
	searcher.exactCalls = append(searcher.exactCalls, input)
	return searcher.output, searcher.err
}
