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
	// Reading is the state of the reading of the books in a build that asks its author (specs/2026-11-13-desk-consent.md
	// CN3.8): the app; the site has none.
	Reading *IndexingReading `json:"reading,omitempty"`
	// Counts are the counts of the reading of the published text whether or not Preparation is there: the port of the
	// reading of a build reads them; they are never answered.
	Counts *IndexingPreparation `json:"-"`
}

// The states of the reading (CN3.8): it waits for the consent — none acts —, goes, is paused, or is done but for the
// chapters that did not read.
const (
	ReadingWaitingConsent = "waiting_consent"
	ReadingGoing          = "reading"
	ReadingPaused         = "paused"
	ReadingDone           = "done"
)

// IndexingReading is the state of the reading of the books of a saga by «Свой ИИ» (CN3.8): the state and why it waits or
// stands, the chapters read and all of them, whether the names and whether the threads wait though the chapters are read,
// the chapters that did not read and why, why the names and the threads did not come together
// (specs/2026-11-15-desk-reading-rest.md RR2.1), what was spent with its stop and step in their unit and whether an estimate
// is in it, and the time left. The lists of the reasons are open for another source of the AI.
type IndexingReading struct {
	State string `json:"state"`
	// Source is who reads the books (specs/2026-11-13-desk-account-ai.md, the table «Страницам B2»): the source of the consent
	// that acts, or of the settings while none does — own or account; nil without a helper.
	Source           *string                `json:"source"`
	Reason           *string                `json:"reason"`
	Pause            *string                `json:"pause"`
	ChaptersRead     int                    `json:"chaptersRead"`
	ChaptersTotal    int                    `json:"chaptersTotal"`
	NamesWait        bool                   `json:"namesWait"`
	ThreadsWait      bool                   `json:"threadsWait"`
	Unread           *IndexingReadingUnread `json:"unread"`
	UnreadRest       *IndexingReadingRest   `json:"unreadRest"`
	Spent            int64                  `json:"spent"`
	Approximate      bool                   `json:"approximate"`
	StopAt           *int64                 `json:"stopAt"`
	Step             *int64                 `json:"step"`
	Unit             string                 `json:"unit"`
	RemainingSeconds *int                   `json:"remainingSeconds"`
	// Limits are the two limits of the tier of the account for the reading over 30 days — the characters of the inputs of the
	// card steps and the work of the reading in tokens, used and the limit — while the reading goes to the helper «Аккаунт
	// CanonKeeper» and the site told them (specs/2026-11-13-desk-account-ai.md AI3.7); absent otherwise.
	Limits *IndexingReadingLimits `json:"limits,omitempty"`
}

// IndexingReadingLimits are the two limits of the tier of the account for the reading (AI3.7).
type IndexingReadingLimits struct {
	Characters IndexingReadingLimit `json:"characters"`
	Work       IndexingReadingLimit `json:"work"`
}

// IndexingReadingLimit is one of them: used over 30 days and the limit.
type IndexingReadingLimit struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// IndexingReadingRest is why the names of the heroes and why the threads of the plot did not come together — cut_answer,
// unreadable, refused, no_answer — or nil for what did (specs/2026-11-15-desk-reading-rest.md RR2.1).
type IndexingReadingRest struct {
	Names   *string `json:"names"`
	Threads *string `json:"threads"`
}

// IndexingReadingUnread are the chapters that did not read and why (CN3.6).
type IndexingReadingUnread struct {
	Chapters int    `json:"chapters"`
	Reason   string `json:"reason"`
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

// PreparationWaitingConsent: the helper is connected and the author has not allowed it to read the books of the saga yet
// (specs/2026-11-13-desk-gateway.md GW4.3).
const PreparationWaitingConsent PreparationWaiting = "consent"

// PreparationWaitingPaused: the consent of the author is given and the reading stands paused
// (specs/2026-11-13-desk-consent.md CN3.8).
const PreparationWaitingPaused PreparationWaiting = "paused"

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
