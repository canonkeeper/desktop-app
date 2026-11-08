package sagadraft

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// specs/2026-11-02-writer-workshop.md, W2.3, W2.4: the words of the author are runs of letters or digits outside
// the marks of what he did not write; the marks are closed and not nested. The page counts by the same table.
func TestAuthorWordsAndMarksByTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/words.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text   string `json:"text"`
		Valid  bool   `json:"valid"`
		Author int    `json:"author"`
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
