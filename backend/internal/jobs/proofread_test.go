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

// specs/2026-11-11-writing-check-signal.md CS1.1, CS3.2: what a check counted and measured is numbers, not text: the row
// of a finished Job keeps it, and only counts that add up and measures with a name and a value are kept.
func TestACheckKeepsItsNumbersInTheResult(t *testing.T) {
	result := ChapterDraftResult{Type: KindChapterDraft, Mode: ChapterDraftCheck, Phase: ChapterDraftPhaseCheck, Total: 1, Done: 1,
		Check: &ChapterDraftCheckTally{Asked: 7, Answered: 7, Weak: 2, Words: 1200, Measures: []ChapterDraftMeasure{{Feature: "sentSd", Value: 8.52, Mean: 3.99, SD: 0.29}}}}
	raw, err := EncodeResult(KindChapterDraft, result)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := DecodeResult(KindChapterDraft, raw); err != nil || !reflect.DeepEqual(back, result) {
		t.Fatalf("decoded %+v, %v", back, err)
	}
	if sealed := result.Sealed(); !reflect.DeepEqual(sealed.Check, result.Check) {
		t.Fatalf("sealed %+v", sealed.Check)
	}
	for _, wrong := range []ChapterDraftCheckTally{{Asked: 7, Answered: 2, Weak: 3}, {Measures: []ChapterDraftMeasure{{Value: 1}}}} {
		bad := result
		bad.Check = &wrong
		if _, err := EncodeResult(KindChapterDraft, bad); err == nil {
			t.Fatalf("a wrong tally was accepted: %+v", wrong)
		}
	}
}
