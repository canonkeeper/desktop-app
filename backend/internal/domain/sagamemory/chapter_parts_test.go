package sagamemory

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// chapterOfLengths is a chapter of the saga «Северный предел» whose paragraphs hold the given runes: sentences of the same
// words, a paragraph shorter than a sentence cut to its length.
func chapterOfLengths(lengths ...int) *Chapter {
	const sentence = "Ветер выл над заставой. "
	chapter := &Chapter{ID: "chapter-north", Key: "7", BookOrder: 1, Title: "Глава 7. Застава"}
	for _, length := range lengths {
		text := strings.Repeat(sentence, length/utf8.RuneCountInString(sentence)+1)
		chapter.Paragraphs = append(chapter.Paragraphs, Paragraph{Text: strings.TrimSpace(string([]rune(text)[:length]))})
	}
	for index := range chapter.Paragraphs {
		if got := utf8.RuneCountInString(chapter.Paragraphs[index].Text); got != lengths[index] {
			chapter.Paragraphs[index].Text += strings.Repeat("ы", lengths[index]-got)
		}
	}
	return chapter
}

func repeated(length, count int) []int {
	lengths := make([]int, count)
	for index := range lengths {
		lengths[index] = length
	}
	return lengths
}

// partRunes are the runes of the text of a part, as PartText gives it.
func partRunes(chapter *Chapter, part PartRange) int {
	runes := 0
	for number := part.First; number <= part.Last; number++ {
		from, to := 0, utf8.RuneCountInString(chapter.Paragraphs[number-1].Text)
		if number == part.First {
			from = part.Start
		}
		if number == part.Last && part.End > 0 {
			to = part.End
		}
		runes += to - from
	}
	return runes
}

// specs/2026-11-17-long-chapters.md LC1.1, LC1.2: a chapter of at most 350 000 runes is one part, and its input and its
// parse are those of before to the byte.
func TestAChapterUpToTheSplitIsOnePartReadAsBefore(t *testing.T) {
	lengths := append(repeated(1_000, 349), 1_000)
	chapter := chapterOfLengths(lengths...)
	if parts := ChapterParts(chapter); !reflect.DeepEqual(parts, []PartRange{{First: 1, Last: 350}}) {
		t.Fatalf("a chapter of 350 000 runes = %+v", parts)
	}
	if parts := PartsOf(lengths); !reflect.DeepEqual(parts, []PartRange{{First: 1, Last: 350}}) {
		t.Fatalf("its lengths = %+v", parts)
	}
	short := &Chapter{Key: "3", BookOrder: 1, Title: "Глава 3. Ледяной перевал", Paragraphs: []Paragraph{{Text: "Застава стояла у перевала."}, {Text: "Огни горели."}}}
	if text := ChapterText(short); text != "Том 1, Глава 3. Ледяной перевал (гл. 3)\n\n¶1 Застава стояла у перевала.\n¶2 Огни горели.\n" {
		t.Fatalf("the input of a chapter = %q", text)
	}
	short.Key = ""
	if text := ChapterText(short); text != "Том 1, Глава 3. Ледяной перевал\n\n¶1 Застава стояла у перевала.\n¶2 Огни горели.\n" {
		t.Fatalf("the input of a chapter without a key = %q", text)
	}
	card, err := ParseCard(`{"summary": "Застава.", "events": [{"event": "Огни", "p": 2}, {"event": "Вне", "p": 3}, {"event": "Ноль", "p": 0}]}`, 1, 2)
	if err != nil || card.Events[0].P != 2 || card.Events[1].P != 0 || card.Events[2].P != 0 {
		t.Fatalf("the parse of a chapter = %+v, %v", card, err)
	}
}

