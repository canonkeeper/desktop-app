package notes

import (
	"context"
	"strings"

	domain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type chapterSnapshot struct {
	governance.CanonicalChapterSourceLocatorV1
	Content    string
	Paragraphs []governance.CanonicalParagraphContent
}

// Read the same published revision as Reader, including when search indexing is
// pending. Lifecycle and paragraph hashes use the existing canonical digest.
func readableChapter(ctx context.Context, tx pgx.Tx, owner pgtype.UUID, source domain.Source) (chapterSnapshot, error) {
	locator := source.Locator
	current := chapterSnapshot{CanonicalChapterSourceLocatorV1: governance.CanonicalChapterSourceLocatorV1{
		ProjectID: locator.ProjectID, BookID: locator.BookID, ChapterID: locator.ChapterID, SourceRef: locator.ChapterID, SourceView: governance.SourceViewPublished,
	}}
	var projectGeneration, bookGeneration, chapterGeneration int64
	err := tx.QueryRow(ctx, `
  SELECT chapter.published_revision,project.lifecycle_generation,book.lifecycle_generation,chapter.lifecycle_generation
  FROM chapters AS chapter
  JOIN books AS book ON book.id=chapter.book_id AND book.project_id=chapter.project_id AND book.deleted_at IS NULL
  JOIN projects AS project ON project.id=chapter.project_id AND project.deleted_at IS NULL
  JOIN project_memberships AS member ON member.project_id=project.id AND member.user_id=$1 AND member.removed_at IS NULL
  WHERE chapter.id=$2::uuid AND book.id=$3::uuid AND project.id=$4::uuid
    AND chapter.deleted_at IS NULL AND chapter.published_revision IS NOT NULL
  FOR SHARE OF chapter,book,project,member
 `, owner, locator.ChapterID, locator.BookID, locator.ProjectID).Scan(&current.SourceRevision, &projectGeneration, &bookGeneration, &chapterGeneration)
	if err != nil {
		return current, err
	}
	current.SourceGeneration, err = governance.SourceGenerationDigest(governance.SourceGenerationVector{
		ProjectID: locator.ProjectID, ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		BookID: locator.BookID, BookGeneration: governance.LifecycleGeneration(bookGeneration),
		ChapterID: locator.ChapterID, ChapterGeneration: governance.LifecycleGeneration(chapterGeneration),
	})
	if err != nil {
		return current, err
	}
	projectID, _ := storedb.ParseUUID(locator.ProjectID)
	bookID, _ := storedb.ParseUUID(locator.BookID)
	chapterID, _ := storedb.ParseUUID(locator.ChapterID)
	rows, err := sqlc.New(tx).ListChapterParagraphsByChapterRevisionVariant(ctx, sqlc.ListChapterParagraphsByChapterRevisionVariantParams{
		ProjectID: projectID, BookID: bookID, ChapterID: chapterID, Revision: int64(current.SourceRevision), Variant: "published",
	})
	if err != nil {
		return current, err
	}
	texts := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Order < 0 {
			return current, domain.ErrUnavailable
		}
		current.Paragraphs = append(current.Paragraphs, governance.CanonicalParagraphContent{ParagraphID: row.ID.String(), Order: uint32(row.Order), Content: row.PlainText})
		texts = append(texts, row.PlainText)
	}
	current.Content = strings.Join(texts, "\n\n")
	current.ContentHash, err = governance.CanonicalChapterContentDigest(current.Paragraphs)
	return current, err
}
