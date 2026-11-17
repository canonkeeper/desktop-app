package sagamemory

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A chapter whose paragraphs hold more than ChapterSplitRunes runes is read in parts of at most ChapterPartMaxRunes, nearly
// equal and by the bounds of its paragraphs (specs/2026-11-17-long-chapters.md LC1.1); a shorter one is read whole, as
// before.
const (
	ChapterSplitRunes   = 350_000
	ChapterPartMaxRunes = 300_000
)

// PartRange is a part of a chapter: its paragraphs First..Last, from one. A part that begins or ends inside a paragraph
// longer than a part — at a piece of it — names where, in runes: Start in First and End in Last; 0 is the beginning of
// First and the end of Last.
type PartRange struct {
	First, Last int
	Start, End  int
}

// unit is what the division moves whole: a paragraph, or a piece of a paragraph longer than a part — its runes start..end.
type unit struct {
	paragraph  int
	start, end int
	length     int
}

// PartsOf divides a chapter by the lengths of its paragraphs in runes (LC1.1): a chapter of at most ChapterSplitRunes is one
// part; a longer one is n = ⌈L / ChapterPartMaxRunes⌉ parts whose bound k is the end of the paragraph nearest to k·L/n, or
// an earlier one when the part would be longer than ChapterPartMaxRunes — and more parts when n cannot hold them. A paragraph
// longer than a part is cut into pieces of at most ChapterPartMaxRunes first, which divide as paragraphs do; by its length
// alone the cut falls at ChapterPartMaxRunes — ChapterParts cuts the text of a chapter at the end of a sentence.
func PartsOf(lengths []int) []PartRange {
	return divide(lengths, nil)
}

// ChapterParts divides a chapter as PartsOf divides the lengths of its paragraphs, measured in runes of their text, cutting
// a paragraph longer than a part at the last end of a sentence a piece holds, else at its last space, else at its last rune.
// The card writer reads a chapter by them, and so does the count of the characters of its inputs.
func ChapterParts(chapter *Chapter) []PartRange {
	lengths := make([]int, len(chapter.Paragraphs))
	for index, paragraph := range chapter.Paragraphs {
		lengths[index] = utf8.RuneCountInString(paragraph.Text)
	}
	return divide(lengths, func(index int) []rune { return []rune(chapter.Paragraphs[index].Text) })
}

func divide(lengths []int, text func(index int) []rune) []PartRange {
	total := 0
	for _, length := range lengths {
		total += length
	}
	if total <= ChapterSplitRunes {
		return []PartRange{{First: 1, Last: len(lengths)}}
	}
	var units []unit
	for index, length := range lengths {
		if length <= ChapterPartMaxRunes {
			units = append(units, unit{paragraph: index + 1, end: length, length: length})
			continue
		}
		var runes []rune
		if text != nil {
			runes = text(index)
		}
		for _, piece := range piecesOf(length, runes) {
			units = append(units, unit{paragraph: index + 1, start: piece[0], end: piece[1], length: piece[1] - piece[0]})
		}
	}
	ends := make([]int, len(units))
	sum := 0
	for index, unit := range units {
		sum += unit.length
		ends[index] = sum
	}
	for n := (sum + ChapterPartMaxRunes - 1) / ChapterPartMaxRunes; n < len(units); n++ {
		if bounds, ok := boundsOf(ends, n); ok {
			return rangesOf(units, lengths, bounds)
		}
	}
	bounds := make([]int, len(units))
	for index := range units {
		bounds[index] = index
	}
	return rangesOf(units, lengths, bounds)
}

