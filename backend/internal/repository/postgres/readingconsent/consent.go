// SPDX-License-Identifier: AGPL-3.0-only

package readingconsent

import (
	"context"
	"time"

	domain "canonkeeper/backend/internal/domain/readingconsent"
	service "canonkeeper/backend/internal/services/readingconsent"
	storedb "canonkeeper/backend/internal/store/db"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const consentColumns = `source, provider_host, state, pause, stop_unit, stop_amount, step_amount, since, resumed_at,
	spent_input_tokens, spent_cached_tokens, spent_output_tokens, spent_micros, spent_estimated`

type row interface {
	Scan(dest ...any) error
}

func scanConsent(scanner row) (*domain.Consent, error) {
	var source, host, pause pgtype.Text
	var state, unit string
	var stop, step pgtype.Int8
	var consent domain.Consent
	err := scanner.Scan(&source, &host, &state, &pause, &unit, &stop, &step, &consent.Since, &consent.ResumedAt,
		&consent.Spent.Tokens.Input, &consent.Spent.Tokens.Cached, &consent.Spent.Tokens.Output, &consent.Spent.Micros, &consent.Spent.Estimated)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	consent.Source, consent.Host = domain.Source(source.String), host.String
	consent.State, consent.Pause, consent.Unit = domain.State(state), domain.Pause(pause.String), domain.Unit(unit)
	if stop.Valid {
		consent.StopAt = &stop.Int64
	}
	if step.Valid {
		consent.Step = &step.Int64
	}
	return &consent, nil
}

// Consent is the consent of a saga, nil without one.
func (repository *Repository) Consent(ctx context.Context, projectID string) (*domain.Consent, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return nil, service.ErrNotFound
	}
	return scanConsent(repository.db.Pool.QueryRow(ctx, `SELECT `+consentColumns+` FROM card_build_permissions WHERE project_id = $1`, project))
}

