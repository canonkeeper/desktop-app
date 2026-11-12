package researchchat

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// The saga layer tools (specs/2026-10-29-chapter-cards-quality.md, UC4–UC5): the profile of a
// character or thing from the facts of the cards, and the story time line of the chapters.

const (
	maxEntityAttributes    = 20
	singleHistoryShown     = 8
	manyValuesShown        = 12
	chosenAttributeShown   = 60
	timelineEventsChapters = 40
	maxTimelineEvents      = 300
)

type EntityInput struct {
	Name       string
	At         string
	Attributes []string
}

type TimelineInput struct {
	From  string
	To    string
	Query string
}

// Entity shows what the cards state about a glossary entry under any of its names, up to a
// chapter: the current value of each single-valued attribute with the history of its changes, and
// the distinct values of the others (R4.1).
func (tools *ReadTools) Entity(ctx context.Context, call ToolContext, input EntityInput) (string, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxQueryRunes {
		return "", argumentError("$.name", "укажите имя до %d символов", maxQueryRunes)
	}
	if len(input.Attributes) > maxEntityAttributes {
		return "", argumentError("$.attributes", "до %d свойств", maxEntityAttributes)
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		at := -1
		if strings.TrimSpace(input.At) != "" {
			chapter, err := saga.chapter("$.at", input.At)
			if err != nil {
				return "", err
			}
			at = chapter.Index
		}
		cards, err := saga.cards(0, -1, sagamemory.CardParts{Mentions: true})
		if err != nil {
			return "", err
		}
		glossary := saga.glossaryOf(cards)
		saga.about(sagamemory.NamedNames(name), glossary)
		records, err := tools.projectMaterials(ctx, saga.authority.ProjectID)
		if err != nil {
			return "", err
		}
		record, hasRecord := materialByName(records, name)
		entry, ok := glossary.Lookup(name)
		if !ok && hasRecord {
			return renderMaterial(record, materialPlan), nil
		}
		if !ok {
			var similar []string
			for i, found := range glossary.Find(name, nil) {
				if i == 5 {
					break
				}
				similar = append(similar, found.Name)
			}
			if len(similar) == 0 {
				return "Такой записи в словаре нет. Проверьте имя через glossary или ищите в тексте через search.", nil
			}
			return "Такой записи в словаре нет; похожие: " + strings.Join(similar, ", ") + ". Повторите с одним из этих имён.", nil
		}
		facts, err := saga.loadFacts(at, entry.Keys())
		if err != nil {
			return "", err
		}
		scoped := facts[:0]
		for _, fact := range facts {
			if saga.inScope(&saga.version().Chapters[fact.Chapter]) {
				scoped = append(scoped, fact)
			}
		}
		profile := sagamemory.BuildProfile(entry, scoped)
		// The chapters that named the entry and those whose facts built the profile are retained,
		// shown or not (AGENTS.md).
		saga.useEntry(entry)
		names := map[string]bool{}
		for _, key := range entry.Keys() {
			names[key] = true
		}
		for _, fact := range scoped {
			if names[sagamemory.GlossaryKey(fact.Fact.Subject)] {
				saga.use(&saga.version().Chapters[fact.Chapter])
			}
		}
		rendered := renderProfile(saga, profile, at, input.Attributes)
		if hasRecord {
			rendered += "\n\n" + renderMaterial(record, materialBook)
		}
		return rendered, nil
	})
}

// useEntry retains the chapters whose cards named a glossary entry: its names come from them.
func (saga *sagaScope) useEntry(entry *sagamemory.GlossaryEntry) {
	for _, index := range entry.Chapters {
		if chapter := &saga.version().Chapters[index]; saga.inScope(chapter) {
			saga.use(chapter)
		}
	}
}

