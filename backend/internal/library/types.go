package library

import (
	"strconv"
	"strings"
	"time"
)

type BookStatus string

const (
	BookStatusDraft BookStatus = "draft"
	BookStatusReady BookStatus = "ready"
	BookStatusError BookStatus = "error"
)

type ChapterStatus string

const (
	ChapterStatusDraft         ChapterStatus = "draft"
	ChapterStatusPublished     ChapterStatus = "published"
	ChapterStatusPublishing    ChapterStatus = "publishing"
	ChapterStatusPublishFailed ChapterStatus = "publish_failed"
	ChapterStatusStale         ChapterStatus = "stale"
	ChapterStatusConflict      ChapterStatus = "conflict"
)

type ParagraphVariant string

const (
	ParagraphVariantDraft     ParagraphVariant = "draft"
	ParagraphVariantPublished ParagraphVariant = "published"
)

type ParagraphKind string

const (
	ParagraphKindParagraph  ParagraphKind = "paragraph"
	ParagraphKindHeading    ParagraphKind = "heading"
	ParagraphKindQuote      ParagraphKind = "quote"
	ParagraphKindListItem   ParagraphKind = "list_item"
	ParagraphKindSceneBreak ParagraphKind = "scene_break"
)

type Book struct {
	ID         string
	ProjectID  string
	Title      string
	Subtitle   *string
	CoverColor *string
	CoverURL   *string
	Order      int
	// Place is the number of «Книга N»: the rank of a living book among the living books of the saga in the order of the
	// shelf, 1…n (specs/2026-11-11-ux7-reorder.md RK1.6); Order keeps the place a deleted book comes back to.
	Place         int
	DisplayNumber string
	Status        BookStatus
	Revision      int64
	ChapterCount  int
	WordCount     int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
	RestoreUntil  *time.Time
	// Opening is how the book begins, the first paragraphs of its first live chapter, on the listing of the shelf only
	// (specs/2026-11-11-ux9-shelf-chat.md SC1.3): a file that begins so is asked about as this book.
	Opening string
	// Update is the last update of the book from a file while the book can be returned to the version before it.
	Update *BookUpdate
}

// BookUpdate is the last update of a book from a file for the 7 days of its snapshot (specs/2026-11-12-ux10-book-revert.md
// BR1.3): when it was, how many chapters it changed, added and removed, until when «Вернуть прежнюю версию» returns the
// book, and its Job, whose result tells «Что изменилось» chapter by chapter.
type BookUpdate struct {
	At          time.Time
	RevertUntil time.Time
	JobID       string
	Changed     int
	Added       int
	Removed     int
}

type Chapter struct {
	ID                string
	BookID            string
	Title             string
	Order             int
	Status            ChapterStatus
	Revision          int64
	PublishedRevision *int64
	PublishedAt       *time.Time
	PublishError      *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletedAt         *time.Time
	RestoreUntil      *time.Time
}

// ChapterDisplayNumber prefers an exact source ordinal preserved in the chapter title.
func ChapterDisplayNumber(order int, title string) string {
	fallback := strconv.Itoa(order)
	remainder, ok := strings.CutPrefix(title, "Глава ")
	if !ok {
		return fallback
	}
	number, suffix, ok := strings.Cut(remainder, ".")
	if !ok || number == "" || (suffix != "" && !strings.HasPrefix(suffix, " ")) {
		return fallback
	}
	for _, digit := range number {
		if digit < '0' || digit > '9' {
			return fallback
		}
	}
	return number
}

type ChapterSummary struct {
	Chapter
	WordCount int
	HasDraft  bool
}

type Paragraph struct {
	ID        string
	ChapterID string
	Revision  int64
	Variant   ParagraphVariant
	Kind      ParagraphKind
	Order     int
	Markdown  string
	PlainText string
	WordCount int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type BookInput struct {
	Title         string
	Subtitle      *string
	CoverColor    *string
	DisplayNumber string
	AfterBookID   OrderPlacement
}

// AddChapterInput is one chapter added to a live book: after AfterChapterID, or at its start when empty.
type AddChapterInput struct {
	ProjectID      string
	BookID         string
	AfterChapterID string
	Title          string
	// Paragraphs are in the Markdown subset of a paragraph of the book (specs/2026-11-08-italic-bold-in-text.md TF1.2).
	Paragraphs []string
	At         time.Time
}

// EditedChapter is a chapter of the book with its text at one revision, as «Письмо» edits it
// (specs/2026-11-05-voice-full-access.md A8.7). PreviousChapterID is the chapter before it in its book. Paragraphs are
// those with words in the Markdown subset of the book, with their italic and bold.
type EditedChapter struct {
	BookID            string
	ChapterID         string
	Title             string
	Revision          int64
	PreviousChapterID string
	Paragraphs        []string
}

// ReplaceChapterTextInput replaces the text of a chapter of the book that stands at ExpectedRevision; Paragraphs are in
// the Markdown subset of the book.
type ReplaceChapterTextInput struct {
	ProjectID        string
	BookID           string
	ChapterID        string
	ExpectedRevision int64
	Title            string
	Paragraphs       []string
}

type ParagraphInput struct {
	ID       string
	Kind     ParagraphKind
	Order    int
	Markdown string
	Text     *string
}
