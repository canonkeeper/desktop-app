package indexingstore

import (
	"strings"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type preparedCanonTextReplacement struct {
	projectID, jobID, bookID, chapterID pgtype.UUID
}

type textIndexChapterOversizeError struct{}

func (textIndexChapterOversizeError) Error() string {
	return "indexing: whole chapter exceeds shared byte budget"
}

func (textIndexChapterOversizeError) Unwrap() error { return ErrTextIndexValidation }

func (textIndexChapterOversizeError) NonRetryableCanonTextIndexError() {}

var ErrTextIndexChapterOversize error = textIndexChapterOversizeError{}

func buildCanonChapterDocument(snapshot CanonTextChapterSnapshot, title string) (indexing.CanonicalChapterDocument, error) {
	paragraphs := make([]indexing.CanonicalChapterParagraph, 0, len(snapshot.Paragraphs))
	for _, paragraph := range snapshot.Paragraphs {
		if strings.TrimSpace(paragraph.Content) == "" {
			continue
		}
		paragraphs = append(paragraphs, indexing.CanonicalChapterParagraph{
			ParagraphID: paragraph.ParagraphID, Order: uint32(paragraph.Order), Content: paragraph.Content,
		})
	}
	if len(paragraphs) == 0 {
		return indexing.CanonicalChapterDocument{
			ProjectID: snapshot.ProjectID, BookID: snapshot.BookID, ChapterID: snapshot.ChapterID,
			SourceView: snapshot.SourceView, SourceRevision: snapshot.SourceRevision,
			SourceGeneration: snapshot.SourceGeneration, Title: title,
			Content: "", ContentHash: snapshot.ChapterContentHash,
		}, nil
	}
	document, err := indexing.BuildCanonicalChapterDocument(indexing.CanonicalChapterSource{
		ProjectID: snapshot.ProjectID, BookID: snapshot.BookID, ChapterID: snapshot.ChapterID,
		SourceView: snapshot.SourceView, SourceRevision: snapshot.SourceRevision,
		SourceGeneration: snapshot.SourceGeneration, Title: title, Paragraphs: paragraphs,
	})
	document.ContentHash = snapshot.ChapterContentHash
	return document, err
}

func preflightCanonChapterDocument(document indexing.CanonicalChapterDocument) error {
	if !indexing.WholeChapterTransferFits(document.Title, document.Content) {
		return ErrTextIndexChapterOversize
	}
	return nil
}

func prepareCanonTextReplacement(input ReplaceCanonTextChapterInput) (preparedCanonTextReplacement, error) {
	if validateCanonTextDescriptor(input.Snapshot.CanonTextChapterDescriptor) != nil || input.Snapshot.Claim.Validate() != nil ||
		input.Snapshot.Claim.ProjectID != input.Snapshot.ProjectID || input.IndexedAt.IsZero() {
		return preparedCanonTextReplacement{}, ErrTextIndexValidation
	}
	projectID, jobID, err := textIndexClaimIDs(input.Snapshot.Claim)
	if err != nil {
		return preparedCanonTextReplacement{}, ErrTextIndexValidation
	}
	bookID, err := storedb.ParseUUID(input.Snapshot.BookID)
	if err != nil {
		return preparedCanonTextReplacement{}, ErrTextIndexValidation
	}
	chapterID, err := storedb.ParseUUID(input.Snapshot.ChapterID)
	if err != nil {
		return preparedCanonTextReplacement{}, ErrTextIndexValidation
	}
	contents := make([]governance.CanonicalParagraphContent, 0, len(input.Snapshot.Paragraphs))
	for _, paragraph := range input.Snapshot.Paragraphs {
		if paragraph.ProjectID != input.Snapshot.ProjectID || paragraph.BookID != input.Snapshot.BookID || paragraph.ChapterID != input.Snapshot.ChapterID ||
			paragraph.SourceView != input.Snapshot.SourceView || paragraph.SourceRevision != input.Snapshot.SourceRevision || paragraph.SourceGeneration != input.Snapshot.SourceGeneration || paragraph.Order < 0 {
			return preparedCanonTextReplacement{}, ErrTextIndexValidation
		}
		contents = append(contents, governance.CanonicalParagraphContent{ParagraphID: paragraph.ParagraphID, Order: uint32(paragraph.Order), Content: paragraph.Content})
	}
	hash, err := governance.CanonicalChapterContentDigest(contents)
	if err != nil || hash != input.Snapshot.ChapterContentHash {
		return preparedCanonTextReplacement{}, ErrTextIndexValidation
	}
	return preparedCanonTextReplacement{projectID: projectID, jobID: jobID, bookID: bookID, chapterID: chapterID}, nil
}
func canonicalStateParams(snapshot CanonTextChapterSnapshot) sqlc.LockMatchingCanonTextChapterStateParams {
	projectID, _ := storedb.ParseUUID(snapshot.ProjectID)
	chapterID, _ := storedb.ParseUUID(snapshot.ChapterID)
	return sqlc.LockMatchingCanonTextChapterStateParams{
		ProjectID: projectID, SourceView: string(snapshot.SourceView), ChapterID: chapterID,
		SourceRevision: int64(snapshot.SourceRevision), SourceGeneration: snapshot.SourceGeneration[:],
		ChapterContentHash: snapshot.ChapterContentHash[:],
	}
}
