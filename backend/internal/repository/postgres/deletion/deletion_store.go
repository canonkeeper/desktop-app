package deletion

import (
	"context"
	"errors"
	"time"

	deletion "canonkeeper/backend/internal/domain/deletion"
	"canonkeeper/backend/internal/governance"
	deletionservice "canonkeeper/backend/internal/services/deletion"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
)

// SourceDeletedModelJobTombstoner — узкий порт владельца атомарного
// source-deleted tombstone для model Job; его закрывает store/jobs.
type SourceDeletedModelJobTombstoner interface {
	TombstoneSourceDeletedModelJobInTx(ctx context.Context, tx pgx.Tx, projectIDValue, jobIDValue string) error
}

type Store struct {
	*storedb.DB
	queries            *sqlc.Queries
	modelJobTombstoner SourceDeletedModelJobTombstoner
}

type Option func(*Store)

func WithSourceDeletedModelJobTombstoner(tombstoner SourceDeletedModelJobTombstoner) Option {
	return func(store *Store) {
		store.modelJobTombstoner = tombstoner
	}
}

func New(db *storedb.DB, options ...Option) *Store {
	store := &Store{DB: db}
	if db != nil && db.Pool != nil {
		store.queries = sqlc.New(db.Pool)
	}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

func (store *Store) LoadDeletionTargetRequestForClaim(
	ctx context.Context,
	claim deletion.TargetClaim,
) (deletion.TargetRequest, error) {
	if store == nil || store.queries == nil {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	projectID, tombstoneID, err := deletionClaimUUIDs(claim)
	if err != nil || claim.Validate(time.Now().UTC()) != nil {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	row, err := store.queries.LoadDeletionTargetRequestForClaim(ctx, sqlc.LoadDeletionTargetRequestForClaimParams{
		ProjectID:        projectID,
		TombstoneID:      tombstoneID,
		Target:           string(claim.Target),
		ClaimIncarnation: int64(claim.ClaimIncarnation),
		LeaseOwnerDigest: claim.LeaseOwnerDigest[:],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return deletion.TargetRequest{}, deletion.ErrLostTargetClaim
	}
	if err != nil {
		return deletion.TargetRequest{}, err
	}
	persistedOwner, err := ParseDeletionDigest(row.LeaseOwnerDigest)
	if err != nil || !row.LeaseExpiresAt.Valid || row.Attempt <= 0 ||
		row.ProjectID.String() != claim.ProjectID || row.TombstoneID.String() != claim.TombstoneID ||
		governance.DeletionTarget(row.Target) != claim.Target || row.ClaimIncarnation != int64(claim.ClaimIncarnation) ||
		persistedOwner != claim.LeaseOwnerDigest ||
		!row.LeaseExpiresAt.Time.Equal(claim.ObservedLeaseExpiry) {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	tombstone, err := deletionTombstoneFromFields(deletionTombstoneFieldsFromLoad(row))
	if err != nil {
		return deletion.TargetRequest{}, err
	}
	request := deletion.TargetRequest{
		Claim:     claim,
		Tombstone: tombstone,
		Attempt:   uint32(row.Attempt),
	}
	if row.TargetState != string(deletion.TargetProcessing) || request.Validate(time.Now().UTC()) != nil {
		return deletion.TargetRequest{}, deletion.ErrInvalidTargetRequest
	}
	return request, nil
}

func (store *Store) HeartbeatDeletionTargetClaim(
	ctx context.Context,
	claim deletion.TargetClaim,
) (time.Time, error) {
	if store == nil || store.queries == nil {
		return time.Time{}, deletion.ErrInvalidTargetRequest
	}
	projectID, tombstoneID, err := deletionClaimUUIDs(claim)
	if err != nil {
		return time.Time{}, deletion.ErrInvalidTargetRequest
	}
	expiresAt, err := store.queries.HeartbeatDeletionTargetClaim(ctx, sqlc.HeartbeatDeletionTargetClaimParams{
		ProjectID:        projectID,
		TombstoneID:      tombstoneID,
		Target:           string(claim.Target),
		ClaimIncarnation: int64(claim.ClaimIncarnation),
		LeaseOwnerDigest: claim.LeaseOwnerDigest[:],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, deletion.ErrLostTargetClaim
	}
	if err != nil {
		return time.Time{}, err
	}
	if !expiresAt.Valid || !expiresAt.Time.After(time.Now().UTC()) {
		return time.Time{}, deletion.ErrLostTargetClaim
	}
	return expiresAt.Time.UTC(), nil
}

func (store *Store) FailDeletionTargetForClaim(
	ctx context.Context,
	claim deletion.TargetClaim,
	failure deletion.TargetFailure,
) error {
	if store == nil || store.queries == nil || failure.Validate() != nil {
		return deletion.ErrInvalidTargetRequest
	}
	projectID, tombstoneID, err := deletionClaimUUIDs(claim)
	if err != nil {
		return deletion.ErrInvalidTargetRequest
	}
	_, err = store.queries.FailDeletionTargetForClaim(ctx, sqlc.FailDeletionTargetForClaimParams{
		ReasonCode:             string(failure.Reason),
		RetryAfterMilliseconds: failure.RetryAfter.Milliseconds(),
		ProjectID:              projectID,
		TombstoneID:            tombstoneID,
		Target:                 string(claim.Target),
		ClaimIncarnation:       int64(claim.ClaimIncarnation),
		LeaseOwnerDigest:       claim.LeaseOwnerDigest[:],
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return deletion.ErrLostTargetClaim
	}
	return err
}

func (store *Store) CompleteDeletionTargetForClaim(
	ctx context.Context,
	claim deletion.TargetClaim,
	result deletion.TargetResult,
) (deletion.TargetReceipt, error) {
	if store == nil || store.DB == nil || store.queries == nil {
		return deletion.TargetReceipt{}, deletion.ErrInvalidTargetRequest
	}
	projectID, tombstoneID, err := deletionClaimUUIDs(claim)
	if err != nil {
		return deletion.TargetReceipt{}, deletion.ErrInvalidTargetRequest
	}

	var receipt deletion.TargetReceipt
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		queries := store.queries.WithTx(tx)
		task, err := queries.GetDeletionTargetTaskForCompletion(ctx, deletionTaskForCompletionParams(claim, projectID, tombstoneID))
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrLostTargetClaim
		}
		if err != nil {
			return err
		}
		tombstoneRow, err := queries.GetDeletionTombstoneForTarget(ctx, sqlc.GetDeletionTombstoneForTargetParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrLostTargetClaim
		}
		if err != nil {
			return err
		}
		tombstone, err := deletionTombstoneFromFields(deletionTombstoneFieldsFromTarget(tombstoneRow))
		if err != nil {
			return err
		}
		request, err := deletionRequestForPersistedTask(claim, tombstone, task)
		if err != nil {
			return err
		}
		if request.ValidateResult(result) != nil {
			return deletion.ErrInvalidTargetRequest
		}

		if task.State == string(deletion.TargetSucceeded) || task.State == string(deletion.TargetNoData) {
			persistedReceipt, persistedResult, err := persistedDeletionTargetReceipt(request, task)
			if err != nil {
				return err
			}
			if persistedResult != result {
				return deletion.ErrInvalidTargetRequest
			}
			receipt = persistedReceipt
			return nil
		}
		if task.State != string(deletion.TargetProcessing) || tombstone.Status != deletion.TombstoneProcessing {
			return deletion.ErrLostTargetClaim
		}
		if tombstone.Identity.Mode == governance.PurgeModeSourceHardPurge &&
			claim.Target == governance.TargetPostgreSQL {
			if _, err := tx.Exec(ctx, `
				SELECT pg_advisory_xact_lock(hashtextextended($1::text, 77341001))
			`, projectID.String()); err != nil {
				return err
			}
		}
		completedAt, err := queries.CurrentDeletionTimestampSecond(ctx)
		if err != nil {
			return err
		}
		if !completedAt.Valid {
			return deletion.ErrInvalidTargetRequest
		}
		receipt, err = deletion.NewTargetReceipt(request, result, completedAt.Time.UTC())
		if err != nil {
			return err
		}
		terminalState, err := deletionTerminalState(result.Disposition)
		if err != nil {
			return err
		}
		_, err = queries.CompleteDeletionTargetTask(ctx, sqlc.CompleteDeletionTargetTaskParams{
			TerminalState:    terminalState,
			HandlerVersion:   string(result.HandlerVersion),
			ReceiptDigest:    receipt.Digest[:],
			CompletedAt:      completedAt,
			ProjectID:        projectID,
			TombstoneID:      tombstoneID,
			Target:           string(claim.Target),
			ClaimIncarnation: int64(claim.ClaimIncarnation),
			LeaseOwnerDigest: claim.LeaseOwnerDigest[:],
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrLostTargetClaim
		}
		if err != nil {
			return err
		}

		incomplete, err := queries.CountIncompleteDeletionTargets(ctx, sqlc.CountIncompleteDeletionTargetsParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		})
		if err != nil || incomplete != 0 {
			return err
		}
		receiptRows, err := queries.ListDeletionTargetReceiptDigests(ctx, sqlc.ListDeletionTargetReceiptDigestsParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		})
		if err != nil {
			return err
		}
		tombstoneDigest, vectorDigest, aggregateDigest, err := deletionReceiptSetDigests(tombstone, receiptRows)
		if err != nil {
			return err
		}
		if _, err := queries.MarkDeletionTombstonePurged(ctx, sqlc.MarkDeletionTombstonePurgedParams{
			CompletedAt: completedAt,
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return deletion.ErrLostTargetClaim
			}
			return err
		}
		if err := fenceChatsEmptiedBySagaDeletionInTx(ctx, tx, tombstone, projectID); err != nil {
			return err
		}
		if err := queries.PinContainedDerivedProofRetentionForSourceHardPurge(ctx, sqlc.PinContainedDerivedProofRetentionForSourceHardPurgeParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		}); err != nil {
			return err
		}
		persistedAggregate, err := queries.InsertDeletionCompletionReceipt(ctx, sqlc.InsertDeletionCompletionReceiptParams{
			VectorDigest:         vectorDigest[:],
			TargetReceiptsDigest: aggregateDigest[:],
			CompletedAt:          completedAt,
			TombstoneID:          tombstoneID,
			ProjectID:            projectID,
			TombstoneDigest:      tombstoneDigest[:],
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrInvalidTargetRequest
		}
		if err != nil {
			return err
		}
		storedAggregate, err := ParseDeletionDigest(persistedAggregate)
		if err != nil || storedAggregate != aggregateDigest {
			return deletion.ErrInvalidTargetRequest
		}
		return nil
	})
	if err != nil {
		return deletion.TargetReceipt{}, err
	}
	return receipt, nil
}

// TombstoneSourceDeletedModelJobs приводит bound source-deleted model Jobs
// к tombstone до step loop. Replay идемпотентен: tombstoned Jobs больше не
// выбираются exact-vector предикатом.
func (store *Store) TombstoneSourceDeletedModelJobs(
	ctx context.Context,
	request deletion.TargetRequest,
) error {
	if request.Tombstone.Identity.PermissionLossActorUserID != "" {
		return nil
	}
	selection, err := postgresDeletionSelectionForRequest(request)
	if err != nil {
		return deletion.ErrInvalidTargetRequest
	}
	if selection.mode == governance.PurgeModeChatEpoch {
		return nil
	}
	if store == nil || store.DB == nil || store.queries == nil || store.modelJobTombstoner == nil {
		return deletion.ErrInvalidTargetRequest
	}
	return store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := lockFilesystemTargetRequest(
			ctx,
			store.queries.WithTx(tx),
			request,
			mustUUID(selection.projectID),
			mustUUID(selection.tombstoneID),
		); err != nil {
			return err
		}
		return tombstoneSourceDeletedModelJobsInTx(ctx, tx, selection, store.modelJobTombstoner)
	})
}

func (store *Store) FindCommittedDeletionTargetReceipt(
	ctx context.Context,
	claim deletion.TargetClaim,
) (deletion.TargetReceipt, error) {
	if store == nil || store.DB == nil || store.queries == nil {
		return deletion.TargetReceipt{}, deletion.ErrInvalidTargetRequest
	}
	projectID, tombstoneID, err := deletionClaimUUIDs(claim)
	if err != nil {
		return deletion.TargetReceipt{}, deletion.ErrInvalidTargetRequest
	}

	var found deletion.TargetReceipt
	err = store.WithTx(ctx, func(tx pgx.Tx) error {
		queries := store.queries.WithTx(tx)
		task, err := queries.GetDeletionTargetTaskForCompletion(ctx, deletionTaskForCompletionParams(claim, projectID, tombstoneID))
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrDeletionReceiptUnavailable
		}
		if err != nil {
			return err
		}
		if task.State != string(deletion.TargetSucceeded) && task.State != string(deletion.TargetNoData) {
			return deletion.ErrDeletionReceiptUnavailable
		}
		tombstoneRow, err := queries.GetDeletionTombstoneForTarget(ctx, sqlc.GetDeletionTombstoneForTargetParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrDeletionReceiptUnavailable
		}
		if err != nil {
			return err
		}
		tombstone, err := deletionTombstoneFromFields(deletionTombstoneFieldsFromTarget(tombstoneRow))
		if err != nil || tombstone.Status != deletion.TombstonePurged {
			return deletion.ErrDeletionReceiptUnavailable
		}
		request, err := deletionRequestForPersistedTask(claim, tombstone, task)
		if err != nil {
			return err
		}
		found, _, err = persistedDeletionTargetReceipt(request, task)
		if err != nil {
			return err
		}
		completion, err := queries.GetRetainedDeletionCompletionReceipt(ctx, sqlc.GetRetainedDeletionCompletionReceiptParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return deletion.ErrDeletionReceiptUnavailable
		}
		if err != nil {
			return err
		}
		receiptRows, err := queries.ListDeletionTargetReceiptDigests(ctx, sqlc.ListDeletionTargetReceiptDigestsParams{
			TombstoneID: tombstoneID,
			ProjectID:   projectID,
		})
		if err != nil {
			return err
		}
		_, vectorDigest, aggregateDigest, err := deletionReceiptSetDigests(tombstone, receiptRows)
		if err != nil {
			return err
		}
		storedVector, vectorErr := ParseDeletionDigest(completion.VectorDigest)
		storedAggregate, aggregateErr := ParseDeletionDigest(completion.TargetReceiptsDigest)
		if vectorErr != nil || aggregateErr != nil || !completion.CompletedAt.Valid ||
			storedVector != vectorDigest || storedAggregate != aggregateDigest {
			return deletion.ErrInvalidTargetRequest
		}
		return nil
	})
	if err != nil {
		return deletion.TargetReceipt{}, err
	}
	return found, nil
}

var _ deletionservice.DeletionTargetStore = (*Store)(nil)
var _ deletionservice.FilesystemTargetStore = (*Store)(nil)
var _ deletionservice.PostgresTargetStore = (*Store)(nil)
var _ deletionservice.NativeRootLookup = (*Store)(nil)
