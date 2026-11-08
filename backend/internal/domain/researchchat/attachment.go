package researchchat

import (
	"path"
	"strings"
	"time"

	"canonkeeper/backend/internal/domain/chapterfile"
)

// The chapter of the author attached to a question of the chat (specs/2026-11-07-chat-attach-chapter.md, CA1): his
// new chapter, not yet in the book, read by the one reader of a chapter file. The input of the turn carries only its
// header; its text lives once, in the row of the turn, and the model reads it by paragraphs with saga.attachment.

// AttachmentNote signs the attachment wherever the model reads it, as the materials of the author are signed: it is the
// author's text, not the book, and the book wins (CA1.4).
const AttachmentNote = "текст автора, в книге его ещё нет; текст книги важнее"

// MaxAttachmentTitleRunes bounds the title of an attachment, as a draft bounds its title.
const MaxAttachmentTitleRunes = 200

// MaxAttachmentNumber bounds the attachments of one chat.
const MaxAttachmentNumber = 999

// Attachment is a chapter of the author attached to one question; Number counts the attachments of its chat.
type Attachment struct {
	Number     int
	Title      string
	FileName   string
	Format     string
	Paragraphs []string
	Words      int
	CreatedAt  time.Time
}

// NewAttachment makes the attachment of a question from the parsed chapter (CA1.1, CA1.2): its paragraphs with words,
// without the title; the title the file names, else the name of the file; a pasted text without a chapter line has none.
func NewAttachment(document chapterfile.Document, fileName string) Attachment {
	fileName = strings.TrimSpace(fileName)
	title := strings.TrimSpace(document.TitleText())
	if title == "" && fileName != "" {
		title = strings.TrimSpace(strings.TrimSuffix(fileName, path.Ext(fileName)))
	}
	if runes := []rune(title); len(runes) > MaxAttachmentTitleRunes {
		title = strings.TrimSpace(string(runes[:MaxAttachmentTitleRunes]))
	}
	return Attachment{
		Title: title, FileName: fileName, Format: string(document.Format),
		Paragraphs: document.Lines(), Words: document.Words(),
	}
}

// Text is the text of the attachment as a working text of a draft holds it: its paragraphs, a blank line between them.
func (attachment Attachment) Text() string {
	return strings.Join(attachment.Paragraphs, "\n\n")
}

// Header is what the input of the turn says of the attachment (CA1.4): no word of its text.
func (attachment Attachment) Header() *AssignmentAttachment {
	return &AssignmentAttachment{
		Number: attachment.Number, Title: attachment.Title, Paragraphs: len(attachment.Paragraphs),
		Words: attachment.Words, Note: AttachmentNote,
	}
}

// AssignmentAttachment is the header of the attachment in the input of the turn.
type AssignmentAttachment struct {
	Number     int    `json:"number"`
	Title      string `json:"title,omitempty"`
	Paragraphs int    `json:"paragraphs"`
	Words      int    `json:"words"`
	Note       string `json:"note"`
}

// PublicAttachment is what the timeline shows of an attachment at its question (CA4.1).
type PublicAttachment struct {
	Number int
	Title  string
	Words  int
}

// valid accepts a header the backend wrote: a number of the chat, its paragraphs and words. The words of the signature
// are not checked: a later wording must not hide the questions of older turns from the feed.
func (header AssignmentAttachment) valid() bool {
	return header.Number >= 1 && header.Number <= MaxAttachmentNumber && header.Paragraphs >= 1 && header.Words >= 1 &&
		len([]rune(header.Title)) <= MaxAttachmentTitleRunes
}
