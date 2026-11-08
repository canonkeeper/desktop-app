package jobworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

func TestJobLifecycleLogContainsOnlyDeterministicSafeFields(t *testing.T) {
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	bookID := "book-1"
	job := jobs.Job{
		ID: "job-1", ProjectID: "project-1", Kind: jobs.KindResearchChatTurn,
		Subject: jobs.ResearchChatTurnSubject{
			Type: jobs.KindResearchChatTurn, ProjectID: "project-1", ChatID: "SECRET_CHAT", TurnID: "SECRET_TURN",
			SourceView: governance.SourceViewDraft, Scope: "book", BookID: &bookID,
		},
		QueueClass: jobs.QueueClassInteractive, RetryCount: 2,
		NextAttemptAt: startedAt.Add(-2500 * time.Millisecond),
		WorkerClaim:   jobs.WorkerClaim{ProjectID: "project-1", JobID: "job-1", ClaimIncarnation: 7},
		Result: jobs.ResearchChatTurnResult{
			Type: jobs.KindResearchChatTurn, ChatID: "SECRET_CHAT", TurnID: "SECRET_TURN", Response: "SECRET_RESPONSE",
		},
	}
	confirmed := job
	confirmed.Status = jobs.StatusFailed
	confirmed.Error = &jobs.Error{Code: HandlerFailedCode, Message: "SECRET_RAW_ERROR"}
	var output bytes.Buffer
	logger := log.New(&output, "", 0)

	logJobStarted(logger, job, startedAt)
	logJobFinished(logger, job, startedAt, startedAt.Add(1234*time.Millisecond), confirmed, nil, nil, nil)

	want := "" +
		`{"event":"job_started","jobId":"job-1","kind":"research_chat_turn","projectId":"project-1","sourceView":"draft","bookId":"book-1","claimIncarnation":7,"queueClass":"interactive","retryCount":2,"queueWaitMs":2500}` + "\n" +
		`{"event":"job_finished","jobId":"job-1","kind":"research_chat_turn","projectId":"project-1","sourceView":"draft","bookId":"book-1","claimIncarnation":7,"durationMs":1234,"terminalStatus":"failed","errorCode":"worker.handler_failed"}` + "\n"
	if got := output.String(); got != want {
		t.Fatalf("lifecycle log = %q, want %q", got, want)
	}
	if strings.Contains(output.String(), "SECRET_") {
		t.Fatalf("lifecycle log contains private job data: %q", output.String())
	}
}

func TestJobAttemptFailureLogUsesOnlySafeClassification(t *testing.T) {
	when := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	job := jobs.Job{ID: "job-1", ProjectID: "project-1", Kind: jobs.KindImport}
	unconfirmed := jobs.Job{Status: jobs.StatusRunning, Error: &jobs.Error{Code: "SECRET_UNCONFIRMED"}}
	var output bytes.Buffer

	logJobFinished(
		log.New(&output, "", 0), job, when, when, unconfirmed,
		fmt.Errorf("SECRET_RAW_ERROR: %w", jobs.ErrConflict),
		nil, nil,
	)

	if got, want := output.String(), `{"event":"job_attempt_failed","jobId":"job-1","kind":"import","projectId":"project-1","claimIncarnation":0,"durationMs":0,"errorCode":"worker.conflict"}`+"\n"; got != want {
		t.Fatalf("failed attempt log = %q, want %q", got, want)
	}
	if strings.Contains(output.String(), "SECRET_") {
		t.Fatalf("failed attempt log contains private error: %q", output.String())
	}
}

func TestJobFinishedLogAllowsOnlySafeTerminalErrorCode(t *testing.T) {
	when := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		code string
		want string
	}{
		{name: "classified runtime", code: "runtime.session_unclosed", want: "runtime.session_unclosed"},
		{name: "goal set", code: "runtime.goal_set_failed", want: "runtime.goal_set_failed"},
		{name: "goal read", code: "runtime.goal_read_failed", want: "runtime.goal_read_failed"},
		{name: "goal cleared", code: "runtime.goal_cleared", want: "runtime.goal_cleared"},
		{name: "import over the chapter limit", code: "import.too_many_chapters", want: "import.too_many_chapters"},
		{name: "import over the text limit", code: "import.too_large", want: "import.too_large"},
		{name: "import encoding", code: "import.unsupported_encoding", want: "import.unsupported_encoding"},
		{name: "import file type", code: "import.unsupported_media_type", want: "import.unsupported_media_type"},
		{name: "import without chapter text", code: "empty_import_content", want: "empty_import_content"},
		{name: "import broken fb2", code: "invalid_fb2_document", want: "invalid_fb2_document"},
		{name: "import broken epub", code: "invalid_epub_document", want: "invalid_epub_document"},
		{name: "import broken docx", code: "invalid_docx_document", want: "invalid_docx_document"},
		{name: "import book order", code: "import_sequence_conflict", want: "import_sequence_conflict"},
		{name: "import stale book order", code: "import_expected_order_conflict", want: "import_expected_order_conflict"},
		{name: "unclassified", code: "SECRET_RAW_CODE", want: HandlerFailedCode},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := jobs.Job{ID: "job-1", ProjectID: "project-1", Kind: jobs.KindImport}
			confirmed := job
			confirmed.Status = jobs.StatusFailed
			confirmed.Error = &jobs.Error{Code: test.code, Message: "SECRET_RAW_DETAIL"}
			var output bytes.Buffer

			logJobFinished(log.New(&output, "", 0), job, when, when, confirmed, nil, nil, nil)

			got := output.String()
			if !strings.Contains(got, `"errorCode":"`+test.want+`"`) || strings.Contains(got, "SECRET_") {
				t.Fatalf("job_finished log = %q", got)
			}
		})
	}
}

