package notes

import (
	library "canonkeeper/backend/internal/domain/library"
	domain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/governance"
	"testing"
)

func TestQuoteMatchesRequiresExactParagraphAndBrowserOffsets(t *testing.T) {
	id := "paragraph"
	current := chapterSnapshot{Paragraphs: []governance.CanonicalParagraphContent{{ParagraphID: id, Content: "A😀BC"}}}
	source := domain.Source{Kind: "chapter", Quote: "😀", Locator: &library.ReaderLocator{ParagraphID: &id, Range: &library.TextRange{StartOffset: 1, EndOffset: 3}}}
	if !quoteMatches(source, current) {
		t.Fatal("valid UTF-16 browser selection rejected")
	}
	source.Locator.Range.EndOffset = 2
	if quoteMatches(source, current) {
		t.Fatal("half surrogate accepted")
	}
	source.Locator.Range = nil
	id = "wrong"
	if quoteMatches(source, current) {
		t.Fatal("wrong paragraph accepted")
	}
}
