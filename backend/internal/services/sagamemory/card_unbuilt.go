package sagamemory

import (
	"context"
	"errors"
	"time"

	"canonkeeper/backend/internal/domain/disclosure"
	domain "canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/jobs"
)

// Unbuilt is a chapter an attempt of a cards Job left without a card once the model was called
// (specs/2026-11-17-long-chapters.md LC4.1): the Job, the saga and the chapter by their ids, the parts it read by, the
// attempts of the cards of all its parts, how it ended, the seconds it took and the tokens in and out — no text, no title,
// no key, no place and no words of an error.
type Unbuilt struct {
	JobID, ProjectID, ChapterID string
	Parts, Attempts             int
	Outcome                     string
	Seconds                     int
	InputTokens, OutputTokens   int64
}

// How a chapter left without a card ended (LC4.1): its answer cut short, an answer that did not read, a request refused, a
// stop of the reading or a refusal of the journal of the app, an attempt its own time ended, or another failure.
const (
	UnbuiltCut        = "cut"
	UnbuiltUnreadable = "unreadable"
	UnbuiltRefused    = "refused"
	UnbuiltStopped    = "stopped"
	UnbuiltTimeout    = "timeout"
	UnbuiltFailed     = "failed"
)

// UnbuiltLog hears every chapter a cards Job left without a card; it is the log of the process.
type UnbuiltLog func(unbuilt Unbuilt)

// WithUnbuilt makes the Job tell every chapter it leaves without a card once the model was called (LC4.1).
func (build *BuildCards) WithUnbuilt(log UnbuiltLog) *BuildCards {
	build.unbuiltLog = log
	return build
}

// markUnread marks a chapter the writer left without a card after paid answers of the model «не прочиталась», under the
// policy of the reading and while the run goes (specs/2026-11-13-desk-consent.md CN3.6); true when it is marked.
func (build *BuildCards) markUnread(ctx, runCtx context.Context, projectID string, key CardKey, written WrittenCard) bool {
	if build.policy == nil || written.Unread == "" || runCtx.Err() != nil {
		return false
	}
	return build.policy.MarkUnread(ctx, projectID, key, domain.CurrentCardInstructions.Version, written.Unread) == nil
}

// unbuilt tells a chapter the writer left without a card: not when the run was canceled — a cancel of the Job, a stop of
// the process — nor when the model was not called.
func (build *BuildCards) unbuilt(runCtx context.Context, claim jobs.WorkerClaim, chapterID string, written WrittenCard, err error, took time.Duration) {
	if build.unbuiltLog == nil || err == nil || runCtx.Err() != nil || written.Attempts == 0 {
		return
	}
	build.unbuiltLog(Unbuilt{
		JobID: claim.JobID, ProjectID: claim.ProjectID, ChapterID: chapterID, Parts: max(written.Parts, 1), Attempts: written.Attempts,
		Outcome: unbuiltOutcome(err, written.Unread), Seconds: int(took / time.Second),
		InputTokens: written.Usage.InputTokens, OutputTokens: written.Usage.OutputTokens,
	})
}

// unbuiltOutcome is how a chapter left without a card ended, by the error of the writer and why it did not read.
func unbuiltOutcome(err error, unread string) string {
	_, stopped := domain.StopOf(err)
	switch {
	case stopped || errors.Is(err, disclosure.ErrJournal) || errors.Is(err, errPartsHalted):
		return UnbuiltStopped
	case errors.Is(err, domain.ErrAttemptTimeout):
		return UnbuiltTimeout
	case errors.Is(err, domain.ErrAnswerCut):
		return UnbuiltCut
	case errors.Is(err, domain.ErrRefused):
		return UnbuiltRefused
	case unread == UnreadUnreadable:
		return UnbuiltUnreadable
	default:
		return UnbuiltFailed
	}
}
