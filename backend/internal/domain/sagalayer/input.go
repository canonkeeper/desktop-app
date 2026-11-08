package sagalayer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// The inputs of the calls of a build, exactly as they were measured (specs/2026-11-01-writer-assistant-index.md,
// R2.3, R2.4). Only the chapter references changed: the model reads the real chapter keys.

const (
	shownEventsWithoutMain = 15
	evidenceChapterRunes   = 30000
	evidenceLaterChapters  = 40
	listedClosedRunes      = 160
)

var (
	digestAttributes = map[string]bool{"цель": true, "обязательство": true, "условие": true, "срок": true, "план": true, "долг": true}
	digestKnowledge  = map[string]bool{"скрывает": true, "не знает": true, "ошибается": true, "предполагает": true}
)

// ChapterDigest is what the map call reads of one chapter card (R2.3).
func ChapterDigest(chapter *sagamemory.Chapter, card *sagamemory.Card) string {
	var events, hooks, knows, facts, changes []string
	for _, event := range card.Events {
		if event.Key {
			events = append(events, fmt.Sprintf("¶%d %s", event.P, event.Event))
		}
	}
	if len(events) == 0 {
		for _, event := range card.Events[:min(len(card.Events), shownEventsWithoutMain)] {
			events = append(events, fmt.Sprintf("¶%d %s", event.P, event.Event))
		}
	}
	for _, hook := range card.Hooks {
		hooks = append(hooks, fmt.Sprintf("¶%d %s", hook.P, hook.What))
	}
	for _, know := range card.Knows {
		if digestKnowledge[know.Kind] {
			knows = append(knows, fmt.Sprintf("%s %s: %s", know.Who, know.Kind, know.What))
		}
	}
	for _, fact := range card.Facts {
		if digestAttributes[strings.ToLower(fact.Attribute)] && !fact.Past {
			facts = append(facts, fmt.Sprintf("%s: %s = %s", fact.Subject, fact.Attribute, fact.Value))
		}
	}
	for _, change := range card.Changes {
		changes = append(changes, fmt.Sprintf("%s: %s", change.Subject, change.Change))
	}
	return fmt.Sprintf("### %s «%s»\nПЕРЕСКАЗ: %s\nГЛАВНЫЕ СОБЫТИЯ: %s\nОСТАЛОСЬ ОТКРЫТЫМ: %s\nКТО ЧТО СКРЫВАЕТ ИЛИ НЕ ЗНАЕТ: %s\nНАМЕРЕНИЯ, ОБЯЗАТЕЛЬСТВА: %s\nИЗМЕНЕНИЯ: %s",
		chapter.Key, chapter.Title, card.Summary, strings.Join(events, " / "), strings.Join(hooks, " / "),
		strings.Join(knows, " / "), strings.Join(facts, " / "), strings.Join(changes, " / "))
}

// BookInput is the whole input of the map call of one book.
func BookInput(order int, digests []string) string {
	return fmt.Sprintf("ТОМ %d\n\n%s", order, strings.Join(digests, "\n\n"))
}

var wordPattern = regexp.MustCompile(`[а-яёa-z′\-]{5,}`)

// stems are the first six letters of the words of five letters and more, the common words the
// evidence of a record is chosen by.
func stems(text string) map[string]bool {
	found := map[string]bool{}
	for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		runes := []rune(word)
		found[string(runes[:min(len(runes), 6)])] = true
	}
	return found
}

func common(words, other map[string]bool) int {
	shared := 0
	for word := range words {
		if other[word] {
			shared++
		}
	}
	return shared
}

// Source is one chapter with a card in reading order.
type Source struct {
	Chapter string
	Title   string
	Card    *sagamemory.Card
}

func laterBlob(card *sagamemory.Card) string {
	var events, changes, hooks []string
	for _, event := range card.Events {
		if event.Key {
			events = append(events, event.Event)
		}
	}
	for _, change := range card.Changes {
		changes = append(changes, fmt.Sprintf("%s: %s", change.Subject, change.Change))
	}
	for _, hook := range card.Hooks {
		hooks = append(hooks, hook.What)
	}
	return fmt.Sprintf("%s\nСОБЫТИЯ: %s\nИЗМЕНЕНИЯ: %s\nОТКРЫТО: %s", card.Summary, strings.Join(events, " / "), strings.Join(changes, " / "), strings.Join(hooks, " / "))
}

// EvidenceIndex builds what a check of one record reads (R2.4).
type EvidenceIndex struct {
	sources   []Source
	positions map[string]int
	blobs     []string
	words     []map[string]bool
}

func NewEvidenceIndex(sources []Source) *EvidenceIndex {
	index := &EvidenceIndex{
		sources: sources, positions: make(map[string]int, len(sources)),
		blobs: make([]string, len(sources)), words: make([]map[string]bool, len(sources)),
	}
	for i, source := range sources {
		index.positions[source.Chapter] = i
		index.blobs[i] = laterBlob(source.Card)
		index.words[i] = stems(index.blobs[i])
	}
	return index
}

