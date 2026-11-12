package researchchat

import (
	"context"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The author's words about what an answer is asked about (specs/2026-11-11-chat-corrections.md CR1): the corrections and the
// plans of the author's memory about the hero, the place or the words a saga tool was asked about come with its answer,
// signed with their kind, so a correction the author made in one chat holds in every next answer without the model reading
// the memory first. They are the author's words, not a chapter: nothing is retained for them.

const (
	// maxAuthorWordsShown is how many records an answer brings; the rest are counted and read by saga.memory.
	maxAuthorWordsShown = 5
	// maxAuthorWordsRead bounds the newest corrections and plans one call reads to find those about what it names.
	maxAuthorWordsRead = 500
	// authorWordsRecordRunes bounds a record in the block; saga.memory reads the whole of it.
	authorWordsRecordRunes = 1200
)

// AuthorMemoryReader reads the records of the turn's project under the authority of the turn: a member of the saga, the
// tool calls of its thread queued.
type AuthorMemoryReader interface {
	AuthorMemoryForTurn(context.Context, ToolContext, researchdomain.ReadAuthority, authormemory.Filter) (authormemory.Page, error)
}

// WithAuthorMemory lets the tools bring the author's words about what they are asked about.
func (tools *ReadTools) WithAuthorMemory(reader AuthorMemoryReader) *ReadTools {
	tools.memory = reader
	return tools
}

// about records what the answer is asked about, with the glossary the tool resolved names by; nil reads it only when a record
// may be about it.
func (saga *sagaScope) about(named sagamemory.Named, glossary *sagamemory.Glossary) {
	saga.named = saga.named.And(named)
	if glossary != nil {
		saga.glossary = glossary
	}
}

// withAuthorWords adds the block of the author's words to an answer; an answer without them stays as it was.
func (tools *ReadTools) withAuthorWords(ctx context.Context, call ToolContext, saga *sagaScope, output string) (string, error) {
	if tools.memory == nil || !saga.named.Asked() {
		return output, nil
	}
	page, err := tools.memory.AuthorMemoryForTurn(ctx, call, saga.authority, authormemory.Filter{Kinds: authormemory.AboutKinds, Limit: maxAuthorWordsRead})
	if err != nil || len(page.Records) == 0 {
		return output, err
	}
	glossary := saga.glossary
	if glossary == nil {
		cards, err := saga.cards(0, -1, sagamemory.CardParts{Mentions: true})
		if err != nil {
			return "", err
		}
		glossary = saga.glossaryOf(cards)
	}
	records := authormemory.About(page.Records, saga.named.Subject(glossary))
	if len(records) == 0 {
		return output, nil
	}
	return strings.TrimRight(output, "\n") + "\n\n" + renderAuthorWords(records, saga.version()), nil
}

// glossaryOf is the glossary of the cards a call may see: a book-scoped chat knows the names of its book.
func (saga *sagaScope) glossaryOf(cards *sagamemory.CardSet) *sagamemory.Glossary {
	if saga.authority.Scope == researchdomain.ScopeBook {
		return sagamemory.BuildGlossary(saga.scopedCards(cards), cards.Names)
	}
	return cards.Glossary
}

// renderAuthorWords lists the newest records, each signed with its kind, the key of its chapter and its name (CR1.3).
func renderAuthorWords(records []authormemory.Record, version *sagamemory.Version) string {
	keys := make(map[string]string, len(version.Chapters))
	for index := range version.Chapters {
		keys[version.Chapters[index].ID] = version.Chapters[index].Key
	}
	var out strings.Builder
	out.WriteString("Автор сказал об этом (записи памяти автора, не текст книги):\n")
	for index, record := range records {
		if index == maxAuthorWordsShown {
			fmt.Fprintf(&out, "… и ещё %d: прочитай memory\n", len(records)-maxAuthorWordsShown)
			break
		}
		text := record.Text
		if runes := []rune(text); len(runes) > authorWordsRecordRunes {
			text = string(runes[:authorWordsRecordRunes]) + "… (целиком — в memory)"
		}
		fmt.Fprintf(&out, "[%s] %s (%s)\n", record.AboutSignature(keys), text, record.CreatedAt.Format("02.01.2006"))
	}
	return out.String()
}
