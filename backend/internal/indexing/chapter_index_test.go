package indexing

import (
	"strings"
	"testing"

	"canonkeeper/backend/internal/governance"
)

func TestBuildCanonicalChapterDocumentKeepsWholeChapter(t *testing.T) {
	source := chapterDocumentSource([]string{
		"Первый абзац.",
		"+10 к интеллекту.",
		"Финал.",
	})

	got, err := BuildCanonicalChapterDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Первый абзац.\n\n+10 к интеллекту.\n\nФинал." {
		t.Fatalf("content = %q", got.Content)
	}
	if got.ChapterID != source.ChapterID || got.ContentHash == (governance.Digest{}) {
		t.Fatalf("document identity = %#v", got)
	}
}

func TestBuildCanonicalChapterDocumentNeverSplits(t *testing.T) {
	content := strings.Repeat("Текст. ", 500_000)
	got, err := BuildCanonicalChapterDocument(chapterDocumentSource([]string{content}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got.Content, "Текст.") != 500_000 {
		t.Fatal("chapter content was truncated or split")
	}
}

func chapterDocumentSource(paragraphs []string) CanonicalChapterSource {
	items := make([]CanonicalChapterParagraph, len(paragraphs))
	for index, content := range paragraphs {
		items[index] = CanonicalChapterParagraph{
			ParagraphID: "40000000-0000-4000-8000-" + chapterDocumentIDPart(index+1),
			Order:       uint32(index),
			Content:     content,
		}
	}
	return CanonicalChapterSource{
		ProjectID:        "10000000-0000-4000-8000-000000000001",
		BookID:           "20000000-0000-4000-8000-000000000001",
		ChapterID:        "30000000-0000-4000-8000-000000000001",
		SourceView:       governance.SourceViewDraft,
		SourceRevision:   1,
		SourceGeneration: governance.Digest{1},
		Title:            "Глава 1",
		Paragraphs:       items,
	}
}

func chapterDocumentIDPart(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}
