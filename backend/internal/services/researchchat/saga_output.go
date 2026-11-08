package researchchat

import (
	"fmt"
	"strconv"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

const (
	snippetWidth      = 320
	maxListedChapters = 400
)

// shownHits are the hits whose snippets a search result prints: the page from offset and, in
// relevance order, the earliest and the latest hit in the book.
func shownHits(result sagamemory.Result, story bool, limit, offset int) []sagamemory.Hit {
	if offset >= len(result.Hits) {
		return nil
	}
	shown := append([]sagamemory.Hit(nil), result.Hits[offset:min(len(result.Hits), offset+limit)]...)
	if earliest, latest, ok := bookEnds(result, story); ok {
		shown = append(shown, earliest, latest)
	}
	return shown
}

// bookEnds are the earliest and the latest hit in the book, named outright in relevance order.
func bookEnds(result sagamemory.Result, story bool) (sagamemory.Hit, sagamemory.Hit, bool) {
	if story || len(result.Hits) < 2 {
		return sagamemory.Hit{}, sagamemory.Hit{}, false
	}
	earliest, latest := result.Hits[0], result.Hits[0]
	for _, hit := range result.Hits[1:] {
		if hit.Para < earliest.Para {
			earliest = hit
		}
		if hit.Para > latest.Para {
			latest = hit
		}
	}
	return earliest, latest, true
}

func writeSearchResult(
	out *strings.Builder, saga *sagaScope, index *sagamemory.Index, text string, mode sagamemory.Mode, story bool,
	result sagamemory.Result, limit, offset int,
) {
	fmt.Fprintf(out, "### «%s» (%s): %d абзацев в %d главах\n", text, mode, len(result.Hits), len(result.Chapters))
	if len(result.Hits) == 0 {
		out.WriteString("Ничего не найдено. Попробуйте другие словоформы, синонимы, часть слова (match=substring) или более короткий запрос.\n\n")
		return
	}
	out.WriteString("Главы с совпадениями: ")
	for i, item := range result.Chapters {
		if i == maxListedChapters {
			fmt.Fprintf(out, "… и ещё %d", len(result.Chapters)-maxListedChapters)
			break
		}
		if i > 0 {
			out.WriteString(", ")
		}
		chapter := &saga.version().Chapters[item.Chapter]
		saga.use(chapter)
		fmt.Fprintf(out, "%s(%d)", chapter.Key, item.Count)
	}
	out.WriteString("\n")
	if offset >= len(result.Hits) {
		fmt.Fprintf(out, "Сдвиг %d больше числа совпадений.\n\n", offset)
		return
	}
	order := "по релевантности"
	if story {
		order = "по порядку книги"
	} else if earliest, latest, ok := bookEnds(result, story); ok {
		// Relevance order hides where a thing first and last appears; both are named outright.
		writeSearchHit(out, saga, index, "Раньше всего по ходу книги: ", earliest)
		writeSearchHit(out, saga, index, "Позже всего: ", latest)
	}
	last := min(len(result.Hits), offset+limit)
	fmt.Fprintf(out, "Совпадения %d–%d из %d (%s):\n", offset+1, last, len(result.Hits), order)
	for _, hit := range result.Hits[offset:last] {
		writeSearchHit(out, saga, index, "- ", hit)
	}
	if last < len(result.Hits) {
		fmt.Fprintf(out, "Есть ещё %d совпадений: повторите с offset=%d.\n", len(result.Hits)-last, last)
	}
	out.WriteString("\n")
}

func writeSearchHit(out *strings.Builder, saga *sagaScope, index *sagamemory.Index, prefix string, hit sagamemory.Hit) {
	chapter, paragraph := index.Locate(hit.Para)
	saga.use(chapter)
	fmt.Fprintf(out, "%s%s%s: %s\n", prefix, chapter.Link(paragraph.N, paragraph.N), labelAfterNumber(chapter), index.Snippet(hit, snippetWidth))
}

func renderRead(chapter *sagamemory.Chapter, first, last int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s — %s, абзацы ¶%d–¶%d из %d; ссылка на абзац: %s\n\n",
		chapter.Label(), chapter.Ref(), first, last, chapter.ParagraphCount, chapter.Link(first, first))
	for _, paragraph := range chapter.Paragraphs[first-1 : last] {
		fmt.Fprintf(&out, "¶%d %s\n", paragraph.N, paragraph.Text)
	}
	return out.String()
}

func renderChapters(
	saga *sagaScope, from, to int, books []string, title string, detail ChaptersDetail, cards *sagamemory.CardSet,
) string {
	allowed := map[string]bool{}
	for _, book := range books {
		allowed[book] = true
	}
	chapters := saga.version().Chapters
	var out strings.Builder
	count, withoutCard := 0, 0
	for i := range chapters {
		chapter := &chapters[i]
		if !saga.inRange(chapter, from, to, allowed) || title != "" && !strings.Contains(sagamemory.Normalize(chapter.Title), title) {
			continue
		}
		count++
		saga.use(chapter)
		fmt.Fprintf(&out, "%s | %s | %d ¶\n", chapter.Ref(), chapter.Label(), chapter.ParagraphCount)
		if detail == ChaptersTitles {
			continue
		}
		card := cards.Cards[chapter.Index]
		if card == nil {
			withoutCard++
			continue
		}
		out.WriteString(indent(card.Summary))
		if detail != ChaptersCard {
			continue
		}
		if len(card.Time) > 0 {
			out.WriteString("    Время: " + strings.Join(card.Time, "; ") + "\n")
		}
		for _, change := range card.Changes {
			fmt.Fprintf(&out, "    Изменение%s: %s — %s\n", paragraphMark(change.P), change.Subject, change.Change)
		}
		for _, known := range card.Knows {
			fmt.Fprintf(&out, "    Знание%s: %s %s — %s\n", paragraphMark(known.P), known.Who, known.Kind, known.What)
		}
		for _, hook := range card.Hooks {
			fmt.Fprintf(&out, "    Открыто%s: %s\n", paragraphMark(hook.P), hook.What)
		}
	}
	if count == 0 {
		return "Подходящих глав нет."
	}
	header := "Глав: " + strconv.Itoa(count) + "\n"
	if withoutCard > 0 {
		header += fmt.Sprintf("Карточки ещё не построены для %d из них: у этих глав только название.\n", withoutCard)
	}
	return header + out.String()
}

// paragraphMark renders " (¶N)" for a known paragraph number.
// labelAfterNumber adds the book and title after a link that names the chapter only by number.
func labelAfterNumber(chapter *sagamemory.Chapter) string {
	if strings.HasPrefix(chapter.Cite(), "гл. ") {
		return " " + chapter.Label()
	}
	return ""
}

func paragraphMark(paragraph int) string {
	if paragraph <= 0 {
		return ""
	}
	return fmt.Sprintf(" (¶%d)", paragraph)
}

func indent(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
