package sagareference

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

const (
	defaultGlossaryLimit = 100
	maxGlossaryLimit     = 200
)

type GlossaryInput struct {
	Scope
	Type   string
	Query  string
	Offset int
	Limit  int
}

// GlossaryItem is an entry with the chapter of its first mention.
type GlossaryItem struct {
	Entry *sagamemory.GlossaryEntry
	First *sagamemory.Chapter
}

type GlossaryResult struct {
	Coverage sagamemory.Coverage
	Types    []sagamemory.TypeCount
	Total    int
	Offset   int
	Entries  []GlossaryItem
}

// ListGlossary pages the saga glossary of a type or of all types, filtered by a query over
// names, aliases and notes, most mentioned first (UC1 «страница записей словаря»).
type ListGlossary struct{ reader }

func NewListGlossary(access Access, memory Memory) *ListGlossary {
	return &ListGlossary{reader: newReader(access, memory)}
}

func (useCase *ListGlossary) Execute(ctx context.Context, input GlossaryInput) (GlossaryResult, error) {
	input.Type = strings.TrimSpace(input.Type)
	if !validText(input.Query, maxQueryRunes) || !validText(input.Type, maxKeyRunes) || input.Offset < 0 ||
		input.Limit < 0 || input.Limit > maxGlossaryLimit {
		return GlossaryResult{}, ErrInvalid
	}
	if input.Limit == 0 {
		input.Limit = defaultGlossaryLimit
	}
	outline, cards, err := useCase.load(ctx, input.Scope, sagamemory.CardParts{Mentions: true})
	if err != nil {
		return GlossaryResult{}, err
	}
	var types []string
	if input.Type != "" {
		types = []string{input.Type}
	}
	found := cards.Glossary.Find(input.Query, types)
	result := GlossaryResult{
		Coverage: cards.Coverage(outline.Version), Types: cards.Glossary.TypeCounts(), Total: len(found), Offset: input.Offset,
	}
	for i := input.Offset; i < len(found) && i < input.Offset+input.Limit; i++ {
		result.Entries = append(result.Entries, GlossaryItem{Entry: found[i], First: &outline.Version.Chapters[found[i].Chapters[0]]})
	}
	return result, nil
}
