// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"canonkeeper/backend/internal/jobs"
	service "canonkeeper/backend/internal/services/readingconsent"
	storedb "canonkeeper/backend/internal/store/db"
	"canonkeeper/backend/internal/store/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SampleQueue queues the cards Job of the sample over the published text in a transaction: the owner of the queue of the
// core (store/indexing).
type SampleQueue interface {
	QueueCardsSampleInTx(ctx context.Context, tx pgx.Tx, projectID string, sample jobs.CardsSample) (jobs.Job, error)
}

// WithSamples gives the repository the queue of the sample.
func (repository *Repository) WithSamples(queue SampleQueue) *Repository {
	repository.samples = queue
	return repository
}

// LastSample is the last sample of a saga with its chapters, its host and how its Job ended; nil before any.
func (repository *Repository) LastSample(ctx context.Context, projectID string) (*service.SampleJob, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	var job service.SampleJob
	var raw []byte
	var code pgtype.Text
	err = repository.db.Pool.QueryRow(ctx, `
		SELECT id::text, status, error_json ->> 'code', subject_json -> 'sample' FROM jobs
		WHERE project_id = $1 AND kind = 'chapter_cards' AND subject_json ? 'sample'
		ORDER BY created_at DESC, id DESC LIMIT 1`, project).Scan(&job.JobID, &job.Status, &code, &raw)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sample jobs.CardsSample
	if err := json.Unmarshal(raw, &sample); err != nil {
		return nil, err
	}
	job.ErrorCode, job.Host = code.String, sample.Host
	for _, chapter := range sample.Chapters {
		job.Chapters = append(job.Chapters, service.Chapter{ID: chapter.ChapterID, Hash: chapter.ContentHash})
	}
	return &job, nil
}

// sampleReplay is what a request of the sample keeps under its key: its Job.
type sampleReplay struct {
	JobID string `json:"jobId"`
}

// QueueSample queues the sample of a saga with its chapters and host under the key of the request, in one transaction: the
// same key answers the Job it queued (CN2.3); a live sample of the saga is jobs.ErrConflict.
func (repository *Repository) QueueSample(ctx context.Context, projectID, host string, chapters []service.Chapter, key string, at time.Time) (string, error) {
	if repository.samples == nil {
		return "", errors.New("reading consent: no queue of the sample")
	}
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return "", service.ErrNotFound
	}
	scope := sha256.Sum256([]byte("reading sample " + projectID))
	keyHash := sha256.Sum256([]byte(key))
	var jobID string
	err = pgx.BeginFunc(ctx, repository.db.Pool, func(tx pgx.Tx) error {
		queries := sqlc.New(tx)
		record, err := queries.CreateIdempotencyRecord(ctx, sqlc.CreateIdempotencyRecordParams{
			ScopeHash: hex.EncodeToString(scope[:]), OperationID: "startReadingSample", IdempotencyKeyHash: hex.EncodeToString(keyHash[:]),
			RequestHash: "", StatusCode: 202, ResponseJson: []byte("{}"), CreatedAt: storedb.Timestamptz(at),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			kept, err := queries.GetIdempotencyRecord(ctx, sqlc.GetIdempotencyRecordParams{
				ScopeHash: hex.EncodeToString(scope[:]), OperationID: "startReadingSample", IdempotencyKeyHash: hex.EncodeToString(keyHash[:]),
			})
			if err != nil {
				return err
			}
			var replay sampleReplay
			if json.Unmarshal(kept.ResponseJson, &replay) != nil || replay.JobID == "" {
				return jobs.ErrConflict
			}
			jobID = replay.JobID
			return nil
		}
		if err != nil {
			return err
		}
		sample := jobs.CardsSample{Host: host}
		for _, chapter := range chapters {
			sample.Chapters = append(sample.Chapters, jobs.SampleChapter{ChapterID: chapter.ID, ContentHash: chapter.Hash})
		}
		job, err := repository.samples.QueueCardsSampleInTx(ctx, tx, storedb.UUIDString(project), sample)
		if err != nil {
			return err
		}
		jobID = job.ID
		response, _ := json.Marshal(sampleReplay{JobID: jobID})
		_, err = queries.UpdateIdempotencyRecordResponse(ctx, sqlc.UpdateIdempotencyRecordResponseParams{ID: record.ID, StatusCode: 202, ResponseJson: response})
		return err
	})
	return jobID, err
}

// SampleRequests are the requests of the cards and passes of the sample Job, kept apart from the journal
// (specs/2026-11-14-desk-journal.md JR2.6).
func (repository *Repository) SampleRequests(ctx context.Context, jobID string) ([]service.SampleRequest, error) {
	job, err := storedb.ParseUUID(jobID)
	if err != nil {
		return nil, nil
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT at, duration_ms, provider_host, model, input_tokens, cached_input_tokens, output_tokens, cost_micros, currency, estimated
		FROM reading_sample_requests WHERE job_id = $1 ORDER BY at`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []service.SampleRequest
	for rows.Next() {
		var request service.SampleRequest
		var duration int32
		var input, cached, output, cost pgtype.Int8
		var currency pgtype.Text
		if err := rows.Scan(&request.At, &duration, &request.Host, &request.Model, &input, &cached, &output, &cost, &currency, &request.Estimated); err != nil {
			return nil, err
		}
		request.Duration = time.Duration(duration) * time.Millisecond
		request.TokensKnown = input.Valid && output.Valid
		request.Tokens.Input, request.Tokens.Cached, request.Tokens.Output = input.Int64, cached.Int64, output.Int64
		if cost.Valid {
			request.Cost = &service.SampleCost{Micros: cost.Int64, Currency: currency.String}
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}
