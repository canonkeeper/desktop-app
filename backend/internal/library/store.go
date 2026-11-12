package library

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projectaccess"
	"canonkeeper/backend/internal/projects"
)

var (
	ErrNotFound                = errors.New("library: not found")
	ErrForbidden               = errors.New("library: forbidden")
	ErrValidation              = errors.New("library: validation failed")
	ErrConflict                = errors.New("library: conflict")
	ErrImportUploadExpired     = errors.New("library: import upload expired")
	ErrActiveImportQueueExists = errors.New("library: active import queue already exists")
)

type Actor struct {
	UserID string
}

type Page struct {
	Limit  int
	Cursor string
	State  SourceListState
}

type SourceListState string

const (
	SourceListActive  SourceListState = "active"
	SourceListDeleted SourceListState = "deleted"
)

type OrderPlacement struct {
	Present bool
	AfterID *string
}

type IDGenerator interface {
	NewBookID() string
	NewChapterID() string
	NewParagraphID() string
	NewImportJobID() string
	NewIdempotencyRecordID() string
	NewImportStorageKey() string
	NewDeletionTombstoneID() string
}

type Clock interface {
	Now() time.Time
}

type Store interface {
	ImportStore
	ActiveProjectRole(context.Context, string, string) (projects.Role, error)
	ResolveBookAccessForActor(context.Context, string, string, projectaccess.Visibility) (projectaccess.ResolvedResourceAccess, error)
	ResolveChapterAccessForActor(context.Context, string, string, projectaccess.Visibility) (projectaccess.ResolvedResourceAccess, error)
	LoadDeletedBookLifecycle(context.Context, string, string) (projectaccess.DeletedSourceLifecycle, error)
	LoadDeletedChapterLifecycle(context.Context, string, string, string) (projectaccess.DeletedSourceLifecycle, error)
	NextBookOrder(context.Context, string) (int, error)
	CreateBook(context.Context, CreateBookStoreInput) (Book, error)
	ListBooks(context.Context, ListBooksStoreInput) ([]Book, error)
	GetBook(context.Context, string, string) (Book, error)
	UpdateBook(context.Context, UpdateBookStoreInput) (Book, error)
	FenceBookDeletion(context.Context, FenceBookDeletionInput) (deletion.SourceFenceResult, error)
	RestoreBook(context.Context, RestoreBookStoreInput) (Book, error)
	ListChapters(context.Context, ListChaptersStoreInput) ([]Chapter, error)
	GetChapter(context.Context, string, string, string) (ChapterRecord, error)
	ListChapterParagraphs(context.Context, ListChapterParagraphsStoreInput) ([]Paragraph, error)
	FenceChapterDeletion(context.Context, FenceChapterDeletionInput) (deletion.SourceFenceResult, error)
	RestoreChapter(context.Context, RestoreChapterStoreInput) (Chapter, error)
}

type ImportStore interface {
	ActiveProjectRole(context.Context, string, string) (projects.Role, error)
	CreateImportStagingForActor(context.Context, CreateImportStagingForActorInput) (jobs.Job, error)
}

type FinalizeImportChapterInput struct {
	Title      string
	Paragraphs []ParagraphInput
}

type FinalizeImportStoreInput struct {
	JobID                    string
	ProjectID                string
	ActorUserID              string
	StorageKey               string
	ExpectedClaimIncarnation int64
	ExpectedBookOrder        int
	BookTitle                string
	// BookID names the book an update_book import rewrites; a new_book import leaves it empty.
	BookID             string
	Chapters           []FinalizeImportChapterInput
	ImportMode         ImportMode
	InitialPublication InitialPublication
	SourceFileName     string
}

type FinalizedImport struct {
	BookID         string
	ChapterCount   int
	ParagraphCount int
	ImportMode     ImportMode
	SourceFileName string
	// An update_book or update_preview import reports how the chapters of the file met the book
	// (specs/2026-10-27-update-book-from-file.md, UC3, UC4): ChangedChapters have other words.
	UnchangedChapters   int
	ChangedChapters     int
	AddedChapters       int
	RemovedChapters     int
	PunctuationChapters int
	RenamedChapters     int
	// Preview is what an update_preview import found (UC6).
	Preview *jobs.ImportPreview
}

type FinalizeImportTerminalForClaimInput struct {
	Claim  jobs.WorkerClaim
	Status jobs.Status
	Error  *jobs.Error
}

type CreateBookStoreInput struct {
	ID            string
	ProjectID     string
	Title         string
	Subtitle      *string
	CoverColor    *string
	Order         int
	DisplayNumber string
	CreatedAt     time.Time
	AfterBookID   OrderPlacement
}

type ListBooksStoreInput struct {
	ActorUserID string
	ProjectID   string
	State       SourceListState
	Limit       int
	After       *ListPosition
}

// ListPosition is the key of the last row of a page; the next page starts after it. An active list
// goes by (Order, ID) up, a deleted one by (DeletedAt, ID) down.
type ListPosition struct {
	Order     int
	DeletedAt time.Time
	ID        string
}

type UpdateBookStoreInput struct {
	ProjectID   string
	BookID      string
	Title       string
	Subtitle    *string
	CoverColor  *string
	UpdatedAt   time.Time
	AfterBookID OrderPlacement
}

type FenceBookDeletionInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
	TombstoneID string
	DeletedAt   time.Time
}

type RestoreBookStoreInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
}

type CreateChapterWithDraftStoreInput struct {
	ID             string
	ProjectID      string
	BookID         string
	Title          string
	Order          int
	CreatedAt      time.Time
	Paragraphs     []Paragraph
	AfterChapterID OrderPlacement
}

type ListChaptersStoreInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
	State       SourceListState
	Limit       int
	After       *ListPosition
}

type ChapterRecord struct {
	Chapter    Chapter
	ProjectID  string
	Navigation ChapterNavigation
}

type ChapterNavigation struct {
	Position int
	Total    int
	Previous *ChapterNavigationItem
	Next     *ChapterNavigationItem
}

type ChapterNavigationItem struct {
	ID            string
	Title         string
	DisplayNumber string
}

type ListChapterParagraphsStoreInput struct {
	ProjectID string
	BookID    string
	ChapterID string
	Revision  int64
	Variant   ParagraphVariant
}

type SaveChapterDraftStoreInput struct {
	ProjectID        string
	BookID           string
	ChapterID        string
	ExpectedRevision int64
	Title            string
	Revision         int64
	Status           ChapterStatus
	UpdatedAt        time.Time
	Paragraphs       []Paragraph
}

type PublishChapterStoreInput struct {
	ProjectID   string
	BookID      string
	ChapterID   string
	Revision    int64
	PublishedAt time.Time
	Paragraphs  []Paragraph
}

type FenceChapterDeletionInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
	ChapterID   string
	TombstoneID string
	DeletedAt   time.Time
}

type RestoreChapterStoreInput struct {
	ActorUserID string
	ProjectID   string
	BookID      string
	ChapterID   string
}
