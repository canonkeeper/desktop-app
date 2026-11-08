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
