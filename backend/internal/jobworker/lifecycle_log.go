package jobworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/observability"
)

const (
	maxDiagnosticErrorBytes = 500
	maxDiagnosticErrorTypes = 8
)

type jobStartedEvent struct {
	Event            string          `json:"event"`
	JobID            string          `json:"jobId"`
	RequestID        string          `json:"requestId,omitempty"`
	Kind             jobs.Kind       `json:"kind"`
	ProjectID        string          `json:"projectId"`
	SourceView       string          `json:"sourceView,omitempty"`
	BookID           string          `json:"bookId,omitempty"`
	ClaimIncarnation int64           `json:"claimIncarnation"`
	QueueClass       jobs.QueueClass `json:"queueClass"`
	RetryCount       int             `json:"retryCount"`
	QueueWaitMS      int64           `json:"queueWaitMs"`
}

type jobFinishedEvent struct {
	Event            string      `json:"event"`
	JobID            string      `json:"jobId"`
	RequestID        string      `json:"requestId,omitempty"`
	Kind             jobs.Kind   `json:"kind"`
	ProjectID        string      `json:"projectId"`
	SourceView       string      `json:"sourceView,omitempty"`
	BookID           string      `json:"bookId,omitempty"`
	ClaimIncarnation int64       `json:"claimIncarnation"`
	DurationMS       int64       `json:"durationMs"`
	TerminalStatus   jobs.Status `json:"terminalStatus,omitempty"`
	ErrorCode        string      `json:"errorCode,omitempty"`
}

// jobDiagnosticEvent — причина отказа runtime.*, model.* или worker.panic: текст цепочки ошибки
// (обрезанный) и типы её звеньев, а для паники — стек. Вопрос, текст книги и ответ модели в
// ошибки этих классов не входят.
type jobDiagnosticEvent struct {
	Event      string    `json:"event"`
	JobID      string    `json:"jobId"`
	RequestID  string    `json:"requestId,omitempty"`
	Kind       jobs.Kind `json:"kind"`
	ProjectID  string    `json:"projectId"`
	ErrorCode  string    `json:"errorCode"`
	Error      string    `json:"error,omitempty"`
	ErrorTypes string    `json:"errorTypes,omitempty"`
	Stack      string    `json:"stack,omitempty"`
}

// workerLoopErrorEvent — ошибка цикла воркера вне задания или до её загрузки (захват, чтение,
// права роли, недоступная база): безопасный класс, причина и число ошибок подряд, без текста.
type workerLoopErrorEvent struct {
	Event             string                       `json:"event"`
	Kinds             []jobs.Kind                  `json:"kinds"`
	ErrorClass        governance.RequestErrorClass `json:"errorClass"`
	ErrorCause        governance.RequestErrorCause `json:"errorCause"`
	ErrorCode         string                       `json:"errorCode"`
	ConsecutiveErrors int                          `json:"consecutiveErrors"`
	Panic             string                       `json:"panic,omitempty"`
	Stack             string                       `json:"stack,omitempty"`
}

func logWorkerLoopError(logger *log.Logger, kinds []jobs.Kind, err error, consecutiveErrors int) {
	event := workerLoopErrorEvent{
		Event: "worker_loop_error", Kinds: kinds, ErrorCode: jobAttemptErrorCode(err),
		ConsecutiveErrors: consecutiveErrors,
	}
	event.ErrorClass, event.ErrorCause = observability.ClassifyError(err)
	var panicked *observability.PanicError
	if errors.As(err, &panicked) {
		event.Panic, event.Stack = panicked.Value, panicked.Stack
	}
	printJobEvent(logger, event)
}

func logJobStarted(logger *log.Logger, job jobs.Job, startedAt time.Time) {
	sourceView, bookID := jobLogScope(job.Subject)
	queueWait := startedAt.Sub(job.NextAttemptAt)
	if job.NextAttemptAt.IsZero() || queueWait < 0 {
		queueWait = 0
	}
	printJobEvent(logger, jobStartedEvent{
		Event:            "job_started",
		JobID:            job.ID,
		RequestID:        job.CreatorRequestID,
		Kind:             job.Kind,
		ProjectID:        job.ProjectID,
		SourceView:       sourceView,
		BookID:           bookID,
		ClaimIncarnation: job.WorkerClaim.ClaimIncarnation,
		QueueClass:       job.QueueClass,
		RetryCount:       job.RetryCount,
		QueueWaitMS:      queueWait.Milliseconds(),
	})
}

