package notes

import (
	"context"
	"encoding/json"

	domain "canonkeeper/backend/internal/domain/notes"
	"canonkeeper/backend/internal/governance"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
)

// SagaNotesInTx reads the notes of the owner whose source is in the saga — a place of one of its chapters or a quote of
// one of its chats — oldest first: the saga file carries them (specs/2026-11-13-desk-sagafile.md SF1.7). The caller has
// checked who reads them.
func SagaNotesInTx(ctx context.Context, tx pgx.Tx, ownerUserID, projectID string) ([]domain.Note, error) {
	owner, err := storedb.ParseUUID(ownerUserID)
	if err != nil {
		return nil, domain.ErrNotFound
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, title, body, revision, source_json, created_at, updated_at
		FROM personal_notes
		WHERE owner_user_id = $1 AND source_json IS NOT NULL
		ORDER BY created_at, id
	`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []domain.Note
	for rows.Next() {
		var note domain.Note
		var body []byte
		if err := rows.Scan(&note.ID, &note.Title, &note.Body, &note.Revision, &body, &note.CreatedAt, &note.UpdatedAt); err != nil {
			return nil, err
		}
		var saved savedSource
		if err := json.Unmarshal(body, &saved); err != nil || saved.Source.Validate() != nil {
			continue
		}
		switch {
		case saved.Source.Kind == "chapter" && saved.Source.Locator.ProjectID == projectID,
			saved.Source.Kind == "chat" && saved.Chat != nil && saved.Chat.ProjectID == projectID:
			source := saved.Source
			note.Source = &source
			notes = append(notes, note)
		}
	}
	return notes, rows.Err()
}

// CreateArchiveNoteInTx keeps a note a saga file brought for the author who opened it, on the transaction of the open
// (specs/2026-11-13-desk-sagafile.md SF3.7): a note of a chapter is bound to the chapter of the new saga by the same code
// that binds a note when it is made, without a check of its quote — the text is the one it was made on, or a later one,
// and the note then says its place changed. The note keeps its dates.
func CreateArchiveNoteInTx(ctx context.Context, tx pgx.Tx, ownerUserID string, note domain.Note) error {
	owner, err := storedb.ParseUUID(ownerUserID)
	if err != nil {
		return domain.ErrNotFound
	}
	if domain.ValidateTitle(note.Title) != nil || domain.ValidateThought(note.Body, note.Source != nil) != nil {
		return domain.ErrValidation
	}
	var body []byte
	if note.Source != nil {
		saved := savedSource{Source: *note.Source}
		if err := saved.Source.Validate(); err != nil || saved.Source.Kind != "chapter" {
			return domain.ErrValidation
		}
		current, err := readableChapter(ctx, tx, owner, saved.Source)
		if err != nil {
			return err
		}
		locator := saved.Source.Locator
		saved.Chapter = &governance.CanonicalChapterSourceLocatorV1{
			ProjectID: locator.ProjectID, BookID: locator.BookID, ChapterID: locator.ChapterID, SourceRef: locator.ChapterID,
			SourceView: governance.SourceViewPublished, SourceRevision: current.SourceRevision, SourceGeneration: current.SourceGeneration,
			ContentHash: current.ContentHash,
		}
		if body, err = json.Marshal(saved); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO personal_notes (owner_user_id, title, body, source_json, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, owner, note.Title, note.Body, body, note.CreatedAt, note.UpdatedAt)
	return err
}