// LC1.1, LC1.2: a chapter one rune over the split is two nearly equal parts by its paragraphs, each read with the numbers of
// its paragraphs in the chapter and parsed within them.
func TestAChapterOverTheSplitIsTwoNearlyEqualParts(t *testing.T) {
	lengths := append(repeated(1_000, 350), 1)
	chapter := chapterOfLengths(lengths...)
	want := []PartRange{{First: 1, Last: 175}, {First: 176, Last: 351}}
	if parts := ChapterParts(chapter); !reflect.DeepEqual(parts, want) {
		t.Fatalf("a chapter of 350 001 runes = %+v, want %+v", parts, want)
	}
	if parts := PartsOf(lengths); !reflect.DeepEqual(parts, want) {
		t.Fatalf("its lengths = %+v, want %+v", parts, want)
	}
	text := PartText(chapter, want[1], 2, 2)
	head := "Том 1, Глава 7. Застава (гл. 7)\nЧасть 2 из 2: абзацы ¶176–¶351 этой главы, остальные части читаются отдельно — пиши карточку только этого текста.\n\n¶176 " +
		chapter.Paragraphs[175].Text + "\n¶177 "
	if !strings.HasPrefix(text, head) || !strings.HasSuffix(text, "\n¶351 "+chapter.Paragraphs[350].Text+"\n") || strings.Contains(text, "¶175 ") {
		t.Fatalf("the input of part 2 begins %q", text[:min(len(text), 400)])
	}
	card, err := ParseCard(`{"summary": "Застава.", "events": [{"event": "До", "p": 10}, {"event": "В части", "p": 200}, {"event": "После", "p": 400}],
	 "hooks": [{"what": "первый абзац части", "p": 176}, {"what": "последний", "p": 351}]}`, 176, 351)
	if err != nil || card.Events[0].P != 0 || card.Events[1].P != 200 || card.Events[2].P != 0 || card.Hooks[0].P != 176 || card.Hooks[1].P != 351 {
		t.Fatalf("the parse of part 2 = %+v, %v", card, err)
	}
	more, err := ParseCardAdditions(`{"hooks": [{"what": "до части", "p": 175}]}`, 176, 351)
	if err != nil || more.Hooks[0].P != 0 {
		t.Fatalf("the additions of part 2 = %+v, %v", more, err)
	}
}

// LC1.1: a chapter as long as the one of production (1 677 528 runes in 8 548 paragraphs) is six parts of 150 000 to
// 300 000 runes, one after another and every paragraph once.
func TestAChapterAsLongAsTheOneOfProductionIsSixParts(t *testing.T) {
	lengths := make([]int, 8_548)
	total := 0
	for index := range lengths[:len(lengths)-1] {
		lengths[index] = 40 + index*7_919%313
		total += lengths[index]
	}
	lengths[len(lengths)-1] = 1_677_528 - total
	if last := lengths[len(lengths)-1]; last < 1 || last > 10_000 {
		t.Fatalf("the last paragraph of the test holds %d runes", last)
	}
	chapter := chapterOfLengths(lengths...)
	parts := ChapterParts(chapter)
	if !reflect.DeepEqual(parts, PartsOf(lengths)) {
		t.Fatalf("the text and the lengths divide differently: %+v, %+v", parts, PartsOf(lengths))
	}
	if len(parts) != 6 {
		t.Fatalf("the chapter of production = %d parts: %+v", len(parts), parts)
	}
	next := 1
	for index, part := range parts {
		if part.First != next || part.Start != 0 || part.End != 0 {
			t.Fatalf("part %d = %+v after paragraph %d", index+1, part, next-1)
		}
		if runes := partRunes(chapter, part); runes < 150_000 || runes > ChapterPartMaxRunes {
			t.Fatalf("part %d holds %d runes", index+1, runes)
		}
		next = part.Last + 1
	}
	if next != len(lengths)+1 {
		t.Fatalf("the parts end at paragraph %d of %d", next-1, len(lengths))
	}
}