// logJobFinished пишет итог попытки; failure — ошибка обработчика, которая привела к отказу, для
// строки job_diagnostic.
func logJobFinished(
	logger *log.Logger,
	job jobs.Job,
	startedAt, finishedAt time.Time,
	confirmed jobs.Job,
	runErr, requeueCause, failure error,
) {
	sourceView, bookID := jobLogScope(job.Subject)
	event := jobFinishedEvent{
		JobID:            job.ID,
		RequestID:        job.CreatorRequestID,
		Kind:             job.Kind,
		ProjectID:        job.ProjectID,
		SourceView:       sourceView,
		BookID:           bookID,
		ClaimIncarnation: job.WorkerClaim.ClaimIncarnation,
		DurationMS:       finishedAt.Sub(startedAt).Milliseconds(),
	}
	switch {
	case isWorkerTerminalStatus(confirmed.Status):
		event.Event = "job_finished"
		event.TerminalStatus = confirmed.Status
		if confirmed.Error != nil {
			event.ErrorCode = safeTerminalJobErrorCode(confirmed.Error.Code)
		}
	case confirmed.Status == jobs.StatusQueued:
		event.Event = "job_requeued"
		if requeueCause != nil {
			event.ErrorCode = jobAttemptErrorCode(requeueCause)
		}
	case runErr != nil:
		event.Event = "job_attempt_failed"
		event.ErrorCode = jobAttemptErrorCode(runErr)
	default:
		return
	}
	printJobEvent(logger, event)
	if event.TerminalStatus == jobs.StatusFailed && isDiagnosedJobErrorCode(event.ErrorCode) {
		logJobDiagnostic(logger, job, event.ErrorCode, failure)
	}
}

func isDiagnosedJobErrorCode(code string) bool {
	return code == PanicCode || strings.HasPrefix(code, "runtime.") || strings.HasPrefix(code, "model.")
}

func logJobDiagnostic(logger *log.Logger, job jobs.Job, code string, failure error) {
	event := jobDiagnosticEvent{
		Event: "job_diagnostic", JobID: job.ID, RequestID: job.CreatorRequestID,
		Kind: job.Kind, ProjectID: job.ProjectID, ErrorCode: code,
	}
	var panicked *observability.PanicError
	switch {
	case errors.As(failure, &panicked):
		event.Error, event.Stack = panicked.Value, panicked.Stack
	case failure != nil:
		event.Error = observability.Truncate(failure.Error(), maxDiagnosticErrorBytes)
		event.ErrorTypes = errorTypes(failure)
	}
	printJobEvent(logger, event)
}

// errorTypes перечисляет типы звеньев цепочки ошибки: «*fmt.wrapError > codex.runStageError > …».
func errorTypes(err error) string {
	var types []string
	var walk func(error)
	walk = func(err error) {
		if err == nil || len(types) == maxDiagnosticErrorTypes {
			return
		}
		types = append(types, fmt.Sprintf("%T", err))
		switch wrapped := err.(type) {
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap())
		case interface{ Unwrap() []error }:
			for _, next := range wrapped.Unwrap() {
				walk(next)
			}
		}
	}
	walk(err)
	return strings.Join(types, " > ")
}

func jobAttemptErrorCode(err error) string {
	switch {
	case errors.Is(err, errWorkerShutdown):
		return ShutdownCode
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	}
	if code, ok := safeClassifiedErrorCode(err); ok {
		return code
	}
	var panicked *observability.PanicError
	switch {
	case errors.As(err, &panicked):
		return PanicCode
	case errors.Is(err, jobs.ErrConflict):
		return "worker.conflict"
	case errors.Is(err, ErrTerminalProtocolInvariant):
		return "worker.terminal_protocol"
	}
	return "worker.internal"
}

func handlerFailureErrorCode(err error) string {
	var panicked *observability.PanicError
	if errors.As(err, &panicked) {
		return PanicCode
	}
	if code, ok := safeClassifiedErrorCode(err); ok {
		return code
	}
	return HandlerFailedCode
}

func safeClassifiedErrorCode(err error) (string, bool) {
	var classified interface{ SafeErrorCode() string }
	if !errors.As(err, &classified) {
		return "", false
	}
	code := classified.SafeErrorCode()
	return code, isSafeJobErrorCode(code)
}

func safeTerminalJobErrorCode(code string) string {
	if isSafeJobErrorCode(code) {
		return code
	}
	return HandlerFailedCode
}

