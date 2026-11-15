// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"math"

	"canonkeeper/backend/internal/domain/aisettings"
	"canonkeeper/backend/internal/domain/modeljournal"
	domain "canonkeeper/backend/internal/domain/readingconsent"
	journalrepo "canonkeeper/backend/internal/repository/postgres/modeljournal"
	service "canonkeeper/backend/internal/services/readingconsent"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReadingJob is the Job of a request of the reading: its saga, its kind, whether it runs, and whether it is the sample with
// the host its window named (specs/2026-11-13-desk-consent.md CN3.3).
func (repository *Repository) ReadingJob(ctx context.Context, jobID string) (service.ReadingJob, bool, error) {
	id, err := storedb.ParseUUID(jobID)
	if err != nil {
		return service.ReadingJob{}, false, nil
	}
	var job service.ReadingJob
	var project pgtype.UUID
	var status string
	var host pgtype.Text
	err = repository.db.Pool.QueryRow(ctx, `
		SELECT project_id, kind, status, subject_json ? 'sample', subject_json -> 'sample' ->> 'host'
		FROM jobs WHERE id = $1 AND kind IN ('chapter_cards', 'saga_layer')`, id).Scan(&project, &job.Kind, &status, &job.Sample, &host)
	if noRows(err) {
		return service.ReadingJob{}, false, nil
	}
	if err != nil {
		return service.ReadingJob{}, false, err
	}
	job.ProjectID, job.Live, job.SampleHost = storedb.UUIDString(project), status == "running", host.String
	return job, true, nil
}

// Settle writes the row of the journal of a request of the reading and what it adds to the count of its consent in one
// transaction (CN3.3), under the lock of the consent: only to the count that began at its start, by the rule of the domain —
// the cost counts in the unit of the consent, and a consent without a sum follows the unit of the settings (CN3.1, CN3.4).
func (repository *Repository) Settle(ctx context.Context, entry modeljournal.Request, count *service.Count) error {
	return pgx.BeginFunc(ctx, repository.db.Pool, func(tx pgx.Tx) error {
		if err := journalrepo.Insert(ctx, tx, entry); err != nil {
			return err
		}
		if count == nil {
			return nil
		}
		project, err := storedb.ParseUUID(count.ProjectID)
		if err != nil {
			return service.ErrNotFound
		}
		current, err := lockConsent(ctx, tx, project)
		if err != nil || current == nil || !current.Since.Equal(count.Since) {
			return err
		}
		tokens := aisettings.Tokens{Input: max(count.Tokens.Input, 0), Cached: max(count.Tokens.Cached, 0), Output: max(count.Tokens.Output, 0)}
		var cost *domain.Cost
		if count.Cost != nil {
			cost = &domain.Cost{Micros: max(count.Cost.Micros, 0), Currency: count.Cost.Currency}
		}
		next := current.In(count.Unit)
		next.Spent = next.Spent.Add(tokens, cost, next.Unit, count.Estimated)
		return writeConsent(ctx, tx, project, next, entry.At)
	})
}

// SettleSample writes the row of the journal of a request of the sample and the request of the sample in one transaction
// (specs/2026-11-14-desk-journal.md JR2.6): the clearing of the journal leaves what the sample spent and measured.
func (repository *Repository) SettleSample(ctx context.Context, projectID string, entry modeljournal.Request) error {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return service.ErrNotFound
	}
	job, err := storedb.ParseUUID(entry.JobID)
	if err != nil {
		return service.ErrNotFound
	}
	if entry.ID == "" {
		entry.ID = modeljournal.NewID()
	}
	id, err := storedb.ParseUUID(entry.ID)
	if err != nil {
		return err
	}
	var input, cached, output, cost pgtype.Int8
	if tokens := entry.Tokens; tokens != nil {
		input, cached, output = pgtype.Int8{Int64: tokens.Input, Valid: true}, pgtype.Int8{Int64: tokens.CachedInput, Valid: true}, pgtype.Int8{Int64: tokens.Output, Valid: true}
	}
	var currency pgtype.Text
	if entry.Cost != nil {
		cost, currency = pgtype.Int8{Int64: max(entry.Cost.Micros, 0), Valid: true}, pgtype.Text{String: entry.Cost.Currency, Valid: true}
	}
	duration := min(max(entry.Duration.Milliseconds(), 0), math.MaxInt32)
	return pgx.BeginFunc(ctx, repository.db.Pool, func(tx pgx.Tx) error {
		if err := journalrepo.Insert(ctx, tx, entry); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO reading_sample_requests (id, project_id, job_id, at, duration_ms, provider_host, model, input_tokens,
			  cached_input_tokens, output_tokens, cost_micros, currency, estimated)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			id, project, job, entry.At, int32(duration), entry.ProviderHost, entry.Model, input, cached, output, cost, currency, entry.Estimated)
		return err
	})
}
