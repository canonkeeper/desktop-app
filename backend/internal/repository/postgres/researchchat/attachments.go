package researchchat

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The chapter of the author attached to a question of the chat (specs/2026-11-07-chat-attach-chapter.md, CA2, CA6): one
// row a turn, numbered in its chat. The Codex thread keeps only its header; its text is here once.

// createTurnAttachmentInTx keeps the chapter attached to the question of a new turn, numbered after the attachments of
// its chat (CA2.4): the session is locked by the start of the turn, so two questions of one chat never share a number.
func createTurnAttachmentInTx(
	ctx context.Context, tx pgx.Tx, session writableSession, turnID string, attachment researchdomain.Attachment, createdAt time.Time,
) error {
	if session.Kind != agent.SessionResearch || !validAttachment(attachment) {
		return researchdomain.ErrValidation
	}
	projectID, projectErr := storedb.ParseUUID(session.ProjectID)
	chatID, chatErr := storedb.ParseUUID(session.ID)
	turn, turnErr := storedb.ParseUUID(turnID)
	if projectErr != nil || chatErr != nil || turnErr != nil {
		return researchdomain.ErrValidation
	}
	queries := sqlc.New(tx)
	number, err := queries.NextChatAttachmentNumber(ctx, sqlc.NextChatAttachmentNumberParams{ProjectID: projectID, ChatID: chatID})
	if err != nil {
		return err
	}
	if number > researchdomain.MaxAttachmentNumber {
		return researchdomain.ErrConflict
	}
	err = queries.CreateChatTurnAttachment(ctx, sqlc.CreateChatTurnAttachmentParams{
		TurnID: turn, ChatID: chatID, ProjectID: projectID, Number: number, Title: attachment.Title,
		FileName: attachment.FileName, Format: attachment.Format, Paragraphs: attachment.Paragraphs,
		Words: int32(attachment.Words), CreatedAt: storedb.Timestamptz(createdAt),
	})
	return mapWriteError(err)
}

func validAttachment(attachment researchdomain.Attachment) bool {
	if len(attachment.Paragraphs) == 0 || attachment.Words < 1 || utf8.RuneCountInString(attachment.Title) > researchdomain.MaxAttachmentTitleRunes ||
		utf8.RuneCountInString(attachment.FileName) > 255 {
		return false
	}
	switch attachment.Format {
	case "docx", "txt", "md", "text":
	default:
		return false
	}
	for _, paragraph := range attachment.Paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			return false
		}
	}
	return true
}

// turnAttachmentHeader is what the input of a turn says of its attachment (CA3.1), or nil when the question has none.
func (repository *Repository) turnAttachmentHeader(ctx context.Context, projectID, turnID string) (*researchdomain.AssignmentAttachment, error) {
	project, projectErr := storedb.ParseUUID(projectID)
	turn, turnErr := storedb.ParseUUID(turnID)
	if projectErr != nil || turnErr != nil {
		return nil, researchdomain.ErrValidation
	}
	row, err := sqlc.New(repository.db.Pool).GetChatTurnAttachmentHeader(ctx, sqlc.GetChatTurnAttachmentHeaderParams{ProjectID: project, TurnID: turn})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return researchdomain.Attachment{
		Number: int(row.Number), Title: row.Title, Paragraphs: make([]string, row.ParagraphCount), Words: int(row.Words),
	}.Header(), nil
}

// ChatAttachment reads an attachment of a live chat by its number; 0 names the latest (CA3.2, CA4.3). The caller has
// checked the authority over the chat.
func (repository *Repository) ChatAttachment(ctx context.Context, projectID, chatID string, number int) (researchdomain.Attachment, error) {
	project, projectErr := storedb.ParseUUID(projectID)
	chat, chatErr := storedb.ParseUUID(chatID)
	if projectErr != nil || chatErr != nil || number < 0 || number > researchdomain.MaxAttachmentNumber {
		return researchdomain.Attachment{}, researchdomain.ErrNotFound
	}
	row, err := sqlc.New(repository.db.Pool).GetChatAttachment(ctx, sqlc.GetChatAttachmentParams{
		ProjectID: project, ChatID: chat, Number: int32(number),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return researchdomain.Attachment{}, researchdomain.ErrNotFound
	}
	if err != nil {
		return researchdomain.Attachment{}, err
	}
	return researchdomain.Attachment{
		Number: int(row.Number), Title: row.Title, FileName: row.FileName, Format: row.Format,
		Paragraphs: row.Paragraphs, Words: int(row.Words), CreatedAt: timestamp(row.CreatedAt),
	}, nil
}

// ChatAttachmentNumbers lists the numbers of the attachments of a chat, for the model that named another one.
func (repository *Repository) ChatAttachmentNumbers(ctx context.Context, projectID, chatID string) ([]int, error) {
	project, projectErr := storedb.ParseUUID(projectID)
	chat, chatErr := storedb.ParseUUID(chatID)
	if projectErr != nil || chatErr != nil {
		return nil, researchdomain.ErrNotFound
	}
	rows, err := sqlc.New(repository.db.Pool).ListChatAttachmentNumbers(ctx, sqlc.ListChatAttachmentNumbersParams{ProjectID: project, ChatID: chat})
	if err != nil {
		return nil, err
	}
	numbers := make([]int, len(rows))
	for index, number := range rows {
		numbers[index] = int(number)
	}
	return numbers, nil
}

func timestamp(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}