func renderProfile(saga *sagaScope, profile sagamemory.Profile, at int, chosen []string) string {
	var out strings.Builder
	chapters := saga.version().Chapters
	ref := func(chapter, p int) string {
		saga.use(&chapters[chapter])
		return chapters[chapter].Ref() + paragraphMark(p)
	}
	entry := profile.Entry
	fmt.Fprintf(&out, "%s (%s", entry.Name, entry.Type)
	if len(entry.Aliases) > 0 {
		fmt.Fprintf(&out, "; также: %s", strings.Join(entry.Aliases, ", "))
	}
	scope := "во всей саге"
	if at >= 0 {
		scope = "до " + chapters[at].Ref() + " включительно"
	}
	fmt.Fprintf(&out, "): фактов %d в %d главах %s.\n", profile.Facts, profile.Chapters, scope)
	if profile.Facts == 0 {
		out.WriteString("Карточки не записали фактов об этой записи (карточки до версии 3 их не содержат). Ищите в тексте через search и read.\n")
		return out.String()
	}
	wanted := map[string]bool{}
	for _, name := range chosen {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			wanted[name] = true
		}
	}
	header := false
	for _, attribute := range profile.Attributes {
		if len(wanted) > 0 && !wanted[attribute.Name] {
			continue
		}
		limit := manyValuesShown
		if attribute.Single {
			limit = singleHistoryShown
		}
		if len(wanted) > 0 {
			limit = chosenAttributeShown
		}
		if attribute.Single {
			if !header {
				out.WriteString("Значения на конец диапазона и история смен ([прошлое] — воспоминание, не значение на момент главы):\n")
				header = true
			}
			fmt.Fprintf(&out, "- %s: ", attribute.Name)
			if attribute.Current != nil {
				fmt.Fprintf(&out, "%s — %s", attribute.Current.Value, ref(attribute.Current.Last, attribute.Current.LastP))
			} else {
				out.WriteString("значения на время действия нет")
			}
			if len(attribute.History) > 1 {
				history := attribute.History
				skipped := max(0, len(history)-limit)
				out.WriteString(". История:")
				if skipped > 0 {
					fmt.Fprintf(&out, " … ещё %d раньше;", skipped)
				}
				for i, value := range history[skipped:] {
					if i > 0 {
						out.WriteString(";")
					}
					fmt.Fprintf(&out, " %s", value.Value)
					if value.Past {
						out.WriteString(" [прошлое]")
					}
					fmt.Fprintf(&out, " (%s)", ref(value.Chapter, value.P))
				}
			}
			out.WriteString("\n")
		}
	}
	others := false
	for _, attribute := range profile.Attributes {
		if attribute.Single || len(wanted) > 0 && !wanted[attribute.Name] {
			continue
		}
		if !others {
			out.WriteString("Прочие свойства — различные значения, недавние сначала:\n")
			others = true
		}
		limit := manyValuesShown
		if len(wanted) > 0 {
			limit = chosenAttributeShown
		}
		fmt.Fprintf(&out, "- %s (%d):", attribute.Name, len(attribute.History))
		for i, value := range attribute.History {
			if i == limit {
				fmt.Fprintf(&out, " … и ещё %d (attributes=[%q] покажет больше)", len(attribute.History)-limit, attribute.Name)
				break
			}
			if i > 0 {
				out.WriteString(";")
			}
			fmt.Fprintf(&out, " %s", value.Value)
			if value.Past {
				out.WriteString(" [прошлое]")
			}
			fmt.Fprintf(&out, " (%s", ref(value.Last, value.LastP))
			if value.Chapter != value.Last {
				fmt.Fprintf(&out, ", впервые %s", ref(value.Chapter, value.P))
			}
			out.WriteString(")")
		}
		out.WriteString("\n")
	}
	if len(wanted) > 0 && !header && !others {
		names := make([]string, 0, len(profile.Attributes))
		for _, attribute := range profile.Attributes {
			names = append(names, attribute.Name)
		}
		fmt.Fprintf(&out, "Таких свойств нет. Есть: %s.\n", strings.Join(names, ", "))
	}
	return out.String()
}

// shownEvents are the events of a chapter a timeline call goes through: a v4 card lists all events of
// the chapter and marks the main ones, and without a query only those are shown.
func shownEvents(card *sagamemory.Card, query string) []sagamemory.Event {
	if query != "" {
		return card.Events
	}
	main := make([]sagamemory.Event, 0, len(card.Events))
	for _, event := range card.Events {
		if event.Key {
			main = append(main, event)
		}
	}
	if len(main) == 0 {
		return card.Events
	}
	return main
}

