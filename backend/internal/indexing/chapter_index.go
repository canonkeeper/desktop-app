package indexing

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
)

var ErrCanonicalChapterDocument = errors.New("indexing: invalid canonical chapter document")

const (
	WholeChapterTransferMaxBytes = 2_000_000
	WholeChapterTransferMaxLines = 50_000
	wholeChapterEnvelopeBytes    = 4_096
)

func WholeChapterTransferFits(title, content string) bool {
	encodedTitle, _ := json.Marshal(title)
	encodedContent, _ := json.Marshal(content)
	return len(encodedTitle)+len(encodedContent)+wholeChapterEnvelopeBytes <= WholeChapterTransferMaxBytes &&
		strings.Count(content, "\n")+1 <= WholeChapterTransferMaxLines
}

type CanonicalChapterParagraph struct {
	ParagraphID string
	Order       uint32
	Content     string
	Kind        string
}

type CanonicalChapterSource struct {
	ProjectID        string
	BookID           string
	ChapterID        string
	SourceView       governance.SourceView
	SourceRevision   uint64
	SourceGeneration governance.Digest
	Title            string
	Paragraphs       []CanonicalChapterParagraph
}

type CanonicalChapterDocument struct {
	ProjectID        string
	BookID           string
	ChapterID        string
	SourceView       governance.SourceView
	SourceRevision   uint64
	SourceGeneration governance.Digest
	Title            string
	Content          string
	ContentHash      governance.Digest
}

func BuildCanonicalChapterDocument(source CanonicalChapterSource) (CanonicalChapterDocument, error) {
	if source.ProjectID == "" || source.BookID == "" || source.ChapterID == "" ||
		source.SourceRevision == 0 || source.SourceGeneration == (governance.Digest{}) ||
		strings.TrimSpace(source.Title) == "" || len(source.Paragraphs) == 0 {
		return CanonicalChapterDocument{}, ErrCanonicalChapterDocument
	}
	if _, err := governance.PresentSourceView(source.SourceView); err != nil {
		return CanonicalChapterDocument{}, ErrCanonicalChapterDocument
	}

	paragraphs := append([]CanonicalChapterParagraph(nil), source.Paragraphs...)
	sort.Slice(paragraphs, func(i, j int) bool {
		if paragraphs[i].Order != paragraphs[j].Order {
			return paragraphs[i].Order < paragraphs[j].Order
		}
		return paragraphs[i].ParagraphID < paragraphs[j].ParagraphID
	})
	canonical := make([]governance.CanonicalParagraphContent, len(paragraphs))
	content := make([]string, len(paragraphs))
	for index, paragraph := range paragraphs {
		if paragraph.ParagraphID == "" || !utf8.ValidString(paragraph.Content) || strings.TrimSpace(paragraph.Content) == "" ||
			(index > 0 && paragraph.Order == paragraphs[index-1].Order) {
			return CanonicalChapterDocument{}, ErrCanonicalChapterDocument
		}
		canonical[index] = governance.CanonicalParagraphContent{
			ParagraphID: paragraph.ParagraphID,
			Order:       paragraph.Order,
			Content:     paragraph.Content,
		}
		content[index] = paragraph.Content
	}
	hash, err := governance.CanonicalChapterContentDigest(canonical)
	if err != nil {
		return CanonicalChapterDocument{}, ErrCanonicalChapterDocument
	}
	return CanonicalChapterDocument{
		ProjectID: source.ProjectID, BookID: source.BookID, ChapterID: source.ChapterID,
		SourceView: source.SourceView, SourceRevision: source.SourceRevision,
		SourceGeneration: source.SourceGeneration, Title: source.Title,
		Content: strings.Join(content, "\n\n"), ContentHash: hash,
	}, nil
}