func TestWorkerRunOnceWritesOneLifecyclePair(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	var output bytes.Buffer
	worker := NewWorker(store, []Handler{handler}, log.New(&output, "", 0))

	ran, err := worker.RunOnce(t.Context())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"event":"job_started"`) ||
		!strings.Contains(lines[1], `"event":"job_finished"`) ||
		!strings.Contains(lines[1], `"terminalStatus":"succeeded"`) {
		t.Fatalf("lifecycle lines = %q, want started then confirmed succeeded", lines)
	}
}

func TestWorkerRequeuedLifecycleUsesOnlySafeModelClassification(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &resultHandlerForTest{
		kind: jobs.KindImport,
		onHandleResult: func(_ context.Context, job jobs.Job) (HandlerResult, error) {
			store.commitSuccessorForTest(job.ID, 1, jobs.StatusQueued)
			return HandlerResult{
				Status:       jobs.StatusQueued,
				RequeueCause: fmt.Errorf("SECRET_WRAPPER: %w", safeLifecycleError{code: "model.server_overloaded", raw: "SECRET_MODEL_DETAILS"}),
			}, nil
		},
	}
	var output bytes.Buffer
	worker := NewWorker(store, []Handler{handler}, log.New(&output, "", 0))

	ran, err := worker.RunOnce(t.Context())
	if err != nil || !ran {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", ran, err)
	}
	if got := output.String(); !strings.Contains(got, `"event":"job_requeued"`) ||
		!strings.Contains(got, `"errorCode":"model.server_overloaded"`) || strings.Contains(got, "SECRET_") {
		t.Fatalf("requeued lifecycle log = %q", got)
	}
	if got := jobAttemptErrorCode(safeLifecycleError{code: "SECRET_CODE", raw: "SECRET_MODEL_DETAILS"}); got != "worker.internal" {
		t.Fatalf("untrusted safe code = %q, want worker.internal", got)
	}
	for _, code := range []string{
		"model.input_too_large", "runtime.activation_failed", "runtime.turn_start_failed",
		"runtime.turn_stream_failed",
		"runtime.reviewer_observation_failed", "runtime.reviewer_checkpoint_failed",
		"runtime.book_delivery_build_failed", "runtime.book_delivery_variant_select_failed",
		"runtime.book_delivery_variant_invalid", "runtime.book_delivery_launch_failed",
		"runtime.book_predecessor_bind_failed", "runtime.book_predecessor_load_failed",
		"runtime.book_predecessor_load_conflict", "runtime.book_predecessor_load_corrupt",
		"runtime.book_predecessor_load_invalid", "runtime.book_predecessor_payload_invalid",
		"runtime.book_assigned_binding_invalid", "runtime.book_assigned_subject_changed",
		"runtime.book_assigned_source_changed", "runtime.book_assigned_reference_failed",
		"runtime.book_assigned_reference_set_invalid", "runtime.book_assigned_payload_invalid",
		"runtime.book_assigned_footprint_invalid", "runtime.book_assigned_token_window_invalid",
	} {
		if got := jobAttemptErrorCode(safeLifecycleError{code: code, raw: "SECRET_RUNTIME_DETAILS"}); got != code {
			t.Fatalf("safe runtime code = %q, want %q", got, code)
		}
	}
}

type safeLifecycleError struct {
	code string
	raw  string
}

func (failure safeLifecycleError) Error() string         { return failure.raw }
func (failure safeLifecycleError) SafeErrorCode() string { return failure.code }

func TestJobLifecycleLoggerKeepsConcurrentEventsAtomic(t *testing.T) {
	const workers = 32
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			job := jobs.Job{
				ID: fmt.Sprintf("job-%d", index), ProjectID: "project-1", Kind: jobs.KindCanonTextIndex,
				Subject: jobs.CanonTextIndexSubject{
					Type: jobs.KindCanonTextIndex, ProjectID: "project-1", SourceView: governance.SourceViewDraft,
				},
				QueueClass:  jobs.QueueClassUserDemanded,
				WorkerClaim: jobs.WorkerClaim{ProjectID: "project-1", JobID: fmt.Sprintf("job-%d", index)},
			}
			confirmed := job
			confirmed.Status = jobs.StatusSucceeded
			logJobStarted(logger, job, startedAt)
			logJobFinished(logger, job, startedAt, startedAt.Add(time.Millisecond), confirmed, nil, nil, nil)
		}()
	}
	group.Wait()

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != workers*2 {
		t.Fatalf("lifecycle line count = %d, want %d", len(lines), workers*2)
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("concurrent lifecycle line is not complete JSON: %q", line)
		}
	}
}

func TestNewWorkerRequiresLogger(t *testing.T) {
	store := newFakeWorkerStore(jobForWorkerTest("job-1", jobs.KindImport))
	handler := &committedHandlerForTest{kind: jobs.KindImport, store: store}
	defer func() {
		if got := recover(); got != "jobworker: Logger is required" {
			t.Fatalf("NewWorker() panic = %v, want missing Logger", got)
		}
	}()

	NewWorker(store, []Handler{handler}, nil)
}
