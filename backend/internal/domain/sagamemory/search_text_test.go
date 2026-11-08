package sagamemory

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPostingsRoundTrip(t *testing.T) {
	text := BuildChapterSearchText([]string{"Тихо-тихо, тихо.", "", "Тихо!"}, map[string]string{"тихо": "тих", "тихотихо": "тихотих"})
	if !reflect.DeepEqual(text.ParagraphTokens, []int32{4, 0, 1}) {
		t.Fatalf("tokens = %v", text.ParagraphTokens)
	}
	var got []string
	for _, word := range text.Words {
		if word.Word != "тихо" {
			continue
		}
		if err := DecodePostings(word.Postings, func(paragraph int, positions []int32) {
			got = append(got, fmt.Sprintf("%d:%s", paragraph, positionsText(positions)))
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, []string{"0:0,0,1", "2:0"}) {
		t.Fatalf("postings of тихо = %v, want both parts of the hyphenated word at position 0", got)
	}
	if DecodePostings([]byte{0, 1, 0}, func(int, []int32) {}) == nil || DecodePostings([]byte{1, 5, 0}, func(int, []int32) {}) == nil {
		t.Fatal("malformed postings must be rejected")
	}
}

func positionsText(positions []int32) string {
	parts := make([]string, len(positions))
	for i, position := range positions {
		parts[i] = fmt.Sprint(position)
	}
	return strings.Join(parts, ",")
}

func TestCheckAcceptsBuiltRowsAndRejectsBrokenOnes(t *testing.T) {
	text := BuildChapterSearchText([]string{"Тихо-тихо, кот и кот.", "", "Кот."}, map[string]string{"тихо": "тих", "кот": "кот", "и": ""})
	if err := text.Check(); err != nil {
		t.Fatalf("built rows: %v", err)
	}
	broken := text
	broken.ParagraphTokens = append([]int32(nil), text.ParagraphTokens...)
	broken.ParagraphTokens[0]++
	if broken.Check() == nil {
		t.Fatal("a token count the postings do not add up to passed")
	}
	dropped := text
	dropped.Words = text.Words[1:]
	if dropped.Check() == nil {
		t.Fatal("rows without a word of the text passed")
	}
	twice := text
	twice.Words = append(append([]SearchWord(nil), text.Words...), text.Words[0])
	if twice.Check() == nil {
		t.Fatal("a repeated word passed")
	}
}
