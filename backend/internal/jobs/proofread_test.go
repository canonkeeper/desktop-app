package jobs

import (
	"reflect"
	"testing"
)

// specs/2026-11-07-writing-proofread.md, PR3.1, PR3.9: a proofreading holds nothing of the author's words in its subject,
// keeps its corrections in its result only while it runs, and only a proofreading keeps them.
func TestAProofreadingJobKeepsItsCorrectionsOnlyWhileItRuns(t *testing.T) {
	subject := ChapterDraftSubject{Type: KindChapterDraft, ProjectID: testProjectID, DraftID: testChapterID, Mode: ChapterDraftProofread, Revision: 2}
	raw, err := EncodeSubject(KindChapterDraft, subject)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSubject(KindChapterDraft, raw)
	if err != nil || !reflect.DeepEqual(decoded, subject) {
		t.Fatalf("decoded %+v, %v", decoded, err)
	}
	words := subject
	words.Instruction = "короче"
	if _, err := EncodeSubject(KindChapterDraft, words); err == nil {
		t.Fatal("a proofreading with the author's words was accepted")
	}
	result := ChapterDraftResult{Type: KindChapterDraft, Mode: ChapterDraftProofread, Phase: ChapterDraftPhaseProofread, Total: 3, Done: 1,
		Findings: []ChapterDraftFinding{{Quote: "и и", Replacement: "и", Category: "опечатка", Start: 4, End: 9}}}
	raw, err = EncodeResult(KindChapterDraft, result)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := DecodeResult(KindChapterDraft, raw); err != nil || !reflect.DeepEqual(back, result) {
		t.Fatalf("decoded %+v, %v", back, err)
	}
	if sealed := result.Sealed(); sealed.Findings != nil || sealed.Done != 1 {
		t.Fatalf("sealed %+v", sealed)
	}
	check := result
	check.Mode = ChapterDraftCheck
	if _, err := EncodeResult(KindChapterDraft, check); err == nil {
		t.Fatal("a check kept corrections of a proofreading")
	}
	empty := result
	empty.Findings = []ChapterDraftFinding{{Quote: "и и", Category: "опечатка", Start: 4, End: 9}}
	if _, err := EncodeResult(KindChapterDraft, empty); err == nil {
		t.Fatal("a correction without its replacement was accepted")
	}
}
