package researchchat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/sagamemory"
)

const (
	defaultGlossaryLimit  = 300
	maxGlossaryLimit      = 1000
	maxGlossaryTypes      = 20
	maxListedEntryChapter = 40
)

type GlossaryInput struct {
	Query string
	Types []string
	Limit int
}

type ChangesInput struct {
	Subject string
	From    string
	To      string
}

// Glossary lists the saga dictionary built from the chapter cards: without a query or
// types it shows the entry types, otherwise the matching entries with aliases and chapters.
func (tools *ReadTools) Glossary(ctx context.Context, call ToolContext, input GlossaryInput) (string, error) {
	if !utf8.ValidString(input.Query) || utf8.RuneCountInString(input.Query) > maxQueryRunes {
		return "", argumentError("$.query", "запрос до %d символов", maxQueryRunes)
	}
	if len(input.Types) > maxGlossaryTypes || input.Limit < 0 || input.Limit > maxGlossaryLimit {
		return "", argumentError("$", "до %d типов, limit до %d", maxGlossaryTypes, maxGlossaryLimit)
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultGlossaryLimit
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		cards, err := saga.cards(0, -1, sagamemory.CardParts{Mentions: true})
		if err != nil {
			return "", err
		}
		glossary := saga.glossaryOf(cards)
		if strings.TrimSpace(input.Query) != "" {
			saga.about(sagamemory.NamedNames(input.Query), glossary)
		}
		var out strings.Builder
		if missing := saga.chaptersWithoutCards(cards); missing > 0 {
			fmt.Fprintf(&out, "Словарь неполон: карточки ещё не построены для %d глав.\n", missing)
		}
		if strings.TrimSpace(input.Query) == "" && len(input.Types) == 0 {
			counts := glossary.Types()
			kinds := make([]string, 0, len(counts))
			for kind := range counts {
				kinds = append(kinds, kind)
			}
			sort.Slice(kinds, func(i, j int) bool {
				return counts[kinds[i]] > counts[kinds[j]] || counts[kinds[i]] == counts[kinds[j]] && kinds[i] < kinds[j]
			})
			fmt.Fprintf(&out, "Записей в словаре: %d. Типы:\n", len(glossary.Entries))
			for _, kind := range kinds {
				fmt.Fprintf(&out, "- %s: %d\n", kind, counts[kind])
			}
			records, err := tools.projectMaterials(ctx, saga.authority.ProjectID)
			if err != nil {
				return "", err
			}
			if len(records) > 0 {
				fmt.Fprintf(&out, "Материалов автора: %d — не текст книги, найдёт query.\n", len(records))
			}
			return out.String(), nil
		}
		entries := glossary.Find(input.Query, input.Types)
		fmt.Fprintf(&out, "Найдено записей: %d\n", len(entries))
		for i, entry := range entries {
			if i == limit {
				fmt.Fprintf(&out, "… и ещё %d (увеличьте limit или уточните query)\n", len(entries)-limit)
				break
			}
			fmt.Fprintf(&out, "- %s (%s", entry.Name, entry.Type)
			if len(entry.Aliases) > 0 {
				fmt.Fprintf(&out, "; также: %s", strings.Join(entry.Aliases, ", "))
			}
			out.WriteString(")")
			if entry.Note != "" {
				out.WriteString(" — " + entry.Note)
			}
			keys := make([]string, 0, min(len(entry.Chapters), maxListedEntryChapter+1))
			for j, index := range entry.Chapters {
				chapter := &saga.version().Chapters[index]
				saga.use(chapter)
				if j < maxListedEntryChapter {
					keys = append(keys, chapter.Key)
				} else if j == maxListedEntryChapter {
					keys = append(keys, fmt.Sprintf("… всего %d", len(entry.Chapters)))
				}
			}
			fmt.Fprintf(&out, ". Главы (%d): %s\n", len(entry.Chapters), strings.Join(keys, ", "))
		}
		if strings.TrimSpace(input.Query) != "" {
			records, err := tools.projectMaterials(ctx, saga.authority.ProjectID)
			if err != nil {
				return "", err
			}
			writeMaterialList(&out, materialMatches(records, input.Query), glossary)
		}
		return out.String(), nil
	})
}

// Changes lists the state changes recorded in the chapter cards for a subject, in story order.
// A subject that names a glossary entry also finds the changes recorded under its other names
// (specs/2026-10-29-chapter-cards-quality.md, UC6): the hero written once «Мирон», once «Ворон».
func (tools *ReadTools) Changes(ctx context.Context, call ToolContext, input ChangesInput) (string, error) {
	subject := sagamemory.Normalize(strings.TrimSpace(input.Subject))
	if subject == "" || !utf8.ValidString(input.Subject) || utf8.RuneCountInString(input.Subject) > maxQueryRunes {
		return "", argumentError("$.subject", "укажите имя или ключевое слово до %d символов", maxQueryRunes)
	}
	return tools.run(ctx, call, func(saga *sagaScope) (string, error) {
		from, to, err := saga.chapterRange(input.From, input.To)
		if err != nil {
			return "", err
		}
		cards, err := saga.cards(0, -1, sagamemory.CardParts{Mentions: true, Changes: true})
		if err != nil {
			return "", err
		}
		glossary := saga.glossaryOf(cards)
		saga.about(sagamemory.NamedNames(input.Subject), glossary)
		names := map[string]bool{}
		var others []string
		if entry, ok := glossary.Lookup(input.Subject); ok {
			for _, name := range append([]string{entry.Name}, entry.Aliases...) {
				key := sagamemory.GlossaryKey(name)
				if key != "" && !names[key] {
					names[key] = true
					if key != sagamemory.GlossaryKey(input.Subject) {
						others = append(others, name)
					}
				}
			}
			if len(others) > 0 {
				saga.useEntry(entry) // the other names come from the chapters naming the entry
			}
		}
		var out strings.Builder
		found := 0
		for i := range saga.version().Chapters {
			chapter := &saga.version().Chapters[i]
			card := cards.Cards[i]
			if card == nil || !saga.inRange(chapter, from, to, nil) {
				continue
			}
			for _, change := range card.Changes {
				if !names[sagamemory.GlossaryKey(change.Subject)] &&
					!strings.Contains(sagamemory.Normalize(change.Subject+" "+change.Change), subject) {
					continue
				}
				found++
				saga.use(chapter)
				fmt.Fprintf(&out, "- %s%s: %s — %s\n", chapter.Ref(), paragraphMark(change.P), change.Subject, change.Change)
			}
		}
		if found == 0 {
			return "Изменений не найдено. Попробуйте другое имя или search по тексту.", nil
		}
		also := ""
		if len(others) > 0 {
			also = "; искал и под именами: " + strings.Join(others, ", ")
		}
		return fmt.Sprintf("Изменений: %d (по порядку книги%s)\n%s", found, also, out.String()), nil
	})
}

// scopedCards keeps the cards of the chapters a book-scoped chat may see.
func (saga *sagaScope) scopedCards(cards *sagamemory.CardSet) map[int]*sagamemory.Card {
	scoped := make(map[int]*sagamemory.Card, len(cards.Cards))
	for index, card := range cards.Cards {
		if saga.inScope(&saga.version().Chapters[index]) {
			scoped[index] = card
		}
	}
	return scoped
}

func (saga *sagaScope) chaptersWithoutCards(cards *sagamemory.CardSet) int {
	missing := 0
	for i := range saga.version().Chapters {
		if cards.Cards[i] == nil && saga.inScope(&saga.version().Chapters[i]) {
			missing++
		}
	}
	return missing
}
