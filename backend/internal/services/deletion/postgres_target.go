package deletion

import (
	"context"
	"errors"
	"time"

	deletiondomain "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
)

var ErrPostgresTargetConflict = errors.New("deletion: PostgreSQL target conflict")

type PostgresStepOutcome string

const (
	PostgresStepSucceeded        PostgresStepOutcome = "succeeded"
	PostgresStepAlreadySucceeded PostgresStepOutcome = "already_succeeded"
	PostgresStepProgressed       PostgresStepOutcome = "progressed"
)

type PostgresTargetStore interface {
	RunPostgresDeletionStep(context.Context, deletiondomain.TargetRequest, deletiondomain.PostgresStep) (PostgresStepOutcome, error)
	VerifyPostgresDeletionComplete(context.Context, deletiondomain.TargetRequest, []deletiondomain.PostgresStep) error
	TombstoneSourceDeletedModelJobs(context.Context, deletiondomain.TargetRequest) error
}

type PostgresTarget struct {
	store PostgresTargetStore
}

func NewPostgresTarget(store PostgresTargetStore) (*PostgresTarget, error) {
	if store == nil {
		return nil, deletiondomain.ErrInvalidTargetRequest
	}
	return &PostgresTarget{store: store}, nil
}

func (target *PostgresTarget) Apply(ctx context.Context, request deletiondomain.TargetRequest) (deletiondomain.TargetResult, error) {
	if target == nil || target.store == nil ||
		request.Claim.Target != governance.TargetPostgreSQL ||
		request.Validate(time.Now().UTC()) != nil {
		return deletiondomain.TargetResult{}, deletiondomain.ErrInvalidTargetRequest
	}
	if request.Tombstone.Identity.PermissionLossActorUserID != "" {
		return deletiondomain.TargetResult{
			Disposition: deletiondomain.DispositionNoData, HandlerVersion: governance.HandlerPostgresExactInventoryV1,
			Reason: governance.ReasonNoData,
		}, nil
	}
	fields, err := request.Tombstone.Identity.SelectedVector.Fields()
	if err != nil {
		return deletiondomain.TargetResult{}, deletiondomain.ErrInvalidTargetRequest
	}
	steps, err := deletiondomain.PostgresStepRegistry(fields.Kind, request.Tombstone.Identity.Mode)
	if err != nil {
		return deletiondomain.TargetResult{}, deletiondomain.ErrInvalidTargetRequest
	}
	if err := target.store.TombstoneSourceDeletedModelJobs(ctx, request); err != nil {
		return deletiondomain.TargetResult{}, err
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return deletiondomain.TargetResult{}, err
		}
		outcome, err := target.store.RunPostgresDeletionStep(ctx, request, step)
		if err != nil {
			return deletiondomain.TargetResult{}, err
		}
		switch outcome {
		case PostgresStepSucceeded, PostgresStepAlreadySucceeded:
			continue
		case PostgresStepProgressed:
			return deletiondomain.TargetResult{Disposition: deletiondomain.DispositionRetry, Reason: governance.ReasonStaleSource}, nil
		default:
			return deletiondomain.TargetResult{}, deletiondomain.ErrInvalidTargetRequest
		}
	}
	if err := target.store.VerifyPostgresDeletionComplete(ctx, request, steps); err != nil {
		return deletiondomain.TargetResult{}, err
	}
	return deletiondomain.TargetResult{
		Disposition:    deletiondomain.DispositionPurged,
		HandlerVersion: governance.HandlerPostgresExactInventoryV1,
		Reason:         governance.ReasonPurged,
	}, nil
}
