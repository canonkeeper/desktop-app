package jobs

import (
	"net/http"

	"canonkeeper/backend/internal/httpapi"
)

type jobResponse struct {
	ID        string             `json:"id"`
	Kind      Kind               `json:"kind"`
	Status    PublicStatus       `json:"status"`
	Progress  *float64           `json:"progress"`
	Phase     *ResearchChatPhase `json:"phase"`
	Subject   Subject            `json:"subject"`
	Result    Result             `json:"result"`
	Error     any                `json:"error"`
	CanCancel bool               `json:"canCancel"`
	// QueuePosition is set only for a waiting chat turn read by getJob (quota spec R10.3).
	QueuePosition *int64           `json:"queuePosition"`
	ExpiresAt     *string          `json:"expiresAt"`
	CreatedAt     string           `json:"createdAt"`
	UpdatedAt     string           `json:"updatedAt"`
	Links         jobLinksResponse `json:"links"`
}

// PublicJobResponse — каноническая HTTP-проекция уже авторизованного задания.
type PublicJobResponse = jobResponse

type jobLinksResponse struct {
	Self     string  `json:"self"`
	Cancel   *string `json:"cancel,omitempty"`
	Timeline *string `json:"timeline"`
}

type jobListResponse struct {
	Data []jobResponse    `json:"data"`
	Meta pageMetaResponse `json:"meta"`
}

type pageMetaResponse struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

func ToPublicStatus(status Status) (PublicStatus, error) {
	switch status {
	case StatusQueued:
		return PublicStatusQueued, nil
	case StatusRunning:
		return PublicStatusRunning, nil
	case StatusCanceling:
		return PublicStatusCanceling, nil
	case StatusSucceeded:
		return PublicStatusSucceeded, nil
	case StatusFailed, StatusExpired:
		return PublicStatusFailed, nil
	case StatusCanceled:
		return PublicStatusCanceled, nil
	default:
		return "", ErrValidation
	}
}

// jobResponseFromJob проецирует задание; supportCode становится requestId его ошибки — кодом,
// который писатель передаёт в поддержку.
func jobResponseFromJob(job Job, supportCode string) (jobResponse, error) {
	return jobResponseForViewer(job, supportCode, "")
}

// jobResponseForViewer projects a Job for one member: only its creator reads the places «было → стало» of a
// book update preview (specs/2026-10-27-update-book-from-file.md, R6.3).
func jobResponseForViewer(job Job, supportCode, viewerUserID string) (jobResponse, error) {
	job = normalizeJob(job)
	status, err := ToPublicStatus(job.Status)
	if err != nil || job.Subject == nil || job.Subject.Kind() != job.Kind || job.Subject.Project() != job.ProjectID {
		return jobResponse{}, ErrConflict
	}
	phase, err := NormalizeResearchChatPhase(job.Kind, job.Status, job.Phase)
	if err != nil || job.Kind == KindResearchChatTurn && job.Progress != nil {
		return jobResponse{}, ErrConflict
	}
	var visibleResult Result
	if isTerminalStatus(job.Status) {
		visibleResult = job.Result
	}
	result, err := publicJobResult(job.Kind, visibleResult, job.CreatorUserID != "" && job.CreatorUserID == viewerUserID)
	if err != nil {
		return jobResponse{}, err
	}
	jobPath := "/api/v1/jobs/" + job.ID
	var expiresAt *string
	if job.ExpiresAt != nil {
		formatted := formatJobTime(*job.ExpiresAt)
		expiresAt = &formatted
	}
	var cancel *string
	if job.CanCancel {
		cancelPath := jobPath + "/cancel"
		cancel = &cancelPath
	}
	var jobError any
	if job.Error != nil {
		if err := validateError(*job.Error); err != nil || supportCode == "" {
			return jobResponse{}, ErrConflict
		}
		jobError = jobProblemFromError(*job.Error, supportCode)
	}
	return jobResponse{
		ID:            job.ID,
		Kind:          job.Kind,
		Status:        status,
		Progress:      job.Progress,
		Phase:         phase,
		Subject:       publicJobSubject(job.Subject),
		Result:        result,
		Error:         jobError,
		CanCancel:     job.CanCancel,
		QueuePosition: queuePosition(job),
		ExpiresAt:     expiresAt,
		CreatedAt:     formatJobTime(job.CreatedAt),
		UpdatedAt:     formatJobTime(job.UpdatedAt),
		Links: jobLinksResponse{
			Self: jobPath, Cancel: cancel,
			Timeline: httpapi.AgentTimelineLink(job.ProjectID, job.ID),
		},
	}, nil
}

// PublicJobResponseFromJob формирует ответ для запуска или повтора уже авторизованного сохранённого задания.
// requestID — id запроса, который запускает задание: он становится кодом поддержки, только если
// задание не хранит id создавшего его запроса.
func PublicJobResponseFromJob(job Job, requestID string) (PublicJobResponse, error) {
	if job.CreatorRequestID != "" {
		requestID = job.CreatorRequestID
	}
	return jobResponseForViewer(job, requestID, job.CreatorUserID)
}

// jobSupportCode — код поддержки задания при каждом опросе: id создавшего запроса, а у заданий
// воркеров и заданий, созданных до его хранения, — id самого задания. По нему находятся строки
// журнала запроса и job_started/job_finished/job_diagnostic задания.
func jobSupportCode(job Job) string {
	if job.CreatorRequestID != "" {
		return job.CreatorRequestID
	}
	return job.ID
}

// publicJobSubject leaves the author's text of a chapter_draft Job out of the job API.
func publicJobSubject(subject Subject) Subject {
	if draft, ok := subject.(ChapterDraftSubject); ok {
		return draft.Public()
	}
	return subject
}

func publicJobResult(kind Kind, result Result, creator bool) (Result, error) {
	if result == nil {
		return nil, nil
	}
	if result.Kind() != kind {
		return nil, ErrConflict
	}
	if err := validateResult(kind, result); err != nil {
		return nil, ErrConflict
	}
	switch value := result.(type) {
	case ImportJobResult:
		if !creator {
			return value.WithoutPlaces(), nil
		}
		return value, nil
	case CanonTextIndexResult, ChapterCardsResult, SagaLayerResult, ResearchChatTurnResult, AuthorMaterialsImportResult, IllustrationResult:
		return value, nil
	case ChapterDraftResult:
		return value.Public(), nil
	default:
		return nil, ErrConflict
	}
}

func jobProblemFromError(jobError Error, requestID string) httpapi.Problem {
	return httpapi.Problem{
		Type:      "about:blank",
		Title:     "Job failed",
		Status:    http.StatusInternalServerError,
		Detail:    "job failed",
		Code:      jobError.Code,
		RequestID: requestID,
	}
}

func queuePosition(job Job) *int64 {
	if job.Kind != KindResearchChatTurn || job.Status != StatusQueued || job.QueuePosition == nil || *job.QueuePosition < 0 {
		return nil
	}
	position := *job.QueuePosition
	return &position
}
