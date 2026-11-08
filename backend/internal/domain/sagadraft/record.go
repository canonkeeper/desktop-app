package sagadraft

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// The refusals of the section «Письмо» (specs/2026-11-01-writer-assistant-index.md, UC7;
// specs/2026-11-02-writer-workshop.md).
var (
	ErrInvalid       = errors.New("chapter draft: invalid")
	ErrNotFound      = errors.New("chapter draft: not found")
	ErrForbidden     = errors.New("chapter draft: forbidden")
	ErrConflict      = errors.New("chapter draft: changed")
	ErrBusy          = errors.New("chapter draft: a Job is working on it")
	ErrAdded         = errors.New("chapter draft: already in the book")
	ErrNoText        = errors.New("chapter draft: nothing to work on")
	ErrNoDestination = errors.New("chapter draft: no such place in the saga")
	// ErrChapterChanged: the chapter of the book an edit stands on changed there since it was opened (A8.7).
	ErrChapterChanged = errors.New("chapter draft: the chapter changed in the book")
	// ErrChapterGone: the chapter of the book an edit stands on is deleted; the draft goes in as a new chapter.
	ErrChapterGone = errors.New("chapter draft: the chapter is no longer in the book")
)

// The limits of a draft, as the table holds them. The working text is as long as a chapter of the book.
const (
	MaxTitleRunes   = 200
	MaxIdeaRunes    = 8000
	MaxChapterRunes = 400000
)

// Kind is the form the author named his draft in (W2.2): scenes, lines and scraps, or a paragraph plan. The helper
// reads it; what should happen is the idea, and the writing agent plans the chapter from both
// (specs/2026-11-06-writing-agent.md, WA1).
type Kind string

const (
	KindSketch Kind = "sketch"
	KindPlan   Kind = "plan"
)

// ParseKind accepts only a known kind.
func ParseKind(value string) (Kind, bool) {
	switch kind := Kind(value); kind {
	case KindSketch, KindPlan:
		return kind, true
	default:
		return "", false
	}
}

// Stage is how far a draft has come: in writing, or in the book.
type Stage string

const (
	StageText  Stage = "text"
	StageAdded Stage = "added"
)

// StageOf is the furthest stage a draft has reached.
func StageOf(added bool) Stage {
	if added {
		return StageAdded
	}
	return StageText
}

// Place is where the chapter goes: after a chapter of a book, or at its start without one.
type Place struct {
	BookID         string
	AfterChapterID string
}

// Job is the last Job of a draft as the page shows it.
type Job struct {
	ID        string
	Mode      string
	Status    string
	Phase     string
	Done      int
	Total     int
	ErrorCode string
}

// Active says the Job may still write into the draft.
func (job *Job) Active() bool {
	return job != nil && (job.Status == "queued" || job.Status == "running" || job.Status == "canceling")
}

// Draft is one chapter in writing. Book and chapter titles name a place while it lives.
type Draft struct {
	ID                string
	ProjectID         string
	Title             string
	Place             Place
	BookTitle         string
	AfterChapterTitle string
	// PlaceGone says the book or the chapter it was to follow is deleted: the place must be chosen again.
	PlaceGone bool
	// Idea is what the author says should happen, as he wrote it (W2.1): the helper plans the chapter from it.
	Idea string
	Kind Kind
	// Text is the one working text the author sees and everything works on (specs/2026-11-06-writing-agent.md, WA10.4):
	// his words, and what the model wrote between the marks ⟦…⟧ until he makes it his own.
	Text string
	// Notes are the notes on the draft and its chapter, by their numbers.
	Notes []Note
	// TypicalWords is the usual length of the author's chapters at the place of the draft, nil when unknown (W2.4).
	TypicalWords *int
	Job          *Job
	// ChatID is the session of the helper of the draft, opened by the first message to it (W1.2).
	ChatID string
	// Edits is the chapter of the book this draft edits (specs/2026-11-05-voice-full-access.md A8.7), nil for a new chapter.
	Edits *Edit
	// AddedChapterID is the chapter of the book it became; AddedChapterTitle is empty once that is deleted.
	AddedChapterID    string
	AddedChapterTitle string
	Revision          int64
	AuthorName        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// The writing agent (specs/2026-11-06-writing-agent.md, WA1, WA4, WA10): the case the author chose (empty — by his
	// words), the latest version of the plan and whether the author approved it, the fingerprint of the idea that
	// version was made for, and the author's choices about the pack.
	CaseChoice   Case
	PlanNumber   int
	PlanApproved bool
	PlanIdeaHash string
	PackChoices  []PackChoice
	// Original is the chapter the author loaded as a file, nil without one (specs/2026-11-07-writing-proofread.md, PR7.1).
	Original *Original
}

