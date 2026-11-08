// Package sagamemorytest serves a text version from memory through the real saga reader, for
// tests of the chat tools and the saga reference: the stored rows are built by the same code as
// the text index Job, and lookups answer like the PostgreSQL queries.
package sagamemorytest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
)

// Text is one text version with its stored rows and cards.
type Text struct {
	version *domain.Version
	texts   []domain.ChapterSearchText
	cards   map[int]*domain.Card
	missing map[int]bool
	lost    map[string]bool
	names   *domain.NameRegistry
	written time.Time
}

// NewSaga returns the saga reader of a version whose chapters hold their paragraphs; stems map
// words to russian_stem lexemes as the database would. Chapters listed in missing have no
// stored search text.
func NewSaga(version *domain.Version, stems map[string]string, cards map[int]*domain.Card, missing ...int) *service.Saga {
	text := NewText(version, stems, cards, missing...)
	return service.NewSaga(text, text)
}

// NewText returns the text and card source NewSaga reads through.
func NewText(version *domain.Version, stems map[string]string, cards map[int]*domain.Card, missing ...int) *Text {
	text := &Text{version: version, cards: cards, missing: map[int]bool{}, lost: map[string]bool{}}
	for _, chapter := range missing {
		text.missing[chapter] = true
	}
	for _, chapter := range version.Chapters {
		paragraphs := make([]string, len(chapter.Paragraphs))
		for i, paragraph := range chapter.Paragraphs {
			paragraphs[i] = paragraph.Text
		}
		text.texts = append(text.texts, domain.BuildChapterSearchText(paragraphs, stems))
	}
	return text
}

// LoseParagraphs makes paragraph reads of the chapter find nothing while its outline and search
// rows stay, as when the chapter is deleted after the outline of a call was read.
func (text *Text) LoseParagraphs(chapter int) {
	text.lost[text.version.Chapters[chapter].ID] = true
}

// SetNames gives the saga a name registry.
func (text *Text) SetNames(names *domain.NameRegistry) { text.names = names }

// RewriteCards marks the cards as written again, as a card of newer instructions does.
func (text *Text) RewriteCards() { text.written = text.written.Add(time.Second) }

func (text *Text) CardsStamp(ctx context.Context, projectID string, keys []service.CardKey) (service.CardsStamp, error) {
	found, err := text.Cards(ctx, projectID, keys, domain.CardParts{})
	if err != nil {
		return service.CardsStamp{}, err
	}
	stamp := service.CardsStamp{Cards: int64(len(found)), Written: text.written}
	if text.names != nil {
		body, err := json.Marshal(text.names)
		if err != nil {
			return service.CardsStamp{}, err
		}
		stamp.Names = fmt.Sprintf("%x", sha256.Sum256(body))
	}
	return stamp, nil
}

func (text *Text) Names(context.Context, string) (*domain.NameRegistry, error) {
	return text.names, nil
}

func textID(chapter int) string { return fmt.Sprintf("text-%d", chapter) }

func (text *Text) Selection(_ context.Context, projectID, view string) (service.Selection, error) {
	selection := service.Selection{ProjectID: projectID, View: view, Fingerprint: text.version.Fingerprint}
	for _, chapter := range text.version.Chapters {
		selection.Chapters = append(selection.Chapters, service.SelectedChapter{
			ID: chapter.ID, Revision: chapter.Revision, ContentHash: contentHash(chapter), BookID: chapter.BookID,
			BookOrder: chapter.BookOrder, BookTitle: chapter.BookTitle, Order: chapter.Order, Title: chapter.Title,
		})
	}
	return selection, nil
}

func contentHash(chapter domain.Chapter) string {
	if chapter.ContentHash != "" {
		return chapter.ContentHash
	}
	return "hash-" + chapter.ID
}

func (text *Text) Outline(ctx context.Context, projectID, view string) (service.Selection, map[service.CardKey]domain.SearchText, error) {
	selection, err := text.Selection(ctx, projectID, view)
	if err != nil {
		return service.Selection{}, nil, err
	}
	stored := map[service.CardKey]domain.SearchText{}
	for i, chapter := range text.version.Chapters {
		if text.missing[i] {
			continue
		}
		orders := make([]int32, len(chapter.Paragraphs))
		for j := range orders {
			orders[j] = int32(j + 1)
		}
		stored[service.CardKey{ChapterID: chapter.ID, ContentHash: contentHash(chapter)}] = domain.SearchText{
			ID: textID(i), Orders: orders, Tokens: text.texts[i].ParagraphTokens, Runes: text.texts[i].Runes,
		}
	}
	return selection, stored, nil
}

func (text *Text) words(textIDs []string, keep func(domain.SearchWord) bool) []service.StoredWord {
	var found []service.StoredWord
	for _, id := range textIDs {
		var chapter int
		if _, err := fmt.Sscanf(id, "text-%d", &chapter); err != nil {
			continue
		}
		for _, word := range text.texts[chapter].Words {
			if keep(word) {
				found = append(found, service.StoredWord{TextID: id, Word: word.Word, Stem: word.Stem, Postings: word.Postings})
			}
		}
	}
	return found
}

