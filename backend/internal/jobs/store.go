package jobs

import (
	"context"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/projects"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCanceling Status = "canceling"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusExpired   Status = "expired"
)

type ResearchChatPhase string

const (
	ResearchChatPhaseWaiting          ResearchChatPhase = "waiting"
	ResearchChatPhasePreparingContext ResearchChatPhase = "preparing_context"
	ResearchChatPhaseWorking          ResearchChatPhase = "working"
	ResearchChatPhaseSaving           ResearchChatPhase = "saving"
)

// NormalizeResearchChatPhase проверяет соответствие публичной фазы статусу chat job.
// Canonical create, claim и terminal transitions всегда записывают фазу явно.
func NormalizeResearchChatPhase(kind Kind, status Status, phase *ResearchChatPhase) (*ResearchChatPhase, error) {
	if kind != KindResearchChatTurn {
		if phase != nil {
			return nil, ErrConflict
		}
		return nil, nil
	}
	switch status {
	case StatusQueued:
		if phase == nil || *phase != ResearchChatPhaseWaiting {
			return nil, ErrConflict
		}
		return phase, nil
	case StatusRunning:
		if phase != nil && (*phase == ResearchChatPhasePreparingContext ||
			*phase == ResearchChatPhaseWorking || *phase == ResearchChatPhaseSaving) {
			return phase, nil
		}
		return nil, ErrConflict
	case StatusCanceling, StatusSucceeded, StatusFailed, StatusCanceled, StatusExpired:
		if phase != nil {
			return nil, ErrConflict
		}
		return nil, nil
	default:
		return nil, ErrConflict
	}
}

type ImportPhase string

const (
	ImportPhaseQueued  ImportPhase = "queued"
	ImportPhaseParsing ImportPhase = "parsing"
	ImportPhaseSaving  ImportPhase = "saving"
	ImportPhaseSaved   ImportPhase = "saved"
)

// ImportProgress хранит только наблюдаемые факты одной попытки импорта.
// Отсутствующие счётчики не подменяются процентами или предположениями.
type ImportProgress struct {
	Phase            ImportPhase `json:"phase"`
	BytesTransferred int64       `json:"bytesTransferred"`
	BytesTotal       int64       `json:"bytesTotal"`
	QueuedAt         time.Time   `json:"queuedAt"`
	TransferredAt    time.Time   `json:"transferredAt"`
	ParsingStartedAt *time.Time  `json:"parsingStartedAt,omitempty"`
	ParsedChapters   *int        `json:"parsedChapters,omitempty"`
	TotalChapters    *int        `json:"totalChapters,omitempty"`
	ParsedAt         *time.Time  `json:"parsedAt,omitempty"`
	SavingStartedAt  *time.Time  `json:"savingStartedAt,omitempty"`
	SavedChapters    *int        `json:"savedChapters,omitempty"`
	SavedAt          *time.Time  `json:"savedAt,omitempty"`
}

// ValidateImportProgress закрывает допустимые формы progress_json по фазе и статусу Job.
func ValidateImportProgress(kind Kind, status Status, progress *ImportProgress) error {
	if kind != KindImport {
		if progress != nil {
			return ErrConflict
		}
		return nil
	}
	if progress == nil || progress.BytesTotal <= 0 || progress.BytesTransferred != progress.BytesTotal ||
		progress.QueuedAt.IsZero() || progress.TransferredAt.IsZero() || progress.TransferredAt.Before(progress.QueuedAt) {
		return ErrConflict
	}
	if !validImportProgressShape(*progress) || !validImportProgressStatus(status, progress.Phase) {
		return ErrConflict
	}
	return nil
}