// ConsentsOf are the consents of the live sagas an author owns (specs/2026-11-13-desk-consent-web.md RW3.1): the service of
// the consent counts those a change of the prices would pause.
func (repository *Repository) ConsentsOf(ctx context.Context, ownerID string) ([]domain.Consent, error) {
	owner, err := storedb.ParseUUID(ownerID)
	if err != nil {
		return nil, nil
	}
	rows, err := repository.db.Pool.Query(ctx, `SELECT `+consentColumns+` FROM card_build_permissions
		WHERE project_id IN (SELECT id FROM projects WHERE owner_user_id = $1 AND deleted_at IS NULL)`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var consents []domain.Consent
	for rows.Next() {
		consent, err := scanConsent(rows)
		if err != nil {
			return nil, err
		}
		consents = append(consents, *consent)
	}
	return consents, rows.Err()
}

// lockConsent takes the lock of the consent of a saga for its transaction: the changes of the author and the pauses of the
// gateway go one after another. The row is read FOR UPDATE: a change writes back the count it read, so the count of an answer
// (Settle) either lands before the read or waits for the change and lands on what it wrote (CN3.3).
func lockConsent(ctx context.Context, tx pgx.Tx, project pgtype.UUID) (*domain.Consent, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('canonkeeper reading consent ' || $1::text, 0))`, project); err != nil {
		return nil, err
	}
	return scanConsent(tx.QueryRow(ctx, `SELECT `+consentColumns+` FROM card_build_permissions WHERE project_id = $1 FOR UPDATE`, project))
}

// writeConsent keeps a consent of a live saga in the transaction; a saga that is gone is not found.
func writeConsent(ctx context.Context, tx pgx.Tx, project pgtype.UUID, consent domain.Consent, at time.Time) error {
	var stop, step pgtype.Int8
	if consent.StopAt != nil {
		stop = pgtype.Int8{Int64: *consent.StopAt, Valid: true}
	}
	if consent.Step != nil {
		step = pgtype.Int8{Int64: *consent.Step, Valid: true}
	}
	var pause pgtype.Text
	if consent.Pause != "" {
		pause = pgtype.Text{String: string(consent.Pause), Valid: true}
	}
	written, err := tx.Exec(ctx, `
		INSERT INTO card_build_permissions (project_id, source, provider_host, state, pause, stop_unit, stop_amount, step_amount,
		  since, resumed_at, spent_input_tokens, spent_cached_tokens, spent_output_tokens, spent_micros, spent_estimated, updated_at)
		SELECT project.id, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		FROM projects AS project WHERE project.id = $1 AND project.deleted_at IS NULL
		ON CONFLICT (project_id) DO UPDATE SET
		  source = EXCLUDED.source, provider_host = EXCLUDED.provider_host, state = EXCLUDED.state, pause = EXCLUDED.pause,
		  stop_unit = EXCLUDED.stop_unit, stop_amount = EXCLUDED.stop_amount, step_amount = EXCLUDED.step_amount,
		  since = EXCLUDED.since, resumed_at = EXCLUDED.resumed_at, spent_input_tokens = EXCLUDED.spent_input_tokens,
		  spent_cached_tokens = EXCLUDED.spent_cached_tokens, spent_output_tokens = EXCLUDED.spent_output_tokens,
		  spent_micros = EXCLUDED.spent_micros, spent_estimated = EXCLUDED.spent_estimated, updated_at = EXCLUDED.updated_at`,
		project, string(consent.Source), consent.Host, string(consent.State), pause, string(consent.Unit), stop, step,
		consent.Since, consent.ResumedAt, consent.Spent.Tokens.Input, consent.Spent.Tokens.Cached, consent.Spent.Tokens.Output,
		consent.Spent.Micros, consent.Spent.Estimated, at)
	if err != nil {
		return err
	}
	if written.RowsAffected() == 0 {
		return service.ErrNotFound
	}
	return nil
}

// ChangeConsent runs a change of the author over the consent of a live saga under its lock and keeps what the change
// gives (CN3.2); «Повторить» and a change that begins a new count forget the marks of the chapters, the names and the threads
// of the saga in the same transaction (CN3.6; specs/2026-11-15-desk-reading-rest.md RR1.7). A refused change keeps them.
func (repository *Repository) ChangeConsent(ctx context.Context, projectID string, at time.Time,
	change func(current *domain.Consent) (domain.Consent, error), retryUnread bool,
) (domain.Consent, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return domain.Consent{}, service.ErrNotFound
	}
	var kept domain.Consent
	err = pgx.BeginFunc(ctx, repository.db.Pool, func(tx pgx.Tx) error {
		current, err := lockConsent(ctx, tx, project)
		if err != nil {
			return err
		}
		if kept, err = change(current); err != nil {
			return err
		}
		if err := writeConsent(ctx, tx, project, kept, at); err != nil {
			return err
		}
		if retryUnread || domain.Begins(current, kept) {
			return forgetUnread(ctx, tx, project)
		}
		return nil
	})
	return kept, err
}

// Pause stops the reading of a saga with a reason (CN3.3, CN3.4) while its consent is allowed and its count is the one that
// began at since; it reports whether it paused.
func (repository *Repository) Pause(ctx context.Context, projectID string, since time.Time, pause domain.Pause, at time.Time) (bool, error) {
	project, err := storedb.ParseUUID(projectID)
	if err != nil {
		return false, service.ErrNotFound
	}
	paused := false
	err = pgx.BeginFunc(ctx, repository.db.Pool, func(tx pgx.Tx) error {
		current, err := lockConsent(ctx, tx, project)
		if err != nil || current == nil || current.State != domain.StateAllowed || !current.Since.Equal(since) {
			return err
		}
		next := *current
		next.State, next.Pause = domain.StatePaused, pause
		if err := writeConsent(ctx, tx, project, next, at); err != nil {
			return err
		}
		paused = true
		return nil
	})
	return paused, err
}

// Paused are the live sagas whose consent of the source stands paused with one of the reasons
// (specs/2026-11-13-desk-account-ai.md AI3.6).
func (repository *Repository) Paused(ctx context.Context, source domain.Source, pauses []domain.Pause) ([]string, error) {
	reasons := make([]string, len(pauses))
	for index, pause := range pauses {
		reasons[index] = string(pause)
	}
	rows, err := repository.db.Pool.Query(ctx, `
		SELECT permission.project_id::text
		FROM card_build_permissions AS permission
		JOIN projects AS project ON project.id = permission.project_id AND project.deleted_at IS NULL
		WHERE permission.source = $1 AND permission.state = 'paused' AND permission.pause = ANY($2::text[])
		ORDER BY permission.project_id`, string(source), reasons)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
