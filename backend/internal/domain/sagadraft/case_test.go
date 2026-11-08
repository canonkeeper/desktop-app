package sagadraft

import "testing"

func TestTheCaseOfAChapterGoesByTheAuthorsWordsAndHisChoice(t *testing.T) {
	typical := 2000
	for _, test := range []struct {
		words   int
		typical *int
		choice  Case
		want    Case
	}{
		{0, &typical, "", CaseScratch},
		{199, &typical, "", CaseScratch},
		{200, &typical, "", CaseContinue},
		{1599, &typical, "", CaseContinue},
		{1600, &typical, "", CaseNearlyDone},
		{149, nil, "", CaseScratch},
		{249, nil, "", CaseScratch}, // 0.1 of the default chapter of 2500 words
		{250, nil, "", CaseContinue},
		{2000, nil, "", CaseNearlyDone},
		{0, &typical, CaseNearlyDone, CaseNearlyDone},
		{1800, &typical, CaseContinue, CaseContinue},
		{1800, &typical, "unknown", CaseNearlyDone},
	} {
		if got := CaseOf(test.words, test.typical, test.choice); got != test.want {
			t.Errorf("CaseOf(%d, %v, %q) = %q, want %q", test.words, test.typical, test.choice, got, test.want)
		}
	}
}

func TestTheCaseOfADraftCountsOnlyTheAuthorsWords(t *testing.T) {
	typical := 100
	draft := Draft{Text: "раз два три четыре пять шесть семь восемь девять десять " + string(MarkOpen) + "много слов помощника тут стоит" + string(MarkClose), TypicalWords: &typical}
	if got := draft.Case(""); got != CaseScratch {
		t.Fatalf("case = %q", got)
	}
}