// named are the positions of the chapters a record names, in reading order.
func (index *EvidenceIndex) named(thread Thread) []int {
	var positions []int
	for _, ref := range append(append([]Ref(nil), thread.First...), thread.Last...) {
		if position, ok := index.positions[ref.Chapter]; ok && !slices.Contains(positions, position) {
			positions = append(positions, position)
		}
	}
	slices.Sort(positions)
	return positions
}

// Chapters are the positions of the chapters whose text a check reads: the two first and the last the
// record names.
func (index *EvidenceIndex) Chapters(thread Thread) []int {
	named := index.named(thread)
	if len(named) == 0 {
		return nil
	}
	return slices.Compact(append(slices.Clone(named[:min(len(named), 2)]), named[len(named)-1]))
}

// ChapterEvidenceText is the text of one chapter in the input of a check.
func ChapterEvidenceText(title string, paragraphs []string) string {
	text := []rune(strings.Join(paragraphs, "\n"))
	return fmt.Sprintf("=== %s ===\n%s", title, string(text[:min(len(text), evidenceChapterRunes)]))
}

// later are the positions after the last named chapter sharing the most words of the record, the
// latest first on a tie, in reading order.
func (index *EvidenceIndex) later(thread Thread) []int {
	named := index.named(thread)
	last := 0
	if len(named) > 0 {
		last = named[len(named)-1]
	}
	words := stems(thread.Text)
	floor := max(2, len(words)/4)
	type scored struct{ shared, position int }
	candidates := make([]scored, 0, max(0, len(index.sources)-last-1))
	for position := last + 1; position < len(index.sources); position++ {
		candidates = append(candidates, scored{common(words, index.words[position]), position})
	}
	slices.SortFunc(candidates, func(a, b scored) int {
		if a.shared != b.shared {
			return b.shared - a.shared
		}
		return b.position - a.position
	})
	var positions []int
	for _, candidate := range candidates[:min(len(candidates), evidenceLaterChapters)] {
		if candidate.shared >= floor {
			positions = append(positions, candidate.position)
		}
	}
	slices.Sort(positions)
	return positions
}

// Evidence is the input of the check of one record; texts holds the texts of Chapters by position.
func (index *EvidenceIndex) Evidence(thread Thread, first, last string, texts map[int]string) string {
	chapters := index.Chapters(thread)
	shown := make([]string, 0, len(chapters))
	for _, position := range chapters {
		shown = append(shown, texts[position])
	}
	later := index.later(thread)
	blobs := make([]string, 0, len(later))
	for _, position := range later {
		blobs = append(blobs, fmt.Sprintf("--- %s\n%s", index.sources[position].Title, index.blobs[position]))
	}
	return fmt.Sprintf("ЗАПИСЬ: %s (возникла %s, последний раз %s)\n\nТЕКСТ ГЛАВ:\n%s\n\nПОЗДНЕЙШИЕ ГЛАВЫ:\n%s",
		thread.Text, first, last, strings.Join(shown, "\n\n"), strings.Join(blobs, "\n\n"))
}

type listedOpen struct {
	Book   int    `json:"т"`
	First  string `json:"first"`
	Last   string `json:"last"`
	Kind   string `json:"kind"`
	Thread string `json:"thread"`
}

type listedClosed struct {
	Book   int    `json:"т"`
	Thread string `json:"thread"`
	How    string `json:"how"`
}

func lists(books []Book, version *sagamemory.Version) ([]listedOpen, []listedClosed) {
	open, closed := []listedOpen{}, []listedClosed{}
	for _, book := range books {
		for _, thread := range book.Threads {
			if thread.Closed {
				closed = append(closed, listedClosed{book.Order, firstRunes(thread.Text), firstRunes(thread.ClosedHow)})
				continue
			}
			open = append(open, listedOpen{book.Order, RenderRefs(thread.First, version), RenderRefs(thread.Last, version), thread.Kind, thread.Text})
		}
	}
	return open, closed
}

func firstRunes(text string) string {
	runes := []rune(text)
	return string(runes[:min(len(runes), listedClosedRunes)])
}

func encode(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err) // Plain strings and numbers always encode.
	}
	return strings.TrimSpace(out.String())
}

// ReduceInput is the input of the reduce call.
func ReduceInput(books []Book, version *sagamemory.Version) string {
	open, closed := lists(books, version)
	return encode(struct {
		Open   []listedOpen   `json:"open"`
		Closed []listedClosed `json:"closed"`
	}{open, closed})
}

// GapInput is the input of the what-was-missed call.
func GapInput(registry []Thread, books []Book, version *sagamemory.Version) string {
	texts := make([]string, len(registry))
	for i, thread := range registry {
		texts[i] = thread.Text
	}
	open, closed := lists(books, version)
	return encode(struct {
		Registry []string       `json:"registry"`
		Open     []listedOpen   `json:"open"`
		Closed   []listedClosed `json:"closed"`
	}{texts, open, closed})
}
