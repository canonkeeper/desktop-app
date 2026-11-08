// Package sagamemory reads the current text version of a project for the saga memory: the
// canonical chapter selection, paragraphs of the selected revisions, the saga search rows and
// the chapter cards.
package sagamemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	domain "canonkeeper/backend/internal/domain/sagamemory"
	service "canonkeeper/backend/internal/services/sagamemory"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Repository { return &Repository{db: db} }

// Selection reads the canonical chapter selection of a text version (the same rows the
// chat turn baseline uses) and fingerprints it. Only chapters whose current revision has
// indexed text are selected: their text has an exact source identity that tool reads retain.
func (repository *Repository) Selection(ctx context.Context, projectID, view string) (service.Selection, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.Selection{}, service.ErrInvalid
	}
	return readSelection(ctx, sqlc.New(repository.db.Pool), project, projectID, view)
}

// Outline reads the chapter selection of a text version and the stored search texts of its
// chapters in one snapshot, so a chapter deleted or rewritten meanwhile is either selected with
// its search text or not selected at all (specs/2026-10-28-saga-search-in-postgres.md, UC2).
func (repository *Repository) Outline(ctx context.Context, projectID, view string) (service.Selection, map[service.CardKey]domain.SearchText, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.Selection{}, nil, service.ErrInvalid
	}
	tx, err := repository.db.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return service.Selection{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	selection, err := readSelection(ctx, queries, project, projectID, view)
	if err != nil {
		return service.Selection{}, nil, err
	}
	texts, err := searchTexts(ctx, queries, project, selection.Chapters)
	if err != nil {
		return service.Selection{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return service.Selection{}, nil, err
	}
	return selection, texts, nil
}

func readSelection(ctx context.Context, queries *sqlc.Queries, project pgtype.UUID, projectID, view string) (service.Selection, error) {
	rows, err := queries.ListCanonicalTextSelectionRows(ctx, sqlc.ListCanonicalTextSelectionRowsParams{ProjectID: project, SourceView: view})
	if err != nil {
		return service.Selection{}, err
	}
	books, err := queries.ListSagaMemoryBooks(ctx, project)
	if err != nil {
		return service.Selection{}, err
	}
	bookOrder := map[string]int{}
	bookTitle := map[string]string{}
	for _, book := range books {
		id := storedb.UUIDString(book.ID)
		bookOrder[id] = int(book.Order)
		bookTitle[id] = book.Title
	}
	selection := service.Selection{ProjectID: projectID, View: view}
	hash := sha256.New()
	for _, row := range rows {
		if !row.LiveBookID.Valid || !row.LiveChapterID.Valid || !row.SourceRevision.Valid || !row.ChapterOrder.Valid ||
			row.StateSourceRevision != row.SourceRevision || len(row.StateChapterContentHash) == 0 {
			continue
		}
		bookID := storedb.UUIDString(row.BookID)
		chapter := service.SelectedChapter{
			ID: storedb.UUIDString(row.ChapterID), Revision: row.SourceRevision.Int64,
			ContentHash: hex.EncodeToString(row.StateChapterContentHash),
			BookID:      bookID, BookOrder: bookOrder[bookID], BookTitle: bookTitle[bookID],
			Order: int(row.ChapterOrder.Int32), Title: row.ChapterTitle.String,
		}
		selection.Chapters = append(selection.Chapters, chapter)
	}
	sort.Slice(selection.Chapters, func(i, j int) bool {
		a, b := selection.Chapters[i], selection.Chapters[j]
		if a.BookOrder != b.BookOrder {
			return a.BookOrder < b.BookOrder
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
	for _, chapter := range selection.Chapters {
		fmt.Fprintf(hash, "%s|%d|%s|%d|%s|%d|%s|%s\n", chapter.BookID, chapter.BookOrder, chapter.BookTitle,
			chapter.Order, chapter.ID, chapter.Revision, chapter.ContentHash, chapter.Title)
	}
	selection.Fingerprint = hex.EncodeToString(hash.Sum(nil))
	return selection, nil
}

// Chapters loads the paragraphs of every selected chapter revision.
func (repository *Repository) Chapters(ctx context.Context, selection service.Selection) ([]domain.Chapter, error) {
	project, err := storedb.ParseUUID(selection.ProjectID)
	if err != nil {
		return nil, service.ErrInvalid
	}
	params := sqlc.ListSagaMemoryParagraphsParams{ProjectID: project, SourceView: selection.View}
	byID := map[string]int{}
	chapters := make([]domain.Chapter, len(selection.Chapters))
	for i, selected := range selection.Chapters {
		id, err := storedb.ParseUUID(selected.ID)
		if err != nil {
			return nil, service.ErrInvalid
		}
		params.ChapterIds = append(params.ChapterIds, id)
		params.ChapterRevisions = append(params.ChapterRevisions, selected.ID+":"+strconv.FormatInt(selected.Revision, 10))
		byID[selected.ID] = i
		chapters[i] = domain.Chapter{
			ID: selected.ID, Revision: selected.Revision, ContentHash: selected.ContentHash, BookID: selected.BookID,
			BookOrder: selected.BookOrder, BookTitle: selected.BookTitle, Order: selected.Order, Title: selected.Title,
		}
	}
	if len(params.ChapterIds) == 0 {
		return chapters, nil
	}
	rows, err := sqlc.New(repository.db.Pool).ListSagaMemoryParagraphs(ctx, params)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		index, ok := byID[storedb.UUIDString(row.ChapterID)]
		if !ok {
			continue
		}
		chapters[index].Paragraphs = append(chapters[index].Paragraphs, domain.Paragraph{Text: row.PlainText})
	}
	return chapters, nil
}