func (text *Text) Vocabulary(_ context.Context, _ string, textIDs []string, lookup domain.SearchLookup) ([]domain.VocabularyWord, error) {
	seen := map[string]bool{}
	var words []domain.VocabularyWord
	for _, row := range text.words(textIDs, func(word domain.SearchWord) bool {
		for _, wanted := range lookup.Words {
			if word.Word == wanted {
				return true
			}
		}
		for _, prefix := range lookup.Prefixes {
			if strings.HasPrefix(word.Word, prefix) {
				return true
			}
		}
		return false
	}) {
		if !seen[row.Word] {
			seen[row.Word] = true
			words = append(words, domain.VocabularyWord{Word: row.Word, Stem: row.Stem})
		}
	}
	return words, nil
}

func (text *Text) StemWords(_ context.Context, _ string, textIDs, stems []string) ([]service.StoredWord, error) {
	return text.words(textIDs, func(word domain.SearchWord) bool {
		for _, stem := range stems {
			if word.Stem == stem {
				return true
			}
		}
		return false
	}), nil
}

func (text *Text) RareStemWords(ctx context.Context, projectID string, textIDs, stems []string, maxBytes int64) ([]service.StoredWord, error) {
	rows, err := text.StemWords(ctx, projectID, textIDs, stems)
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{}
	for _, row := range rows {
		sizes[row.Stem] += int64(len(row.Postings))
	}
	return slices.DeleteFunc(rows, func(row service.StoredWord) bool { return sizes[row.Stem] > maxBytes }), nil
}

func (text *Text) WordsContaining(_ context.Context, _ string, textIDs []string, part string) ([]service.StoredWord, error) {
	return text.words(textIDs, func(word domain.SearchWord) bool { return strings.Contains(word.Word, part) }), nil
}

func (text *Text) Paragraphs(_ context.Context, _, _ string, keys []service.ParagraphKey) ([]service.ParagraphText, error) {
	var paragraphs []service.ParagraphText
	for _, key := range keys {
		for _, chapter := range text.version.Chapters {
			if chapter.ID == key.ChapterID && !text.lost[chapter.ID] && int(key.Order) >= 1 && int(key.Order) <= len(chapter.Paragraphs) {
				paragraphs = append(paragraphs, service.ParagraphText{ChapterID: chapter.ID, Order: key.Order, Text: chapter.Paragraphs[key.Order-1].Text})
			}
		}
	}
	return paragraphs, nil
}

func (text *Text) Cards(_ context.Context, _ string, keys []service.CardKey, parts domain.CardParts) (map[service.CardKey]domain.Card, error) {
	found := map[service.CardKey]domain.Card{}
	for i, chapter := range text.version.Chapters {
		key := service.CardKey{ChapterID: chapter.ID, ContentHash: contentHash(chapter)}
		if card := text.cards[i]; card != nil {
			for _, wanted := range keys {
				if wanted == key {
					found[key] = cardParts(*card, parts)
				}
			}
		}
	}
	return found, nil
}

// cardParts leaves out the parts a reader did not ask for, as the card query does.
func cardParts(card domain.Card, parts domain.CardParts) domain.Card {
	if !parts.Summary {
		card.Summary = ""
	}
	if !parts.Time {
		card.Time = nil
	}
	if !parts.Mentions {
		card.Characters, card.Entities = nil, nil
	}
	if !parts.Changes {
		card.Changes = nil
	}
	if !parts.ChapterTime {
		card.When = nil
	}
	if !parts.Events {
		card.Events = nil
	}
	if !parts.Facts {
		card.Facts = nil
	}
	if !parts.Knows {
		card.Knows = nil
	}
	if !parts.Hooks {
		card.Hooks = nil
	}
	return card
}

// Facts returns the facts of the cards whose subject is one of the names, as the card query
// narrows them.
func (text *Text) Facts(_ context.Context, _ string, keys []service.CardKey, names []string) (map[service.CardKey][]domain.Fact, error) {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[domain.GlossaryKey(name)] = true
	}
	found := map[service.CardKey][]domain.Fact{}
	for i, chapter := range text.version.Chapters {
		key := service.CardKey{ChapterID: chapter.ID, ContentHash: contentHash(chapter)}
		card := text.cards[i]
		if card == nil || !slices.Contains(keys, key) {
			continue
		}
		for _, fact := range card.Facts {
			if wanted[domain.GlossaryKey(fact.Subject)] {
				found[key] = append(found[key], fact)
			}
		}
		for _, known := range card.Knows {
			if wanted[domain.GlossaryKey(known.Who)] {
				found[key] = append(found[key], domain.Fact{Subject: known.Who, Attribute: known.Kind, Value: known.What, P: known.P})
			}
		}
	}
	return found, nil
}
