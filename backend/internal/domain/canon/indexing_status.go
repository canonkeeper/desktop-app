package canon

import (
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

type IndexingState string

type IndexingStepKind string

type IndexingStepStatus string

const (
	IndexingStateReady      IndexingState = "ready"
	IndexingStateProcessing IndexingState = "processing"
	IndexingStatePartial    IndexingState = "partial"
)

// A book of a text version is ready for the chat after import, the text index of its
// version and the cards of its chapters (R5.3).
const (
	IndexingStepImport    IndexingStepKind = "import"
	IndexingStepTextIndex IndexingStepKind = "text_index"
	IndexingStepCards     IndexingStepKind = "cards"
	IndexingStepReady     IndexingStepKind = "ready"
)

const (
	IndexingStepPending       IndexingStepStatus = "pending"
	IndexingStepQueued        IndexingStepStatus = "queued"
	IndexingStepRunning       IndexingStepStatus = "running"
	IndexingStepCanceling     IndexingStepStatus = "canceling"
	IndexingStepRetrying      IndexingStepStatus = "retrying"
	IndexingStepSucceeded     IndexingStepStatus = "succeeded"
	IndexingStepPartial       IndexingStepStatus = "partial"
	IndexingStepFailed        IndexingStepStatus = "failed"
	IndexingStepCanceled      IndexingStepStatus = "canceled"
	IndexingStepNotApplicable IndexingStepStatus = "not_applicable"
)

type IndexingStatusResponse struct {
	Published IndexingViewEnvelope  `json:"published"`
	Draft     *IndexingViewEnvelope `json:"draft,omitempty"`
	Steps     []IndexingStep        `json:"steps"`
	// Preparation is there while the saga is read: its published text is in work, or a new book of an open wave is still
	// to be imported (specs/2026-11-09-first-session.md FS5.1, specs/2026-11-10-ux3-saga-state.md SS1.1).
	Preparation *IndexingPreparation `json:"preparation,omitempty"`
	// SagaReady is there for seven days after the saga became ready (FS4.1).
	SagaReady *IndexingSagaReady `json:"sagaReady,omitempty"`
}

// IndexingPreparation is how far the reading of the saga is (SS1.1): the chapters of the published text with their
// retelling and all of them, the books read through and all of them, the first book not read yet — none while the file of
// a new book is still parsed — each book's count, and whether the search already finds a chapter. Of an open wave it also
// says the estimate of the chapters left, when there is a pace to draw it from, and whether the reader will be written to
// when the saga is ready (FS5.2, FS5.6).
type IndexingPreparation struct {
	RemainingSeconds *int                      `json:"remainingSeconds"`
	LetterPromised   bool                      `json:"letterPromised"`
	ChaptersRead     int                       `json:"chaptersRead"`
	ChaptersTotal    int                       `json:"chaptersTotal"`
	BooksRead        int                       `json:"booksRead"`
	BooksTotal       int                       `json:"booksTotal"`
	CurrentBookID    *string                   `json:"currentBookId"`
	Askable          bool                      `json:"askable"`
	Books            []IndexingPreparationBook `json:"books"`
	// Waiting is what the reading waits for besides time (specs/2026-11-12-desk-window.md WN3.1): nobody reads the books
	// until it comes. None while the helper reads — the site never waits.
	Waiting PreparationWaiting `json:"waiting,omitempty"`
}

// PreparationWaiting is what a reading of the saga waits for.
type PreparationWaiting string

// PreparationWaitingHelper: the helper of the build is not connected (the desktop until the author connects one).
const PreparationWaitingHelper PreparationWaiting = "helper"

// IndexingPreparationBook is the reading of one book with chapters, in the order of the saga.
type IndexingPreparationBook struct {
	BookID        string `json:"bookId"`
	ChaptersRead  int    `json:"chaptersRead"`
	ChaptersTotal int    `json:"chaptersTotal"`
}

// IndexingSagaReady is the saga that became ready at At, with what is read of it now (FS4.1).
type IndexingSagaReady struct {
	At       time.Time `json:"at"`
	Books    int       `json:"books"`
	Chapters int       `json:"chapters"`
}

// IndexingStatusScope ограничивает checklist одной текущей версией книги.
// Нулевое значение сохраняет общую проектную сводку.
type IndexingStatusScope struct {
	BookID     string
	SourceView governance.SourceView
}

type IndexingStep struct {
	Kind            IndexingStepKind       `json:"kind"`
	Status          IndexingStepStatus     `json:"status"`
	Progress        *float64               `json:"progress"`
	Counters        *IndexingStepCounters  `json:"counters,omitempty"`
	ImportPhase     *jobs.ImportPhase      `json:"importPhase,omitempty"`
	StartedAt       *time.Time             `json:"startedAt,omitempty"`
	CompletedAt     *time.Time             `json:"completedAt,omitempty"`
	BookID          *string                `json:"bookId,omitempty"`
	SourceView      *governance.SourceView `json:"sourceView,omitempty"`
	JobID           *string                `json:"jobId,omitempty"`
	JobCreatedAt    *time.Time             `json:"jobCreatedAt,omitempty"`
	JobStartedAt    *time.Time             `json:"jobStartedAt,omitempty"`
	JobCompletedAt  *time.Time             `json:"jobCompletedAt,omitempty"`
	LastHeartbeatAt *time.Time             `json:"lastHeartbeatAt,omitempty"`
	CanCancel       bool                   `json:"canCancel,omitempty"`
	CanRetry        bool                   `json:"canRetry,omitempty"`
	Error           *IndexingStepProblem   `json:"error,omitempty"`
}

type IndexingStepCounters struct {
	BytesTransferred *int64 `json:"bytesTransferred,omitempty"`
	BytesTotal       *int64 `json:"bytesTotal,omitempty"`
	ParsedChapters   *int   `json:"parsedChapters,omitempty"`
	SavedChapters    *int   `json:"savedChapters,omitempty"`
	IndexedChapters  *int   `json:"indexedChapters,omitempty"`
	TotalChapters    *int   `json:"totalChapters,omitempty"`
}

type IndexingStepProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	RequestID string `json:"requestId"`
}

type IndexingViewEnvelope struct {
	Exists  bool                 `json:"exists"`
	Context *IndexingViewContext `json:"context"`
}

type IndexingViewContext struct {
	State               IndexingState                `json:"state"`
	CurrentBook         *IndexingCurrentBook         `json:"currentBook"`
	RetryableFailedBook *IndexingRetryableFailedBook `json:"retryableFailedBook"`
}

type IndexingCurrentBook struct {
	BookID   string `json:"bookId"`
	Position int    `json:"position"`
	Total    int    `json:"total"`
}

type IndexingRetryableFailedBook struct {
	BookID string `json:"bookId"`
}
