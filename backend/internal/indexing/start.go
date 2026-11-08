package indexing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
)

const (
	indexingStartOperationID         = "canon.text.index.start.v1"
	indexingStartIdempotencyStatusOK = 202
	minIdempotencyKeyLength          = 8
	maxIdempotencyKeyLength          = 128
)

func (s *Service) StartIndexing(ctx context.Context, actor Actor, input StartIndexingInput) (jobs.Job, error) {
	input, err := normalizeStartIndexingInput(input)
	if err != nil {
		return jobs.Job{}, err
	}
	actor.UserID = strings.TrimSpace(actor.UserID)
	if actor.UserID == "" {
		return jobs.Job{}, ErrNotFound
	}
	candidateJobID := s.ids.NewIndexingJobID()
	idempotencyRecordID := s.ids.NewIdempotencyRecordID()
	if candidateJobID == "" || idempotencyRecordID == "" {
		return jobs.Job{}, ErrValidation
	}
	job, err := s.store.StartIndexing(ctx, StartIndexingStoreInput{
		CandidateJobID:        candidateJobID,
		IdempotencyRecordID:   idempotencyRecordID,
		ActorUserID:           actor.UserID,
		ProjectID:             input.ProjectID,
		SourceView:            input.SourceView,
		ScopeHash:             indexingStartScopeHash(actor.UserID, input),
		OperationID:           indexingStartOperationID,
		IdempotencyKeyHash:    sha256HexString(input.IdempotencyKey),
		RequestHash:           indexingStartRequestHash(input),
		IdempotencyStatusCode: indexingStartIdempotencyStatusOK,
		CreatedAt:             s.clock.Now(),
		SessionWriteProof:     input.SessionWriteProof,
	})
	if err != nil {
		return jobs.Job{}, mapIndexingJobStartError(err)
	}
	return job, nil
}

func normalizeStartIndexingInput(input StartIndexingInput) (StartIndexingInput, error) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	switch input.SourceView {
	case governance.SourceViewDraft, governance.SourceViewPublished:
	default:
		return StartIndexingInput{}, ErrValidation
	}
	input.RequestMethod = strings.ToUpper(strings.TrimSpace(input.RequestMethod))
	input.RequestPath = strings.TrimSpace(input.RequestPath)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.ProjectID == "" ||
		input.RequestMethod == "" ||
		input.RequestPath == "" ||
		len(input.IdempotencyKey) < minIdempotencyKeyLength ||
		len(input.IdempotencyKey) > maxIdempotencyKeyLength {
		return StartIndexingInput{}, ErrValidation
	}
	return input, nil
}

func indexingStartScopeHash(actorUserID string, input StartIndexingInput) string {
	return sha256HexString(strings.Join([]string{
		actorUserID,
		input.RequestMethod,
		input.RequestPath,
	}, "\x00"))
}

func indexingStartRequestHash(input StartIndexingInput) string {
	return sha256HexString(strings.Join([]string{input.ProjectID, string(input.SourceView)}, "\x00"))
}

func sha256HexString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func mapIndexingJobStartError(err error) error {
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, jobs.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, jobs.ErrValidation):
		return ErrValidation
	case errors.Is(err, jobs.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}
