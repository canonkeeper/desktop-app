package sagadraft

// Case says what of the working text is the author's and what the writing may write
// (specs/2026-11-06-writing-agent.md, WA1, D7).
type Case string

// The three cases of a chapter.
const (
	// CaseScratch: the chapter is written from the idea; a text, if any, is a sketch the chapter replaces.
	CaseScratch Case = "scratch"
	// CaseContinue: the author's beginning stays byte for byte; the writing goes on after it.
	CaseContinue Case = "continue"
	// CaseNearlyDone: the chapter is the author's; the model only checks it and fills the gaps he marked.
	CaseNearlyDone Case = "nearly_done"
)

// Thresholds of the cases (WA1, Data): shares of the usual chapter of the author, tuned by the exam.
const (
	ScratchMinWords = 150
	ScratchShare    = 0.1
	NearlyDoneShare = 0.8
)

// DefaultChapterWords is the usual chapter when the saga has too few chapters before the place to measure one.
const DefaultChapterWords = 2500

// ParseCase reads a case the author chose.
func ParseCase(value string) (Case, bool) {
	switch Case(value) {
	case CaseScratch, CaseContinue, CaseNearlyDone:
		return Case(value), true
	}
	return "", false
}

// CaseOf counts the case of a chapter from the author's words and his usual chapter (typical, nil without
// enough chapters before the place); the author's own choice wins (WA1.1, WA1.2).
func CaseOf(authorWords int, typical *int, choice Case) Case {
	if _, ok := ParseCase(string(choice)); ok {
		return choice
	}
	usual := float64(DefaultChapterWords)
	if typical != nil && *typical > 0 {
		usual = float64(*typical)
	}
	words := float64(authorWords)
	switch {
	case words < max(ScratchMinWords, ScratchShare*usual):
		return CaseScratch
	case words >= NearlyDoneShare*usual:
		return CaseNearlyDone
	default:
		return CaseContinue
	}
}

// Case is the case of the draft: by the author's words in its working text, unless he chose one. A draft that edits a
// chapter of the book is the author's written chapter whatever its length: it is nearly done, and nothing asks what to do
// with his text (specs/2026-11-11-ux9-writing-a.md WA4.1); one whose chapter is deleted goes in as a new chapter.
func (draft Draft) Case(choice Case) Case {
	if draft.Edits != nil && !draft.Edits.Gone {
		return CaseNearlyDone
	}
	return CaseOf(AuthorWords(draft.Text), draft.TypicalWords, choice)
}
