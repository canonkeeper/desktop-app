// SPDX-License-Identifier: AGPL-3.0-only

// Package modeljournal writes the journal of the requests of the local gateway of the desktop app (model_requests,
// specs/2026-11-13-desk-gateway.md GW1.7): one row a request, the saga taken from its Job.
package modeljournal

import (
	"context"
	"errors"
	"math"

	domain "canonkeeper/backend/internal/domain/modeljournal"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Journal is the writer of the journal.
type Journal struct {
	db *storedb.DB
}

func New(db *storedb.DB) *Journal {
	if db == nil {
		panic("model journal: the database is required")
	}
	return &Journal{db: db}
}

// Record writes one request.
func (journal *Journal) Record(ctx context.Context, request domain.Request) error {
	return Insert(ctx, journal.db.Pool, request)
}

// Executor runs a statement: the pool, or the transaction a write of the count of a consent shares with its row
// (specs/2026-11-13-desk-consent.md CN3.3).
type Executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Insert writes one request. Its saga is the saga of its Job; a Job that is not a Job of this database, or no Job, leaves
// the request without a saga.
func Insert(ctx context.Context, executor Executor, request domain.Request) error {
	if request.At.IsZero() || request.Purpose == "" || request.Outcome == "" {
		return errors.New("model journal: a request without its time, purpose or outcome")
	}
	var job, id, check pgtype.UUID
	if request.JobID != "" {
		if parsed, err := storedb.ParseUUID(request.JobID); err == nil {
			job = parsed
		}
	}
	if request.ID != "" {
		parsed, err := storedb.ParseUUID(request.ID)
		if err != nil {
			return errors.New("model journal: the number of a request is no uuid")
		}
		id = parsed
	}
	if request.CheckID != "" {
		parsed, err := storedb.ParseUUID(request.CheckID)
		if err != nil {
			return errors.New("model journal: the number of a check is no uuid")
		}
		check = parsed
	}
	var input, cached, output, cost pgtype.Int8
	if tokens := request.Tokens; tokens != nil {
		input = pgtype.Int8{Int64: tokens.Input, Valid: true}
		cached = pgtype.Int8{Int64: tokens.CachedInput, Valid: true}
		output = pgtype.Int8{Int64: tokens.Output, Valid: true}
	}
	var currency, refusal, failure pgtype.Text
	if request.Failure != "" {
		failure = pgtype.Text{String: request.Failure, Valid: true}
	}
	if request.RefusalCode != "" {
		refusal = pgtype.Text{String: request.RefusalCode, Valid: true}
	}
	if request.Cost != nil {
		cost = pgtype.Int8{Int64: max(request.Cost.Micros, 0), Valid: true}
		currency = pgtype.Text{String: request.Cost.Currency, Valid: true}
	}
	duration := min(max(request.Duration.Milliseconds(), 0), math.MaxInt32)
	_, err := executor.Exec(ctx, `
		INSERT INTO model_requests (id, at, project_id, job_id, purpose, profile_name, provider_host, model,
		  input_tokens, cached_input_tokens, output_tokens, request_bytes, outcome, duration_ms, cost_micros, currency, estimated,
		  refusal_code, check_id, failure)
		SELECT coalesce($17, gen_random_uuid()), $1, job.project_id, job.id, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $18, $19
		FROM (SELECT 1) AS one
		LEFT JOIN jobs AS job ON job.id = $2
	`, request.At, job, request.Purpose, request.Profile, request.ProviderHost, request.Model,
		input, cached, output, max(request.RequestBytes, 0), string(request.Outcome), int32(duration), cost, currency, request.Estimated,
		refusal, id, check, failure)
	return err
}
