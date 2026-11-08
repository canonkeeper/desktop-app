package indexing

import (
	"context"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

type textIndexError string

func (err textIndexError) Error() string { return string(err) }

func (textIndexError) NonRetryableCanonTextIndexError() {}

var (
	ErrTextIndexValidation error = textIndexError("indexing: invalid text index input")
	ErrTextIndexConflict   error = textIndexError("indexing: text index conflict")
	ErrTextIndexRetryable        = errors.New("indexing: text index retryable failure")
)

const textIndexChapterParallelism = 4

type textIndexChapterResult struct {
	index int
	err   error
}

type IDGenerator interface {
	NewIndexingJobID() string
	NewIdempotencyRecordID() string
}

type Service struct {
	store Store
	ids   IDGenerator
	clock jobs.Clock
}

func NewService(store Store, ids IDGenerator, clock jobs.Clock) *Service {
	if store == nil {
		panic("indexing: Store is required")
	}
	if ids == nil {
		panic("indexing: IDGenerator is required")
	}
	if clock == nil {
		panic("indexing: Clock is required")
	}
	return &Service{store: store, ids: ids, clock: clock}
}

func (s *Service) RunForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject) error {
	if err := validateTextIndexClaim(claim, subject); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	descriptors, err := s.store.ListCanonTextChapterDescriptorsForClaim(ctx, claim)
	if err != nil {
		return textIndexServiceError(err)
	}
	for _, descriptor := range descriptors {
		if !descriptorMatchesTextIndexSubject(descriptor, subject) {
			return ErrTextIndexConflict
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	results := make(chan textIndexChapterResult, textIndexChapterParallelism)
	start := func(index int) {
		descriptor := descriptors[index]
		go func() {
			results <- textIndexChapterResult{index: index, err: s.runChapter(ctx, claim, descriptor)}
		}()
	}
	next, running := 0, 0
	for running < textIndexChapterParallelism && next < len(descriptors) && ctx.Err() == nil {
		start(next)
		next++
		running++
	}
	errs := make([]error, len(descriptors))
	stopped := false
	for running > 0 {
		result := <-results
		running--
		errs[result.index] = result.err
		if result.err != nil || ctx.Err() != nil {
			stopped = true
		}
		if !stopped && next < len(descriptors) {
			start(next)
			next++
			running++
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return textIndexServiceError(s.store.CompleteCanonTextIndexForClaim(ctx, claim, subject))
}

func (s *Service) runChapter(ctx context.Context, claim jobs.WorkerClaim, descriptor CanonTextChapterDescriptor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := s.store.LoadCanonTextChapterSnapshotForClaim(ctx, claim, descriptor)
	if err != nil {
		return textIndexServiceError(err)
	}
	if snapshot.Claim != claim || snapshot.CanonTextChapterDescriptor != descriptor {
		return ErrTextIndexConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.Unchanged {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	disposition, err := s.store.ReplaceCanonTextChapterForClaim(ctx, ReplaceCanonTextChapterInput{
		Snapshot: snapshot, IndexedAt: s.clock.Now(),
	})
	if err != nil {
		return textIndexServiceError(err)
	}
	if disposition != CanonTextChapterReplaced && disposition != CanonTextChapterUnchanged {
		return ErrTextIndexConflict
	}
	return nil
}

func (s *Service) CancelForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject) error {
	if err := validateTextIndexClaim(claim, subject); err != nil {
		return err
	}
	return textIndexServiceError(s.store.CancelCanonTextIndexForClaim(ctx, claim, subject))
}

func (s *Service) FailForClaim(ctx context.Context, claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject, failure jobs.Error) error {
	if err := validateTextIndexClaim(claim, subject); err != nil {
		return err
	}
	if _, err := jobs.EncodeError(failure); err != nil {
		return ErrTextIndexValidation
	}
	return textIndexServiceError(s.store.FailCanonTextIndexForClaim(ctx, claim, subject, failure))
}

func validateTextIndexClaim(claim jobs.WorkerClaim, subject jobs.CanonTextIndexSubject) error {
	if claim.Validate() != nil || claim.ProjectID != subject.ProjectID {
		return ErrTextIndexValidation
	}
	if _, err := jobs.EncodeSubject(jobs.KindCanonTextIndex, subject); err != nil {
		return ErrTextIndexValidation
	}
	return nil
}

func descriptorMatchesTextIndexSubject(descriptor CanonTextChapterDescriptor, subject jobs.CanonTextIndexSubject) bool {
	return descriptor.ProjectID == subject.ProjectID && descriptor.SourceView == subject.SourceView &&
		descriptor.CatalogRevision == subject.CatalogRevision && descriptor.ProjectLifecycleGeneration > 0 &&
		descriptor.SourceRevision > 0 && descriptor.SourceGeneration != (governance.Digest{}) &&
		descriptor.BookID != "" && descriptor.ChapterID != ""
}

func textIndexServiceError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrTextIndexValidation) || errors.Is(err, ErrTextIndexConflict) {
		return err
	}
	return ErrTextIndexRetryable
}
