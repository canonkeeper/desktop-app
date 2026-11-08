package researchchat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

// The chapter of the author attached to a question, read by the chat agent (specs/2026-11-07-chat-attach-chapter.md,
// CA3): by paragraphs, as `read` reads a chapter of the book, signed as the author's text the book outweighs. It is not a
// chapter of the book: no manifest records it, and it is read while the search of the saga is still being prepared.

// maxAttachmentWordsPerCall bounds the words one call returns: a chapter of the author (about 3 000 words) is read at
// once, a long text by parts.
const maxAttachmentWordsPerCall = 6000

// OutcomeAttachmentNotFound is a number the chat has no attachment for, or a chat without attachments.
const OutcomeAttachmentNotFound = "attachment_not_found"

// AttachmentReader reads the attachments of a chat whose authority the caller checked.
type AttachmentReader interface {
	ChatAttachment(ctx context.Context, projectID, chatID string, number int) (researchdomain.Attachment, error)
	ChatAttachmentNumbers(ctx context.Context, projectID, chatID string) ([]int, error)
}

// AttachmentInput names the attachment of the chat (0 — the latest) and the paragraphs, from 1.
type AttachmentInput struct {
	Attachment int
	From       int
	To         int
}

// WithAttachments lets the tools read the chapters attached to the questions of the chat.
func (tools *ReadTools) WithAttachments(reader AttachmentReader) *ReadTools {
	tools.attachments = reader
	return tools
}

// Attachment is the step «прочитать приложение по абзацам» (CA3.2–CA3.4): the authority of the turn names the chat, the
// attachment of that chat is read by paragraphs with its signature; no chapter is retained.
func (tools *ReadTools) Attachment(ctx context.Context, call ToolContext, input AttachmentInput) (string, error) {
	if input.Attachment < 0 || input.Attachment > researchdomain.MaxAttachmentNumber || input.From < 0 || input.To < 0 {
		return "", argumentError("$", "номер приложения и номера абзацев начинаются с 1")
	}
	if err := ValidToolContext(ctx, call); err != nil {
		return "", err
	}
	authority, err := tools.repository.ReadAuthority(ctx, call)
	if err != nil {
		return "", err
	}
	if tools.attachments == nil {
		return "", attachmentsOfChat(nil)
	}
	attachment, err := tools.attachments.ChatAttachment(ctx, authority.ProjectID, authority.ChatID, input.Attachment)
	if errors.Is(err, researchdomain.ErrNotFound) {
		numbers, listErr := tools.attachments.ChatAttachmentNumbers(ctx, authority.ProjectID, authority.ChatID)
		if listErr != nil {
			return "", listErr
		}
		return "", attachmentsOfChat(numbers)
	}
	if err != nil {
		return "", err
	}
	total := len(attachment.Paragraphs)
	first, last := 1, total
	if input.From > 0 {
		first = input.From
	}
	if input.To > 0 {
		last = min(input.To, total)
	}
	if first > total || first > last {
		return "", argumentError("$", "в приложении %d всего %d %s", attachment.Number, total, russianCount(total, "абзац", "абзаца", "абзацев"))
	}
	return renderAttachment(attachment, first, last), nil
}

func attachmentsOfChat(numbers []int) error {
	if len(numbers) == 0 {
		return &ToolOutcome{Type: OutcomeAttachmentNotFound, Message: "в этом чате нет приложенных глав", Next: []string{"answer_without_attachment"}}
	}
	named := make([]string, len(numbers))
	for index, number := range numbers {
		named[index] = fmt.Sprint(number)
	}
	return &ToolOutcome{
		Type:    OutcomeAttachmentNotFound,
		Message: "такого приложения в этом чате нет; приложения чата: " + strings.Join(named, ", "),
		Next:    []string{"correct_arguments_and_retry"},
	}
}

// renderAttachment is the header with the signature and the paragraphs first..last, no more than
// maxAttachmentWordsPerCall words; the rest is named by where to go on.
func renderAttachment(attachment researchdomain.Attachment, first, last int) string {
	total := len(attachment.Paragraphs)
	title := attachment.Title
	if title == "" {
		title = "без названия"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Приложение %d «%s» — %s. %d %s, %d %s. Ссылка на абзац: [прил. ¶1](attachment:%d/1)\n",
		attachment.Number, title, researchdomain.AttachmentNote,
		total, russianCount(total, "абзац", "абзаца", "абзацев"),
		attachment.Words, russianCount(attachment.Words, "слово", "слова", "слов"), attachment.Number)
	words, shown := 0, first-1
	for index := first; index <= last; index++ {
		paragraph := attachment.Paragraphs[index-1]
		count := wordCount(paragraph)
		if index > first && words+count > maxAttachmentWordsPerCall {
			break
		}
		words += count
		shown = index
		fmt.Fprintf(&out, "¶%d %s\n", index, paragraph)
	}
	if shown < last {
		fmt.Fprintf(&out, "Показаны ¶%d–¶%d из %d; дальше — from=%d\n", first, shown, total, shown+1)
	}
	return out.String()
}

// wordCount counts the words of a paragraph as the reader of a chapter file does: runs of letters and digits.
func wordCount(text string) int {
	return len(strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) }))
}

// russianCount picks the form of a word for a number: 1 абзац, 3 абзаца, 5 абзацев.
func russianCount(number int, one, few, many string) string {
	tens, units := number%100, number%10
	switch {
	case tens >= 11 && tens <= 14:
		return many
	case units == 1:
		return one
	case units >= 2 && units <= 4:
		return few
	default:
		return many
	}
}
