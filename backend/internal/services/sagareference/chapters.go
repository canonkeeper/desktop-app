package sagareference

import (
	"context"
	"strings"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// maxFoundChapters bounds the chapters a search of the contents finds.
const maxFoundChapters = 200

// BookView is a book with the keys of its chapters in reading order: a key is a chapter number or «bNcM», both in one
// book, so the book of a key is the one that lists it — not a range of its first and last keys (prod 06.11).
type BookView struct {
	sagamemory.Book
	Keys []string
}

func bookViews(version *sagamemory.Version) []BookView {
	books := version.Books()
	views := make([]BookView, len(books))
	for i, value := range books {
		keys := make([]string, 0, value.Last-value.First+1)
		for index := value.First; index <= value.Last; index++ {
			keys = append(keys, version.Chapters[index].Key)
		}
		views[i] = BookView{Book: value, Keys: keys}
	}
	return views
}

type ContentsInput struct {
	Scope
	BookID string
	Query  string
	// Chapter is a chapter key as a kept place holds it (a link of an answer, the place of a drawing, a key the voice
	// heard): the server reads it by the one rule of the keys and shows its book (specs/2026-11-11-ux7-reorder.md RK1.5).
	Chapter string
}

type ContentsResult struct {
	Coverage sagamemory.Coverage
	Books    []BookView
	Book     *sagamemory.Book // the book shown; nil when searching the whole saga
	Chapters []sagamemory.ContentsChapter
	// Found is the chapter of ContentsInput.Chapter under its key of now; nil when the text has no such chapter.
	Found *sagamemory.Chapter
}

// GetContents lists the chapters of a book with their card summaries, or the chapters of the
// whole saga whose title or summary matches a query (UC4 «Оглавление с пересказами»).
type GetContents struct{ reader }

func NewGetContents(access Access, memory Memory) *GetContents {
	return &GetContents{reader: newReader(access, memory)}
}

func (useCase *GetContents) Execute(ctx context.Context, input ContentsInput) (ContentsResult, error) {
	if !validText(input.Query, maxQueryRunes) || !validText(input.BookID, maxKeyRunes) || !validText(input.Chapter, maxKeyRunes) {
		return ContentsResult{}, ErrInvalid
	}
	outline, cards, err := useCase.load(ctx, input.Scope, sagamemory.CardParts{Summary: true})
	if err != nil {
		return ContentsResult{}, err
	}
	version := outline.Version
	result := ContentsResult{Coverage: cards.Coverage(version), Books: bookViews(version)}
	if strings.TrimSpace(input.Query) != "" {
		result.Chapters = cards.Contents(version, 0, len(version.Chapters)-1, input.Query, maxFoundChapters)
		return result, nil
	}
	bookID := input.BookID
	if key := strings.TrimSpace(input.Chapter); key != "" {
		if found, ok := version.Find(key); ok {
			result.Found, bookID = found, found.BookID
		}
	}
	selected, err := book(version, bookID)
	if err != nil || selected == nil {
		return result, err
	}
	result.Book = selected
	result.Chapters = cards.Contents(version, selected.First, selected.Last, "", 0)
	return result, nil
}