func isSafeJobErrorCode(code string) bool {
	switch code {
	case HandlerFailedCode,
		PanicCode,
		jobs.RetryExhaustedCode,
		"cards.incomplete",
		"cards.portion",
		"cards.consent",
		"quota.project_books",
		"quota.project_chapters",
		"quota.project_characters",
		"quota.account_books",
		"quota.account_chapters",
		"quota.account_characters",
		"import.too_many_chapters",
		"import.too_large",
		"import.unsupported_encoding",
		"import.unsupported_media_type",
		"empty_import_content",
		"invalid_fb2_document",
		"invalid_epub_document",
		"invalid_docx_document",
		"import_sequence_conflict",
		"import_expected_order_conflict",
		"model.turn_failed",
		"model.turn_interrupted",
		"model.input_too_large",
		"model.context_window_exceeded",
		"model.session_budget_exceeded",
		"model.usage_limit_exceeded",
		"model.server_overloaded",
		"model.cyber_policy",
		"model.http_connection_failed",
		"model.response_stream_connection_failed",
		"model.internal_server_error",
		"model.unauthorized",
		"model.bad_request",
		"model.thread_rollback_failed",
		"model.sandbox_error",
		"model.response_stream_disconnected",
		"model.response_too_many_failed_attempts",
		"model.active_turn_not_steerable",
		"model.other",
		"runtime.transport_start_failed",
		"runtime.client_start_failed",
		"runtime.notifications_failed",
		"runtime.goal_set_failed",
		"runtime.goal_read_failed",
		"runtime.goal_cleared",
		"runtime.turn_list_failed",
		"runtime.turn_bind_failed",
		"runtime.activation_failed",
		"runtime.project_create_failed",
		"runtime.run_request_invalid",
		"runtime.thread_list_failed",
		"runtime.thread_start_failed",
		"runtime.thread_resume_failed",
		"runtime.thread_read_failed",
		"runtime.root_correlation_conflict",
		"runtime.root_bind_failed",
		"runtime.assignment_input_invalid",
		"runtime.assignment_subject_load_failed",
		"runtime.assignment_subject_decode_failed",
		"runtime.assignment_snapshot_load_failed",
		"runtime.assignment_manifest_load_failed",
		"runtime.assignment_manifest_changed",
		"runtime.assignment_summaries_load_failed",
		"runtime.assignment_active_books_load_failed",
		"runtime.assignment_binding_load_failed",
		"runtime.assignment_build_failed",
		"runtime.indexing_request_invalid",
		"runtime.indexing_input_build_failed",
		"runtime.native_run_failed",
		"runtime.run_state_failed",
		"runtime.turn_start_failed",
		"runtime.turn_stream_failed",
		"runtime.reviewer_observation_failed",
		"runtime.reviewer_checkpoint_failed",
		"runtime.session_unclosed",
		"runtime.book_delivery_build_failed",
		"runtime.book_delivery_variant_select_failed",
		"runtime.book_delivery_variant_invalid",
		"runtime.book_delivery_launch_failed",
		"runtime.book_predecessor_bind_failed",
		"runtime.book_predecessor_load_failed",
		"runtime.book_predecessor_load_conflict",
		"runtime.book_predecessor_load_corrupt",
		"runtime.book_predecessor_load_invalid",
		"runtime.book_predecessor_payload_invalid",
		"runtime.book_assigned_binding_invalid",
		"runtime.book_assigned_subject_changed",
		"runtime.book_assigned_source_changed",
		"runtime.book_assigned_reference_failed",
		"runtime.book_assigned_reference_set_invalid",
		"runtime.book_assigned_payload_invalid",
		"runtime.book_assigned_footprint_invalid",
		"runtime.book_assigned_token_window_invalid":
		return true
	default:
		return false
	}
}

func printJobEvent(logger *log.Logger, event any) {
	// События содержат только скалярные JSON-поля, поэтому Marshal не может завершиться ошибкой.
	line, _ := json.Marshal(event)
	logger.Print(string(line))
}

func jobLogScope(subject jobs.Subject) (sourceView, bookID string) {
	switch subject := subject.(type) {
	case jobs.CanonTextIndexSubject:
		return string(subject.SourceView), ""
	case jobs.ChapterCardsSubject:
		return string(subject.SourceView), ""
	case jobs.SagaLayerSubject:
		return string(subject.SourceView), ""
	case jobs.ChapterDraftSubject:
		return "", ""
	case jobs.ResearchChatTurnSubject:
		if subject.BookID != nil {
			bookID = *subject.BookID
		}
		return string(subject.SourceView), bookID
	default:
		return "", ""
	}
}
