package jobworker

import (
	"errors"
	"testing"

	"canonkeeper/backend/internal/domain/disclosure"
	"canonkeeper/backend/internal/jobs"
)

// specs/2026-11-14-desk-journal.md JR1.6: a request the journal of the app did not send is repeated while the Job has
// attempts — the attempt says its code — and fails the Job with its code on the last one.
func TestARepeatedFailureEndsTheJobWithItsCodeOnTheLastAttempt(t *testing.T) {
	refused := &disclosure.JournalError{Code: disclosure.CodeUnavailable}
	early := RepeatedFailure(jobs.Job{RetryCount: 0}, refused)
	if !errors.Is(early, ErrRetryableHandler) || jobAttemptErrorCode(early) != disclosure.CodeUnavailable {
		t.Fatalf("an early attempt = %v, code %q", early, jobAttemptErrorCode(early))
	}
	last := RepeatedFailure(jobs.Job{RetryCount: jobs.MaxJobAttempts - 1}, refused)
	if errors.Is(last, ErrRetryableHandler) || handlerFailureErrorCode(last) != disclosure.CodeUnavailable {
		t.Fatalf("the last attempt = %v, code %q", last, handlerFailureErrorCode(last))
	}
	if handlerFailureErrorCode(&disclosure.JournalError{Code: disclosure.CodeUnnamed}) != disclosure.CodeUnnamed {
		t.Fatal("journal.unnamed is no safe code")
	}
}
