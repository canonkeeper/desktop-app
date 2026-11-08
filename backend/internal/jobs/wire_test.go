package jobs

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
)

const (
	testProjectID = "10000000-0000-4000-8000-000000000001"
	testBookID    = "10000000-0000-4000-8000-000000000002"
	testChatID    = "10000000-0000-4000-8000-000000000003"
	testTurnID    = "10000000-0000-4000-8000-000000000004"
	testChapterID = "10000000-0000-4000-8000-000000000005"
)

func TestJobsServiceProducerContract(t *testing.T) {
	if _, ok := reflect.TypeOf((*Service)(nil)).MethodByName("CreateJob"); ok {
		t.Fatal("jobs.Service still exposes the caller-free generic CreateJob producer")
	}

	raw, err := json.Marshal(auth.SessionWriteProof{SessionHash: "session", CSRFSecretHash: "csrf"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("SessionWriteProof JSON = %s, want {}", raw)
	}
}

func TestCurrentJobKindsAreClosed(t *testing.T) {
	want := []Kind{
		KindImport,
		KindCanonTextIndex,
		KindResearchChatTurn,
		KindChapterCards,
		KindIllustration,
		KindSagaLayer,
		KindChapterDraft,
		KindAuthorMaterialsImport,
	}
	if got := CurrentKinds(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CurrentKinds() = %v, want %v", got, want)
	}
	got := CurrentKinds()
	got[0] = Kind("mutated")
	if CurrentKinds()[0] != KindImport {
		t.Fatal("CurrentKinds returned mutable backing storage")
	}
}

func TestSubjectCodecRoundTripsEveryArm(t *testing.T) {
	bookID := testBookID
	cases := []Subject{
		ImportSubject{Type: KindImport, ID: testProjectID, ProjectID: testProjectID},
		ImportSubject{Type: KindImport, ID: testProjectID, ProjectID: testProjectID, BookID: testBookID},
		CanonTextIndexSubject{Type: KindCanonTextIndex, ProjectID: testProjectID, SourceView: governance.SourceViewPublished, CatalogRevision: 1},
		ChapterCardsSubject{Type: KindChapterCards, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
		SagaLayerSubject{Type: KindSagaLayer, ProjectID: testProjectID, SourceView: governance.SourceViewDraft},
		ChapterDraftSubject{
			Type: KindChapterDraft, ProjectID: testProjectID, DraftID: testChapterID, Mode: ChapterDraftScenes, BookID: &bookID, AfterChapterID: &bookID,
			Revision: 4, PlanNumber: 2, Scenes: []int{1, 3}, From: &ChapterDraftPlace{From: "Начало.", To: "Конец."}, Tail: ChapterDraftTailKeep,
		},
		ChapterDraftSubject{Type: KindChapterDraft, ProjectID: testProjectID, DraftID: testChapterID, Mode: ChapterDraftSelection, Revision: 1, Place: &ChapterDraftPlace{Quote: "место"}, Instruction: "короче"},
		// specs/2026-11-06-writing-agent.md, WA13: a Job of a deleted draft and of the earlier writing is sealed.
		ChapterDraftSubject{Type: KindChapterDraft, ProjectID: testProjectID, DraftID: testChapterID, Mode: ChapterDraftWrite, BookID: &bookID, Erased: true},
		ChapterDraftSubject{Type: KindChapterDraft, ProjectID: testProjectID, DraftID: testChapterID, Mode: ChapterDraftSelection, Erased: true},
		ResearchChatTurnSubject{Type: KindResearchChatTurn, ProjectID: testProjectID, ChatID: testChatID, TurnID: testTurnID, SourceView: governance.SourceViewDraft, Scope: "book", BookID: &bookID},
		IllustrationSubject{Type: KindIllustration, ProjectID: testProjectID, ImageID: testChapterID},
		IllustrationSubject{Type: KindIllustration, ProjectID: testProjectID, ImageID: testChapterID, LayerID: testTurnID},
	}
	for _, want := range cases {
		t.Run(string(want.Kind()), func(t *testing.T) {
			raw, err := EncodeSubject(want.Kind(), want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeSubject(want.Kind(), raw)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DecodeSubject() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestImportSubjectCodecRoundTripsQueueLineage(t *testing.T) {
	raw := []byte(`{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `","expectedBookOrder":4,"importQueueId":"` + testBookID + `","importQueueItemId":"` + testChapterID + `"}`)
	got, err := DecodeSubject(KindImport, raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeSubject(KindImport, got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["importQueueId"] != testBookID || fields["importQueueItemId"] != testChapterID {
		t.Fatalf("queue lineage = %#v/%#v", fields["importQueueId"], fields["importQueueItemId"])
	}
}

func TestSubjectCodecRejectsUnknownDuplicateOrMismatchedFields(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		raw  string
	}{
		{"unknown discriminator", KindImport, `{"type":"future","id":"` + testProjectID + `","projectId":"` + testProjectID + `"}`},
		{"trailing json", KindImport, `{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `"} {}`},
		{"unknown property", KindImport, `{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `","extra":true}`},
		{"incomplete queue lineage", KindImport, `{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `","importQueueId":"` + testBookID + `"}`},
		{"missing required", KindImport, `{"type":"import","projectId":"` + testProjectID + `"}`},
		{"duplicate discriminator", KindImport, `{"type":"import","type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `"}`},
		{"duplicate nested field", KindCanonTextIndex, `{"type":"canon_text_index","projectId":"` + testProjectID + `","sourceView":"draft","catalogRevision":1,"nested":{"x":1,"x":2}}`},
		{"project scope with book", KindResearchChatTurn, `{"type":"research_chat_turn","projectId":"` + testProjectID + `","chatId":"` + testChatID + `","turnId":"` + testTurnID + `","sourceView":"draft","scope":"project","bookId":"` + testBookID + `"}`},
		{"book scope without book", KindResearchChatTurn, `{"type":"research_chat_turn","projectId":"` + testProjectID + `","chatId":"` + testChatID + `","turnId":"` + testTurnID + `","sourceView":"draft","scope":"book"}`},
		{"zero revision", KindCanonTextIndex, `{"type":"canon_text_index","projectId":"` + testProjectID + `","sourceView":"draft","catalogRevision":0}`},
		{"text only flag", KindCanonTextIndex, `{"type":"canon_text_index","projectId":"` + testProjectID + `","sourceView":"draft","catalogRevision":1,"textOnly":true}`},
		{"removed enrichment", KindChapterCards, `{"type":"canon_book_enrichment","projectId":"` + testProjectID + `","sourceView":"draft"}`},
		{"kind mismatch", KindCanonTextIndex, `{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `"}`},
		{"removed export", KindImport, `{"type":"export","id":"` + testBookID + `","projectId":"` + testProjectID + `"}`},
		{"update with order", KindImport, `{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `","bookId":"` + testBookID + `","expectedBookOrder":2}`},
		// specs/2026-11-06-writing-agent.md, WA13: the earlier writing is history only, sealed; a sealed Job keeps no text.
		{"an unknown mode", KindChapterDraft, `{"type":"chapter_draft","projectId":"` + testProjectID + `","draftId":"` + testChapterID + `","mode":"plan","revision":1}`},
		{"the earlier writing unsealed", KindChapterDraft, `{"type":"chapter_draft","projectId":"` + testProjectID + `","draftId":"` + testChapterID + `","mode":"write"}`},
		{"the text of the earlier writing", KindChapterDraft, `{"type":"chapter_draft","projectId":"` + testProjectID + `","draftId":"` + testChapterID + `","mode":"write","sketch":"набросок","erased":true}`},
		{"a sealed Job with the author's words", KindChapterDraft, `{"type":"chapter_draft","projectId":"` + testProjectID + `","draftId":"` + testChapterID + `","mode":"selection","instruction":"короче","erased":true}`},
		{"an agent without its revision", KindChapterDraft, `{"type":"chapter_draft","projectId":"` + testProjectID + `","draftId":"` + testChapterID + `","mode":"check"}`},
		{"update in queue", KindImport, `{"type":"import","id":"` + testProjectID + `","projectId":"` + testProjectID + `","bookId":"` + testBookID + `","importQueueId":"` + testChatID + `","importQueueItemId":"` + testTurnID + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeSubject(tc.kind, []byte(tc.raw)); err == nil {
				t.Fatal("DecodeSubject accepted invalid payload")
			}
		})
	}
}

func TestResultCodecRoundTripsEveryArm(t *testing.T) {
	cases := []Result{
		ImportJobResult{Type: KindImport, BookID: testBookID, ChapterCount: 2, ParagraphCount: 3, ImportMode: "new_book", SourceFileName: "book.docx"},
		ImportJobResult{
			Type: KindImport, BookID: testBookID, ChapterCount: 4, ParagraphCount: 5, ImportMode: "update_book", SourceFileName: "book.docx",
			UnchangedChapters: ptr(1), ChangedChapters: ptr(2), AddedChapters: ptr(1), RemovedChapters: ptr(0),
		},
		CanonTextIndexResult{Type: KindCanonTextIndex, SourceView: governance.SourceViewDraft, TextCoverage: TextCoverage{CurrentBooks: 1, TotalBooks: 2, NonCurrentBooks: 1, TotalNonCurrentBooks: 2}},
		ChapterCardsResult{Type: KindChapterCards, SourceView: governance.SourceViewDraft, Chapters: 3, Built: 2},
		SagaLayerResult{Type: KindSagaLayer, SourceView: governance.SourceViewDraft, Chapters: 3, Threads: 2, BooksBuilt: 1, Unverified: 1},
		ChapterDraftResult{Type: KindChapterDraft, Mode: ChapterDraftWrite, Total: 2, Chapters: []ChapterDraftSource{{ChapterID: testChapterID, Revision: 3}}},
		ChapterDraftResult{
			Type: KindChapterDraft, Mode: ChapterDraftScenes, Phase: ChapterDraftPhaseScenes, Total: 3, Done: 1, Pack: testBookID, BeforeVersion: 2,
			SceneRuns: []ChapterDraftRun{{Scene: 1, Words: 400, Attempts: 1, From: "Начало.", To: "Конец.", Text: "Начало. Конец."}},
		},
		IllustrationResult{Type: KindIllustration, ImageNumber: 7},
		ResearchChatTurnResult{Type: KindResearchChatTurn, ChatID: testChatID, TurnID: testTurnID, Response: "Exact completed response."},
	}
	for _, want := range cases {
		t.Run(string(want.Kind()), func(t *testing.T) {
			raw, err := EncodeResult(want.Kind(), want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeResult(want.Kind(), raw)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DecodeResult() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestResearchChatTurnResultCodecAndProjectionPreserveExactResponse(t *testing.T) {
	const response = "Exact completed response.\nSource: chapter 3."
	raw := []byte(`{"type":"research_chat_turn","chatId":"` + testChatID + `","turnId":"` + testTurnID + `","response":"Exact completed response.\nSource: chapter 3."}`)

	result, err := DecodeResult(KindResearchChatTurn, raw)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := publicJobResult(KindResearchChatTurn, result, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(KindResearchChatTurn, projected)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.Response != response {
		t.Fatalf("projected response = %q, want %q", fields.Response, response)
	}
}

func TestResearchChatTurnResultCodecRejectsMissingOrBlankResponse(t *testing.T) {
	for _, raw := range []string{
		`{"type":"research_chat_turn","chatId":"` + testChatID + `","turnId":"` + testTurnID + `"}`,
		`{"type":"research_chat_turn","chatId":"` + testChatID + `","turnId":"` + testTurnID + `","response":""}`,
		`{"type":"research_chat_turn","chatId":"` + testChatID + `","turnId":"` + testTurnID + `","response":" \t\n"}`,
	} {
		if _, err := DecodeResult(KindResearchChatTurn, []byte(raw)); err == nil {
			t.Fatalf("DecodeResult accepted invalid response payload: %s", raw)
		}
	}
}

func TestResultCodecRejectsOpenOrMismatchedPayload(t *testing.T) {
	cases := []struct {
		kind Kind
		raw  string
	}{
		{KindImport, `{}`},
		{KindImport, `{"type":"progress"}`},
		{KindImport, `{"type":"import","bookId":"` + testBookID + `","chapterCount":1,"paragraphCount":1,"importMode":"new_book","sourceFileName":"x","private":true}`},
		{KindImport, `{"type":"export","bookId":"` + testBookID + `","format":"epub"}`},
		{KindCanonTextIndex, `{"type":"import","bookId":"` + testBookID + `","chapterCount":1,"paragraphCount":1,"importMode":"new_book","sourceFileName":"x"}`},
		{KindImport, `{"type":"import","bookId":"` + testBookID + `","chapterCount":1,"paragraphCount":1,"importMode":"update_book","sourceFileName":"x"}`},
		{KindImport, `{"type":"import","bookId":"` + testBookID + `","chapterCount":1,"paragraphCount":1,"importMode":"new_book","sourceFileName":"x","addedChapters":1}`},
		{KindChapterDraft, `{"type":"chapter_draft","mode":"plan"}`},
		{KindChapterDraft, `{"type":"chapter_draft","mode":"write","plan":"план","examples":6,"threads":12}`},
		{KindChapterDraft, `{"type":"chapter_draft","mode":"write","review":{"round":2}}`},
		// specs/2026-11-06-writing-agent.md, WA13: what the earlier writing kept is sealed by the migration of S8.
		{KindChapterDraft, `{"type":"chapter_draft","mode":"write","phase":"pieces","pieces":["кусок"],"judged":true}`},
		{KindChapterDraft, `{"type":"chapter_draft","mode":"write","text":"глава","notes":[{"kind":"meaning","quote":"q","body":"b","status":"open"}]}`},
	}
	for _, tc := range cases {
		if _, err := DecodeResult(tc.kind, []byte(tc.raw)); err == nil {
			t.Fatalf("DecodeResult(%q, %s) accepted invalid payload", tc.kind, tc.raw)
		}
	}
}

func TestJobErrorCodecIsClosedAndBounded(t *testing.T) {
	want := Error{Code: "worker.handler_failed", Message: "handler failed"}
	raw, err := EncodeError(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeError(raw)
	if err != nil || got != want {
		t.Fatalf("DecodeError() = %#v, %v", got, err)
	}
	cases := []string{
		`{"code":"worker.handler_failed","message":"handler failed","private":true}`,
		`{"code":"","message":"handler failed"}`,
		`{"code":"Worker.failed","message":"handler failed"}`,
		`{"code":"worker..failed","message":"handler failed"}`,
		"{\"code\":\"worker.failed\",\"message\":\"line\\nfeed\"}",
		`{"code":"worker.failed","message":"` + strings.Repeat("é", 121) + `"}`,
		`{"code":"worker.failed","message":"safe"} {}`,
		`{"code":"worker.failed","message":"safe","wrapped":"database exploded"}`,
	}
	for _, raw := range cases {
		if _, err := DecodeError([]byte(raw)); err == nil {
			t.Fatalf("DecodeError accepted %s", raw)
		}
	}
	if _, err := json.Marshal(want); err != nil {
		t.Fatal(err)
	}
}

func ptr(value int) *int { return &value }
