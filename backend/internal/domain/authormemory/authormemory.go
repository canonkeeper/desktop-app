// Package authormemory is the memory of the author (specs/2026-11-01-writer-assistant-index.md, UC4):
// what he said or approved about his saga and the text does not hold. It belongs to the project.
package authormemory

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid   = errors.New("author memory: invalid input")
	ErrNotFound  = errors.New("author memory: not found")
	ErrForbidden = errors.New("author memory: writing needs an editor")
	ErrConflict  = errors.New("author memory: revision or replay conflict")
	// ErrNoChapter is a chapter key the saga does not have.
	ErrNoChapter = errors.New("author memory: no such chapter")
)

const (
	// MaxTextRunes bounds a record: as long as a question of the author.
	MaxTextRunes = 8000
	// MaxNameRunes bounds the name a record is about.
	MaxNameRunes = 200
	// MaxQueryRunes bounds a search over the records.
	MaxQueryRunes = 200
)

// Kind is what a record is. The kinds differ in what the agent may do with them (R4.3, R4.4).
type Kind string

const (
	KindPlan       Kind = "plan"
	KindCorrection Kind = "correction"
	KindStyleRule  Kind = "style_rule"
	KindRejected   Kind = "rejected"
)

// Kinds lists every kind in the order they are shown.
var Kinds = []Kind{KindPlan, KindCorrection, KindStyleRule, KindRejected}

// ParseKind reads a kind; false for anything else.
func ParseKind(value string) (Kind, bool) {
	for _, kind := range Kinds {
		if string(kind) == value {
			return kind, true
		}
	}
	return "", false
}

// Label is how a record of the kind is signed wherever a model reads it: a plan is never taken for
// what happened in the book (R4.3), a correction stands over the cards and not over the text (R4.4).
func (kind Kind) Label() string {
	switch kind {
	case KindPlan:
		return "план автора, в книге этого ещё нет"
	case KindCorrection:
		return "поправка автора, важнее карточек, но не текста"
	case KindStyleRule:
		return "правило стиля автора"
	case KindRejected:
		return "отвергнуто автором"
	default:
		return string(kind)
	}
}

// Draft is what a record says.
type Draft struct {
	Kind Kind
	Text string
	// ChapterID is the chapter the record is about, empty without one.
	ChapterID string
	Name      string
	// JobID is the Job of the chat turn that wrote the record, empty when the author wrote it on the page.
	JobID string
}

// CleanText is the text of a record without its outer blanks; false when it cannot be one.
func CleanText(value string) (string, bool) {
	text := strings.TrimSpace(value)
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || text == "" || utf8.RuneCountInString(text) > MaxTextRunes {
		return "", false
	}
	return text, true
}

// CleanName is the name a record is about without its outer blanks, empty without one; false when it
// cannot be one.
func CleanName(value string) (string, bool) {
	name := strings.TrimSpace(value)
	if !utf8.ValidString(name) || strings.ContainsRune(name, 0) || utf8.RuneCountInString(name) > MaxNameRunes {
		return "", false
	}
	return name, true
}

// CleanQuery is a search over the records; false when it cannot be one.
func CleanQuery(value string) (string, bool) {
	query := strings.TrimSpace(value)
	if !utf8.ValidString(query) || strings.ContainsRune(query, 0) || utf8.RuneCountInString(query) > MaxQueryRunes {
		return "", false
	}
	return query, true
}

// Record is one record of the project.
type Record struct {
	ID         string
	ProjectID  string
	Kind       Kind
	Text       string
	ChapterID  string
	Name       string
	Revision   int64
	AuthorID   string
	AuthorName string
	// JobID is the Job of the turn that wrote the record, empty when the author wrote it on the page.
	JobID     string
	CreatedAt time.Time
	UpdatedAt time.Time
	// Origin is where the record came from, as the page says it (specs/2026-11-10-ux8-memory-records.md MR1.5).
	Origin Origin
}

// OriginKind is where a record came from: written on the page, or in a conversation with the chat or a helper.
type OriginKind string

const (
	OriginManual  OriginKind = "manual"
	OriginChat    OriginKind = "chat"
	OriginDraft   OriginKind = "draft"
	OriginDrawing OriginKind = "drawing"
)

// Origin names where a record came from; ChatID and Title name a chat the reader may open, Title also the draft whose helper
// wrote it. Neither the question nor the answer of the turn: they live in the thread of the model.
type Origin struct {
	Kind   OriginKind
	ChatID string
	Title  string
}

// Conversation is what is left of the session of the turn that wrote a record: its kind (research, drawing, draft), whether
// it is alive, whether the reader may see it in the list of chats, and the title of the draft its helper writes.
type Conversation struct {
	SessionKind string
	ChatID      string
	ChatTitle   string
	DraftTitle  string
	Alive       bool
	Visible     bool
}

// OriginOf says where a record came from by the Job it keeps and what is left of its session. A record without a Job was
// written on the page; a session purged after its deletion leaves nothing to tell a chat from a helper, and the record is
// said to come from a chat, as most do.
func OriginOf(jobID string, conversation *Conversation) Origin {
	switch {
	case jobID == "":
		return Origin{Kind: OriginManual}
	case conversation == nil:
		return Origin{Kind: OriginChat}
	case conversation.SessionKind == "draft":
		return Origin{Kind: OriginDraft, Title: conversation.DraftTitle}
	case conversation.SessionKind == "drawing":
		return Origin{Kind: OriginDrawing}
	case conversation.Alive && conversation.Visible:
		return Origin{Kind: OriginChat, ChatID: conversation.ChatID, Title: conversation.ChatTitle}
	default:
		return Origin{Kind: OriginChat}
	}
}

// Signature is what a model reads a record under: its kind, the key of its chapter and the name. It names
// no chapter title: nothing of the chapter reaches the model. A chapter chapterKeys does not know is gone.
func (record Record) Signature(chapterKeys map[string]string) string {
	return record.signed(record.Kind.Label(), chapterKeys)
}

// AboutSignature is the signature of a record that comes with the answer of a saga tool about what it names
// (specs/2026-11-11-chat-corrections.md CR1.3): what the model does with a correction the text of the book contradicts.
func (record Record) AboutSignature(chapterKeys map[string]string) string {
	label := record.Kind.Label()
	switch record.Kind {
	case KindCorrection:
		label = "поправка автора: важнее карточек и пересказов; если текст книги говорит иначе — назови оба варианта"
	case KindPlan:
		label = "план автора: в книге этого ещё нет"
	}
	return record.signed(label, chapterKeys)
}

func (record Record) signed(label string, chapterKeys map[string]string) string {
	var out strings.Builder
	out.WriteString(label)
	if record.ChapterID != "" {
		key, known := chapterKeys[record.ChapterID]
		if !known {
			key = "удалена"
		}
		out.WriteString("; глава " + key)
	}
	if record.Name != "" {
		out.WriteString("; имя: " + record.Name)
	}
	return out.String()
}

// Filter selects records, newest first. Cursor continues after the record it names.
type Filter struct {
	Kinds []Kind
	Query string
	// JobID keeps the records the Job of a chat turn wrote, so the chat can undo them; empty keeps all.
	JobID  string
	Cursor *Cursor
	Limit  int
}

// Cursor names the last record of a page.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Page is the records of a filter; Total counts them all, HasMore says the next page has more. Shared says the saga has
// members besides one: the page then signs a record with its author (MR1.5).
type Page struct {
	Records []Record
	Total   int64
	HasMore bool
	Shared  bool
}