// LC1.1: a paragraph of 700 000 runes is cut into three nearly equal pieces at the ends of its sentences — each with the
// number of its paragraph, none tiny, every rune of the paragraph in one of them —, and the pieces divide as paragraphs do;
// by the lengths alone the cuts fall at a third and two thirds, and the pieces are as many.
func TestAParagraphLongerThanAPartIsCutIntoPiecesOfItsNumber(t *testing.T) {
	chapter := chapterOfLengths(1_000, 700_000, 1_000)
	parts := ChapterParts(chapter)
	var pieces []string
	for index, part := range parts {
		if runes := partRunes(chapter, part); runes > ChapterPartMaxRunes || runes < 150_000 {
			t.Fatalf("part %d = %+v holds %d runes", index+1, part, runes)
		}
		text := PartText(chapter, part, index+1, len(parts))
		if runes := utf8.RuneCountInString(text); runes > ChapterPartMaxRunes+300 {
			t.Fatalf("part %d gives the model %d runes", index+1, runes)
		}
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n")[3:] {
			if strings.HasPrefix(line, "¶2 ") {
				pieces = append(pieces, strings.TrimPrefix(line, "¶2 "))
			} else if !strings.HasPrefix(line, "¶1 ") && !strings.HasPrefix(line, "¶3 ") {
				t.Fatalf("part %d reads %.40q", index+1, line)
			}
		}
	}
	if len(pieces) != 3 {
		t.Fatalf("the paragraph of 700 000 runes went in %d pieces: %+v", len(pieces), parts)
	}
	for index, piece := range pieces {
		if runes := utf8.RuneCountInString(piece); runes < 200_000 || runes > 240_000 {
			t.Fatalf("piece %d holds %d runes", index+1, runes)
		}
		if index < len(pieces)-1 && !strings.HasSuffix(piece, ". ") {
			t.Fatalf("piece %d does not end at the end of a sentence", index+1)
		}
	}
	if strings.Join(pieces, "") != chapter.Paragraphs[1].Text {
		t.Fatal("the pieces are not the paragraph")
	}
	byLength := PartsOf([]int{1_000, 700_000, 1_000})
	want := []PartRange{{First: 1, Last: 2, End: 233_333}, {First: 2, Last: 2, Start: 233_333, End: 466_666}, {First: 2, Last: 3, Start: 466_666}}
	if !reflect.DeepEqual(byLength, want) {
		t.Fatalf("by the lengths = %+v, want %+v", byLength, want)
	}
}

// LC1.1: a paragraph one rune longer than a part is two pieces of about a half, by its text and by its length; a word without
// spaces is cut at the middle; a chapter of it and a paragraph of 50 000 is two parts of 150 000 to 300 000.
func TestAParagraphOneRuneOverAPartIsTwoPieces(t *testing.T) {
	text := []rune(chapterOfLengths(300_001).Paragraphs[0].Text)
	for _, pieces := range [][][2]int{piecesOf(300_001, text), piecesOf(300_001, nil), piecesOf(300_001, []rune(strings.Repeat("ы", 300_001)))} {
		if len(pieces) != 2 || pieces[0][0] != 0 || pieces[1][1] != 300_001 || pieces[0][1] != pieces[1][0] ||
			pieces[0][1] < 75_000 || pieces[0][1] > 150_000 {
			t.Fatalf("a paragraph of 300 001 runes = %v", pieces)
		}
	}
	if pieces := piecesOf(300_001, []rune(strings.Repeat("ы", 300_001))); pieces[0][1] != 150_000 {
		t.Fatalf("a word without spaces is cut at %d", pieces[0][1])
	}
	chapter := chapterOfLengths(300_001, 50_000)
	parts := ChapterParts(chapter)
	if len(parts) != 2 || len(PartsOf([]int{300_001, 50_000})) != 2 {
		t.Fatalf("a chapter of 300 001 and 50 000 runes = %+v", parts)
	}
	for index, part := range parts {
		if runes := partRunes(chapter, part); runes < 150_000 || runes > ChapterPartMaxRunes {
			t.Fatalf("part %d = %+v holds %d runes", index+1, part, runes)
		}
	}
}
