package sagadraft

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// specs/2026-11-02-writer-workshop.md, W2.3, W2.4: the words of the author are runs of letters or digits outside
// the marks of what he did not write; the marks are closed and not nested. The page counts by the same table. A mark of
// a face does not part a word, and the marks of all three kinds make a tree
// (specs/2026-11-08-italic-bold-in-text.md, TF1.1, TF1.4).
func TestAuthorWordsAndMarksByTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/words.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text   string `json:"text"`
		Valid  bool   `json:"valid"`
		Author int    `json:"author"`
		Words  *int   `json:"words"`
		// All are the words of the working text as the head of the chapter and the list of «Письмо» say them
		// (specs/2026-11-12-ux12-writing.md WR2.1): the author's and the helper's, a mark of the helper parting a word.
		All *int `json:"all"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("the table = %v", err)
	}
	for _, test := range cases {
		if got := ValidMarks(test.Text); got != test.Valid {
			t.Fatalf("ValidMarks(%q) = %v", test.Text, got)
		}
		if test.Valid {
			if got := AuthorWords(test.Text); got != test.Author {
				t.Fatalf("AuthorWords(%q) = %d, want %d", test.Text, got, test.Author)
			}
		}
		if test.Valid && (test.All == nil || len(WordsOf(test.Text)) != *test.All) {
			t.Fatalf("WordsOf(%q) = %d, the table says %v", test.Text, len(WordsOf(test.Text)), test.All)
		}
		if test.Words != nil {
			if got := len(WordsOf(WithoutMarks(test.Text))); got != *test.Words {
				t.Fatalf("WordsOf(WithoutMarks(%q)) = %d, want %d", test.Text, got, *test.Words)
			}
		}
	}
}

// W2.4: the typical length is the median of the last chapters before the place; fewer than three say nothing.
func TestTypicalWordsIsTheMedian(t *testing.T) {
	for _, test := range []struct {
		words []int
		want  int
		known bool
	}{
		{[]int{3000, 1000}, 0, false},
		{[]int{3000, 1000, 2000}, 2000, true},
		{[]int{4000, 1000, 2000, 3000}, 2500, true},
	} {
		got := TypicalWords(test.words)
		if (got != nil) != test.known || got != nil && *got != test.want {
			t.Fatalf("TypicalWords(%v) = %v", test.words, got)
		}
	}
}

// W2.3: an edit of the draft with broken marks is refused.
func TestBrokenMarksAreRefused(t *testing.T) {
	if !errors.Is(CheckMarks("а ⟦б"), ErrInvalidMarks) || CheckMarks("а ⟦б⟧") != nil {
		t.Fatal("CheckMarks does not hold the marks")
	}
}

// TF1.4, TF2.1: the words of a text are the same with its faces as without them, and what a Job writes from has no
// mark of any kind.
func TestTheFacesDoNotPartWords(t *testing.T) {
	if got := WordsOf("сло⟨в⟩о ⟪мёд⟫"); len(got) != 2 || got[0] != "слово" || got[1] != "мёд" {
		t.Fatalf("WordsOf = %q", got)
	}
	if got := WithoutMarks("⟦Он ⟨шёл⟩⟧ ⟪домой⟫"); got != "Он шёл домой" {
		t.Fatalf("WithoutMarks = %q", got)
	}
}