func validImportProgressShape(progress ImportProgress) bool {
	noParsing := progress.ParsingStartedAt == nil && progress.ParsedChapters == nil &&
		progress.TotalChapters == nil && progress.ParsedAt == nil && progress.SavingStartedAt == nil &&
		progress.SavedChapters == nil && progress.SavedAt == nil
	switch progress.Phase {
	case ImportPhaseQueued:
		return noParsing
	case ImportPhaseParsing:
		return progress.ParsingStartedAt != nil && !progress.ParsingStartedAt.Before(progress.QueuedAt) &&
			progress.ParsedChapters == nil && progress.TotalChapters == nil && progress.ParsedAt == nil &&
			progress.SavingStartedAt == nil && progress.SavedChapters == nil && progress.SavedAt == nil
	case ImportPhaseSaving:
		return validParsedImportProgress(progress) && progress.SavedChapters != nil && *progress.SavedChapters == 0 &&
			progress.SavedAt == nil
	case ImportPhaseSaved:
		return validParsedImportProgress(progress) && progress.SavedChapters != nil &&
			*progress.SavedChapters == *progress.TotalChapters && progress.SavedAt != nil &&
			!progress.SavedAt.Before(*progress.SavingStartedAt)
	default:
		return false
	}
}

func validParsedImportProgress(progress ImportProgress) bool {
	return progress.ParsingStartedAt != nil && progress.ParsedChapters != nil && progress.TotalChapters != nil &&
		progress.ParsedAt != nil && progress.SavingStartedAt != nil && *progress.TotalChapters > 0 &&
		*progress.ParsedChapters == *progress.TotalChapters && !progress.ParsingStartedAt.Before(progress.QueuedAt) &&
		!progress.ParsedAt.Before(*progress.ParsingStartedAt) && progress.SavingStartedAt.Equal(*progress.ParsedAt)
}

func validImportProgressStatus(status Status, phase ImportPhase) bool {
	switch status {
	case StatusQueued:
		return phase == ImportPhaseQueued
	case StatusRunning, StatusCanceling:
		return phase == ImportPhaseQueued || phase == ImportPhaseParsing || phase == ImportPhaseSaving
	case StatusSucceeded:
		return phase == ImportPhaseSaved
	case StatusFailed, StatusCanceled, StatusExpired:
		return phase == ImportPhaseQueued || phase == ImportPhaseParsing || phase == ImportPhaseSaving
	default:
		return false
	}
}

func IsWorkerKind(kind Kind) bool {
	switch kind {
	case KindImport, KindCanonTextIndex, KindResearchChatTurn, KindChapterCards, KindIllustration, KindSagaLayer, KindChapterDraft, KindAuthorMaterialsImport:
		return true
	default:
		return false
	}
}

type WorkerClaimDecision struct {
	Claim           WorkerClaim
	ExhaustedStatus Status
}