// Timeline places the chapters of a range on the story time line (R5.1, R5.2): the story day of
// each chapter from the times the cards give, the date of the story world, and the events of the
// chapters — of a short range all of them, of a long one those matching the query.
func (tools *ReadTools) Timeline(ctx context.Context, call ToolContext, input TimelineInput) (string, error) {
	if !utf8.ValidString(input.Query) || utf8.RuneCountInString(input.Query) > maxQueryRunes {
		return "", argumentError("$.query", "запрос до %d символов", maxQueryRunes)
	}
	query := sagamemory.Normalize(strings.TrimSpace(input.Query))
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		saga.about(sagamemory.NamedWords(input.Query), nil)
		from, to, err := saga.chapterRange(input.From, input.To)
		if err != nil {
			return "", err
		}
		chapters := saga.version().Chapters
		if to < 0 {
			to = len(chapters) - 1
		}
		times, err := saga.cards(0, -1, sagamemory.CardParts{ChapterTime: true})
		if err != nil {
			return "", err
		}
		days := saga.storyDays(times)
		// Every chapter whose time builds the story days of the answer is retained (AGENTS.md).
		for i := 0; i <= to; i++ {
			if card := times.Cards[i]; card != nil && card.When != nil && saga.inScope(&chapters[i]) {
				saga.use(&chapters[i])
			}
		}
		withEvents := query != "" || to-from < timelineEventsChapters
		var events *sagamemory.CardSet
		if withEvents {
			if events, err = saga.cards(from, to, sagamemory.CardParts{Events: true}); err != nil {
				return "", err
			}
		}
		first := -1
		for i := range chapters {
			if saga.inScope(&chapters[i]) {
				first = i
				break
			}
		}
		var out strings.Builder
		if first < 0 {
			return "В этом чате нет глав.", nil
		}
		fmt.Fprintf(&out, "Шкала времени сюжета по карточкам: день считается от начала %s; промежутки между главами и длительности глав — из текста или оценены моделью. ", chapters[first].Ref())
		out.WriteString("Неизвестные промежутки считаются нулём, поэтому день — нижняя оценка; счётчики «неизв.» и «оцен.» идут от начала саги. ")
		out.WriteString("Главы другой сюжетной линии, воспоминания и послесловия идут вне основной шкалы; пометка у главы называет её особое время (игра, другой мир), соотношение времён карточки не называют.\n")
		start, end := days[from], days[to]
		if strings.TrimSpace(input.From) != "" && strings.TrimSpace(input.To) != "" {
			fmt.Fprintf(&out, "От начала %s до конца %s: %s (неизвестных промежутков %d, оценённых %d).\n",
				chapters[from].Ref(), chapters[to].Ref(), sagamemory.FormatDays(end.End-start.Start),
				end.Unknown-start.UnknownAtStart, end.Approx-start.ApproxAtStart)
		}
		shown := 0
		for i := from; i <= to; i++ {
			chapter := &chapters[i]
			if !saga.inScope(chapter) {
				continue
			}
			var matched []sagamemory.Event
			if withEvents {
				if card := events.Cards[i]; card != nil {
					for _, event := range shownEvents(card, query) {
						if query == "" || strings.Contains(sagamemory.Normalize(event.Event+" "+event.Time+" "+strings.Join(event.Who, " ")), query) {
							matched = append(matched, event)
						}
					}
				}
				if query != "" && len(matched) == 0 {
					continue
				}
			}
			saga.use(chapter)
			if days[i].OffLine {
				fmt.Fprintf(&out, "- %s: вне основной шкалы — %s\n", chapter.Ref(), times.Cards[i].When.Note)
				continue
			}
			fmt.Fprintf(&out, "- %s: день %s–%s", chapter.Ref(), dayNumber(days[i].Start), dayNumber(days[i].End))
			if card := times.Cards[i]; card != nil && card.When != nil {
				when := card.When
				if i != first {
					fmt.Fprintf(&out, "; до главы %s", spanText(when.After))
				}
				fmt.Fprintf(&out, "; длится %s", spanText(when.Lasts))
				if when.Opens != "" {
					fmt.Fprintf(&out, "; «%s»", when.Opens)
				}
				if when.Date != "" {
					fmt.Fprintf(&out, "; дата: %s", when.Date)
				}
				if when.Note != "" {
					fmt.Fprintf(&out, "; %s", when.Note)
				}
			} else {
				out.WriteString("; времени главы в карточке нет")
			}
			fmt.Fprintf(&out, " [неизв. %d, оцен. %d]\n", days[i].Unknown, days[i].Approx)
			for _, event := range matched {
				if shown == maxTimelineEvents {
					break
				}
				shown++
				fmt.Fprintf(&out, "  - ¶%d %s", event.P, event.Event)
				if event.Time != "" {
					fmt.Fprintf(&out, " [время: %s]", event.Time)
				}
				out.WriteString("\n")
			}
		}
		if shown == maxTimelineEvents {
			fmt.Fprintf(&out, "… показаны первые %d событий; сузьте from/to или query.\n", maxTimelineEvents)
		}
		if !withEvents {
			fmt.Fprintf(&out, "События глав показываются для диапазона до %d глав или по query.\n", timelineEventsChapters)
		}
		return out.String(), nil
	})
}

// storyDays places the chapters of the scope on the story time line, counting only their own
// spans: a book-scoped chat starts the line at its book and never counts another book's chapters.
func (saga *sagaScope) storyDays(times *sagamemory.CardSet) map[int]sagamemory.StoryDay {
	chapters := saga.version().Chapters
	var scope []int
	cards := map[int]*sagamemory.Card{}
	for i := range chapters {
		if saga.inScope(&chapters[i]) {
			if card := times.Cards[i]; card != nil {
				cards[len(scope)] = card
			}
			scope = append(scope, i)
		}
	}
	days := map[int]sagamemory.StoryDay{}
	for position, day := range sagamemory.StoryDays(len(scope), cards) {
		days[scope[position]] = day
	}
	return days
}

func dayNumber(days float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", days), ".", ",", 1)
}

func spanText(span *sagamemory.Span) string {
	if span == nil {
		return "неизвестно"
	}
	n := strings.Replace(strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", span.N), "0"), "."), ".", ",", 1)
	text := n + " " + span.Unit
	if span.Approx {
		text += " (оценка)"
	}
	return text
}
