package jobstore

import (
	"context"
	"encoding/json"
	"errors"

	"canonkeeper/backend/internal/jobs"
	"canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const idempotencyPendingResponseJSON = `{"status":"pending"}`

func createJobWithQueries(ctx context.Context, queries *sqlc.Queries, input jobs.CreateJobStoreInput, ownerID pgtype.UUID) (jobs.Job, error) {
	jobID, err := storedb.ParseUUID(input.ID)
	if err != nil {
		return jobs.Job{}, err
	}
	projectUUID, err := storedb.ParseUUID(input.ProjectID)
	if err != nil {
		return jobs.Job{}, jobs.ErrNotFound
	}
	subjectJSON, err := jobs.EncodeSubject(input.Kind, input.Subject)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	if input.Result != nil && input.Result.Kind() != input.Kind {
		return jobs.Job{}, jobs.ErrValidation
	}
	resultJSON, err := marshalJobResult(input.Result)
	if err != nil {
		return jobs.Job{}, err
	}
	errorJSON, err := marshalJobError(input.Error)
	if err != nil {
		return jobs.Job{}, err
	}
	creatorUserID, err := optionalUUIDValue(input.CreatorUserID)
	if err != nil {
		return jobs.Job{}, jobs.ErrValidation
	}
	row, err := queries.CreateJob(ctx, sqlc.CreateJobParams{
		ID:                    jobID,
		Kind:                  string(input.Kind),
		Status:                string(input.Status),
		Progress:              input.Progress,
		SubjectJson:           subjectJSON,
		CanCancel:             input.CanCancel,
		CreatorUserID:         creatorUserID,
		QueueClass:            string(input.QueueClass),
		RetryCount:            int32(input.RetryCount),
		MaxAttempts:           int32(input.MaxAttempts),
		NextAttemptAt:         storedb.Timestamptz(input.NextAttemptAt),
		ResultJson:            resultJSON,
		ErrorJson:             errorJSON,
		ArtifactSourceOwnerID: ownerID,
		CreatorRequestID:      storedb.CreatorRequestID(ctx),
		CreatedAt:             storedb.Timestamptz(input.CreatedAt),
		UpdatedAt:             storedb.Timestamptz(input.UpdatedAt),
		ProjectID:             projectUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	if isUniqueViolation(err) {
		return jobs.Job{}, jobs.ErrConflict
	}
	if err != nil {
		return jobs.Job{}, err
	}
	return JobFromSQLJob(row)
}

type idempotentJobReplayEnvelope struct {
	StatusCode      int32  `json:"statusCode"`
	JobID           string `json:"jobId"`
	ReplayAuthority []byte `json:"replayAuthority,omitempty"`
}

func JobIDFromIdempotencyRecord(record sqlc.IdempotencyRecord, requestHash string) (string, error) {
	envelope, err := idempotentJobReplayEnvelopeFromRecord(record, requestHash)
	if err != nil {
		return "", err
	}
	return envelope.JobID, nil
}

func idempotentJobReplayEnvelopeFromRecord(
	record sqlc.IdempotencyRecord,
	requestHash string,
) (idempotentJobReplayEnvelope, error) {
	if record.RequestHash != requestHash {
		return idempotentJobReplayEnvelope{}, jobs.ErrConflict
	}
	var envelope idempotentJobReplayEnvelope
	if err := json.Unmarshal(record.ResponseJson, &envelope); err != nil {
		return idempotentJobReplayEnvelope{}, jobs.ErrConflict
	}
	if envelope.StatusCode != record.StatusCode || envelope.JobID == "" {
		return idempotentJobReplayEnvelope{}, jobs.ErrConflict
	}
	return envelope, nil
}
