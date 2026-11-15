// SPDX-License-Identifier: AGPL-3.0-only

package librarystore

import (
	"context"
	"errors"

	domainlibrary "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/library"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// UpdateChanges says whether «Обновить из файла» of a live book by the chapters of a file would change anything
// (specs/2026-11-14-desk-obsidian-books.md OK0.4, OK2.2): the plan the update writes by (planImportedBookUpdate), read
// without a lock — a chapter added or removed, of other words or signs, under another title or at another place, a row
// that takes other italic or bold, or a chapter whose published text lags behind its draft. Nothing is written; a book that
// is not live in the saga is ErrNotFound.
func (s *Store) UpdateChanges(ctx context.Context, projectID, bookID string, file []library.ImportedChapter) (bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, library.ErrNotFound
	}
	book, err := storedb.ParseUUID(bookID)
	if err != nil {
		return false, library.ErrNotFound
	}
	inputs := make([]library.FinalizeImportChapterInput, len(file))
	for index, chapter := range file {
		paragraphs := make([]library.ParagraphInput, len(chapter.Paragraphs))
		for row := range chapter.Paragraphs {
			text, markdown := chapter.Paragraphs[row], chapter.Paragraphs[row]
			if row < len(chapter.Markdown) {
				markdown = chapter.Markdown[row]
			}
			paragraphs[row] = library.ParagraphInput{Kind: library.ParagraphKindParagraph, Order: row + 1, Markdown: markdown, Text: &text}
		}
		inputs[index] = library.FinalizeImportChapterInput{Title: chapter.Title, Paragraphs: paragraphs}
	}
	changed := false
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		var update importedBookUpdate
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation, title FROM books WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL
		`, book, project).Scan(&update.bookGeneration, &update.bookTitle); errors.Is(err, pgx.ErrNoRows) {
			return library.ErrNotFound
		} else if err != nil {
			return err
		}
		chapters, err := bookChapterRows(ctx, tx, project, book, "")
		if err != nil {
			return err
		}
		if chapters, err = readBookChapterTexts(ctx, tx, project, book, chapters); err != nil {
			return err
		}
		if update, err = planImportedBookUpdate(update, chapters, inputs); err != nil {
			return err
		}
		changed = update.changes(inputs)
		return nil
	})
	return changed, err
}

// changes says whether the update by its plan writes anything into the book: what updateImportedBookInTx does for each
// chapter of the file and for each chapter the file no longer has.
func (update importedBookUpdate) changes(file []library.FinalizeImportChapterInput) bool {
	if len(update.plan.Removed) > 0 {
		return true
	}
	for index, pairing := range update.plan.Chapters {
		if pairing.Book < 0 || pairing.Change != domainlibrary.TextSame {
			return true
		}
		chapter := update.chapters[pairing.Book]
		if chapter.title != file[index].Title || chapter.order != index+1 || len(chapter.contents) != len(update.fileContents[index]) {
			return true
		}
		for row, content := range update.fileContents[index] {
			if markdown, ok := restyledMarkdown(chapter.contents[row], content); !ok || markdown != chapter.contents[row].markdown {
				return true
			}
		}
	}
	return false
}
