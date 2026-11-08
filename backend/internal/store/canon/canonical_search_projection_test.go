package canonstore

import (
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/search"
	"canonkeeper/backend/internal/store/sqlc"
)

func TestLexicalSearchRowKeepsParagraphNavigationSeparate(t *testing.T) {
	input := search.LexicalSearchInput{ProjectID: indexingStoreUUIDForTest(9501), SourceView: governance.SourceViewDraft}
	digest := governance.Digest{1}
	row := sqlc.SearchCanonicalLexicalRow{
		ID: mustStoreUUID(indexingStoreUUIDForTest(9503)), ProjectID: mustStoreUUID(input.ProjectID),
		BookID: mustStoreUUID(indexingStoreUUIDForTest(9502)), ChapterID: mustStoreUUID(indexingStoreUUIDForTest(9503)),
		SourceRevision: 7, SourceView: "draft", LocatorSourceGeneration: digest[:], ContentHash: digest[:],
		Content: "whole chapter", ChapterTitle: "Глава 582. Порог", ChapterPosition: 2, Rank: 1,
		SourceParagraphID: indexingStoreUUIDForTest(9504), SourceParagraphPosition: 18, SourceParagraphContent: "matched paragraph",
	}
	got, err := lexicalSearchRow(input, row)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != row.Content || got.ChapterNumber != "582" ||
		got.ParagraphMatch == nil || got.ParagraphMatch.ID != row.SourceParagraphID ||
		got.ParagraphMatch.Position != 18 || got.ParagraphMatch.Content != row.SourceParagraphContent {
		t.Fatalf("chapter projection = %#v", got)
	}
	row.SourceParagraphID, row.SourceParagraphPosition, row.SourceParagraphContent = "", 0, ""
	got, err = lexicalSearchRow(input, row)
	if err != nil || got.ParagraphMatch != nil || got.ChapterSource == nil {
		t.Fatalf("whole-chapter projection = %#v, %v", got, err)
	}
}