// PackChoice is the author's decision about an entry of the pack of the writing agent (specs/2026-11-06-writing-agent.md,
// WA2.5): include a character or leave a name out, or leave out the summary of the next chapter by its place «гл. K».
type PackChoice struct {
	Action string `json:"action"` // include | exclude
	Entry  string `json:"entry"`  // a name as the author wrote it, or «гл. K» of the next chapter
}

// Actions of a choice about the pack.
const (
	ChoiceInclude = "include"
	ChoiceExclude = "exclude"
)

// PlanStale says the idea changed since the latest version of the plan was made for it (WA3.10, WA7.6).
func (draft Draft) PlanStale() bool {
	return draft.PlanNumber > 0 && draft.PlanIdeaHash != IdeaHash(draft.Idea)
}

// Edit is the chapter of the book a draft edits and where the edit stands against it (A8.7).
type Edit struct {
	ChapterID    string
	ChapterTitle string
	BookID       string
	// Revision is the revision of the book text the draft stands on: the one it was opened from or last put there.
	Revision int64
	// PreviousRevision is the revision before the last replacement, 0 when there is nothing to return to.
	PreviousRevision int64
	// BookRevision is the revision the chapter has in the book now; BookWords its words there.
	BookRevision int64
	BookWords    int
	// Gone says the chapter is deleted from the book: the draft goes in as a new chapter.
	Gone bool
}

// Changed says the chapter changed in the book since the draft took its text.
func (edit Edit) Changed() bool { return !edit.Gone && edit.BookRevision != edit.Revision }

// Stage is how far the draft has come.
func (draft Draft) Stage() Stage {
	return StageOf(draft.AddedChapterID != "")
}

// CleanTitle is a title as it is kept; false — it cannot be one. A draft may have none until it is added.
func CleanTitle(title string) (string, bool) {
	cleaned := strings.TrimSpace(title)
	return cleaned, validText(cleaned, MaxTitleRunes)
}

// ValidBody says a text of the author can be kept as it is, up to limit characters: it reaches a Job byte
// for byte.
func ValidBody(text string, limit int) bool {
	return validText(text, limit)
}

func validText(text string, limit int) bool {
	return utf8.ValidString(text) && !strings.ContainsRune(text, 0) && utf8.RuneCountInString(text) <= limit
}

// Change is an edit of a draft: what is nil stays. Place set with an empty book clears the place.
type Change struct {
	Title *string
	Place *Place
	Idea  *string
	Kind  *Kind
	Text  *string
	// CaseChoice is the case of the chapter the author chose; an empty one gives the case back to his words
	// (specs/2026-11-06-writing-agent.md, WA1). KeepVersion keeps the working text in a version before the edit: the
	// author takes a piece out with the menu of a selection (WA7.7).
	CaseChoice  *Case
	KeepVersion bool
}

// Empty says the edit changes nothing.
func (change Change) Empty() bool {
	return change == Change{} || change == Change{KeepVersion: true}
}

// Apply is the draft after the edit.
func (change Change) Apply(draft Draft) Draft {
	if change.Title != nil {
		draft.Title = *change.Title
	}
	if change.Place != nil {
		draft.Place = *change.Place
	}
	if change.Idea != nil {
		draft.Idea = *change.Idea
	}
	if change.Kind != nil {
		draft.Kind = *change.Kind
	}
	if change.Text != nil {
		draft.Text = *change.Text
	}
	return draft
}

// Paragraphs is the text of a chapter as a book holds it: one paragraph per line that is not blank.
func Paragraphs(chapter string) []string {
	return Lines(chapter)
}

// Summary is a draft in the list: nothing of its text.
type Summary struct {
	ID    string
	Title string
	Kind  Kind
	Stage Stage
	// Words are those of the working text (owner 03.11: «название · сколько слов · когда»).
	Words     int
	JobStatus string
	Revision  int64
	// Edits says the draft edits a chapter of the book (A8.7).
	Edits     bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Cursor continues a list after the draft it names.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Page is a page of a list, newest first.
type Page struct {
	Drafts  []Summary
	Total   int64
	HasMore bool
}

// IdeaHash is the fingerprint of an idea: a plan made for another idea is stale (WA3.10).
func IdeaHash(idea string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(idea), " ")))
	return hex.EncodeToString(sum[:])
}

// Original is what the page shows of the chapter loaded as a file: its name, its kind, its words and when it came.
type Original struct {
	FileName string
	Format   string
	Words    int
	LoadedAt time.Time
}
