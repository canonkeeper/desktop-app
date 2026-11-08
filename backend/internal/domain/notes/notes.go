package notes

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	library "canonkeeper/backend/internal/domain/library"
	"canonkeeper/backend/internal/governance"
)

var (
	ErrValidation  = errors.New("notes: invalid input")
	ErrNotFound    = errors.New("notes: not found")
	ErrConflict    = errors.New("notes: revision or replay conflict")
	ErrUnavailable = errors.New("notes: source temporarily unavailable")
)

const MaxTextLength = 100000
const MaxTitleLength = 200

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidID(value string) bool { return uuidPattern.MatchString(value) }
func ValidateBody(value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > MaxTextLength {
		return ErrValidation
	}
	return nil
}
func ValidateTitle(value string) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > MaxTitleLength {
		return ErrValidation
	}
	return nil
}

type Source struct {
	Kind           string                 `json:"kind"`
	ChatID         string                 `json:"chatId,omitempty"`
	ProviderTurnID string                 `json:"providerTurnId,omitempty"`
	ProviderItemID string                 `json:"providerItemId,omitempty"`
	Locator        *library.ReaderLocator `json:"locator,omitempty"`
	Quote          string                 `json:"quote"`
}

func (source Source) Validate() error {
	if ValidateBody(source.Quote) != nil {
		return ErrValidation
	}
	switch source.Kind {
	case "chat":
		if !ValidID(source.ChatID) || source.Locator != nil || !validNativeID(source.ProviderTurnID) || !validNativeID(source.ProviderItemID) {
			return ErrValidation
		}
	case "chapter":
		if source.ChatID != "" || source.ProviderTurnID != "" || source.ProviderItemID != "" || source.Locator == nil {
			return ErrValidation
		}
		locator := source.Locator
		canonical := governance.CanonicalChapterSourceLocatorV1{ProjectID: locator.ProjectID, BookID: locator.BookID, ChapterID: locator.ChapterID, SourceRef: locator.ChapterID, SourceView: governance.SourceViewPublished, SourceRevision: uint64(locator.Revision), SourceGeneration: governance.Digest{1}, ContentHash: governance.Digest{1}}
		if locator.TargetView != "published" || !locator.ValidFor(canonical) {
			return ErrValidation
		}
	default:
		return ErrValidation
	}
	return nil
}

func validNativeID(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= 512
}

type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Source    *Source   `json:"source"`
}

type ResolvedSource struct {
	Source
	ProjectID  string `json:"projectId,omitempty"`
	SourceView string `json:"sourceView,omitempty"`
}
type Resolution struct {
	State  string          `json:"state"`
	Source *ResolvedSource `json:"source,omitempty"`
}

type Entry struct {
	ID           string    `json:"id"`
	SourceKind   string    `json:"sourceKind"`
	Title        string    `json:"title"`
	Preview      string    `json:"preview"`
	UpdatedAt    time.Time `json:"updatedAt"`
	ProjectTitle string    `json:"projectTitle,omitempty"`
	BookTitle    string    `json:"bookTitle,omitempty"`
	ChapterTitle string    `json:"chapterTitle,omitempty"`
}
type PageMeta struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
	TotalCount int64   `json:"totalCount"`
}
type Page struct {
	Data []Entry  `json:"data"`
	Meta PageMeta `json:"meta"`
}