// boundsOf are the last units of the n parts: the bound of part k is the end of the unit nearest to k·L/n — the first of
// two as near — of those that keep the part within ChapterPartMaxRunes and leave a unit to every part after it; false when
// the last part is longer than ChapterPartMaxRunes.
func boundsOf(ends []int, n int) ([]int, bool) {
	total := ends[len(ends)-1]
	bounds := make([]int, 0, n)
	previous, before := -1, 0
	for k := 1; k < n; k++ {
		target := k * total // k·L/n, compared at n times its scale
		chosen := -1
		for index := previous + 1; index <= len(ends)-1-(n-k); index++ {
			if ends[index]-before > ChapterPartMaxRunes {
				break
			}
			if chosen < 0 || distance(ends[index]*n, target) < distance(ends[chosen]*n, target) {
				chosen = index
			}
		}
		if chosen < 0 {
			return nil, false
		}
		bounds = append(bounds, chosen)
		previous, before = chosen, ends[chosen]
	}
	if total-before > ChapterPartMaxRunes {
		return nil, false
	}
	return append(bounds, len(ends)-1), true
}

func distance(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// rangesOf names the parts by their last units.
func rangesOf(units []unit, lengths []int, bounds []int) []PartRange {
	parts := make([]PartRange, 0, len(bounds))
	first := 0
	for _, last := range bounds {
		head, tail := units[first], units[last]
		part := PartRange{First: head.paragraph, Last: tail.paragraph, Start: head.start}
		if tail.end < lengths[tail.paragraph-1] {
			part.End = tail.end
		}
		parts = append(parts, part)
		first = last + 1
	}
	return parts
}

// piecesOf cuts a paragraph longer than a part into m = ⌈length / ChapterPartMaxRunes⌉ nearly equal pieces, each the runes
// start..end: cut i falls after the last end of a sentence — «.», «!», «?» or «…» and the space after it — at most at
// i·length/m and not further back from it than keeps every piece within a part and at least half of an equal one; else
// after the last space there; else at i·length/m itself. A space at a cut ends the piece before it and counts in it.
// Without the text the cuts fall at i·length/m: the pieces are as many.
func piecesOf(length int, runes []rune) [][2]int {
	count := (length + ChapterPartMaxRunes - 1) / ChapterPartMaxRunes
	back := min(ChapterPartMaxRunes-(length+count-1)/count, length/(2*count))
	pieces := make([][2]int, 0, count)
	start := 0
	for i := 1; i < count; i++ {
		end := i * length / count
		if runes != nil {
			end = cutOf(runes, max(start, end-back), end)
		}
		pieces = append(pieces, [2]int{start, end})
		start = end
	}
	return append(pieces, [2]int{start, length})
}

// cutOf is where a piece of runes ends between from and limit: after the space after the last end of a sentence there,
// else after the last space there, else at limit.
func cutOf(runes []rune, from, limit int) int {
	for end := limit; end > from+1; end-- {
		if unicode.IsSpace(runes[end-1]) && strings.ContainsRune(".!?…", runes[end-2]) {
			return end
		}
	}
	for end := limit; end > from; end-- {
		if unicode.IsSpace(runes[end-1]) {
			return end
		}
	}
	return limit
}

// chapterHead is the first line of the input of a chapter: its label and its key.
func chapterHead(chapter *Chapter) string {
	if chapter.Key == "" {
		return chapter.Label()
	}
	return fmt.Sprintf("%s (гл. %s)", chapter.Label(), chapter.Key)
}

// PartText renders part k of n of a chapter (LC1.2): the head of ChapterText, the line of the part, an empty line and its
// paragraphs with their numbers in the chapter — a piece with the number of its paragraph.
func PartText(chapter *Chapter, part PartRange, k, n int) string {
	var builder strings.Builder
	builder.WriteString(chapterHead(chapter))
	fmt.Fprintf(&builder, "\nЧасть %d из %d: абзацы ¶%d–¶%d этой главы, остальные части читаются отдельно — пиши карточку только этого текста.\n\n",
		k, n, part.First, part.Last)
	for number := part.First; number <= part.Last; number++ {
		text := chapter.Paragraphs[number-1].Text
		if (number == part.First && part.Start > 0) || (number == part.Last && part.End > 0) {
			runes := []rune(text)
			from, to := 0, len(runes)
			if number == part.First {
				from = part.Start
			}
			if number == part.Last && part.End > 0 {
				to = part.End
			}
			text = string(runes[from:to])
		}
		fmt.Fprintf(&builder, "¶%d %s\n", number, text)
	}
	return builder.String()
}

// MergeCards is the card of a chapter read in parts, from the cards of its parts in their order (LC1.4), within the bounds of
// a card: the retellings of the parts one per line — each cut to an equal share at the end of a sentence when together they
// are longer than a summary —; the time of the chapter of the first part without how long it lasts; the lists one after
// another without what repeats by the keys of Card.Add — the first saying stays —, and a list longer than its bound taken by
// shares counted by turns, each spread over its part, the main events first; a character or an entity once by its name, held by the
// part that named it first, with the aliases, the type, the notes and the first paragraph of every part.
func MergeCards(parts []Card) Card {
	card := Card{Summary: mergedSummary(parts)}
	if when := parts[0].When; when != nil {
		first := *when
		first.Lasts = nil
		if first != (ChapterTime{}) {
			card.When = &first
		}
	}
	card.Events = mergedList(parts, func(part Card) []Event { return part.Events }, func(item Event) string { return item.Event }, maxCardItems,
		func(item Event) bool { return item.Key })
	card.Time = mergedList(parts, func(part Card) []string { return part.Time }, func(value string) string { return value }, maxCardTimes, nil)
	card.Facts = mergedList(parts, func(part Card) []Fact { return part.Facts }, func(item Fact) string {
		return item.Subject + "\x00" + item.Attribute + "\x00" + item.Value
	}, maxCardItems, nil)
	card.Changes = mergedList(parts, func(part Card) []Change { return part.Changes }, func(item Change) string { return item.Subject + "\x00" + item.Change }, maxCardItems, nil)
	card.Knows = mergedList(parts, func(part Card) []Knowledge { return part.Knows }, func(item Knowledge) string { return item.Who + "\x00" + item.What }, maxCardItems, nil)
	card.Hooks = mergedList(parts, func(part Card) []Hook { return part.Hooks }, func(item Hook) string { return item.What }, maxCardItems, nil)
	card.Characters = mergedMentions(parts, func(part Card) []Mention { return part.Characters })
	card.Entities = mergedMentions(parts, func(part Card) []Mention { return part.Entities })
	return card
}

// mergedSummary are the retellings of the parts one per line; longer together than a summary, each is cut to an equal share
// of it at the last end of a sentence the share holds, else before its last space.
func mergedSummary(parts []Card) string {
	summaries := make([]string, len(parts))
	total := len(parts) - 1
	for index, part := range parts {
		summaries[index] = part.Summary
		total += utf8.RuneCountInString(part.Summary)
	}
	if total > maxSummaryRunes {
		share := (maxSummaryRunes - (len(parts) - 1)) / len(parts)
		for index, summary := range summaries {
			if runes := []rune(summary); len(runes) > share {
				summaries[index] = strings.TrimSpace(string(runes[:cutOf(runes, 0, share)]))
			}
		}
	}
	return strings.Join(summaries, "\n")
}

// mergedList is a list of the parts one after another without what repeats by its key, the first saying kept; longer than its
// bound, it is taken as byTurns says and keeps the order of the parts and of each part.
func mergedList[T any](parts []Card, list func(Card) []T, key func(T) string, limit int, main func(T) bool) []T {
	seen := map[string]bool{}
	kept := make([][]T, len(parts))
	count := 0
	for index, part := range parts {
		for _, item := range list(part) {
			if text := Normalize(key(item)); !seen[text] {
				seen[text] = true
				kept[index] = append(kept[index], item)
				count++
			}
		}
	}
	return byTurns(kept, count, limit, main)
}

// byTurns joins the lists of the parts within the bound: longer, the shares of the parts are counted by turns — one item of
// each part that has one left at a time, first those main marks, then the others — and each part gives its share spread
// over its whole list, the middles of equal stretches of it, not its first items: the list of a part is in the order of its
// story, so its end is not lost (specs/2026-11-17-long-chapters.md LC1.4). The order of the parts and of each part stays.
func byTurns[T any](kept [][]T, count, limit int, main func(T) bool) []T {
	taken := make([][]bool, len(kept))
	for index := range kept {
		taken[index] = make([]bool, len(kept[index]))
	}
	if count <= limit {
		for index := range taken {
			for item := range taken[index] {
				taken[index][item] = true
			}
		}
	} else {
		chosen := 0
		phases := []func(T) bool{func(T) bool { return true }}
		if main != nil {
			phases = []func(T) bool{main, func(item T) bool { return !main(item) }}
		}
		for _, phase := range phases {
			pool := make([][]int, len(kept))
			for index := range kept {
				for item, value := range kept[index] {
					if !taken[index][item] && phase(value) {
						pool[index] = append(pool[index], item)
					}
				}
			}
			shares := make([]int, len(kept))
			for more := true; more && chosen < limit; {
				more = false
				for index := range pool {
					if shares[index] < len(pool[index]) && chosen < limit {
						shares[index]++
						chosen++
						more = true
					}
				}
			}
			for index, share := range shares {
				for _, item := range spread(pool[index], share) {
					taken[index][item] = true
				}
			}
		}
	}
	var joined []T
	for index := range kept {
		for item, value := range kept[index] {
			if taken[index][item] {
				joined = append(joined, value)
			}
		}
	}
	return joined
}

// spread picks share of the items over them all: the middle item of each of share equal stretches.
func spread(items []int, share int) []int {
	if share >= len(items) {
		return items
	}
	picked := make([]int, share)
	for stretch := range share {
		picked[stretch] = items[(2*stretch+1)*len(items)/(2*share)]
	}
	return picked
}

// mergedMentions are the characters or the entities of the parts, one by their name: the record of the part that named it
// first with its name; the aliases of all without repeats, at most a card's; the first type; the first note and the next
// that say otherwise, while they fit a line of a card; the first paragraph of all. Longer than a list of a card, they are
// taken by turns of the parts that named them first.
func mergedMentions(parts []Card, list func(Card) []Mention) []Mention {
	type record struct {
		mention Mention
		aliases map[string]bool
		notes   map[string]bool
	}
	at := map[string]int{}
	var records []record
	byPart := make([][]int, len(parts))
	for index, part := range parts {
		for _, item := range list(part) {
			name := Normalize(item.Name)
			found, known := at[name]
			if !known {
				found = len(records)
				at[name] = found
				first := item
				first.Aliases = nil
				records = append(records, record{mention: first, aliases: map[string]bool{name: true}, notes: map[string]bool{}})
				byPart[index] = append(byPart[index], found)
				if item.Note != "" {
					records[found].notes[Normalize(item.Note)] = true
				}
			} else {
				merged := &records[found].mention
				if merged.Type == "" {
					merged.Type = item.Type
				}
				if note := Normalize(item.Note); item.Note != "" && !records[found].notes[note] {
					records[found].notes[note] = true
					switch joined := merged.Note + "; " + item.Note; {
					case merged.Note == "":
						merged.Note = item.Note
					case utf8.RuneCountInString(joined) <= maxCardTextRunes:
						merged.Note = joined
					}
				}
				if item.P > 0 && (merged.P == 0 || item.P < merged.P) {
					merged.P = item.P
				}
			}
			current := &records[found]
			for _, alias := range item.Aliases {
				if text := Normalize(alias); !current.aliases[text] && len(current.mention.Aliases) < maxCardAliases {
					current.aliases[text] = true
					current.mention.Aliases = append(current.mention.Aliases, alias)
				}
			}
		}
	}
	kept := make([][]Mention, len(parts))
	for index, numbers := range byPart {
		for _, number := range numbers {
			kept[index] = append(kept[index], records[number].mention)
		}
	}
	return byTurns(kept, len(records), maxCardItems, nil)
}
