package indexingstore

import (
	"strings"
	"testing"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
)

func TestBuildCanonChapterDocumentSkipsBlankParagraphsButKeepsSourceHash(t *testing.T) {
	generation := governance.Digest{1}
	paragraphs := []indexing.CanonicalParagraphSource{
		{ParagraphID: "00000000-0000-4000-8000-000000000101", Order: 1, Content: "Первый"},
		{ParagraphID: "00000000-0000-4000-8000-000000000102", Order: 2, Content: "  \n"},
		{ParagraphID: "00000000-0000-4000-8000-000000000103", Order: 3, Content: "Второй"},
	}
	canonical := make([]governance.CanonicalParagraphContent, len(paragraphs))
	for index, paragraph := range paragraphs {
		canonical[index] = governance.CanonicalParagraphContent{
			ParagraphID: paragraph.ParagraphID, Order: uint32(paragraph.Order), Content: paragraph.Content,
		}
		paragraph.ProjectID = "00000000-0000-4000-8000-000000000001"
		paragraph.BookID = "00000000-0000-4000-8000-000000000002"
		paragraph.ChapterID = "00000000-0000-4000-8000-000000000003"
		paragraph.SourceView = governance.SourceViewDraft
		paragraph.SourceRevision = 2
		paragraph.SourceGeneration = generation
		paragraphs[index] = paragraph
	}
	sourceHash, err := governance.CanonicalChapterContentDigest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	document, err := buildCanonChapterDocument(CanonTextChapterSnapshot{
		CanonTextChapterDescriptor: CanonTextChapterDescriptor{
			ProjectID: paragraphs[0].ProjectID, BookID: paragraphs[0].BookID, ChapterID: paragraphs[0].ChapterID,
			SourceView: governance.SourceViewDraft, SourceRevision: 2, SourceGeneration: generation,
		},
		Paragraphs: paragraphs, ChapterContentHash: sourceHash,
	}, "Глава")
	if err != nil {
		t.Fatal(err)
	}
	if document.Content != "Первый\n\nВторой" || document.ContentHash != sourceHash {
		t.Fatalf("document content/hash = %q/%x", document.Content, document.ContentHash)
	}
}

func TestPreflightCanonChapterDocumentRejectsTransferOverflow(t *testing.T) {
	for name, content := range map[string]string{
		"encoded bytes": strings.Repeat("\u0001", 400_000),
		"lines":         strings.Repeat("x\n", 50_001),
	} {
		t.Run(name, func(t *testing.T) {
			err := preflightCanonChapterDocument(indexing.CanonicalChapterDocument{Title: "Глава", Content: content})
			if err == nil {
				t.Fatal("unsafe whole chapter accepted")
			}
		})
	}
}