type Actor struct {
	UserID string
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Job struct {
	ID             string
	ProjectID      string
	Kind           Kind
	Status         Status
	Progress       *float64
	Phase          *ResearchChatPhase
	ImportProgress *ImportProgress
	Subject        Subject
	Result         Result
	Error          *Error
	CanCancel      bool
	// QueuePosition is the number of waiting questions claimed before a waiting chat turn
	// (getJob only); nil otherwise.
	QueuePosition *int64
	CreatorUserID string
	// CreatorRequestID — id HTTP-запроса, создавшего задание; пуст у заданий воркеров и старых
	// заданий.
	CreatorRequestID string
	QueueClass       QueueClass
	RetryCount       int
	MaxAttempts      int
	NextAttemptAt    time.Time
	WorkerClaim      WorkerClaim
	ExpiresAt        *time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Page struct {
	Limit  int
	Cursor string
}

type PageMeta struct {
	HasMore    bool
	NextCursor string
}

type JobPage struct {
	Jobs []Job
	Meta PageMeta
}

type Store interface {
	ActiveProjectRole(context.Context, string, string) (projects.Role, error)
	ListProjectJobs(context.Context, ListProjectJobsStoreInput) (ProjectJobCandidates, error)
	ResolveJobAccessForActor(context.Context, ResolveJobAccessForActorStoreInput) (ResolvedJobAccess, error)
	GetJob(context.Context, GetJobStoreInput) (Job, error)
	CancelJobStatus(context.Context, CancelJobStatusStoreInput) (Job, error)
}

type TargetAccessResolver interface {
	CanReadJobTarget(context.Context, Actor, Subject) (bool, error)
}

type CancelResult struct {
	Job
	Opaque bool
}

type TargetCancelInput struct {
	ActorUserID    string
	Job            Job
	TargetReadable bool
	UpdatedAt      time.Time
}

type TargetCancelResolver interface {
	CancelJobTarget(context.Context, TargetCancelInput) (CancelResult, error)
}

type CreateJobStoreInput struct {
	ID            string
	ProjectID     string
	Kind          Kind
	Status        Status
	Progress      float64
	Subject       Subject
	Result        Result
	Error         *Error
	CanCancel     bool
	CreatorUserID string
	QueueClass    QueueClass
	RetryCount    int
	MaxAttempts   int
	NextAttemptAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type IdempotentJobStoreInput struct {
	RecordID           string
	ScopeHash          string
	OperationID        string
	IdempotencyKeyHash string
	RequestHash        string
	StatusCode         int
}

type CreateModelJobInput struct {
	JobID                string                  `json:"-"`
	ProjectID            string                  `json:"-"`
	Subject              Subject                 `json:"-"`
	Origin               ModelOrigin             `json:"-"`
	ActorUserID          string                  `json:"-"`
	SessionProof         auth.SessionWriteProof  `json:"-"`
	InputContractVersion uint32                  `json:"-"`
	Idempotency          IdempotentJobStoreInput `json:"-"`
	CreatedAt            time.Time               `json:"-"`
}

type JobSourceManifest struct {
	SourceView governance.SourceViewArm
	Refs       []governance.ArtifactSourceRef
}

type CanonTextIndexIntentInput struct {
	JobID           string
	CreateIfMissing bool
	Subject         CanonTextIndexSubject
	CreatorUserID   string
	Source          JobSourceManifest
	CreatedAt       time.Time
}

// ChapterCardsIntentInput queues the cards Job of one project text version.
type ChapterCardsIntentInput struct {
	JobID     string
	Subject   ChapterCardsSubject
	Source    JobSourceManifest
	CreatedAt time.Time
}

// SagaLayerIntentInput queues the thread layer Job of one project.
type SagaLayerIntentInput struct {
	JobID     string
	Subject   SagaLayerSubject
	Source    JobSourceManifest
	CreatedAt time.Time
}

type ModelClaimLockPreview struct {
	Claim   WorkerClaim
	Subject Subject
	Binding ModelBinding
}

// ListProjectJobsInput filters by kind and, with ActiveOnly, to the Jobs still in work:
// queued, running or canceling (specs/2026-10-28-fast-job-list-and-postgres.md, UC1).
type ListProjectJobsInput struct {
	Kind       Kind
	ActiveOnly bool
	Limit      int
	Cursor     string
}

type ListProjectJobsStoreInput struct {
	ActorUserID string
	ProjectID   string
	Kind        Kind
	ActiveOnly  bool
	Limit       int
	HasCursor   bool
	CursorAt    time.Time
	CursorID    string
}

// ProjectJobCandidates is one page of Jobs in list order whose read right was checked only for
// the rows of the page (specs/2026-10-28-fast-job-list-and-postgres.md, UC2): the readable Jobs,
// how many candidates the page held and the last candidate, after which the next page starts.
type ProjectJobCandidates struct {
	Readable      []Job
	Count         int
	LastCreatedAt time.Time
	LastID        string
}

type JobAccessVisibility string

const JobAccessVisibilityActive JobAccessVisibility = "active"

type ResolveJobAccessForActorStoreInput struct {
	ActorUserID string
	JobID       string
}

type ResolvedJobAccess struct {
	ProjectID   string
	JobID       string
	CurrentRole projects.Role
	Visibility  JobAccessVisibility
}

type GetJobStoreInput struct {
	ActorUserID string
	ProjectID   string
	JobID       string
}

type RequeueWorkerClaimStoreInput struct {
	Claim              WorkerClaim
	Kind               Kind
	ExpectedRetryCount int
}

type CancelJobStatusStoreInput struct {
	ActorUserID string
	ProjectID   string
	JobID       string
	From        Status
	To          Status
	Progress    float64
	Result      Result
	Error       *Error
	UpdatedAt   time.Time
}
