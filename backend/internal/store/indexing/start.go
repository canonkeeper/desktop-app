package indexingstore

import (
	"context"
	"encoding/json"
	"errors"

	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/indexing"
	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/projects"
	authstore "canonkeeper/backend/internal/store/auth"
	storedb "canonkeeper/backend/internal/store/db"
	jobstore "canonkeeper/backend/internal/store/jobs"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const idempotencyPendingResponseJSON = `{"status":"pending"}`

type indexingJobReplayEnvelope struct {
	StatusCode int32  `json:"statusCode"`
	JobID      string `json:"jobId"`
}

func (s *Store) StartIndexing(ctx context.Context, input indexing.StartIndexingStoreInput) (jobs.Job, error) {
	if _, err := storedb.ParseUUID(input.CandidateJobID); err != nil {
		return jobs.Job{}, indexing.ErrValidation
	}
	recordID, err := storedb.ParseUUID(input.IdempotencyRecordID)
	if err != nil {
		return jobs.Job{}, indexing.ErrValidation
	}
	projectID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.Job{}, indexing.ErrNotFound
	}
	actorID, err := storedb.ParseUUID(input.ActorUserID)
	if err != nil {
		return jobs.Job{}, indexing.ErrNotFound
	}
	if input.SourceView != governance.SourceViewDraft && input.SourceView != governance.SourceViewPublished ||
		input.ScopeHash == "" || input.OperationID == "" || input.IdempotencyKeyHash == "" ||
		input.RequestHash == "" || input.CreatedAt.IsZero() {
		return jobs.Job{}, indexing.ErrValidation
	}

	var result jobs.Job
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := authstore.LockVerifiedSessionWriteProof(ctx, tx, actorID, input.SessionWriteProof); err != nil {
			return err
		}

		var projectGeneration int64
		if err := tx.QueryRow(ctx, `
			SELECT lifecycle_generation
			FROM projects
			WHERE id = $1 AND deleted_at IS NULL
			FOR SHARE
		`, projectID).Scan(&projectGeneration); errors.Is(err, pgx.ErrNoRows) {
			return indexing.ErrNotFound
		} else if err != nil {
			return err
		}

		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role
			FROM project_memberships
			WHERE project_id = $1 AND user_id = $2 AND removed_at IS NULL
			FOR SHARE
		`, projectID, actorID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
			return indexing.ErrNotFound
		} else if err != nil {
			return err
		}
		if !projects.CanMutateProject(projects.Role(role)) {
			return indexing.ErrForbidden
		}
		queries := sqlc.New(tx)
		record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ID:                 recordID,
			ScopeHash:          input.ScopeHash,
			OperationID:        input.OperationID,
			IdempotencyKeyHash: input.IdempotencyKeyHash,
			RequestHash:        input.RequestHash,
			StatusCode:         int32(input.IdempotencyStatusCode),
			ResponseJson:       []byte(idempotencyPendingResponseJSON),
			CreatedAt:          storedb.Timestamptz(input.CreatedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return replayTextIndexingJob(ctx, queries, projectID, actorID, input, &result)
		}
		if err != nil {
			return err
		}

		lockedView, err := queries.LockProjectSourceView(ctx, sqlc.LockProjectSourceViewParams{
			ProjectID:                  projectID,
			SourceView:                 string(input.SourceView),
			ProjectLifecycleGeneration: projectGeneration,
		})
		if err != nil {
			return err
		}
		if lockedView.CatalogRevision < 1 {
			return indexing.ErrConflict
		}
		view, err := governance.PresentSourceView(input.SourceView)
		if err != nil {
			return indexing.ErrValidation
		}
		projectRef, err := governance.NewViewBoundProjectRef(governance.SourceGenerationVector{
			ProjectID:         input.ProjectID,
			ProjectGeneration: governance.LifecycleGeneration(projectGeneration),
		}, view, governance.AbsentChatArm())
		if err != nil {
			return indexing.ErrValidation
		}

		var present bool
		result, present, err = jobstore.New(s.DB).UpsertCanonTextIndexIntent(ctx, tx, jobs.CanonTextIndexIntentInput{
			JobID:           input.CandidateJobID,
			CreateIfMissing: true,
			Subject: jobs.CanonTextIndexSubject{
				Type:            jobs.KindCanonTextIndex,
				ProjectID:       input.ProjectID,
				SourceView:      input.SourceView,
				CatalogRevision: uint64(lockedView.CatalogRevision),
			},
			CreatorUserID: input.ActorUserID,
			Source: jobs.JobSourceManifest{
				SourceView: view,
				Refs:       []governance.ArtifactSourceRef{projectRef},
			},
			CreatedAt: input.CreatedAt,
		})
		if err != nil {
			return err
		}
		if !present {
			return jobs.ErrConflict
		}

		responseJSON, err := json.Marshal(indexingJobReplayEnvelope{StatusCode: record.StatusCode, JobID: result.ID})
		if err != nil {
			return err
		}
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{
			StatusCode: int32(input.IdempotencyStatusCode), ResponseJson: responseJSON, ID: record.ID,
		})
		return err
	})
	return result, mapIndexingStartError(err)
}

func replayTextIndexingJob(
	ctx context.Context,
	queries *sqlc.Queries,
	projectID, actorID pgtype.UUID,
	input indexing.StartIndexingStoreInput,
	result *jobs.Job,
) error {
	record, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
		ScopeHash: input.ScopeHash, OperationID: input.OperationID, IdempotencyKeyHash: input.IdempotencyKeyHash,
	})
	if err != nil {
		return err
	}
	replayJobID, err := jobstore.JobIDFromIdempotencyRecord(record, input.RequestHash)
	if err != nil {
		return err
	}
	replayID, err := storedb.ParseUUID(replayJobID)
	if err != nil {
		return jobs.ErrConflict
	}
	row, err := queries.GetJob(ctx, sqlc.GetJobParams{JobID: replayID, ProjectID: projectID, ActorUserID: actorID})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrConflict
	}
	if err != nil {
		return err
	}
	*result, err = jobstore.JobFromSQLJob(row)
	if err != nil || result.Kind != jobs.KindCanonTextIndex {
		return jobs.ErrConflict
	}
	subject, ok := result.Subject.(jobs.CanonTextIndexSubject)
	if !ok || subject.ProjectID != input.ProjectID || subject.SourceView != input.SourceView {
		return jobs.ErrConflict
	}
	return nil
}

func mapIndexingStartError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, indexing.ErrNotFound), errors.Is(err, indexing.ErrForbidden),
		errors.Is(err, indexing.ErrValidation), errors.Is(err, indexing.ErrConflict):
		return err
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, jobs.ErrNotFound):
		return indexing.ErrNotFound
	case errors.Is(err, jobs.ErrForbidden):
		return indexing.ErrForbidden
	case errors.Is(err, jobs.ErrValidation):
		return indexing.ErrValidation
	case errors.Is(err, jobs.ErrConflict):
		return indexing.ErrConflict
	default:
		return err
	}
}
